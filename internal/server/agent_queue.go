package server

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) enqueueTurn(id, userMessage string, attachments []store.Attachment, model store.ModelSettings) error {
	if server.tasks.has(id) {
		return errors.New("上一条任务正在结束，请稍后再发送")
	}
	if err := server.store.QueueSession(id, model.Model, time.Now()); err != nil {
		return err
	}
	if err := server.store.AppendMessage(id, store.Message{Role: "user", Content: userMessage, Attachments: attachments, ToolCalls: []store.ToolCall{}, CreatedAt: time.Now()}); err != nil {
		_ = server.store.FailSession(id, err, store.Usage{}, time.Now())
		return err
	}
	return server.startQueuedTurn(id, model)
}

func (server *Server) startQueuedTurn(id string, model store.ModelSettings) error {
	if server.tasks.has(id) {
		return errors.New("任务已在当前进程队列中")
	}
	if !server.beginBackground() {
		return errors.New("服务正在停止")
	}
	taskContext, taskCancel := context.WithCancel(server.ctx)
	taskToken := newID()
	server.tasks.set(id, taskToken, taskCancel)
	go func() {
		defer server.completeBackground()
		defer server.tasks.clear(id, taskToken)
		defer taskCancel()
		session, loadErr := server.store.LoadSessionWindow(id, 1, 1)
		if loadErr != nil {
			_ = server.store.FailSession(id, loadErr, store.Usage{}, time.Now())
			server.recordAutomationSessionResult(id, "failed", loadErr.Error())
			return
		}
		projectKey := server.taskConflictKey(session)
		if err := server.scheduler.acquire(taskContext, projectKey); err != nil {
			// 服务停机时保留尚未开始的 queued 任务，下一次启动会恢复；用户
			// 主动停止则 CancelSession 已经把状态改成 canceled。
			if server.ctx.Err() == nil {
				_ = server.store.FailSession(id, err, store.Usage{}, time.Now())
				server.recordAutomationSessionResult(id, "failed", err.Error())
			}
			return
		}
		defer server.scheduler.release(projectKey)
		if err := server.store.MarkRunning(id, time.Now()); err != nil {
			// 用户可能在任务刚获得执行槽时点击了停止，此时 canceled 状态应保留。
			_ = server.store.FailSession(id, err, store.Usage{}, time.Now())
			server.recordAutomationSessionResult(id, "failed", err.Error())
			return
		}
		server.recordAutomationSessionResult(id, "running", "")
		server.tasks.setProgress(id, "正在准备运行时")
		usage := store.Usage{}
		runtimeSettings, settingsErr := server.store.GetRuntimeSettings()
		if settingsErr != nil {
			_ = server.store.FailSession(id, settingsErr, usage, time.Now())
			server.recordAutomationSessionResult(id, "failed", settingsErr.Error())
			return
		}
		// 整轮上限属于两个 Runtime 共用的调度层。Codex 仍会把同一个上限传给
		// app-server，以便它主动 interrupt；EasyAgent 则由这里取消整个循环。
		model.TurnTimeoutSeconds = runtimeSettings.TurnTimeoutSeconds
		turnContext, turnCancel := context.WithTimeout(taskContext, time.Duration(runtimeSettings.TurnTimeoutSeconds)*time.Second)
		defer turnCancel()
		if err := server.executeSessionTurn(turnContext, id, model, &usage); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = errors.New("整轮任务超过配置的时间上限")
			}
			_ = server.store.FailSession(id, err, usage, time.Now())
			server.recordAutomationSessionResult(id, "failed", err.Error())
			return
		}
		server.recordAutomationSessionResult(id, "completed", "")
	}()
	return nil
}

func (server *Server) recordAutomationSessionResult(sessionID, status, errorMessage string) {
	_ = server.store.RecordAutomationSessionResult(sessionID, status, errorMessage, time.Now())
}

// taskConflictKey keeps worktrees parallel when the project only exposes the
// isolated repository. If a project adds any shared source folder, all of its
// sessions serialize because those extra roots are not worktree-isolated.
func (server *Server) taskConflictKey(session store.Session) string {
	if session.ProjectID == "" {
		return session.Workspace
	}
	project, err := server.store.GetProject(session.ProjectID)
	if err != nil {
		return session.Workspace
	}
	for _, directory := range project.Directories {
		path := filepath.Clean(directory)
		if path != filepath.Clean(session.SourceWorkspace) && path != filepath.Clean(session.Workspace) {
			return "project:" + session.ProjectID
		}
	}
	return session.Workspace
}

func (server *Server) resumeQueuedSessions() error {
	queued, err := server.store.ListQueuedSessions()
	if err != nil {
		return err
	}
	for _, session := range queued {
		model, err := server.store.GetModelSettingsByProfileID(session.ProfileID)
		if err != nil {
			_ = server.store.FailSession(session.ID, err, store.Usage{}, time.Now())
			continue
		}
		_ = server.startQueuedTurn(session.ID, model)
	}
	return nil
}

func isActiveSessionStatus(status string) bool {
	return status == "queued" || status == "running" || status == "paused"
}
