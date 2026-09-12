package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/codexruntime"
	"github.com/lakernote/easy-agent/internal/store"
)

type codexRuntimeStatus = codexruntime.Status

const codexDeveloperInstructions = "使用与用户最新请求相同的语言回答；用户使用中文时必须使用中文。不要在最终回答中输出内部计划、翻译提示或推理草稿。用户明确指定语言或格式时，遵循用户要求。"

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

func (server *Server) runCodexTurn(ctx context.Context, session store.Session, settings store.ModelSettings, workspace string, directories []string, usage *store.Usage) error {
	status := server.detectCodex(ctx)
	if !status.Installed {
		return errors.New(status.Message)
	}
	if !status.AppServerAvailable {
		return errors.New(status.Message)
	}
	runtimeSettings, err := server.store.GetRuntimeSettings()
	if err != nil {
		return err
	}
	policy := session.Permissions
	if policy.Mode == "" {
		policy = runtimeSettings.Permissions
	}
	message := ""
	var attachments []store.Attachment
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == "user" {
			message = strings.TrimSpace(session.Messages[index].Content)
			attachments = session.Messages[index].Attachments
			break
		}
	}
	if message == "" && len(attachments) == 0 {
		return errors.New("Codex Runtime 没有找到本轮用户消息")
	}
	startedAt := time.Now()
	skillRefs, capabilityEnv, err := server.syncCodexCapabilities()
	if err != nil {
		return err
	}
	selectedSkillsForTurn := selectedCodexSkills(session.Messages, skillRefs)
	environment, err := server.codexEnvironmentWith(capabilityEnv)
	if err != nil {
		return err
	}
	codexAttachments := make([]codexruntime.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.Kind == "audio" {
			continue
		}
		codexAttachments = append(codexAttachments, codexruntime.Attachment{Name: attachment.Name, MIMEType: attachment.MIMEType, Kind: attachment.Kind, Data: attachment.Data})
	}
	selectedSkillNamesForTurn := make([]string, 0, len(selectedSkillsForTurn))
	for _, skill := range selectedSkillsForTurn {
		selectedSkillNamesForTurn = append(selectedSkillNamesForTurn, skill.Name)
	}
	server.tasks.setProgress(session.ID, "Codex · 启动 app-server")
	if err := server.store.AppendEvent(session.ID, store.Event{Kind: "codex_start", Turn: session.UserTurnCount, Status: "started", Name: settings.Model, Detail: "由 Codex app-server 接管工具、Skill、沙箱和会话历史", CreatedAt: startedAt}); err != nil {
		return fmt.Errorf("保存 Codex Trace: %w", err)
	}
	if err := server.appendSelectedCapabilityEvents(session.ID, session.UserTurnCount, selectedSkillNamesForTurn, selectedMCPIDs(session.Messages)); err != nil {
		return err
	}
	turnTimeoutSeconds := settings.TurnTimeoutSeconds
	if turnTimeoutSeconds <= 0 {
		turnTimeoutSeconds = store.DefaultCodexTurnTimeoutSeconds
	}
	var lastProgressAt time.Time
	var lastProgressName string
	var observedUsage codexruntime.Usage
	effectiveModel := strings.TrimSpace(settings.Model)
	completedActivities := make(map[string]struct{})
	activityIDs := newCodexActivityResolver()
	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	var persistenceErr error
	recordPersistenceError := func(err error) {
		if err != nil && persistenceErr == nil {
			persistenceErr = err
			cancelRuntime()
		}
	}
	result, runErr := codexruntime.RunMessage(runtimeContext, codexruntime.Config{
		Path: status.Path, Workspace: workspace, AdditionalDirectories: directories, Model: settings.Model, Provider: settings.Provider, ThreadID: session.ResponseID,
		DeveloperInstructions: codexDeveloperInstructions,
		Timeout:               time.Duration(turnTimeoutSeconds) * time.Second,
		Permissions:           policy,
		Env:                   environment,
		Skills:                selectedSkillsForTurn,
		Attachments:           codexAttachments,
		OnDelta:               func(delta string) { server.tasks.appendPartial(session.ID, delta) },
		OnThreadStarted: func(info codexruntime.ThreadInfo) {
			if model := strings.TrimSpace(info.Model); model != "" {
				effectiveModel = model
				_ = server.store.SetSessionModel(session.ID, model)
			}
		},
		OnUsage: func(value codexruntime.Usage) {
			observedUsage = value
			server.tasks.setUsage(session.ID, store.Usage{
				InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
				CachedTokens: value.CachedInputTokens, CacheWriteTokens: value.CacheWriteInputTokens,
				TotalTokens: value.TotalTokens, ModelCalls: 1, CacheReported: value.Reported,
				ContextWindowTokens: value.ModelContextWindow,
				ToolCalls:           usage.ToolCalls, ToolDurationMS: usage.ToolDurationMS,
			})
		},
		OnEvent: func(event codexruntime.Event) {
			server.tasks.setProgress(session.ID, codexProgress(event))
			if event.Kind == "codex_item" && (event.ActivityKind == "tool" || event.ActivityKind == "mcp") {
				identity := activityIDs.resolve(event)
				event.ActivityID = identity.ID
				now := time.Now()
				operation := store.ToolOperation{
					Runtime: store.RuntimeCodex, Turn: session.UserTurnCount,
					Guarantee:  identity.Guarantee,
					ActivityID: event.ActivityID, Name: event.Name, ActivityKind: event.ActivityKind,
					ActivitySource: event.ActivitySource, DisplayName: event.DisplayName,
					Input: redactTraceAttachmentData(event.Input), Output: redactTraceAttachmentData(event.Output),
					StartedAt: now,
				}
				if event.Status == "started" {
					recordPersistenceError(server.store.BeginToolOperation(session.ID, operation))
				} else {
					operation.StartedAt = now.Add(-event.Duration)
					operation.CompletedAt = now
					operation.Status = store.ToolOperationSucceeded
					if event.Status == "error" {
						operation.Status = store.ToolOperationFailed
						operation.Error = event.Detail
					}
					recordPersistenceError(server.store.SettleToolOperation(session.ID, operation))
				}
			}
			if isCompletedCodexBusinessActivity(event) {
				key := strings.TrimSpace(event.ActivityID)
				if key == "" {
					key = fmt.Sprintf("%s\x00%s\x00%d", event.Name, event.Input, len(completedActivities))
				}
				if _, counted := completedActivities[key]; !counted {
					completedActivities[key] = struct{}{}
					usage.ToolCalls++
					usage.ToolDurationMS += event.Duration.Milliseconds()
					liveUsage := server.tasks.usage(session.ID)
					liveUsage.ToolCalls = usage.ToolCalls
					liveUsage.ToolDurationMS = usage.ToolDurationMS
					server.tasks.setUsage(session.ID, liveUsage)
				}
			}
			// Delta-heavy Codex notifications are useful for the live status, but
			// persisting every one of them makes the trace noisy and grows SQLite
			// much faster than the user can inspect it. Keep a responsive status
			// while sampling repeated progress events.
			if event.Kind == "codex_progress" && event.Name != "plan" && event.Name == lastProgressName && !lastProgressAt.IsZero() && time.Since(lastProgressAt) < time.Second {
				return
			}
			if event.Kind == "codex_progress" {
				lastProgressAt = time.Now()
				lastProgressName = event.Name
			}
			recordPersistenceError(server.store.AppendEvent(session.ID, store.Event{Kind: event.Kind, ProtocolMethod: event.ProtocolMethod, RawPayload: redactTraceAttachmentData(event.RawPayload), Turn: session.UserTurnCount, Status: event.Status, Name: event.Name, Detail: event.Detail, Input: redactTraceAttachmentData(event.Input), Output: redactTraceAttachmentData(event.Output), ActivityID: event.ActivityID, ActivityKind: event.ActivityKind, ActivitySource: event.ActivitySource, DisplayName: event.DisplayName, DurationMS: event.Duration.Milliseconds(), CreatedAt: time.Now()}))
		},
		OnServerRequest: func(request codexruntime.ServerRequest) (any, error) {
			return server.awaitCodexRequest(runtimeContext, session.ID, request)
		},
	}, message)
	unknownReason := "Codex app-server 未提供可确定配对的工具终态；该账本仅表示已观察到通知，不会自动重放"
	if ledgerErr := server.store.MarkUnfinishedToolOperationsUnknown(session.ID, store.RuntimeCodex, session.UserTurnCount, unknownReason, time.Now()); ledgerErr != nil {
		if runErr == nil {
			runErr = fmt.Errorf("收敛 Codex 工具账本失败: %w", ledgerErr)
		} else {
			runErr = fmt.Errorf("%v；收敛 Codex 工具账本失败: %w", runErr, ledgerErr)
		}
	}
	if persistenceErr != nil {
		runErr = fmt.Errorf("保存 Codex 工具账本或 Trace 失败: %w", persistenceErr)
	}
	if runErr != nil {
		stopReason := "error"
		incompleteReason := runErr.Error()
		var termination *codexruntime.TurnTerminationError
		if errors.As(runErr, &termination) {
			stopReason = termination.StopReason
			incompleteReason = termination.Detail
		}
		if errors.Is(runErr, context.DeadlineExceeded) {
			runErr = fmt.Errorf("Codex 整轮任务超过 %d 秒上限: %w", turnTimeoutSeconds, runErr)
			stopReason = "interrupted"
			incompleteReason = runErr.Error()
		} else if errors.Is(runErr, context.Canceled) {
			stopReason = "interrupted"
			incompleteReason = "Codex turn 已取消；不会接受或续跑部分结果"
		}
		failureDuration := time.Since(startedAt)
		if observedUsage.Reported {
			usage.ModelCalls++
			usage.ModelDurationMS += failureDuration.Milliseconds()
			usage.InputTokens += observedUsage.InputTokens
			usage.OutputTokens += observedUsage.OutputTokens
			usage.CachedTokens += observedUsage.CachedInputTokens
			usage.CacheWriteTokens += observedUsage.CacheWriteInputTokens
			usage.TotalTokens += observedUsage.TotalTokens
			usage.CacheReported = true
			usage.ContextWindowTokens = observedUsage.ModelContextWindow
			_ = server.store.AppendEvent(session.ID, store.Event{
				Kind: "codex_usage", Turn: session.UserTurnCount, Status: "error", Name: effectiveModel,
				ProtocolMethod: "thread/tokenUsage/updated", Detail: "本轮未完整结束；保留已上报用量",
				InputTokens: observedUsage.InputTokens, OutputTokens: observedUsage.OutputTokens,
				CachedTokens: observedUsage.CachedInputTokens, CacheWriteTokens: observedUsage.CacheWriteInputTokens,
				CacheReported: true, TotalTokens: observedUsage.TotalTokens,
				ContextWindowTokens: observedUsage.ModelContextWindow, Protocol: "codex_app_server", CreatedAt: time.Now(),
			})
		}
		_ = server.store.AppendEvent(session.ID, store.Event{Kind: "codex_end", Turn: session.UserTurnCount, Status: "error", StopReason: stopReason, IncompleteReason: incompleteReason, Name: effectiveModel, Detail: runErr.Error(), DurationMS: failureDuration.Milliseconds(), CreatedAt: time.Now()})
		return runErr
	}
	if model := strings.TrimSpace(result.Model); model != "" {
		effectiveModel = model
		_ = server.store.SetSessionModel(session.ID, model)
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
			Kind: "codex_usage", Turn: session.UserTurnCount, Status: "success", Name: effectiveModel,
			ProtocolMethod: "thread/tokenUsage/updated",
			Detail:         "thread/tokenUsage/updated · 本轮用量", InputTokens: result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens, CachedTokens: result.Usage.CachedInputTokens,
			CacheWriteTokens: result.Usage.CacheWriteInputTokens, CacheReported: true,
			TotalTokens: result.Usage.TotalTokens, ContextWindowTokens: result.Usage.ModelContextWindow,
			Protocol: "codex_app_server", CreatedAt: time.Now(),
		})
	}
	if err := server.store.AppendMessage(session.ID, store.Message{Role: "assistant", Content: result.Answer, ToolCalls: []store.ToolCall{}, Attachments: []store.Attachment{}, CreatedAt: time.Now()}); err != nil {
		return fmt.Errorf("保存 Codex 回答: %w", err)
	}
	if err := server.store.AppendEvent(session.ID, store.Event{Kind: "codex_end", Turn: session.UserTurnCount, Status: "success", StopReason: result.StopReason, Name: effectiveModel, Output: result.Answer, Protocol: "codex_app_server", DurationMS: result.Duration.Milliseconds(), CreatedAt: time.Now()}); err != nil {
		return fmt.Errorf("保存 Codex Trace: %w", err)
	}
	providerKey := strings.Join([]string{"codex", effectiveModel, status.Path}, "|")
	return server.store.FinishSession(session.ID, result.ThreadID, providerKey, *usage, time.Now())
}

type codexActivityIdentity struct {
	ID        string
	Guarantee string
}

type codexActivityResolver struct {
	sequence int
	pending  map[string][]string
}

func newCodexActivityResolver() *codexActivityResolver {
	return &codexActivityResolver{pending: make(map[string][]string)}
}

func (resolver *codexActivityResolver) nextID() string {
	resolver.sequence++
	return fmt.Sprintf("codex-fallback-%d", resolver.sequence)
}

// resolve only pairs a missing-ID terminal event when exactly one matching
// start is pending. Multiple identical concurrent calls are intentionally left
// unknown instead of guessing FIFO and attaching a result to the wrong effect.
func (resolver *codexActivityResolver) resolve(event codexruntime.Event) codexActivityIdentity {
	if id := strings.TrimSpace(event.ActivityID); id != "" {
		return codexActivityIdentity{ID: id, Guarantee: store.ToolGuaranteeObserved}
	}
	key := event.Name + "\x00" + event.Input
	if event.Status == "started" {
		id := resolver.nextID()
		resolver.pending[key] = append(resolver.pending[key], id)
		return codexActivityIdentity{ID: id, Guarantee: store.ToolGuaranteeObserved}
	}
	pending := resolver.pending[key]
	if len(pending) == 1 {
		delete(resolver.pending, key)
		return codexActivityIdentity{ID: pending[0], Guarantee: store.ToolGuaranteeObserved}
	}
	return codexActivityIdentity{ID: resolver.nextID(), Guarantee: store.ToolGuaranteeUncorrelated}
}

func isCompletedCodexBusinessActivity(event codexruntime.Event) bool {
	if event.Kind != "codex_item" || event.Status == "started" {
		return false
	}
	return event.ActivityKind == "tool" || event.ActivityKind == "mcp"
}

func (server *Server) awaitCodexRequest(ctx context.Context, sessionID string, request codexruntime.ServerRequest) (any, error) {
	pending := pendingCodexRequest{ID: string(request.ID), Method: request.Method, Params: append(json.RawMessage(nil), request.Params...), Response: make(chan pendingCodexResponse, 1)}
	if err := server.tasks.setPending(sessionID, pending); err != nil {
		return nil, err
	}
	defer server.tasks.clearPending(sessionID)
	rawPayload, _ := json.Marshal(map[string]any{"id": request.ID, "method": request.Method, "params": request.Params})
	_ = server.store.AppendEvent(sessionID, store.Event{Kind: "codex_request", ProtocolMethod: request.Method, RawPayload: redactTraceAttachmentData(string(rawPayload)), Status: "waiting", Name: request.Method, Detail: "Codex app-server -> EasyAgent UI，等待用户处理反向请求", Input: redactTraceAttachmentData(string(request.Params)), Protocol: "codex_app_server", CreatedAt: time.Now()})
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

func (server *Server) awaitEasyAgentApproval(ctx context.Context, sessionID string, call agent.ToolCall, workspace string) error {
	requestID, _ := json.Marshal(fmt.Sprintf("easyagent-approval-%d", time.Now().UnixNano()))
	params, _ := json.Marshal(map[string]any{
		"sessionId": sessionID, "tool": call.Name, "command": string(call.Arguments), "cwd": workspace,
	})
	value, err := server.awaitCodexRequest(ctx, sessionID, codexruntime.ServerRequest{
		ID: requestID, Method: "item/commandExecution/requestApproval", Params: params,
	})
	if err != nil {
		return err
	}
	response, ok := value.(map[string]any)
	decision, _ := response["decision"].(string)
	if !ok || (decision != "accept" && decision != "acceptForSession") {
		return errors.New("用户拒绝了这次工具执行")
	}
	return nil
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
