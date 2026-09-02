package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/codexruntime"
	"github.com/lakernote/easy-agent/internal/store"
)

type codexRuntimeStatus = codexruntime.Status

func (server *Server) detectCodex(ctx context.Context) codexRuntimeStatus {
	status := codexruntime.Detect(server.env)
	if status.Installed {
		return status
	}
	if ctx.Err() != nil {
		status.Message = "Codex Runtime 检测被取消"
	}
	return status
}

func (server *Server) runCodexTurn(ctx context.Context, session store.Session, settings store.ModelSettings, workspace string, usage *store.Usage) error {
	status := server.detectCodex(ctx)
	if !status.Installed {
		return errors.New(status.Message)
	}
	message := ""
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == "user" {
			message = strings.TrimSpace(session.Messages[index].Content)
			break
		}
	}
	if message == "" {
		return errors.New("Codex Runtime 没有找到本轮用户消息")
	}
	startedAt := time.Now()
	if err := server.store.AppendEvent(session.ID, store.Event{Kind: "codex_start", Turn: session.UserTurnCount, Status: "started", Name: settings.Model, Detail: "由 Codex app-server 接管工具、Skill、沙箱和会话历史", CreatedAt: startedAt}); err != nil {
		return fmt.Errorf("保存 Codex Trace: %w", err)
	}
	result, runErr := codexruntime.RunMessage(ctx, codexruntime.Config{
		Path: status.Path, Workspace: workspace, Model: settings.Model, ThreadID: session.ResponseID,
		Timeout: time.Duration(settings.RequestTimeoutSeconds) * time.Second,
		Env:     server.env.Environ(nil),
		OnDelta: func(delta string) { server.appendTaskPartial(session.ID, delta) },
		OnEvent: func(event codexruntime.Event) {
			_ = server.store.AppendEvent(session.ID, store.Event{Kind: event.Kind, Turn: session.UserTurnCount, Status: event.Status, Name: event.Name, Detail: event.Detail, Input: event.Input, Output: event.Output, DurationMS: event.Duration.Milliseconds(), CreatedAt: time.Now()})
		},
	}, message)
	if runErr != nil {
		_ = server.store.AppendEvent(session.ID, store.Event{Kind: "codex_end", Turn: session.UserTurnCount, Status: "error", Name: settings.Model, Detail: runErr.Error(), DurationMS: time.Since(startedAt).Milliseconds(), CreatedAt: time.Now()})
		return runErr
	}
	usage.ModelCalls++
	usage.ModelDurationMS += result.Duration.Milliseconds()
	usage.InputTokens += result.InputTokens
	usage.OutputTokens += result.OutputTokens
	usage.TotalTokens += result.TotalTokens
	if err := server.store.AppendMessage(session.ID, store.Message{Role: "assistant", Content: result.Answer, ToolCalls: []store.ToolCall{}, Attachments: []store.Attachment{}, CreatedAt: time.Now()}); err != nil {
		return fmt.Errorf("保存 Codex 回答: %w", err)
	}
	if err := server.store.AppendEvent(session.ID, store.Event{Kind: "codex_end", Turn: session.UserTurnCount, Status: "success", Name: settings.Model, Output: result.Answer, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, TotalTokens: result.TotalTokens, Protocol: "codex_app_server", DurationMS: result.Duration.Milliseconds(), CreatedAt: time.Now()}); err != nil {
		return fmt.Errorf("保存 Codex Trace: %w", err)
	}
	providerKey := strings.Join([]string{"codex", settings.Model, status.Path}, "|")
	return server.store.FinishSession(session.ID, result.ThreadID, providerKey, *usage, time.Now())
}
