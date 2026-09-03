package server

import (
	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/openai"
	"github.com/lakernote/easy-agent/internal/store"
)

// decorateContext 将消息数、真实 Usage、压缩检查点和模型配置组合成页面账本。
func decorateContext(session *store.Session, settings store.ModelSettings) {
	threshold := compressionThreshold(settings)
	historyMessages := session.MessageCount
	if historyMessages == 0 && len(session.Messages) > 0 {
		historyMessages = len(session.Messages)
	}
	userTurns := session.UserTurnCount
	if userTurns == 0 && len(session.Messages) > 0 {
		for _, message := range session.Messages {
			if message.Role == string(agent.RoleUser) {
				userTurns++
			}
		}
	}
	info := store.ContextInfo{
		HistoryMessages: historyMessages, ContextWindowTokens: settings.ContextWindowTokens,
		CompressionMode: "auto", CompressionThresholdPercent: threshold,
		RetainedMessages: historyMessages, HistoryMode: "full_history",
	}
	if threshold <= 0 {
		info.CompressionMode = "disabled"
	}
	if len(session.Compactions) > 0 {
		latest := session.Compactions[len(session.Compactions)-1]
		info.CompressionCount = len(session.Compactions)
		info.CompressedMessages = latest.CompactedMessages
		info.RetainedMessages = max(0, historyMessages-latest.CompactedMessages)
	}
	if settings.Protocol == string(openai.Responses) {
		info.HistoryMode = "responses_full_input"
	}
	info.UserTurns = userTurns
	for index := len(session.Events) - 1; index >= 0; index-- {
		event := session.Events[index]
		// Codex 的最终响应事件不重复携带 token usage；真实账本以紧邻的
		// thread/tokenUsage/updated 事件为准，避免把一轮用量算两次。
		if session.Runtime == "codex" && event.Kind == "codex_end" {
			continue
		}
		if event.Kind != string(agent.EventModelEnd) && event.Kind != "codex_usage" {
			continue
		}
		info.LastInputTokens = event.InputTokens
		info.LastCachedTokens = event.CachedTokens
		info.LastCacheWriteTokens = event.CacheWriteTokens
		info.CacheReported = event.CacheReported
		if event.ContextWindowTokens > 0 {
			info.ContextWindowTokens = event.ContextWindowTokens
		}
		if event.HistoryMode != "" {
			info.HistoryMode = event.HistoryMode
		}
		info.RequestMessages = event.RequestMessages
		info.ToolDefinitions = event.ToolDefinitions
		break
	}
	session.Context = info
}
