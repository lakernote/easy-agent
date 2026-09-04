package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

func (server *Server) cleanupWorktree(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	session, err := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if session.Status == "queued" || session.Status == "running" || session.Status == "paused" {
		writeError(response, http.StatusConflict, "任务仍在运行或等待中，不能清理 worktree")
		return
	}
	if server.tasks.has(id) {
		writeError(response, http.StatusConflict, "任务仍在结束处理中，请稍后再清理 worktree")
		return
	}
	shared, err := server.store.CountOtherSessionsUsingWorkspace(id, session.Workspace)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if shared > 0 {
		writeError(response, http.StatusConflict, "另有会话仍在使用这个 worktree；请保留工作树，避免后续继续任务时目录失效")
		return
	}
	notice, err := server.cleanupSessionWorktree(request.Context(), session)
	if err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	source := strings.TrimSpace(session.SourceWorkspace)
	if source == "" {
		source = session.Workspace
	}
	if err := server.store.SetSessionWorkspace(id, source, source, "", notice); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	server.writeSession(response, request, id, http.StatusOK)
}
