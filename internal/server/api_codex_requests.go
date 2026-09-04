package server

import (
	"encoding/json"
	"net/http"

	"github.com/lakernote/easy-agent/internal/store"
)

type codexRequestResponse struct {
	RequestID string          `json:"requestId"`
	Response  json.RawMessage `json:"response"`
}

func (server *Server) resolveCodexRequest(response http.ResponseWriter, request *http.Request) {
	var input codexRequestResponse
	if !decodeJSON(response, request, &input) {
		return
	}
	if input.RequestID == "" || len(input.Response) == 0 {
		writeError(response, http.StatusBadRequest, "requestId 和 response 不能为空")
		return
	}
	var value any
	if err := json.Unmarshal(input.Response, &value); err != nil {
		writeError(response, http.StatusBadRequest, "response 必须是合法 JSON")
		return
	}
	id := request.PathValue("id")
	if !server.tasks.resolvePending(id, input.RequestID, value) {
		writeError(response, http.StatusConflict, "该 Codex 请求已处理或已失效")
		return
	}
	_ = server.store.AppendEvent(id, store.Event{Kind: "codex_request", Status: "resolved", Name: input.RequestID, Detail: "UI 已回复 app-server 反向请求", Protocol: "codex_app_server"})
	response.WriteHeader(http.StatusNoContent)
}
