package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (server *Server) streamSession(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusNotImplemented, "当前 HTTP Server 不支持事件流")
		return
	}
	id := request.PathValue("id")
	value, err := server.loadLiveSession(request.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("Connection", "keep-alive")
	response.Header().Set("X-Accel-Buffering", "no")

	lastID, _ := strconv.ParseInt(strings.TrimSpace(request.Header.Get("Last-Event-ID")), 10, 64)
	writeSSE(response, "session", 0, server.sessionView(value))
	if lastID == 0 && len(value.Events) > 0 {
		lastID = value.Events[len(value.Events)-1].ID
	}
	flusher.Flush()

	ticker := time.NewTicker(350 * time.Millisecond)
	runtimeSettings, _ := server.store.GetRuntimeSettings()
	heartbeat := time.NewTicker(time.Duration(runtimeSettings.SSEHeartbeatSeconds) * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()
	lastState := ""
	for {
		select {
		case <-request.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(response, ": keep-alive\n\n")
			flusher.Flush()
		case <-ticker.C:
			for {
				events, eventErr := server.store.ListEventsAfter(id, lastID, 300)
				if eventErr != nil {
					writeSSE(response, "error", 0, map[string]string{"error": eventErr.Error()})
					flusher.Flush()
					return
				}
				for _, event := range events {
					writeSSE(response, "trace", event.ID, event)
					lastID = event.ID
				}
				if len(events) < 300 {
					break
				}
			}
			current, loadErr := server.loadLiveSession(request.Context(), id)
			if loadErr != nil {
				return
			}
			state := fmt.Sprintf("%s|%s|%s|%d|%d|%d", current.Status, current.PartialOutput, current.RunProgress, current.MessageCount, current.EventCount, current.UpdatedAt.UnixNano())
			if state != lastState {
				writeSSE(response, "session", 0, server.sessionView(current))
				lastState = state
			}
			flusher.Flush()
			if current.Status != "queued" && current.Status != "running" {
				return
			}
		}
	}
}

func writeSSE(response http.ResponseWriter, event string, id int64, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	if id > 0 {
		_, _ = fmt.Fprintf(response, "id: %d\n", id)
	}
	_, _ = fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event, data)
}
