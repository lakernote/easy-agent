package server

import (
	"context"
	"errors"
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
	taskContext, taskCancel := context.WithCancel(server.context)
	taskToken := newID()
	server.tasks.set(id, taskToken, taskCancel)
	server.wait.Add(1)
	go func() {
		defer server.wait.Done()
		defer server.tasks.clear(id, taskToken)
		defer taskCancel()
		select {
		case server.semaphore <- struct{}{}:
			defer func() { <-server.semaphore }()
		case <-taskContext.Done():
			_ = server.store.FailSession(id, taskContext.Err(), store.Usage{}, time.Now())
			return
		}
		if err := server.store.MarkRunning(id, time.Now()); err != nil {
			// 用户可能在任务刚获得执行槽时点击了停止，此时 canceled 状态应保留。
			_ = server.store.FailSession(id, err, store.Usage{}, time.Now())
			return
		}
		server.tasks.setProgress(id, "正在准备运行时")
		usage := store.Usage{}
		// 不再给整轮 Agent 叠加一个固定总超时。每次模型请求和工具调用都有
		// 自己的超时，循环也有最大步数；用户还可以随时点击“停止”。固定总
		// 超时会让合法的多步任务在最后阶段被无故中断。
		if err := server.runAgentTurn(taskContext, id, model, &usage); err != nil {
			_ = server.store.FailSession(id, err, usage, time.Now())
		}
	}()
	return nil
}
