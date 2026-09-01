package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	builtinmcp "github.com/lakernote/easy-agent/internal/builtin/mcp"
	"github.com/lakernote/easy-agent/internal/builtin/prompt"
	builtinskills "github.com/lakernote/easy-agent/internal/builtin/skills"
	builtintools "github.com/lakernote/easy-agent/internal/builtin/tools"
	"github.com/lakernote/easy-agent/internal/mcpclient"
	"github.com/lakernote/easy-agent/internal/store"
)

type bootstrapPayload struct {
	Sessions     []store.Session       `json:"sessions"`
	Model        store.ModelSettings   `json:"model"`
	Skills       []store.SkillOverride `json:"skills"`
	BuiltinTools []builtintools.Info   `json:"builtinTools"`
	MCPPresets   []builtinmcp.Preset   `json:"mcpPresets"`
	ModelRules   modelRulesPayload     `json:"modelRules"`
	MCPs         []store.MCPConfig     `json:"mcps"`
	SystemPrompt string                `json:"systemPrompt"`
	Ollama       ollamaStatus          `json:"ollama"`
	Runtime      runtimeInfoPayload    `json:"runtime"`
}

type runtimeInfoPayload struct {
	Home      string `json:"home"`
	Workspace string `json:"workspace"`
	Runtime   string `json:"runtime"`
}

type modelRulesPayload struct {
	DefaultMaxOutputTokens             int `json:"defaultMaxOutputTokens"`
	DefaultRequestTimeoutSeconds       int `json:"defaultRequestTimeoutSeconds"`
	MinRequestTimeoutSeconds           int `json:"minRequestTimeoutSeconds"`
	MaxRequestTimeoutSeconds           int `json:"maxRequestTimeoutSeconds"`
	DefaultCompressionThresholdPercent int `json:"defaultCompressionThresholdPercent"`
	MinCompressionThresholdPercent     int `json:"minCompressionThresholdPercent"`
	MaxCompressionThresholdPercent     int `json:"maxCompressionThresholdPercent"`
}

func (server *Server) bootstrap(response http.ResponseWriter, request *http.Request) {
	sessions, err := server.store.ListSessions(100)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	model, err := server.store.Model()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	catalog, err := loadSkillCatalog(server.store)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	mcps, err := server.store.MCPs()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	toolInfo := builtintools.InfoList(server.env, catalog)
	for _, config := range mcps {
		if config.Enabled {
			toolInfo = append(toolInfo, builtintools.Info{Name: "load_mcp", Description: "按需连接一个已启用的 MCP Server，再加载它的真实工具。", Source: "运行时"})
			break
		}
	}
	detectedModel := enrichOllamaContextWindow(request.Context(), model)
	model = detectedModel
	writeJSON(response, http.StatusOK, bootstrapPayload{
		Sessions: sessions, Model: publicModel(model), Skills: catalog.All(),
		BuiltinTools: toolInfo, MCPPresets: builtinmcp.Catalog(), ModelRules: modelRules(),
		MCPs: publicMCPs(mcps), SystemPrompt: prompt.Template(), Ollama: server.detectOllama(request.Context()),
		Runtime: runtimeInfoPayload{Home: server.env.Home(), Workspace: server.env.Workspace(), Runtime: server.env.Runtime()},
	})
}

func modelRules() modelRulesPayload {
	return modelRulesPayload{
		DefaultMaxOutputTokens:             store.DefaultMaxOutputTokens,
		DefaultRequestTimeoutSeconds:       store.DefaultRequestTimeoutSeconds,
		MinRequestTimeoutSeconds:           store.MinRequestTimeoutSeconds,
		MaxRequestTimeoutSeconds:           store.MaxRequestTimeoutSeconds,
		DefaultCompressionThresholdPercent: store.DefaultCompressionThresholdPercent,
		MinCompressionThresholdPercent:     store.MinCompressionThresholdPercent,
		MaxCompressionThresholdPercent:     store.MaxCompressionThresholdPercent,
	}
}

func (server *Server) getSession(response http.ResponseWriter, request *http.Request) {
	value, err := server.store.Session(request.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, "会话不存在")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	settings, _ := server.store.Model()
	settings = enrichOllamaContextWindow(request.Context(), settings)
	decorateContext(&value, settings)
	value.PartialOutput = server.taskPartial(value.ID)
	writeJSON(response, http.StatusOK, value)
}

type messageRequest struct {
	Message     string              `json:"message"`
	Attachments []attachmentRequest `json:"attachments"`
	// Workspace 只在创建会话时使用；后续多轮对话始终沿用会话保存的工作区。
	Workspace string `json:"workspace,omitempty"`
}

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func (server *Server) createSession(response http.ResponseWriter, request *http.Request) {
	var input messageRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	attachments, err := validateAttachments(input.Attachments)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if input.Message == "" && len(attachments) == 0 {
		writeError(response, http.StatusBadRequest, "请输入消息或添加附件")
		return
	}
	runEnvironment, err := server.env.WithWorkspace(input.Workspace)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	model, err := server.store.Model()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	id := newID()
	if _, err := server.store.CreateSession(id, attachmentTitle(input.Message, attachments), model.Model, runEnvironment.Workspace(), time.Now()); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.queue(id, input.Message, attachments, model); err != nil {
		_ = server.store.DeleteSession(id)
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, _ := server.store.Session(id)
	model = enrichOllamaContextWindow(request.Context(), model)
	decorateContext(&value, model)
	writeJSON(response, http.StatusAccepted, value)
}

func (server *Server) continueSession(response http.ResponseWriter, request *http.Request) {
	var input messageRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	attachments, err := validateAttachments(input.Attachments)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if input.Message == "" && len(attachments) == 0 {
		writeError(response, http.StatusBadRequest, "请输入消息或添加附件")
		return
	}
	if strings.TrimSpace(input.Workspace) != "" {
		writeError(response, http.StatusBadRequest, "工作区在创建会话时确定；请新建会话后选择其他工作区")
		return
	}
	model, err := server.store.Model()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	id := request.PathValue("id")
	if _, err := server.store.Session(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if err := server.queue(id, input.Message, attachments, model); err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, _ := server.store.Session(id)
	model = enrichOllamaContextWindow(request.Context(), model)
	decorateContext(&value, model)
	writeJSON(response, http.StatusAccepted, value)
}

func (server *Server) deleteSession(response http.ResponseWriter, request *http.Request) {
	value, err := server.store.Session(request.PathValue("id"))
	if err == nil && (value.Status == "queued" || value.Status == "running") {
		writeError(response, http.StatusConflict, "Agent 正在运行，暂时不能删除")
		return
	}
	if err := server.store.DeleteSession(request.PathValue("id")); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) cancelSession(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	changed, err := server.store.CancelSession(id, time.Now())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if !changed {
		writeError(response, http.StatusConflict, "任务当前不在排队或运行中")
		return
	}
	server.cancelTask(id)
	value, err := server.store.Session(id)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	settings, _ := server.store.Model()
	settings = enrichOllamaContextWindow(request.Context(), settings)
	decorateContext(&value, settings)
	writeJSON(response, http.StatusOK, value)
}

func (server *Server) saveModel(response http.ResponseWriter, request *http.Request) {
	var input store.ModelSettings
	if !decodeJSON(response, request, &input) {
		return
	}
	current, _ := server.store.Model()
	input, err := prepareModelInput(input, current)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	input.SecretConfigured = false
	if err := server.store.SaveModel(input); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicModel(enrichOllamaContextWindow(request.Context(), input)))
}

func sameModelEndpoint(left, right store.ModelSettings) bool {
	return strings.EqualFold(strings.TrimSpace(left.Provider), strings.TrimSpace(right.Provider)) &&
		strings.EqualFold(strings.TrimRight(strings.TrimSpace(left.BaseURL), "/"), strings.TrimRight(strings.TrimSpace(right.BaseURL), "/"))
}

func (server *Server) saveSkill(response http.ResponseWriter, request *http.Request) {
	var input store.SkillOverride
	if !decodeJSON(response, request, &input) {
		return
	}
	input.Name = request.PathValue("name")
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Description) == "" || strings.TrimSpace(input.Content) == "" {
		writeError(response, http.StatusBadRequest, "Skill 名称、描述和内容不能为空")
		return
	}
	if !skillNamePattern.MatchString(input.Name) {
		writeError(response, http.StatusBadRequest, "Skill 名称只能包含小写英文、数字和短横线")
		return
	}
	parsed, err := builtinskills.Parse(input.Content)
	if err != nil {
		writeError(response, http.StatusBadRequest, "SKILL.md 格式错误："+err.Error())
		return
	}
	if parsed.Name != input.Name {
		writeError(response, http.StatusBadRequest, "SKILL.md 中的 name 必须与 Skill 名称一致")
		return
	}
	// frontmatter 是 Skill 元数据的唯一事实来源，避免列表描述和真正交给
	// Agent 的 SKILL.md 内容不一致。
	input.Description = parsed.Description
	catalog, err := loadSkillCatalog(server.store)
	if err == nil {
		for _, value := range catalog.All() {
			if value.Name == input.Name {
				input.Builtin = value.Builtin
			}
		}
	}
	if err := server.store.SaveSkill(input); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, input)
}

func (server *Server) resetSkill(response http.ResponseWriter, request *http.Request) {
	if err := server.store.DeleteSkill(request.PathValue("name")); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) saveMCP(response http.ResponseWriter, request *http.Request) {
	var input store.MCPConfig
	if !decodeJSON(response, request, &input) {
		return
	}
	input.ID = request.PathValue("id")
	if strings.TrimSpace(input.Name) == "" {
		writeError(response, http.StatusBadRequest, "MCP 名称不能为空")
		return
	}
	input.Transport = strings.ToLower(strings.TrimSpace(input.Transport))
	input.AuthType = strings.ToLower(strings.TrimSpace(input.AuthType))
	input.Description = strings.TrimSpace(input.Description)
	if input.Description == "" {
		input.Description = input.Name + " 提供的外部工具"
	}
	if input.Args == nil {
		input.Args = []string{}
	}
	if input.Headers == nil {
		input.Headers = map[string]string{}
	}
	if input.Environment == nil {
		input.Environment = map[string]string{}
	}
	// 页面不会回传已经保存的密钥，因此必须先恢复旧值，再做认证校验和连接测试。
	current, _ := server.store.MCPs()
	for _, value := range current {
		if value.ID == input.ID {
			if input.Token == "" {
				input.Token = value.Token
			}
			if input.Password == "" {
				input.Password = value.Password
			}
		}
	}
	if err := validateMCP(input); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	// “已启用”必须代表此刻确实可以连接，避免保存一个看似开启、实际不可用的配置。
	if input.Enabled {
		ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
		connection, err := mcpclient.Connect(ctx, server.env, input)
		cancel()
		if err != nil {
			writeError(response, http.StatusBadGateway, "MCP 连接测试失败，未启用："+err.Error())
			return
		}
		_ = connection.Close()
	}
	input.SecretConfigured = false
	if err := server.store.SaveMCP(input); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicMCP(input))
}

func (server *Server) deleteMCP(response http.ResponseWriter, request *http.Request) {
	if err := server.store.DeleteMCP(request.PathValue("id")); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) testMCP(response http.ResponseWriter, request *http.Request) {
	configs, err := server.store.MCPs()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	var selected *store.MCPConfig
	for index := range configs {
		if configs[index].ID == request.PathValue("id") {
			copy := configs[index]
			copy.Enabled = true
			selected = &copy
			break
		}
	}
	if selected == nil {
		writeError(response, http.StatusNotFound, "MCP 配置不存在")
		return
	}
	if err := validateMCP(*selected); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
	defer cancel()
	connection, err := mcpclient.Connect(ctx, server.env, *selected)
	if err != nil {
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	defer connection.Close()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "tools": connection.Info})
}

type mcpInstallResult struct {
	Ready   bool                 `json:"ready"`
	Status  string               `json:"status"`
	Message string               `json:"message"`
	MCP     store.MCPConfig      `json:"mcp"`
	Tools   []mcpclient.ToolInfo `json:"tools"`
}

type mcpPresetCheckResult struct {
	OK        bool   `json:"ok"`
	Installed bool   `json:"installed"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

// checkMCPPreset 只读取宿主命令、版本和私有安装目录，不保存 MCP 配置，也不
// 下载依赖。页面因此可以明确区分“检测环境”和“安装并启用”。
func (server *Server) checkMCPPreset(response http.ResponseWriter, request *http.Request) {
	preset, found := builtinmcp.Find(request.PathValue("id"))
	if !found {
		writeError(response, http.StatusNotFound, "MCP 预设不存在")
		return
	}
	if preset.Action != "install" {
		writeJSON(response, http.StatusOK, mcpPresetCheckResult{OK: true, Status: "configuration_required", Message: "这是远程 MCP，只需要配置连接和认证，无需本地安装"})
		return
	}
	if err := server.checkMCPPresetRuntime(request.Context(), preset); err != nil {
		writeJSON(response, http.StatusOK, mcpPresetCheckResult{OK: false, Status: "missing_dependency", Message: err.Error()})
		return
	}
	installed := false
	if preset.Command != "" {
		_, installedErr := server.env.ResolveCommand(preset.Command)
		installed = installedErr == nil
	}
	if installed {
		writeJSON(response, http.StatusOK, mcpPresetCheckResult{OK: true, Installed: true, Status: "installed", Message: "运行环境和私有 MCP 包均已就绪，可以测试连接或启用"})
		return
	}
	writeJSON(response, http.StatusOK, mcpPresetCheckResult{OK: true, Status: "ready_to_install", Message: "运行环境满足要求；MCP 包尚未安装，安装只会写入 EasyAgent 私有目录"})
}

// installMCPPreset 完成真正的一键流程：检查 Node.js、把固定版本安装到
// EasyAgent 私有 runtime 目录、连接并读取工具清单，全部成功后才启用。
func (server *Server) installMCPPreset(response http.ResponseWriter, request *http.Request) {
	preset, found := builtinmcp.Find(request.PathValue("id"))
	if !found {
		writeError(response, http.StatusNotFound, "MCP 预设不存在")
		return
	}
	if preset.Action != "install" {
		writeError(response, http.StatusBadRequest, "该 MCP 需要先填写配置")
		return
	}

	config := mcpConfigFromPreset(preset)
	if err := server.checkMCPPresetRuntime(request.Context(), preset); err != nil {
		writeJSON(response, http.StatusOK, mcpInstallResult{Ready: false, Status: "missing_dependency", Message: err.Error(), MCP: publicMCP(config), Tools: []mcpclient.ToolInfo{}})
		return
	}
	if preset.NPMPackage != "" {
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
		command, installErr := server.env.InstallNPMPackage(ctx, preset.ID, preset.NPMPackage, preset.NPMExecutable)
		cancel()
		if installErr != nil {
			writeJSON(response, http.StatusOK, mcpInstallResult{Ready: false, Status: "install_failed", Message: installErr.Error(), MCP: publicMCP(config), Tools: []mcpclient.ToolInfo{}})
			return
		}
		config.Command = command
	}

	candidate := config
	candidate.Enabled = true
	ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
	defer cancel()
	connection, err := mcpclient.Connect(ctx, server.env, candidate)
	if err != nil {
		// 包已经成功安装时保留一份停用配置，方便用户检查路径、调整参数并重试；
		// 缺少宿主依赖或安装命令失败时不会提前写入误导性的 MCP 记录。
		if saveErr := server.store.SaveMCP(config); saveErr != nil {
			writeError(response, http.StatusInternalServerError, saveErr.Error())
			return
		}
		writeJSON(response, http.StatusOK, mcpInstallResult{Ready: false, Status: "connect_failed", Message: "安装命令已执行，但连接测试失败：" + err.Error(), MCP: publicMCP(config), Tools: []mcpclient.ToolInfo{}})
		return
	}
	defer connection.Close()
	if err := server.store.SaveMCP(candidate); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, mcpInstallResult{Ready: true, Status: "ready", Message: "依赖检查、安装和连接测试均已通过", MCP: publicMCP(candidate), Tools: connection.Info})
}

// uninstallMCPPreset 删除预设安装在 EasyAgent 私有 Runtime 中的包和对应配置。
// 宿主机 Node/npm、全局包以及工作区文件都不在删除范围内。
func (server *Server) uninstallMCPPreset(response http.ResponseWriter, request *http.Request) {
	preset, found := builtinmcp.Find(request.PathValue("id"))
	if !found {
		writeError(response, http.StatusNotFound, "MCP 预设不存在")
		return
	}
	if preset.Action != "install" || preset.NPMPackage == "" {
		writeError(response, http.StatusBadRequest, "该 MCP 没有 EasyAgent 私有安装包")
		return
	}
	if err := server.env.UninstallNPMPackage(preset.ID); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.store.DeleteMCP(preset.ID); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func mcpConfigFromPreset(preset builtinmcp.Preset) store.MCPConfig {
	return store.MCPConfig{
		ID: preset.ID, Name: preset.Name, Description: preset.Description, Enabled: false, Transport: preset.Transport,
		Command: preset.Command, Args: append([]string(nil), preset.Args...), Endpoint: preset.Endpoint, AuthType: preset.AuthType,
		Headers: cloneMap(preset.Headers), Environment: map[string]string{},
	}
}

func cloneMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (server *Server) checkMCPPresetRuntime(parent context.Context, preset builtinmcp.Preset) error {
	for _, command := range preset.RequiredCommands {
		if _, err := server.env.ResolveCommand(command); err != nil {
			return errors.New("服务器 PATH 中找不到 " + command + "；" + preset.Requirement + "。EasyAgent 不执行系统级运行时安装")
		}
	}
	if preset.MinimumNodeMajor == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	node, err := server.env.ResolveCommand("node")
	if err != nil {
		return errors.New("无法定位 Node.js")
	}
	command := exec.CommandContext(ctx, node, "--version")
	command.Env = server.env.Environ(nil)
	output, err := command.Output()
	if err != nil {
		return errors.New("无法读取 Node.js 版本")
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(output)), "v")
	majorText, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil || major < preset.MinimumNodeMajor {
		return errors.New(preset.Name + " 需要 Node.js " + strconv.Itoa(preset.MinimumNodeMajor) + "+，当前版本为 " + strings.TrimSpace(string(output)))
	}
	return nil
}

type ollamaModel struct {
	Name       string    `json:"name"`
	Model      string    `json:"model,omitempty"`
	Size       int64     `json:"size,omitempty"`
	ModifiedAt time.Time `json:"modifiedAt,omitempty"`
}

type ollamaStatus struct {
	Installed bool          `json:"installed"`
	Running   bool          `json:"running"`
	BaseURL   string        `json:"baseUrl"`
	Models    []ollamaModel `json:"models"`
	Message   string        `json:"message"`
}

func (server *Server) getOllama(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, server.detectOllama(request.Context()))
}

func (server *Server) detectOllama(parent context.Context) ollamaStatus {
	_, lookupErr := server.env.ResolveCommand("ollama")
	status := ollamaStatus{Installed: lookupErr == nil, BaseURL: ollamaServerURL(), Models: []ollamaModel{}}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, status.BaseURL+"/api/tags", nil)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		status.Message = "Ollama 未启动；可执行 ollama serve"
		return status
	}
	defer response.Body.Close()
	var payload struct {
		Models []ollamaModel `json:"models"`
	}
	if response.StatusCode >= 300 || json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&payload) != nil {
		status.Message = "Ollama 服务响应异常"
		return status
	}
	status.Running, status.Models = true, payload.Models
	if len(payload.Models) == 0 {
		status.Message = "Ollama 已运行，但尚未下载模型"
	} else {
		status.Message = "Ollama 已运行，可直接使用"
	}
	return status
}

// ollamaServerURL 允许服务器通过环境变量连接另一个 Ollama 实例。
// 默认仍是本机，不要求用户增加配置；路径统一在这里处理，避免 API 各处
// 写死 127.0.0.1。
func ollamaServerURL() string {
	value := strings.TrimSpace(os.Getenv("EASYAGENT_OLLAMA_URL"))
	if value == "" {
		value = strings.TrimSuffix(store.DefaultOllamaBaseURL, "/v1")
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	return strings.TrimRight(value, "/")
}

// enrichOllamaContextWindow 从 /api/ps 读取当前真正加载的上下文窗口。
// /api/show 的 context_length 是模型理论上限，不一定等于 Ollama 根据显存实际
// 选择的窗口；用理论值做压缩阈值会在小窗口机器上过晚压缩。
func enrichOllamaContextWindow(parent context.Context, value store.ModelSettings) store.ModelSettings {
	if !value.IsOllama() || strings.TrimSpace(value.Model) == "" {
		return value
	}
	baseURL := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(value.BaseURL), "/"), "/v1")
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/ps", nil)
	if err != nil {
		return value
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return value
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return value
	}
	var payload struct {
		Models []struct {
			Name          string `json:"name"`
			Model         string `json:"model"`
			ContextLength int    `json:"context_length"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&payload) != nil {
		return value
	}
	for _, running := range payload.Models {
		if (running.Name == value.Model || running.Model == value.Model) && running.ContextLength > 0 {
			value.ContextWindowTokens = running.ContextLength
			return value
		}
	}
	return value
}

func (server *Server) useOllama(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Model string `json:"model"`
	}
	if !decodeJSON(response, request, &input) {
		return
	}
	status := server.detectOllama(request.Context())
	if !status.Running {
		writeError(response, http.StatusBadGateway, status.Message)
		return
	}
	found := false
	for _, value := range status.Models {
		if input.Model == value.Name || input.Model == value.Model {
			found = true
		}
	}
	if !found {
		writeError(response, http.StatusBadRequest, "模型尚未下载")
		return
	}
	model := store.DefaultModelSettings()
	model.BaseURL = strings.TrimRight(status.BaseURL, "/") + "/v1"
	model.Model = input.Model
	if err := server.store.SaveModel(model); err != nil {
		writeError(response, 500, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicModel(enrichOllamaContextWindow(request.Context(), model)))
}

func publicModel(value store.ModelSettings) store.ModelSettings {
	value.SecretConfigured = value.APIKey != "" || (value.APIKeyEnv != "" && os.Getenv(value.APIKeyEnv) != "")
	value.APIKey = ""
	return value
}

func publicMCPs(values []store.MCPConfig) []store.MCPConfig {
	result := make([]store.MCPConfig, 0, len(values))
	for _, value := range values {
		result = append(result, publicMCP(value))
	}
	return result
}

func publicMCP(value store.MCPConfig) store.MCPConfig {
	value.SecretConfigured = value.Token != "" || value.Password != ""
	value.Token, value.Password = "", ""
	return value
}

func validateModel(value store.ModelSettings) error {
	if strings.TrimSpace(value.BaseURL) == "" || strings.TrimSpace(value.Model) == "" {
		return errors.New("模型地址和名称不能为空")
	}
	if value.Protocol != "chat_completions" && value.Protocol != "responses" {
		return errors.New("协议只能是 chat_completions 或 responses")
	}
	if value.MaxOutputTokens <= 0 {
		return errors.New("最大输出 Token 必须大于 0")
	}
	if value.RequestTimeoutSeconds < store.MinRequestTimeoutSeconds || value.RequestTimeoutSeconds > store.MaxRequestTimeoutSeconds {
		return errors.New("模型超时必须在 " + strconv.Itoa(store.MinRequestTimeoutSeconds) + " 到 " + strconv.Itoa(store.MaxRequestTimeoutSeconds) + " 秒之间")
	}
	if value.ContextWindowTokens < 0 {
		return errors.New("上下文窗口 Token 不能小于 0")
	}
	if value.ContextWindowTokens > 0 && value.ContextWindowTokens <= value.MaxOutputTokens {
		return errors.New("上下文窗口必须大于最大输出 Token")
	}
	if value.CompressionThresholdPercent < store.MinCompressionThresholdPercent || value.CompressionThresholdPercent > store.MaxCompressionThresholdPercent {
		return errors.New("自动压缩阈值必须在 " + strconv.Itoa(store.MinCompressionThresholdPercent) + "% 到 " + strconv.Itoa(store.MaxCompressionThresholdPercent) + "% 之间")
	}
	return nil
}

func validateMCP(value store.MCPConfig) error {
	switch value.Transport {
	case "stdio":
		if strings.TrimSpace(value.Command) == "" {
			return errors.New("stdio MCP 缺少命令")
		}
	case "http", "streamable_http":
		if strings.TrimSpace(value.Endpoint) == "" {
			return errors.New("HTTP MCP 缺少 Endpoint")
		}
	default:
		return errors.New("MCP Transport 只能是 stdio、http 或 streamable_http")
	}
	if !value.Enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(value.AuthType)) {
	case "", "none":
	case "bearer", "token":
		if strings.TrimSpace(value.Token) == "" {
			return errors.New("启用 Bearer 认证前必须填写 Token")
		}
	case "basic":
		if strings.TrimSpace(value.Username) == "" || value.Password == "" {
			return errors.New("启用 Basic 认证前必须填写用户名和密码")
		}
	default:
		return errors.New("MCP 认证方式只能是无认证、Bearer Token 或用户名密码")
	}
	return nil
}
