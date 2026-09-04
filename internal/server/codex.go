package server

import (
	"context"
	"encoding/json"
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
	if !status.AppServerAvailable {
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
	server.tasks.setProgress(session.ID, "Codex · 启动 app-server")
	if err := server.store.AppendEvent(session.ID, store.Event{Kind: "codex_start", Turn: session.UserTurnCount, Status: "started", Name: settings.Model, Detail: "由 Codex app-server 接管工具、Skill、沙箱和会话历史", CreatedAt: startedAt}); err != nil {
		return fmt.Errorf("保存 Codex Trace: %w", err)
	}
	turnTimeoutSeconds := settings.TurnTimeoutSeconds
	if turnTimeoutSeconds <= 0 {
		turnTimeoutSeconds = store.DefaultCodexTurnTimeoutSeconds
	}
	var lastProgressAt time.Time
	var lastProgressName string
	result, runErr := codexruntime.RunMessage(ctx, codexruntime.Config{
		Path: status.Path, Workspace: workspace, Model: settings.Model, ThreadID: session.ResponseID,
		Timeout: time.Duration(turnTimeoutSeconds) * time.Second,
		Env:     server.codexEnvironment(),
		OnDelta: func(delta string) { server.tasks.appendPartial(session.ID, delta) },
		OnUsage: func(value codexruntime.Usage) {
			server.tasks.setUsage(session.ID, store.Usage{
				InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
				CachedTokens: value.CachedInputTokens, CacheWriteTokens: value.CacheWriteInputTokens,
				TotalTokens: value.TotalTokens, ModelCalls: 1, CacheReported: value.Reported,
				ContextWindowTokens: value.ModelContextWindow,
			})
		},
		OnEvent: func(event codexruntime.Event) {
			server.tasks.setProgress(session.ID, codexProgress(event))
			// Delta-heavy Codex notifications are useful for the live status, but
			// persisting every one of them makes the trace noisy and grows SQLite
			// much faster than the user can inspect it. Keep a responsive status
			// while sampling repeated progress events.
			if event.Kind == "codex_progress" && event.Name == lastProgressName && !lastProgressAt.IsZero() && time.Since(lastProgressAt) < time.Second {
				return
			}
			if event.Kind == "codex_progress" {
				lastProgressAt = time.Now()
				lastProgressName = event.Name
			}
			_ = server.store.AppendEvent(session.ID, store.Event{Kind: event.Kind, Turn: session.UserTurnCount, Status: event.Status, Name: event.Name, Detail: event.Detail, Input: event.Input, Output: event.Output, DurationMS: event.Duration.Milliseconds(), CreatedAt: time.Now()})
		},
		OnServerRequest: func(request codexruntime.ServerRequest) (any, error) {
			return server.awaitCodexRequest(ctx, session.ID, request)
		},
	}, message)
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) {
			runErr = fmt.Errorf("Codex 整轮任务超过 %d 秒上限: %w", turnTimeoutSeconds, runErr)
		}
		_ = server.store.AppendEvent(session.ID, store.Event{Kind: "codex_end", Turn: session.UserTurnCount, Status: "error", Name: settings.Model, Detail: runErr.Error(), DurationMS: time.Since(startedAt).Milliseconds(), CreatedAt: time.Now()})
		return runErr
	}
	usage.ModelCalls++
	usage.ModelDurationMS += result.Duration.Milliseconds()
	usage.InputTokens += result.Usage.InputTokens
	usage.OutputTokens += result.Usage.OutputTokens
	usage.CachedTokens += result.Usage.CachedInputTokens
	usage.CacheWriteTokens += result.Usage.CacheWriteInputTokens
	usage.TotalTokens += result.Usage.TotalTokens
	usage.CacheReported = usage.CacheReported || result.Usage.Reported
	usage.ContextWindowTokens = result.Usage.ModelContextWindow
	if result.Usage.Reported {
		_ = server.store.AppendEvent(session.ID, store.Event{
			Kind: "codex_usage", Turn: session.UserTurnCount, Status: "success", Name: settings.Model,
			Detail: "thread/tokenUsage/updated · 本轮用量", InputTokens: result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens, CachedTokens: result.Usage.CachedInputTokens,
			CacheWriteTokens: result.Usage.CacheWriteInputTokens, CacheReported: true,
			TotalTokens: result.Usage.TotalTokens, ContextWindowTokens: result.Usage.ModelContextWindow,
			Protocol: "codex_app_server", CreatedAt: time.Now(),
		})
	}
	if err := server.store.AppendMessage(session.ID, store.Message{Role: "assistant", Content: result.Answer, ToolCalls: []store.ToolCall{}, Attachments: []store.Attachment{}, CreatedAt: time.Now()}); err != nil {
		return fmt.Errorf("保存 Codex 回答: %w", err)
	}
	if err := server.store.AppendEvent(session.ID, store.Event{Kind: "codex_end", Turn: session.UserTurnCount, Status: "success", Name: settings.Model, Output: result.Answer, Protocol: "codex_app_server", DurationMS: result.Duration.Milliseconds(), CreatedAt: time.Now()}); err != nil {
		return fmt.Errorf("保存 Codex Trace: %w", err)
	}
	providerKey := strings.Join([]string{"codex", settings.Model, status.Path}, "|")
	return server.store.FinishSession(session.ID, result.ThreadID, providerKey, *usage, time.Now())
}

func (server *Server) awaitCodexRequest(ctx context.Context, sessionID string, request codexruntime.ServerRequest) (any, error) {
	pending := pendingCodexRequest{ID: string(request.ID), Method: request.Method, Params: append(json.RawMessage(nil), request.Params...), Response: make(chan pendingCodexResponse, 1)}
	if err := server.tasks.setPending(sessionID, pending); err != nil {
		return nil, err
	}
	defer server.tasks.clearPending(sessionID)
	_ = server.store.AppendEvent(sessionID, store.Event{Kind: "codex_request", Status: "waiting", Name: request.Method, Detail: "等待 UI 处理 app-server 反向请求", Input: string(request.Params), Protocol: "codex_app_server", CreatedAt: time.Now()})
	select {
	case response := <-pending.Response:
		if response.Err != nil {
			return nil, response.Err
		}
		return response.Value, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func codexProgress(event codexruntime.Event) string {
	if event.Detail != "" {
		return "Codex · " + event.Detail
	}
	switch event.Name {
	case "agentMessage":
		return "Codex · 整理回答"
	case "reasoning":
		return "Codex · 分析任务"
	case "commandExecution":
		return "Codex · 执行命令"
	case "fileChange":
		return "Codex · 处理文件变更"
	case "mcpToolCall", "dynamicToolCall":
		return "Codex · 调用工具"
	case "webSearch":
		return "Codex · 联网搜索"
	case "imageView":
		return "Codex · 查看图片"
	case "enteredReviewMode":
		return "Codex · 进入审查模式"
	case "exitedReviewMode":
		return "Codex · 退出审查模式"
	case "collabToolCall":
		return "Codex · 协作 Agent"
	case "turn":
		return "Codex · 处理本轮任务"
	default:
		return "Codex · 处理中"
	}
}
