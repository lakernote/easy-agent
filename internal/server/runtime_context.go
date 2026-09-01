package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
)

const (
	runtimeRecentMessages      = 8
	runtimeToolResultLimit     = 1200
	runtimeMinOutputTokens     = 128
	runtimeMinSafetyMargin     = 64
	runtimeMaxSafetyMargin     = 512
	runtimeSafetyMarginPercent = 2
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
		return fitRuntimeOutputBudget(request, settings), false, nil
	}

	compactedMessages, microChanged := microCompactAgentMessages(request.Messages)
	if microChanged {
		request.Messages = compactedMessages
		// Responses 只会在没有 PreviousResponseID 时使用 Messages；历史被
		// 微压缩后必须放弃旧的 Provider continuation，避免服务端仍看到旧结果。
		request.NewMessages = append([]agent.Message(nil), compactedMessages...)
		request.PreviousResponseID = ""
	}
	// 大型 Tool Result 是执行型任务最常见的上下文膨胀来源。先做确定性的
	// 头尾保留，再考虑调用模型生成摘要；这比每次读取网页或日志后额外花一轮
	// LLM 更快、更省 Token。SQLite 中仍保存完整结果，Trace 也不受影响。
	if estimateAgentRequestTokens(request) >= threshold || force {
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
		return fitRuntimeOutputBudget(request, settings), microChanged, nil
	}

	session, err := server.store.RuntimeSession(id)
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
		return fitRuntimeOutputBudget(request, settings), true, nil
	}
	return fitRuntimeOutputBudget(request, settings), microChanged, nil
}

func runtimeCompactionThreshold(settings store.ModelSettings) int {
	if settings.ContextWindowTokens <= 0 {
		return 0
	}
	percent := compressionThreshold(settings)
	threshold := settings.ContextWindowTokens * percent / 100
	// 压缩由“输入是否接近窗口”决定，不为配置的最大输出固定占位。单次请求
	// 真正剩余多少输出空间由 fitRuntimeOutputBudget 动态限制。否则 4K 模型会
	// 因 1600 的理论输出上限，连一次普通工具调用都先触发摘要。
	safeThreshold := settings.ContextWindowTokens - runtimeMinOutputTokens - runtimeSafetyMargin(settings.ContextWindowTokens)
	if safeThreshold > 0 && safeThreshold < threshold {
		threshold = safeThreshold
	}
	return threshold
}

// fitRuntimeOutputBudget 只收紧当前模型请求，不修改用户保存的模型配置。
// 上下文较短时仍保留原上限；工具结果较大时则把剩余窗口优先留给真实输入，
// 避免 Provider 因 input + max_output 超过窗口而拒绝请求。
func fitRuntimeOutputBudget(request agent.Request, settings store.ModelSettings) agent.Request {
	if settings.ContextWindowTokens <= 0 {
		return request
	}
	available := settings.ContextWindowTokens - estimateAgentRequestTokens(request) - runtimeSafetyMargin(settings.ContextWindowTokens)
	if available <= 0 {
		return request
	}
	if request.MaxOutputTokens <= 0 || request.MaxOutputTokens > available {
		request.MaxOutputTokens = available
	}
	return request
}

func runtimeSafetyMargin(contextWindow int) int {
	margin := contextWindow * runtimeSafetyMarginPercent / 100
	if margin < runtimeMinSafetyMargin {
		return runtimeMinSafetyMargin
	}
	if margin > runtimeMaxSafetyMargin {
		return runtimeMaxSafetyMargin
	}
	return margin
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
