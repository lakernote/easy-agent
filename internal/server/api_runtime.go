package server

import (
	"net/http"

	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) saveRuntimeSettings(response http.ResponseWriter, request *http.Request) {
	var input store.RuntimeSettings
	if !decodeJSON(response, request, &input) {
		return
	}
	// 兼容只包含早期两个字段的客户端；新页面始终会提交完整设置。
	if input.TurnTimeoutSeconds == 0 {
		input.TurnTimeoutSeconds = store.DefaultTurnTimeoutSeconds
	}
	if input.SSEHeartbeatSeconds == 0 {
		input.SSEHeartbeatSeconds = store.DefaultSSEHeartbeatSeconds
	}
	if input.MaxConcurrentTasks < store.MinMaxConcurrentTasks || input.MaxConcurrentTasks > store.MaxMaxConcurrentTasks {
		writeError(response, http.StatusBadRequest, "并发任务数必须在 1 到 16 之间")
		return
	}
	if input.TurnTimeoutSeconds < store.MinTurnTimeoutSeconds || input.TurnTimeoutSeconds > store.MaxTurnTimeoutSeconds {
		writeError(response, http.StatusBadRequest, "整轮任务上限必须在 5 分钟到 24 小时之间")
		return
	}
	if input.SSEHeartbeatSeconds < store.MinSSEHeartbeatSeconds || input.SSEHeartbeatSeconds > store.MaxSSEHeartbeatSeconds {
		writeError(response, http.StatusBadRequest, "实时连接心跳必须在 5 到 60 秒之间")
		return
	}
	saved, err := server.store.SaveRuntimeSettings(input)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	server.scheduler.setLimit(saved.MaxConcurrentTasks)
	writeJSON(response, http.StatusOK, saved)
}
