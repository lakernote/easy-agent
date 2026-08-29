package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
)

const (
	runtimeRecentMessages  = 8
	runtimeToolResultLimit = 2400
	runtimeSafetyMargin    = 1024
)

// prepareRuntimeRequest 在每次模型请求前运行。它先清理较早的大型工具结果，
// 再尝试使用 SQLite 中的检查点做完整压缩。压缩只改变本次运行的 active context；
// 原始消息仍由 Runner 在 step 完成后原子保存，后续新轮次可以重新建立检查点。
func (server *Server) prepareRuntimeRequest(
	ctx context.Context,
	id string,
	settings store.ModelSettings,
	systemPrompt string,
	turn int,
	usage *store.Usage,
	model agent.Model,
	runner *agent.Runner,
	request agent.Request,
	force bool,
) (agent.Request, bool, error) {
	threshold := runtimeCompactionThreshold(settings)
	if !force && (threshold <= 0 || estimateAgentRequestTokens(request) < threshold) {
		return request, false, nil
	}

	compactedMessages, microChanged := microCompactAgentMessages(request.Messages)
	if microChanged {
		request.Messages = compactedMessages
		// Responses 只会在没有 PreviousResponseID 时使用 Messages；历史被
		// 微压缩后必须放弃旧的 Provider continuation，避免服务端仍看到旧结果。
		request.NewMessages = append([]agent.Message(nil), compactedMessages...)
		request.PreviousResponseID = ""
	}
	if force {
		// 当前轮次的 tool result 可能仍在最近 8 条消息内，普通微压缩会
		// 有意保留它；但 Provider 已明确报超限时，必须把它也纳入兜底。
		compactedMessages, recentChanged := compactOversizedToolResults(request.Messages)
		if recentChanged {
			request.Messages = compactedMessages
			request.NewMessages = append([]agent.Message(nil), compactedMessages...)
			request.PreviousResponseID = ""
			microChanged = true
		}
	}

	// 微压缩已经足够时，不额外消耗一次摘要模型调用。force=true 表示
	// Provider 已经明确报上下文超限，此时继续尝试一次完整检查点。
	if !force && estimateAgentRequestTokens(request) < threshold {
		return request, microChanged, nil
	}

	session, err := server.store.Session(id)
	if err != nil {
		return request, microChanged, err
	}
	loadedTools := []agent.Tool(nil)
	if runner != nil {
		loadedTools = runner.Tools
	}
	didCompact, err := server.compactIfNeeded(ctx, &session, settings, model, systemPrompt, loadedTools, turn, usage, threshold, force)
	if err != nil {
		return request, microChanged, err
	}
	if didCompact {
		messages := coreMessagesForSession(session, systemPrompt)
		// split-turn 可能保留当前 assistant/tool 链，而超大的 tool result
		// 恰好位于这个后缀中；请求级截断避免摘要后仍然立即超限。SQLite
		// 中的原始结果不变，后续仍可通过会话审计或重新调用工具获取。
		messages, _ = compactOversizedToolResults(messages)
		request.Messages = messages
		request.NewMessages = append([]agent.Message(nil), messages...)
		request.PreviousResponseID = ""
		return request, true, nil
	}
	return request, microChanged, nil
}

func runtimeCompactionThreshold(settings store.ModelSettings) int {
	if settings.ContextWindowTokens <= 0 {
		return 0
	}
	percent := compressionThreshold(settings)
	threshold := settings.ContextWindowTokens * percent / 100
	maxOutput := settings.MaxOutputTokens
	if maxOutput <= 0 {
		maxOutput = store.DefaultMaxOutputTokens
	}
	safeThreshold := settings.ContextWindowTokens - maxOutput - runtimeSafetyMargin
	if safeThreshold > 0 && safeThreshold < threshold {
		threshold = safeThreshold
	}
	return threshold
}

func estimateAgentRequestTokens(request agent.Request) int {
	total := estimateTextTokens(request.Model)
	for _, message := range request.Messages {
		total += estimateTextTokens(message.Content) + estimateTextTokens(message.Name) + estimateTextTokens(message.ToolCallID)
		for _, attachment := range message.Attachments {
			total += estimateTextTokens(attachment.Name) + 64
			if attachment.Kind == "text" {
				total += estimateTextTokens(string(attachment.Data))
			} else {
				total += 1024
			}
		}
		for _, call := range message.ToolCalls {
			total += estimateTextTokens(call.Name) + estimateTextTokens(string(call.Arguments))
		}
	}
	for _, tool := range request.Tools {
		data, _ := json.Marshal(tool)
		total += estimateTextTokens(string(data))
	}
	return total
}

// microCompactAgentMessages 保留最近几条消息原样，只压缩较早的 Tool Result。
// Tool Call 本身不删除，完整结果仍在 SQLite 中，模型需要时可以重新调用工具。
func microCompactAgentMessages(messages []agent.Message) ([]agent.Message, bool) {
	cutoff := len(messages) - runtimeRecentMessages
	if cutoff <= 0 {
		return messages, false
	}
	result := append([]agent.Message(nil), messages...)
	changed := false
	for index := 0; index < cutoff; index++ {
		if result[index].Role != agent.RoleTool || len([]rune(result[index].Content)) <= runtimeToolResultLimit {
			continue
		}
		result[index].Content = compactToolResultText(result[index].Content)
		changed = true
	}
	return result, changed
}

func compactOversizedToolResults(messages []agent.Message) ([]agent.Message, bool) {
	result := append([]agent.Message(nil), messages...)
	changed := false
	for index := range result {
		if result[index].Role != agent.RoleTool || len([]rune(result[index].Content)) <= runtimeToolResultLimit {
			continue
		}
		result[index].Content = compactToolResultText(result[index].Content)
		changed = true
	}
	return result, changed
}

func compactToolResultText(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= runtimeToolResultLimit {
		return string(runes)
	}
	part := (runtimeToolResultLimit - 96) / 2
	return string(runes[:part]) + "\n[… 历史工具结果已微压缩，完整结果仍保存在会话中]\n" + string(runes[len(runes)-part:])
}

func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	markers := []string{
		"context length", "context_length_exceeded", "context window", "maximum context", "max context",
		"prompt is too long", "prompt too long", "input is too long", "request too large",
		"too many tokens", "maximum number of tokens", "token limit",
		"上下文", "令牌数超", "token 数超",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
