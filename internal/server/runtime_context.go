package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
)

const (
	runtimeRecentMessages      = 8
	runtimeMinToolResultTokens = 96
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

	compactedMessages, microChanged := microCompactAgentMessages(request.Messages, messageTokenTarget(request, threshold))
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
		compactedMessages, recentChanged := compactOversizedToolResults(request.Messages, messageTokenTarget(request, threshold))
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
		request.Messages = messages
		messages, _ = compactOversizedToolResults(messages, messageTokenTarget(request, threshold))
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
	// 因较小的理论输出上限，连一次普通工具调用都先触发摘要。
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
	total := estimateTextTokens(request.Model) + estimateAgentMessageTokens(request.Messages)
	for _, tool := range request.Tools {
		total += estimateAgentToolTokens(tool)
	}
	return total
}

// estimateAgentToolTokens 只估算真正进入 Provider 请求的标准 Function 字段。
// Activity、Group 和 Loader 等本地元数据不会由 Adapter 编码；把整个 ToolSpec
// Marshal 会高估上下文并过早触发一次昂贵的模型摘要。少量 envelope 预算覆盖
// 不同协议的 type/function/input_schema 包装。
func estimateAgentToolTokens(tool agent.ToolSpec) int {
	wire := struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters"`
	}{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}
	data, _ := json.Marshal(wire)
	return estimateTextTokens(string(data)) + 12
}

func estimateAgentMessageTokens(messages []agent.Message) int {
	total := 0
	for _, message := range messages {
		total += estimateTextTokens(modelMessageContent(message)) + estimateTextTokens(message.Name) + estimateTextTokens(message.ToolCallID)
		for _, part := range message.Parts {
			total += estimateTextTokens(part.Type) + estimateTextTokens(part.Name) + estimateTextTokens(part.MIMEType) + estimateTextTokens(part.URI)
			switch part.Type {
			case "text":
				total += estimateTextTokens(part.Text)
			case "json":
				total += estimateTextTokens(string(part.JSON))
			default:
				if len(part.Data) > 0 {
					total += 1024
				}
			}
		}
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
	return total
}

func modelMessageContent(message agent.Message) string {
	if message.Role == agent.RoleTool && message.ToolResult != nil {
		return message.ToolResult.ModelText()
	}
	return message.Content
}

// messageTokenTarget converts the complete request budget into a message-only
// budget. Tool schemas and other fixed request overhead are never hidden by a
// magic character limit.
func messageTokenTarget(request agent.Request, requestTarget int) int {
	currentMessages := estimateAgentMessageTokens(request.Messages)
	if requestTarget <= 0 {
		return max(runtimeMinToolResultTokens, currentMessages*2/3)
	}
	over := estimateAgentRequestTokens(request) - requestTarget
	if over <= 0 {
		return currentMessages
	}
	return max(0, currentMessages-over)
}

// microCompactAgentMessages keeps the recent protocol chain intact and spends
// only the token reduction actually required by the current context window.
// The optional argument is retained so low-level tests and callers can request
// a deterministic fraction without knowing the full request overhead.
func microCompactAgentMessages(messages []agent.Message, targets ...int) ([]agent.Message, bool) {
	cutoff := len(messages) - runtimeRecentMessages
	if cutoff <= 0 {
		return messages, false
	}
	return compactToolResultsToBudget(messages, resolveMessageTarget(messages, targets), cutoff)
}

func compactOversizedToolResults(messages []agent.Message, targets ...int) ([]agent.Message, bool) {
	return compactToolResultsToBudget(messages, resolveMessageTarget(messages, targets), len(messages))
}

func resolveMessageTarget(messages []agent.Message, targets []int) int {
	current := estimateAgentMessageTokens(messages)
	if len(targets) > 0 {
		return max(0, targets[0])
	}
	return max(runtimeMinToolResultTokens, current*2/3)
}

func compactToolResultsToBudget(messages []agent.Message, target, cutoff int) ([]agent.Message, bool) {
	current := estimateAgentMessageTokens(messages)
	toRemove := current - target
	if toRemove <= 0 {
		return messages, false
	}
	result := append([]agent.Message(nil), messages...)
	changed := false
	for index := 0; index < cutoff && toRemove > 0; index++ {
		if result[index].Role != agent.RoleTool {
			continue
		}
		originalTokens := estimateTextTokens(modelMessageContent(result[index]))
		if originalTokens <= runtimeMinToolResultTokens {
			continue
		}
		retained := max(runtimeMinToolResultTokens, originalTokens-toRemove)
		result[index] = compactToolResultMessage(result[index], retained, originalTokens)
		removed := originalTokens - estimateTextTokens(modelMessageContent(result[index]))
		if removed <= 0 {
			continue
		}
		toRemove -= removed
		changed = true
	}
	return result, changed
}

func compactToolResultMessage(message agent.Message, targetTokens, originalTokens int) agent.Message {
	original := message.ToolResult
	if original == nil {
		value := agent.NewToolResult(message.Content)
		original = &value
	}
	result := *original
	result.Content = append([]agent.ContentBlock(nil), original.Content...)
	result.StructuredContent = append(json.RawMessage(nil), original.StructuredContent...)
	metadata := &agent.ToolResultTruncation{Strategy: "adaptive_token_budget", OriginalTokens: originalTokens}
	if len(result.StructuredContent) > 0 {
		result.StructuredContent, metadata.OmittedItems = compactStructuredResult(result.StructuredContent, targetTokens, metadata)
		result.Content = nil
	} else {
		preview := compactTextToTokens(original.ModelText(), targetTokens, "\n[… 工具结果按当前上下文 Token 预算压缩；完整结果仍保存在会话中 …]\n")
		result.Content = []agent.ContentBlock{{Type: "text", Text: preview}}
	}
	result.Truncation = metadata
	metadata.RetainedTokens = estimateTextTokens(result.ModelText())
	if len(result.StructuredContent) > 0 {
		result.StructuredContent = refreshStructuredTruncation(result.StructuredContent, metadata)
		metadata.RetainedTokens = estimateTextTokens(result.ModelText())
		result.StructuredContent = refreshStructuredTruncation(result.StructuredContent, metadata)
	}
	message.ToolResult = &result
	message.Content = result.ModelText()
	return message
}

func refreshStructuredTruncation(raw json.RawMessage, metadata *agent.ToolResultTruncation) json.RawMessage {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	value["_easyagent_truncation"] = metadata
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}

func compactTextToTokens(value string, target int, marker string) string {
	value = strings.TrimSpace(value)
	if estimateTextTokens(value) <= target {
		return value
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	best := marker
	for low <= high {
		kept := (low + high) / 2
		head := kept / 2
		candidate := string(runes[:head]) + marker + string(runes[len(runes)-(kept-head):])
		if estimateTextTokens(candidate) <= target {
			best = candidate
			low = kept + 1
		} else {
			high = kept - 1
		}
	}
	return best
}

func compactStructuredResult(raw json.RawMessage, target int, metadata *agent.ToolResultTruncation) (json.RawMessage, int) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		preview := compactTextToTokens(string(raw), max(16, target/2), " … ")
		encoded, _ := json.Marshal(map[string]any{"_easyagent_truncation": metadata, "preview": preview})
		return encoded, 0
	}
	budget := max(16, target-estimateTextTokens(`{"_easyagent_truncation":{}}`)-24)
	summary, omitted := summarizeJSON(value, budget, 0)
	encoded, _ := json.Marshal(map[string]any{"_easyagent_truncation": metadata, "value": summary})
	if estimateTextTokens(string(encoded)) <= target {
		return encoded, omitted
	}
	preview := compactTextToTokens(string(raw), max(16, target/2), " … ")
	encoded, _ = json.Marshal(map[string]any{"_easyagent_truncation": metadata, "preview": preview})
	return encoded, max(omitted, 1)
}

func summarizeJSON(value any, budget, depth int) (any, int) {
	if depth >= 3 {
		data, _ := json.Marshal(value)
		return map[string]any{"_summary": compactTextToTokens(string(data), max(8, budget), " … ")}, 1
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		limit := max(2, budget/12)
		selected := keys
		omitted := 0
		if len(keys) > limit {
			head := limit / 2
			selected = append(append([]string(nil), keys[:head]...), keys[len(keys)-(limit-head):]...)
			omitted = len(keys) - len(selected)
		}
		result := make(map[string]any, len(selected)+1)
		perItem := max(8, budget/max(1, len(selected)))
		for _, key := range selected {
			summary, nestedOmitted := summarizeJSON(typed[key], perItem, depth+1)
			result[key] = summary
			omitted += nestedOmitted
		}
		if omitted > 0 {
			result["_omitted_items"] = omitted
		}
		return result, omitted
	case []any:
		if len(typed) == 0 {
			return typed, 0
		}
		limit := min(len(typed), max(2, budget/16))
		head := (limit + 1) / 2
		items := make([]any, 0, limit)
		omitted := len(typed) - limit
		perItem := max(8, budget/max(1, limit))
		indices := append(makeRange(0, head), makeRange(len(typed)-(limit-head), len(typed))...)
		for _, index := range indices {
			summary, nestedOmitted := summarizeJSON(typed[index], perItem, depth+1)
			items = append(items, summary)
			omitted += nestedOmitted
		}
		return map[string]any{"_type": "array", "count": len(typed), "items": items, "omitted": max(0, len(typed)-limit)}, omitted
	case string:
		compacted := compactTextToTokens(typed, max(8, budget), " … ")
		if compacted != typed {
			return compacted, 1
		}
		return typed, 0
	default:
		return typed, 0
	}
}

func makeRange(start, end int) []int {
	result := make([]int, 0, max(0, end-start))
	for value := start; value < end; value++ {
		result = append(result, value)
	}
	return result
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
