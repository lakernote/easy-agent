package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/codexruntime"
)

func (server *Server) getCodexConfig(response http.ResponseWriter, request *http.Request) {
	config, err := codexruntime.LoadProviderConfig()
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, config)
}

func (server *Server) saveCodexConfig(response http.ResponseWriter, request *http.Request) {
	var input codexruntime.ProviderConfigInput
	if !decodeJSON(response, request, &input) {
		return
	}
	config, err := codexruntime.SaveProviderConfig(input)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.reloadCodexEnvironment(); err != nil {
		writeError(response, http.StatusInternalServerError, "Codex 配置已保存，但加载 API Key 失败: "+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, config)
}

func (server *Server) installCodex(response http.ResponseWriter, request *http.Request) {
	status := server.detectCodex(request.Context())
	if status.Installed && status.AppServerAvailable {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "status": status, "message": "Codex CLI 与 app-server 已经就绪"})
		return
	}
	installContext, cancel := context.WithTimeout(request.Context(), 3*time.Minute)
	defer cancel()
	output, err := codexruntime.Install(installContext, server.env)
	if err != nil {
		message := strings.TrimSpace(output)
		if message != "" {
			message = ": " + message
		}
		writeError(response, http.StatusBadGateway, "Codex CLI 安装失败"+message)
		return
	}
	status = server.detectCodex(installContext)
	if !status.Installed || !status.AppServerAvailable {
		writeError(response, http.StatusBadGateway, "安装脚本已执行，但重新检测未找到可用的 Codex app-server")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "status": status, "message": "Codex CLI 与 app-server 安装完成"})
}
