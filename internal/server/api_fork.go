package server

import (
	"context"
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
	var input struct {
		WorkspaceMode string `json:"workspaceMode"`
	}
	if request.ContentLength != 0 && !decodeJSON(response, request, &input) {
		return
	}
	if input.WorkspaceMode == "" {
		input.WorkspaceMode = "shared"
	}
	if input.WorkspaceMode != "shared" && input.WorkspaceMode != "worktree" {
		writeError(response, http.StatusBadRequest, "workspaceMode 只能是 shared 或 worktree")
		return
	}
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
	if source.Status == "queued" || source.Status == "running" || source.Status == "paused" {
		writeError(response, http.StatusConflict, "任务运行中，完成后再创建分支")
		return
	}
	id := newID()
	workspace := sessionWorkspace{Execution: source.Workspace, Source: source.SourceWorkspace, Branch: source.WorktreeBranch, Notice: source.WorkspaceNotice}
	createdWorktree := false
	if input.WorkspaceMode == "worktree" {
		workspace, err = server.createForkWorkspace(request.Context(), id, source)
		if err != nil {
			writeError(response, http.StatusConflict, "无法创建独立 worktree："+err.Error())
			return
		}
		createdWorktree = true
	} else {
		workspace.Notice = "该对话分支复用源会话的工作区；两个会话会按同一执行目录串行"
	}
	rollbackWorkspace := func() {
		if !createdWorktree {
			return
		}
		_, _ = server.cleanupSessionWorktree(context.Background(), store.Session{Workspace: workspace.Execution, SourceWorkspace: workspace.Source, WorktreeBranch: workspace.Branch})
	}
	value, err := server.codexQueryWithParams(request.Context(), source.Workspace, "thread/fork", map[string]any{"threadId": source.ResponseID})
	if err != nil {
		rollbackWorkspace()
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
		rollbackWorkspace()
		writeError(response, http.StatusBadGateway, "Codex thread/fork 未返回新 thread id")
		return
	}
	titlePrefix := "分支 · "
	if createdWorktree {
		titlePrefix = "独立分支 · "
	}
	if _, err := server.store.CreateSessionWithProject(id, titlePrefix+source.Title, source.Runtime, source.ProfileID, source.Model, source.ProjectID, workspace.Execution, time.Now()); err != nil {
		rollbackWorkspace()
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.store.SetSessionWorkspace(id, workspace.Execution, workspace.Source, workspace.Branch, workspace.Notice); err != nil {
		_ = server.store.DeleteSession(id)
		rollbackWorkspace()
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.store.SetSessionContinuation(id, result.Thread.ID); err != nil {
		_ = server.store.DeleteSession(id)
		rollbackWorkspace()
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
			rollbackWorkspace()
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
