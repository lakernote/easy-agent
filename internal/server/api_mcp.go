package server

import (
	"context"
	"net/http"
	"strings"
	"time"

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
	if _, _, err := server.syncCodexCapabilities(); err != nil {
		writeError(response, http.StatusInternalServerError, "MCP 已保存，但同步 Codex 失败："+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicMCP(input))
}

func (server *Server) deleteMCP(response http.ResponseWriter, request *http.Request) {
	if err := server.store.DeleteMCP(request.PathValue("id")); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if _, _, err := server.syncCodexCapabilities(); err != nil {
		writeError(response, http.StatusInternalServerError, "MCP 已删除，但同步 Codex 失败："+err.Error())
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
