package server

import (
	"net/http"
	"strings"

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

func (server *Server) deleteCodexProvider(response http.ResponseWriter, request *http.Request) {
	providerID := strings.TrimSpace(request.PathValue("id"))
	config, err := codexruntime.DeleteProviderConfig(providerID)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.store.ClearCodexProvider(providerID); err != nil {
		writeError(response, http.StatusInternalServerError, "Provider 已删除，但切换 Codex 配置失败: "+err.Error())
		return
	}
	if err := server.reloadCodexEnvironment(); err != nil {
		writeError(response, http.StatusInternalServerError, "Provider 已删除，但刷新 Codex 环境失败: "+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, config)
}
