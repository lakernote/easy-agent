package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) forkSession(response http.ResponseWriter, request *http.Request) {
	source, err := server.store.LoadSession(request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if source.Runtime != store.RuntimeCodex || strings.TrimSpace(source.ResponseID) == "" {
		writeError(response, http.StatusBadRequest, "只有已创建 Codex thread 的会话可以分支")
		return
	}
	if source.Status == "queued" || source.Status == "running" {
		writeError(response, http.StatusConflict, "任务运行中，完成后再创建分支")
		return
	}
	value, err := server.codexQueryWithParams(request.Context(), source.Workspace, "thread/fork", map[string]any{"threadId": source.ResponseID})
	if err != nil {
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	data, _ := json.Marshal(value)
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(data, &result) != nil || result.Thread.ID == "" {
		writeError(response, http.StatusBadGateway, "Codex thread/fork 未返回新 thread id")
		return
	}
	id := newID()
	if _, err := server.store.CreateSessionWithProfile(id, "分支 · "+source.Title, source.Runtime, source.ProfileID, source.Model, source.Workspace, time.Now()); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.store.SetSessionWorkspace(id, source.Workspace, source.SourceWorkspace, source.WorktreeBranch); err != nil {
		_ = server.store.DeleteSession(id)
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.store.SetSessionContinuation(id, result.Thread.ID); err != nil {
		_ = server.store.DeleteSession(id)
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	for _, message := range source.Messages {
		clone := message
		clone.ID = 0
		clone.Attachments = append([]store.Attachment(nil), message.Attachments...)
		for index := range clone.Attachments {
			clone.Attachments[index].ID = newID()
		}
		if err := server.store.AppendMessage(id, clone); err != nil {
			_ = server.store.DeleteSession(id)
			writeError(response, http.StatusInternalServerError, fmt.Sprintf("复制分支历史: %v", err))
			return
		}
	}
	forked, err := server.loadLiveSession(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusCreated, server.sessionView(forked))
}
