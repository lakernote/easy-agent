package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/store"
)

type automationTaskRequest struct {
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	ProjectID       string `json:"projectId"`
	ProfileID       string `json:"profileId,omitempty"`
	TriggerType     string `json:"triggerType"`
	IntervalMinutes int    `json:"intervalMinutes,omitempty"`
	Repeat          string `json:"repeat,omitempty"`
	ScheduleTime    string `json:"scheduleTime,omitempty"`
	ScheduleWeekday int    `json:"scheduleWeekday,omitempty"`
	Enabled         bool   `json:"enabled"`
}

type automationTaskRunResponse struct {
	Task      store.AutomationTask `json:"task"`
	SessionID string               `json:"sessionId"`
}

var supportedAutomationIntervals = map[int]struct{}{60: {}, 360: {}, 1440: {}, 10080: {}}

func (server *Server) listAutomationTasks(response http.ResponseWriter, request *http.Request) {
	values, err := server.store.ListAutomationTasks()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, values)
}

func (server *Server) createAutomationTask(response http.ResponseWriter, request *http.Request) {
	var input automationTaskRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	value, err := server.normalizeAutomationTask(input, time.Now())
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	value.ID = newID()
	value.CreatedAt = time.Now()
	value.UpdatedAt = value.CreatedAt
	created, err := server.store.CreateAutomationTask(value)
	if err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (server *Server) updateAutomationTask(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	current, err := server.store.GetAutomationTask(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "自动化任务不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	var input automationTaskRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	value, err := server.normalizeAutomationTask(input, time.Now())
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	value.ID = current.ID
	value.CreatedAt = current.CreatedAt
	value.UpdatedAt = time.Now()
	value.LastRunAt = current.LastRunAt
	value.LastStatus = current.LastStatus
	value.LastError = current.LastError
	value.LastSessionID = current.LastSessionID
	updated, err := server.store.UpdateAutomationTask(value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "自动化任务不存在")
		} else {
			writeError(response, http.StatusConflict, err.Error())
		}
		return
	}
	writeJSON(response, http.StatusOK, updated)
}

func (server *Server) deleteAutomationTask(response http.ResponseWriter, request *http.Request) {
	if err := server.store.DeleteAutomationTask(request.PathValue("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "自动化任务不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) runAutomationTask(response http.ResponseWriter, request *http.Request) {
	task, err := server.store.GetAutomationTask(request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "自动化任务不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if !task.Enabled {
		writeError(response, http.StatusConflict, "请先启用自动化任务")
		return
	}
	sessionID, err := server.triggerAutomationTask(request.Context(), task)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := server.store.GetAutomationTask(task.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusAccepted, automationTaskRunResponse{Task: updated, SessionID: sessionID})
}

func (server *Server) normalizeAutomationTask(input automationTaskRequest, now time.Time) (store.AutomationTask, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || utf8.RuneCountInString(name) > 80 || containsControl(name) {
		return store.AutomationTask{}, errors.New("任务名称必须为 1 到 80 个有效字符")
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" || utf8.RuneCountInString(prompt) > 50000 || containsControl(prompt) {
		return store.AutomationTask{}, errors.New("Prompt 必须为 1 到 50000 个有效字符")
	}
	triggerType := strings.TrimSpace(input.TriggerType)
	if triggerType == "" {
		triggerType = "manual"
	}
	if triggerType != "manual" && triggerType != "interval" {
		return store.AutomationTask{}, errors.New("暂不支持这种触发方式")
	}
	if triggerType == "interval" {
		if input.Repeat == "" || input.Repeat == "interval" {
			input.Repeat = "interval"
			if _, ok := supportedAutomationIntervals[input.IntervalMinutes]; !ok {
				return store.AutomationTask{}, errors.New("请选择有效的定时间隔")
			}
		}
	} else {
		input.IntervalMinutes = 0
		input.Repeat = "interval"
	}
	repeat, scheduleTime, scheduleWeekday, err := normalizeAutomationSchedule(strings.TrimSpace(input.Repeat), strings.TrimSpace(input.ScheduleTime), input.ScheduleWeekday)
	if err != nil {
		return store.AutomationTask{}, err
	}
	projectID := strings.TrimSpace(input.ProjectID)
	project, err := server.store.GetProject(projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.AutomationTask{}, errors.New("请选择有效的项目")
		}
		return store.AutomationTask{}, err
	}
	if len(project.Directories) == 0 {
		return store.AutomationTask{}, errors.New("所选项目没有源文件夹")
	}
	workspace := project.Directories[0]
	environment, err := server.env.WithWorkspace(workspace)
	if err != nil {
		return store.AutomationTask{}, fmt.Errorf("工作目录不可用: %w", err)
	}
	workspace = filepath.Clean(environment.Workspace())
	found := false
	for _, directory := range project.Directories {
		if filepath.Clean(directory) == workspace {
			found = true
			break
		}
	}
	if !found {
		return store.AutomationTask{}, errors.New("工作目录必须属于所选项目")
	}
	if input.ProfileID != "" {
		if _, err := server.store.GetModelSettingsByProfileID(input.ProfileID); err != nil {
			return store.AutomationTask{}, errors.New("所选模型配置不存在")
		}
	}
	nextRunAt := (*time.Time)(nil)
	if input.Enabled && triggerType == "interval" {
		next, err := nextAutomationRun(now, repeat, input.IntervalMinutes, scheduleWeekday, scheduleTime)
		if err != nil {
			return store.AutomationTask{}, err
		}
		nextRunAt = &next
	}
	return store.AutomationTask{Name: name, Prompt: prompt, ProjectID: projectID, Workspace: workspace, ProfileID: strings.TrimSpace(input.ProfileID), TriggerType: triggerType, IntervalMinutes: input.IntervalMinutes, Repeat: repeat, ScheduleTime: scheduleTime, ScheduleWeekday: scheduleWeekday, Enabled: input.Enabled, NextRunAt: nextRunAt}, nil
}

func (server *Server) triggerAutomationTask(ctx context.Context, task store.AutomationTask) (string, error) {
	now := time.Now()
	var nextRunAt *time.Time
	if task.Enabled && task.TriggerType == "interval" {
		repeat, scheduleTime, scheduleWeekday, err := normalizeAutomationSchedule(task.Repeat, task.ScheduleTime, task.ScheduleWeekday)
		if err != nil {
			return "", err
		}
		next, err := nextAutomationRun(now, repeat, task.IntervalMinutes, scheduleWeekday, scheduleTime)
		if err != nil {
			return "", err
		}
		nextRunAt = &next
	}
	marked, err := server.store.MarkAutomationTaskRun(task.ID, now, nextRunAt)
	if err != nil {
		return "", err
	}
	if !marked {
		return "", errors.New("任务已停用或正在被其他调度器执行")
	}
	sessionID, err := server.startAutomationSession(ctx, task)
	if err != nil {
		_ = server.store.RecordAutomationTaskResult(task.ID, "failed", err.Error(), "", time.Now())
		return "", err
	}
	_ = server.store.RecordAutomationTaskResult(task.ID, "queued", "", sessionID, time.Now())
	return sessionID, nil
}

func (server *Server) startAutomationSession(ctx context.Context, task store.AutomationTask) (string, error) {
	environment, err := server.env.WithWorkspace(task.Workspace)
	if err != nil {
		return "", fmt.Errorf("工作目录不可用: %w", err)
	}
	model, err := server.store.GetModelSettingsByProfileID(task.ProfileID)
	if err != nil {
		return "", err
	}
	model = model.WithDefaults()
	if err := validateModel(model); err != nil {
		return "", fmt.Errorf("当前模型配置不可用: %w", err)
	}
	runtimeSettings, err := server.store.GetRuntimeSettings()
	if err != nil {
		return "", err
	}
	id := newID()
	workspace := server.prepareSessionWorkspace(ctx, id, environment.Workspace(), runtimeSettings)
	if _, err := server.store.CreateSession(store.CreateSessionParams{ID: id, Title: "自动化 · " + task.Name, Runtime: model.Runtime, ProfileID: model.ProfileID, Model: model.Model, ProjectID: task.ProjectID, Workspace: workspace.Execution, CreatedAt: time.Now()}); err != nil {
		server.discardPreparedWorkspace(workspace)
		return "", err
	}
	if err := server.store.SetSessionWorkspace(id, workspace.Execution, workspace.Source, workspace.Branch, workspace.Notice); err != nil {
		_ = server.store.DeleteSession(id)
		server.discardPreparedWorkspace(workspace)
		return "", err
	}
	if err := server.enqueueTurn(id, task.Prompt, nil, model); err != nil {
		_ = server.store.DeleteSession(id)
		server.discardPreparedWorkspace(workspace)
		return "", err
	}
	return id, nil
}
