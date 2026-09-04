package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/codexruntime"
)

func (server *Server) getCodex(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, server.detectCodex(request.Context()))
}

func (server *Server) codexQuery(request *http.Request, method string) (any, error) {
	status := server.detectCodex(request.Context())
	if !status.Installed || !status.AppServerAvailable {
		return nil, &codexUnavailableError{message: status.Message}
	}
	result, err := codexruntime.Call(request.Context(), codexruntime.Config{Path: status.Path, Workspace: server.env.Workspace(), Timeout: 20 * time.Second, Env: server.codexEnvironment()}, method, map[string]any{})
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
