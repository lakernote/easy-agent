package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/codexruntime"
)

func (server *Server) getCodex(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, server.detectCodex(request.Context()))
}

func (server *Server) codexQuery(request *http.Request, method string) (any, error) {
	return server.codexQueryWithParams(request.Context(), server.env.Workspace(), method, map[string]any{})
}

func (server *Server) codexQueryWithParams(ctx context.Context, workspace, method string, params any) (any, error) {
	status := server.detectCodex(ctx)
	if !status.Installed || !status.AppServerAvailable {
		return nil, &codexUnavailableError{message: status.Message}
	}
	result, err := codexruntime.Call(ctx, codexruntime.Config{Path: status.Path, Workspace: workspace, Timeout: 20 * time.Second, Env: server.codexEnvironment()}, method, params)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(result, &value); err != nil {
		return nil, err
	}
	return sanitizeCodexValue(value), nil
}

func sanitizeCodexValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, item := range current {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "accesstoken") || strings.Contains(lower, "refreshtoken") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "apikey") || strings.Contains(lower, "authorization") {
				continue
			}
			result[key] = sanitizeCodexValue(item)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, item := range current {
			result[index] = sanitizeCodexValue(item)
		}
		return result
	default:
		return value
	}
}

type codexUnavailableError struct{ message string }

func (err *codexUnavailableError) Error() string { return err.message }

func (server *Server) getCodexModels(response http.ResponseWriter, request *http.Request) {
	value, err := server.codexQuery(request, "model/list")
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (server *Server) getCodexAccount(response http.ResponseWriter, request *http.Request) {
	value, err := server.codexQuery(request, "account/read")
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (server *Server) getCodexThreads(response http.ResponseWriter, request *http.Request) {
	limit := 50
	if value, err := strconv.Atoi(request.URL.Query().Get("limit")); err == nil && value > 0 && value <= 200 {
		limit = value
	}
	// app-server 在 sourceKinds 缺省时只返回 CLI/VS Code 的交互线程；这里显式
	// 包含 appServer 和自动化来源，才能让 EasyAgent 自己创建的线程出现在历史中。
	params := map[string]any{"limit": limit, "archived": false, "sortKey": "updated_at", "sortDirection": "desc", "sourceKinds": []string{
		"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown",
	}}
	if cursor := strings.TrimSpace(request.URL.Query().Get("cursor")); cursor != "" {
		params["cursor"] = cursor
	}
	if search := strings.TrimSpace(request.URL.Query().Get("search")); search != "" {
		params["searchTerm"] = search
	}
	value, err := server.codexQueryWithParams(request.Context(), server.env.Workspace(), "thread/list", params)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (server *Server) getCodexThread(response http.ResponseWriter, request *http.Request) {
	value, err := server.codexQueryWithParams(request.Context(), server.env.Workspace(), "thread/read", map[string]any{"threadId": request.PathValue("id"), "includeTurns": true})
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, value)
}
