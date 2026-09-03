package server

import (
	"net/http"
	"time"
)

func (server *Server) health(response http.ResponseWriter, request *http.Request) {
	if err := server.store.Ping(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "SQLite 不可用")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "name": "EasyAgent", "time": time.Now()})
}
