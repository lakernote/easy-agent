package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/openai"
	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) newTraceObserver(id string, turn int, usage *store.Usage, onTraceError func(error)) agent.Observer {
	return func(event agent.Event) {
		if event.Kind == agent.EventModelStart {
			server.tasks.setProgress(id, "EasyAgent · 请求模型")
		} else if event.Kind == agent.EventToolStart && event.ToolCall != nil {
			server.tasks.setProgress(id, "EasyAgent · 执行工具 "+event.ToolCall.Name)
		} else if event.Kind == agent.EventModelEnd {
			server.tasks.setProgress(id, "EasyAgent · 整理回答")
		}
		value := store.Event{Kind: string(event.Kind), Turn: turn, Step: event.Step, Attempt: event.Attempt, Status: "success", CreatedAt: time.Now(), DurationMS: event.Duration.Milliseconds()}
		if event.ToolCall != nil {
			value.Name = event.ToolCall.Name
			value.Input = string(event.ToolCall.Arguments)
			value.ActivityID = event.ToolCall.ID
			value.ActivityKind = event.ToolCall.ActivityKind
			value.ActivitySource = event.ToolCall.ActivitySource
			value.DisplayName = event.ToolCall.DisplayName
		}
		if event.Kind == agent.EventModelStart || event.Kind == agent.EventToolStart {
			// start 事件是一条已经发生的事实，不代表页面轮询时仍然运行。
			value.Status = "started"
		}
		if event.Kind == agent.EventModelEnd {
			usage.ModelCalls++
			usage.ModelDurationMS += event.Duration.Milliseconds()
			usage.InputTokens += event.Exchange.Usage.InputTokens
			usage.OutputTokens += event.Exchange.Usage.OutputTokens
			usage.CachedTokens += event.Exchange.Usage.CachedInputTokens
			usage.CacheWriteTokens += event.Exchange.Usage.CacheWriteTokens
			usage.TotalTokens += event.Exchange.Usage.TotalTokens
			usage.CacheReported = usage.CacheReported || event.Exchange.Usage.CacheReported
			usage.CacheInputTokens += event.Exchange.Usage.InputTokens
			value.Name = event.Exchange.Model
			value.Input, value.Output = event.Exchange.Request, event.Exchange.Response
			value.InputTokens = event.Exchange.Usage.InputTokens
			value.OutputTokens = event.Exchange.Usage.OutputTokens
			value.CachedTokens = event.Exchange.Usage.CachedInputTokens
			value.CacheWriteTokens = event.Exchange.Usage.CacheWriteTokens
			value.CacheReported = event.Exchange.Usage.CacheReported
			value.TotalTokens = event.Exchange.Usage.TotalTokens
			value.Protocol = event.Exchange.Protocol
			value.StatusCode = event.Exchange.StatusCode
			value.HistoryMode, value.RequestMessages, value.ToolDefinitions = modelRequestShape(event.Exchange)
		}
		if event.Kind == agent.EventToolEnd {
			if isBusinessToolActivity(value.ActivityKind, value.Name) {
				usage.ToolCalls++
				usage.ToolDurationMS += event.Duration.Milliseconds()
			}
			value.Output = event.Output
		}
		if event.Err != nil {
			value.Status, value.Detail = "error", event.Err.Error()
		}
		// Trace 面向本机运行审计，必须保留工具真实返回的工作目录和文件路径，
		// 否则用户无法根据 Trace 复现问题。附件二进制仍只保留结构和 MIME。
		value.Input = redactTraceAttachmentData(value.Input)
		value.Output = redactTraceAttachmentData(value.Output)
		if err := server.store.AppendEvent(id, value); err != nil && onTraceError != nil {
			onTraceError(err)
		}
		if event.Kind == agent.EventModelEnd || event.Kind == agent.EventToolEnd {
			// SQLite 只在整轮结束时保存累计 Usage；运行中的指标由 taskManager
			// 提供。每个完成事件后同步一次，避免 Trace 已经出现而顶部仍显示 0。
			server.tasks.setUsage(id, *usage)
		}
	}
}

// Loader、Skill 注入和 MCP 工具发现属于能力编排，不是用户关心的业务工具
// 调用。它们仍完整保留在 Trace 中，但不抬高首页和用量页的 Tool 指标。
func isBusinessToolActivity(kind, name string) bool {
	switch strings.TrimSpace(kind) {
	case "loader", "skill", "mcp_loader", "mcp_selected":
		return false
	case "tool", "mcp":
		return true
	}
	switch strings.TrimSpace(name) {
	case "load_tools", "load_skill", "search_mcp_tools":
		return false
	default:
		return true
	}
}

// userTurnCount 只按已持久化的 user 消息计算轮次。一次 Turn 可以包含多条
// assistant/tool 消息，因此不能用消息总数除以二推导。
func userTurnCount(messages []store.Message) int {
	turns := 0
	for _, message := range messages {
		if message.Role == string(agent.RoleUser) {
			turns++
		}
	}
	return turns
}

// redactTraceAttachmentData 保留多模态请求结构和 MIME 类型，但不把图片/PDF
// 的 Base64 原文塞进 Trace。这样页面仍可审计输入，同时不会生成数 MB 的事件。
func redactTraceAttachmentData(value string) string {
	if !strings.Contains(value, ";base64,") {
		return value
	}
	var payload any
	if json.Unmarshal([]byte(value), &payload) != nil {
		return value
	}
	var sanitize func(any) any
	sanitize = func(item any) any {
		switch typed := item.(type) {
		case string:
			if strings.HasPrefix(typed, "data:") {
				if marker := strings.Index(typed, ";base64,"); marker > len("data:") {
					return fmt.Sprintf("<%s attachment data omitted>", typed[len("data:"):marker])
				}
			}
			return typed
		case []any:
			for index := range typed {
				typed[index] = sanitize(typed[index])
			}
			return typed
		case map[string]any:
			for key := range typed {
				typed[key] = sanitize(typed[key])
			}
			return typed
		default:
			return item
		}
	}
	encoded, err := json.Marshal(sanitize(payload))
	if err != nil {
		return value
	}
	return string(encoded)
}

// modelRequestShape 只提取 Trace 所需的结构数据，不在运行时重新估算 Token。
// Chat Completions 每次发送完整 messages；Responses 有 previous_response_id
// 时只发送本轮新增 input，历史由 Provider 续接。
func modelRequestShape(exchange agent.Exchange) (string, int, int) {
	var payload struct {
		Messages           []json.RawMessage `json:"messages"`
		Input              []json.RawMessage `json:"input"`
		Tools              []json.RawMessage `json:"tools"`
		PreviousResponseID string            `json:"previous_response_id"`
	}
	if json.Unmarshal([]byte(exchange.Request), &payload) != nil {
		return "unknown", 0, 0
	}
	if exchange.Protocol == string(openai.Responses) {
		mode := "responses_full_input"
		if strings.TrimSpace(payload.PreviousResponseID) != "" {
			mode = "provider_continuation"
		}
		return mode, len(payload.Input), len(payload.Tools)
	}
	return "full_history", len(payload.Messages), len(payload.Tools)
}
