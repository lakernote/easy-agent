package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/store"
)

type messageRequest struct {
	Message     string              `json:"message"`
	Attachments []attachmentRequest `json:"attachments"`
	ProfileID   string              `json:"profileId,omitempty"`
	ProjectID   string              `json:"projectId,omitempty"`
	// Workspace 只在创建会话时使用；后续多轮对话始终沿用会话保存的工作区。
	Workspace string `json:"workspace,omitempty"`
}

type updateSessionRequest struct {
	Title     *string `json:"title"`
	ProjectID *string `json:"projectId"`
}

func (server *Server) updateSession(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	current, err := server.store.LoadSessionWindow(id, 1, 1)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	var input updateSessionRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	if input.Title == nil && input.ProjectID == nil {
		writeError(response, http.StatusBadRequest, "缺少需要更新的会话信息")
		return
	}
	title := current.Title
	if input.Title != nil {
		title = strings.TrimSpace(*input.Title)
	}
	if title == "" || utf8.RuneCountInString(title) > 120 || containsControl(title) {
		writeError(response, http.StatusBadRequest, "会话名称必须为 1 到 120 个有效字符")
		return
	}
	projectID := current.ProjectID
	if input.ProjectID != nil {
		projectID = strings.TrimSpace(*input.ProjectID)
		if projectID != current.ProjectID {
			if isActiveSessionStatus(current.Status) {
				writeError(response, http.StatusConflict, "任务正在处理或暂停时不能更换项目")
				return
			}
			if projectID != "" {
				project, err := server.store.GetProject(projectID)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						writeError(response, http.StatusBadRequest, "所选项目不存在")
					} else {
						writeError(response, http.StatusInternalServerError, err.Error())
					}
					return
				}
				if !projectContainsSessionSource(project, current) {
					writeError(response, http.StatusConflict, "目标项目不包含会话原来的源文件夹")
					return
				}
			}
		}
	}
	if title == current.Title && projectID == current.ProjectID {
		server.writeSessionResponse(response, request, id, http.StatusOK)
		return
	}
	if err := server.store.UpdateSessionMetadata(id, title, projectID); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	server.writeSessionResponse(response, request, id, http.StatusOK)
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
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
	projectID, workspacePath, err := server.resolveNewSessionProject(input.ProjectID, input.Workspace)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	runEnvironment, err := server.env.WithWorkspace(workspacePath)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	model, err := server.store.GetModelSettingsByProfileID(input.ProfileID)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	id := newID()
	runtimeSettings, err := server.store.GetRuntimeSettings()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := validateModel(model.WithDefaults()); err != nil {
		writeError(response, http.StatusBadRequest, "当前模型配置不可用："+err.Error())
		return
	}
	workspace := server.prepareSessionWorkspace(request.Context(), id, runEnvironment.Workspace(), runtimeSettings)
	if _, err := server.store.CreateSession(store.CreateSessionParams{
		ID: id, Title: attachmentTitle(input.Message, attachments), Runtime: model.Runtime,
		ProfileID: model.ProfileID, Model: model.Model, ProjectID: projectID,
		Workspace: workspace.Execution, CreatedAt: time.Now(),
	}); err != nil {
		server.discardPreparedWorkspace(workspace)
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.store.SetSessionWorkspace(id, workspace.Execution, workspace.Source, workspace.Branch, workspace.Notice); err != nil {
		_ = server.store.DeleteSession(id)
		server.discardPreparedWorkspace(workspace)
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.enqueueTurn(id, input.Message, attachments, model); err != nil {
		_ = server.store.DeleteSession(id)
		server.discardPreparedWorkspace(workspace)
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, err := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	value.RunProgress = server.tasks.progress(id)
	model = enrichOllamaContextWindow(request.Context(), model)
	decorateContext(&value, model)
	writeJSON(response, http.StatusAccepted, server.sessionView(value))
}

func (server *Server) resolveNewSessionProject(projectID, workspace string) (string, string, error) {
	projectID = strings.TrimSpace(projectID)
	workspace = strings.TrimSpace(workspace)
	if projectID != "" {
		project, err := server.store.GetProject(projectID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", "", errors.New("所选项目不存在")
			}
			return "", "", err
		}
		if len(project.Directories) == 0 {
			return "", "", errors.New("所选项目没有源文件夹")
		}
		return project.ID, project.Directories[0], nil
	}
	if workspace != "" {
		return "", workspace, nil
	}
	if err := server.ensureDefaultProject(); err != nil {
		return "", "", err
	}
	project, err := server.store.DefaultProject()
	if err != nil || len(project.Directories) == 0 {
		return "", "", errors.New("默认项目没有可用的源文件夹")
	}
	return project.ID, project.Directories[0], nil
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
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateModel(model.WithDefaults()); err != nil {
		writeError(response, http.StatusBadRequest, "当前模型配置不可用："+err.Error())
		return
	}
	if err := server.enqueueTurn(id, input.Message, attachments, model); err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, err := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	value.RunProgress = server.tasks.progress(id)
	model = enrichOllamaContextWindow(request.Context(), model)
	decorateContext(&value, model)
	writeJSON(response, http.StatusAccepted, server.sessionView(value))
}

func projectContainsSessionSource(project store.Project, session store.Session) bool {
	source := strings.TrimSpace(session.SourceWorkspace)
	if source == "" {
		source = strings.TrimSpace(session.Workspace)
	}
	if source == "" {
		return false
	}
	source = filepath.Clean(source)
	for _, directory := range project.Directories {
		if filepath.Clean(directory) == source {
			return true
		}
	}
	return false
}

func (server *Server) deleteSession(response http.ResponseWriter, request *http.Request) {
	// 删除前只需要检查状态，不要为了一个状态字段把超长消息和 Trace 全量读入内存。
	id := request.PathValue("id")
	value, err := server.store.LoadSessionWindow(id, 1, 1)
	if err == nil && (value.Status == "queued" || value.Status == "running") {
		writeError(response, http.StatusConflict, "Agent 正在运行，暂时不能删除")
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err == nil && value.WorktreeBranch != "" {
		shared, countErr := server.store.CountOtherSessionsUsingWorkspace(id, value.Workspace)
		if countErr != nil {
			writeError(response, http.StatusInternalServerError, countErr.Error())
			return
		}
		if shared == 0 {
			if _, cleanupErr := server.cleanupSessionWorktree(request.Context(), value); cleanupErr != nil {
				writeError(response, http.StatusConflict, "删除会话前无法安全清理 worktree："+cleanupErr.Error())
				return
			}
		}
	}
	if err := server.store.DeleteSession(id); err != nil {
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
	server.recordAutomationSessionResult(id, "canceled", "任务已停止")
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
	server.writeSessionResponse(response, request, id, http.StatusOK)
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
	server.writeSessionResponse(response, request, id, http.StatusAccepted)
}

func (server *Server) writeSessionResponse(response http.ResponseWriter, request *http.Request, id string, status int) {
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
