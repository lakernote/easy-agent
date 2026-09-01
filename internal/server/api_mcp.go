package server

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	mcppresets "github.com/lakernote/easy-agent/internal/mcp/presets"
	"github.com/lakernote/easy-agent/internal/mcpclient"
	"github.com/lakernote/easy-agent/internal/store"
)

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
	// 页面不会回传已经保存的密钥，因此必须先恢复旧值，再做认证校验和连接测试。
	current, _ := server.store.ListMCPConfigs()
	for _, value := range current {
		if value.ID == input.ID {
			if input.Token == "" {
				input.Token = value.Token
			}
			if input.Password == "" {
				input.Password = value.Password
			}
			if input.Args == nil {
				input.Args = append([]string(nil), value.Args...)
			}
			if input.Headers == nil {
				input.Headers = cloneMap(value.Headers)
			}
			if input.Environment == nil {
				input.Environment = cloneMap(value.Environment)
			}
			input.Args = restoreRedactedArgs(input.Args, value.Args)
			input.Headers = restoreRedactedMap(input.Headers, value.Headers)
			input.Environment = restoreRedactedMap(input.Environment, value.Environment)
		}
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
	if err := validateMCP(input); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	// “已启用”必须代表此刻确实可以连接，避免保存一个看似开启、实际不可用的配置。
	if input.Enabled {
		ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
		connection, err := mcpclient.Connect(ctx, server.env, mcpClientConfig(input))
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
	configs, err := server.store.ListMCPConfigs()
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
	connection, err := mcpclient.Connect(ctx, server.env, mcpClientConfig(*selected))
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
	preset, found := mcppresets.Find(request.PathValue("id"))
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
	preset, found := mcppresets.Find(request.PathValue("id"))
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
	connection, err := mcpclient.Connect(ctx, server.env, mcpClientConfig(candidate))
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
	preset, found := mcppresets.Find(request.PathValue("id"))
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

func mcpConfigFromPreset(preset mcppresets.Preset) store.MCPConfig {
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

func (server *Server) checkMCPPresetRuntime(parent context.Context, preset mcppresets.Preset) error {
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

func publicMCPs(values []store.MCPConfig) []store.MCPConfig {
	result := make([]store.MCPConfig, 0, len(values))
	for _, value := range values {
		result = append(result, publicMCP(value))
	}
	return result
}

func publicMCP(value store.MCPConfig) store.MCPConfig {
	value.SecretConfigured = value.Token != "" || value.Password != "" || hasRedactedMCPValue(value.Args, value.Headers, value.Environment)
	value.Token, value.Password = "", ""
	// Args、Header 和环境变量都可能承载凭证；只遮蔽敏感值并保留非敏感配置，
	// 保存时由服务端把占位恢复成原值，避免 bootstrap 把秘密发到浏览器。
	value.Args = redactMCPArgs(value.Args)
	value.Headers = redactMCPMap(value.Headers)
	value.Environment = redactMCPMap(value.Environment)
	return value
}

const redactedMCPValue = "__EASYAGENT_REDACTED__"

func isSensitiveMCPKey(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{"authorization", "api-key", "apikey", "token", "secret", "password", "passwd", "cookie", "credential", "private-key", "access-key"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func redactMCPMap(values map[string]string) map[string]string {
	result := cloneMap(values)
	for key, value := range result {
		if isSensitiveMCPKey(key) || isSensitiveMCPKey(value) {
			result[key] = redactedMCPValue
		}
	}
	return result
}

func restoreRedactedMap(values, original map[string]string) map[string]string {
	result := cloneMap(values)
	for key, value := range result {
		if value == redactedMCPValue && original != nil {
			if old, ok := original[key]; ok {
				result[key] = old
			}
		}
	}
	return result
}

func redactMCPArgs(values []string) []string {
	result := append([]string(nil), values...)
	redactNext := false
	for index, value := range result {
		if redactNext {
			result[index] = redactedMCPValue
			redactNext = false
			continue
		}
		if key, _, found := strings.Cut(value, "="); found && isSensitiveMCPKey(key) {
			result[index] = key + "=" + redactedMCPValue
			continue
		}
		if isSensitiveMCPKey(value) {
			redactNext = true
		}
	}
	return result
}

func restoreRedactedArgs(values, original []string) []string {
	result := append([]string(nil), values...)
	for index, value := range result {
		if value != redactedMCPValue || index >= len(original) {
			continue
		}
		result[index] = original[index]
	}
	return result
}

func hasRedactedMCPValue(args []string, headers, environment map[string]string) bool {
	for _, value := range args {
		if value == redactedMCPValue || isSensitiveMCPKey(value) {
			return true
		}
	}
	for key, value := range headers {
		if isSensitiveMCPKey(key) || isSensitiveMCPKey(value) {
			return true
		}
	}
	for key, value := range environment {
		if isSensitiveMCPKey(key) || isSensitiveMCPKey(value) {
			return true
		}
	}
	return false
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
