package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

type messageRequest struct {
	Message     string              `json:"message"`
	Attachments []attachmentRequest `json:"attachments"`
	ProfileID   string              `json:"profileId,omitempty"`
	// Workspace 只在创建会话时使用；后续多轮对话始终沿用会话保存的工作区。
	Workspace string `json:"workspace,omitempty"`
}

func (server *Server) createSession(response http.ResponseWriter, request *http.Request) {
	var input messageRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	attachments, err := validateAttachments(input.Attachments)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if input.Message == "" && len(attachments) == 0 {
		writeError(response, http.StatusBadRequest, "请输入消息或添加附件")
		return
	}
	runEnvironment, err := server.env.WithWorkspace(input.Workspace)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	model, err := server.store.GetModelSettingsByProfileID(input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	id := newID()
	runtimeSettings, _ := server.store.GetRuntimeSettings()
	workspace := server.prepareSessionWorkspace(request.Context(), id, runEnvironment.Workspace(), runtimeSettings)
	if _, err := server.store.CreateSessionWithProfile(id, attachmentTitle(input.Message, attachments), model.Runtime, model.ProfileID, model.Model, workspace.Execution, time.Now()); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.store.SetSessionWorkspace(id, workspace.Execution, workspace.Source, workspace.Branch); err != nil {
		_ = server.store.DeleteSession(id)
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.enqueueTurn(id, input.Message, attachments, model); err != nil {
		_ = server.store.DeleteSession(id)
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, _ := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	value.RunProgress = server.tasks.progress(id)
	model = enrichOllamaContextWindow(request.Context(), model)
	decorateContext(&value, model)
	writeJSON(response, http.StatusAccepted, server.sessionView(value))
}

func (server *Server) continueSession(response http.ResponseWriter, request *http.Request) {
	var input messageRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	attachments, err := validateAttachments(input.Attachments)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if input.Message == "" && len(attachments) == 0 {
		writeError(response, http.StatusBadRequest, "请输入消息或添加附件")
		return
	}
	if strings.TrimSpace(input.Workspace) != "" {
		writeError(response, http.StatusBadRequest, "工作区在创建会话时确定；请新建会话后选择其他工作区")
		return
	}
	id := request.PathValue("id")
	// 这里只验证会话存在，避免继续对话前把完整历史加载两次。
	loaded, err := server.store.LoadSessionWindow(id, 1, 1)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	model, err := server.store.GetModelSettingsByProfileID(loaded.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.enqueueTurn(id, input.Message, attachments, model); err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, _ := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	value.RunProgress = server.tasks.progress(id)
	model = enrichOllamaContextWindow(request.Context(), model)
	decorateContext(&value, model)
	writeJSON(response, http.StatusAccepted, server.sessionView(value))
}

func (server *Server) deleteSession(response http.ResponseWriter, request *http.Request) {
	// 删除前只需要检查状态，不要为了一个状态字段把超长消息和 Trace 全量读入内存。
	value, err := server.store.LoadSessionWindow(request.PathValue("id"), 1, 1)
	if err == nil && (value.Status == "queued" || value.Status == "running") {
		writeError(response, http.StatusConflict, "Agent 正在运行，暂时不能删除")
		return
	}
	if err := server.store.DeleteSession(request.PathValue("id")); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) cancelSession(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	changed, err := server.store.CancelSession(id, time.Now())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if !changed {
		writeError(response, http.StatusConflict, "任务当前不在排队或运行中")
		return
	}
	server.tasks.cancel(id)
	value, err := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	settings, _ := server.store.GetModelSettingsByProfileID(value.ProfileID)
	if settings.Runtime == "" {
		settings, _ = server.store.GetModelSettings()
	}
	settings = enrichOllamaContextWindow(request.Context(), settings)
	decorateContext(&value, settings)
	value.RunProgress = server.tasks.progress(id)
	writeJSON(response, http.StatusOK, server.sessionView(value))
}

func (server *Server) pauseSession(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	changed, err := server.store.PauseQueuedSession(id, time.Now())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if !changed {
		writeError(response, http.StatusConflict, "只有仍在排队、尚未开始执行的任务可以安全暂停")
		return
	}
	server.tasks.cancel(id)
	settleContext, settleCancel := context.WithTimeout(request.Context(), 2*time.Second)
	_ = server.tasks.wait(settleContext, id)
	settleCancel()
	server.writeSession(response, request, id, http.StatusOK)
}

func (server *Server) resumeSession(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if server.tasks.has(id) {
		writeError(response, http.StatusConflict, "暂停操作正在完成，请稍后再继续")
		return
	}
	value, err := server.store.LoadSessionWindow(id, 1, 1)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	model, err := server.store.GetModelSettingsByProfileID(value.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	changed, err := server.store.ResumePausedSession(id, time.Now())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if !changed {
		writeError(response, http.StatusConflict, "任务当前不在暂停状态")
		return
	}
	if err := server.startQueuedTurn(id, model); err != nil {
		_ = server.store.FailSession(id, err, store.Usage{}, time.Now())
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	server.writeSession(response, request, id, http.StatusAccepted)
}

func (server *Server) writeSession(response http.ResponseWriter, request *http.Request, id string, status int) {
	value, err := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	settings, _ := server.store.GetModelSettingsByProfileID(value.ProfileID)
	if settings.Runtime == "" {
		settings, _ = server.store.GetModelSettings()
	}
	settings = enrichOllamaContextWindow(request.Context(), settings)
	decorateContext(&value, settings)
	value.RunProgress = server.tasks.progress(id)
	writeJSON(response, status, server.sessionView(value))
}
