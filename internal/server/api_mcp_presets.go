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
