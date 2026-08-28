package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/builtin/prompt"
	"github.com/lakernote/easy-agent/internal/store"
)

const recentContextPercent = 25

type compactionPlan struct {
	Messages          []store.Message
	PreviousSummary   string
	ThroughMessageID  int64
	SourceMessages    int
	CompactedMessages int
	EstimatedTokens   int
	ThresholdTokens   int
}

// compactIfNeeded 在普通 Agent 循环之前创建检查点。它直接使用同一个模型，
// 但不提供任何 Tool，避免摘要过程产生副作用。
func (server *Server) compactIfNeeded(ctx context.Context, session *store.Session, settings store.ModelSettings, model agent.Model, systemPrompt string, tools []agent.Tool, usage *store.Usage) (bool, error) {
	plan := makeCompactionPlan(*session, settings, systemPrompt, tools)
	if plan == nil {
		return false, nil
	}

	startedAt := time.Now()
	_ = server.store.AppendEvent(session.ID, store.Event{
		Kind: "compaction_start", Status: "started", Name: settings.Model, CreatedAt: startedAt,
		Detail: fmt.Sprintf("估算上下文 %d Token，达到自动压缩阈值 %d；准备压缩 %d 条较早消息", plan.EstimatedTokens, plan.ThresholdTokens, plan.SourceMessages),
	})

	request := agent.Request{
		Model: settings.Model,
		Messages: []agent.Message{
			{Role: agent.RoleSystem, Content: prompt.CompactionTemplate()},
			{Role: agent.RoleUser, Content: compactionInput(plan.PreviousSummary, plan.Messages)},
		},
		MaxOutputTokens: compactionMaxOutput(settings.MaxOutputTokens),
	}
	response, err := model.Generate(ctx, request)
	duration := time.Since(startedAt)
	if err != nil {
		_ = server.store.AppendEvent(session.ID, store.Event{
			Kind: "compaction_end", Status: "error", Name: settings.Model, Detail: err.Error(), DurationMS: duration.Milliseconds(), CreatedAt: time.Now(),
		})
		return false, fmt.Errorf("上下文压缩失败: %w", err)
	}
	if len(response.Message.ToolCalls) > 0 {
		err = errors.New("压缩模型错误地请求了工具调用")
	} else if strings.TrimSpace(response.Message.Content) == "" {
		err = errors.New("压缩模型返回了空摘要")
	}
	if err != nil {
		_ = server.store.AppendEvent(session.ID, store.Event{
			Kind: "compaction_end", Status: "error", Name: settings.Model, Detail: err.Error(), DurationMS: duration.Milliseconds(), CreatedAt: time.Now(),
		})
		return false, err
	}

	currentUsage := toStoreUsage(response.Usage, duration)
	if err := server.store.AppendCompaction(session.ID, store.Compaction{
		Summary: strings.TrimSpace(response.Message.Content), ThroughMessageID: plan.ThroughMessageID,
		SourceMessages: plan.SourceMessages, CompactedMessages: plan.CompactedMessages,
		Usage: currentUsage, CreatedAt: time.Now(),
	}); err != nil {
		return false, err
	}
	addStoreUsage(usage, currentUsage)
	historyMode, requestMessages, toolDefinitions := modelRequestShape(response.Exchange)
	_ = server.store.AppendEvent(session.ID, store.Event{
		Kind: "compaction_end", Status: "success", Name: response.Exchange.Model,
		Detail: fmt.Sprintf("检查点已保存；%d 条原始消息由摘要表示，最近消息继续原样保留", plan.CompactedMessages),
		Input:  response.Exchange.Request, Output: response.Exchange.Response,
		InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens,
		CachedTokens: response.Usage.CachedInputTokens, CacheWriteTokens: response.Usage.CacheWriteTokens,
		CacheReported: response.Usage.CacheReported, TotalTokens: response.Usage.TotalTokens,
		Protocol: response.Exchange.Protocol, HistoryMode: historyMode,
		RequestMessages: requestMessages, ToolDefinitions: toolDefinitions,
		DurationMS: duration.Milliseconds(), CreatedAt: time.Now(),
	})

	updated, err := server.store.Session(session.ID)
	if err != nil {
		return false, err
	}
	*session = updated
	return true, nil
}

func makeCompactionPlan(session store.Session, settings store.ModelSettings, systemPrompt string, tools []agent.Tool) *compactionPlan {
	thresholdPercent := compressionThreshold(settings)
	if settings.ContextWindowTokens <= 0 || thresholdPercent <= 0 {
		return nil
	}
	threshold := settings.ContextWindowTokens * thresholdPercent / 100
	estimated := estimateActiveContext(session, systemPrompt, tools)
	if estimated < threshold {
		return nil
	}

	var previous store.Compaction
	if len(session.Compactions) > 0 {
		previous = session.Compactions[len(session.Compactions)-1]
	}
	active := make([]store.Message, 0, len(session.Messages))
	for _, message := range session.Messages {
		if message.ID > previous.ThroughMessageID {
			active = append(active, message)
		}
	}
	userIndexes := make([]int, 0)
	for index, message := range active {
		if message.Role == string(agent.RoleUser) {
			userIndexes = append(userIndexes, index)
		}
	}
	// 至少保留最近一个完整用户轮次；只有一个轮次时没有安全的旧轮次可压缩。
	if len(userIndexes) < 2 {
		return nil
	}

	keepIndex := userIndexes[len(userIndexes)-1]
	keepBudget := settings.ContextWindowTokens * recentContextPercent / 100
	keptTokens := estimateStoredMessages(active[keepIndex:])
	for index := len(userIndexes) - 2; index >= 0; index-- {
		candidate := userIndexes[index]
		candidateTokens := estimateStoredMessages(active[candidate:keepIndex])
		if keptTokens+candidateTokens > keepBudget {
			break
		}
		keepIndex = candidate
		keptTokens += candidateTokens
	}
	if keepIndex <= 0 {
		return nil
	}

	throughID := active[keepIndex-1].ID
	compactedMessages := 0
	for _, message := range session.Messages {
		if message.ID <= throughID {
			compactedMessages++
		}
	}
	return &compactionPlan{
		Messages: active[:keepIndex], PreviousSummary: previous.Summary,
		ThroughMessageID: throughID, SourceMessages: keepIndex,
		CompactedMessages: compactedMessages, EstimatedTokens: estimated, ThresholdTokens: threshold,
	}
}

func compressionThreshold(settings store.ModelSettings) int {
	if settings.CompressionThresholdPercent == 0 {
		return 75
	}
	return settings.CompressionThresholdPercent
}

func estimateActiveContext(session store.Session, systemPrompt string, tools []agent.Tool) int {
	latestInput, latestEventAt := 0, time.Time{}
	for index := len(session.Events) - 1; index >= 0; index-- {
		event := session.Events[index]
		if event.Kind == string(agent.EventModelEnd) {
			latestInput, latestEventAt = event.InputTokens, event.CreatedAt
			break
		}
	}
	if latestInput > 0 {
		added := 0
		for _, message := range session.Messages {
			if message.CreatedAt.After(latestEventAt) {
				added += estimateTextTokens(message.Content)
			}
		}
		return latestInput + added
	}
	var previous store.Compaction
	if len(session.Compactions) > 0 {
		previous = session.Compactions[len(session.Compactions)-1]
	}
	active := make([]store.Message, 0, len(session.Messages))
	for _, message := range session.Messages {
		if message.ID > previous.ThroughMessageID {
			active = append(active, message)
		}
	}
	total := estimateTextTokens(systemPrompt) + estimateTextTokens(previous.Summary) + estimateStoredMessages(active)
	for _, tool := range tools {
		data, _ := json.Marshal(tool.Spec)
		total += estimateTextTokens(string(data))
	}
	return total
}

func estimateStoredMessages(messages []store.Message) int {
	total := 0
	for _, message := range messages {
		total += estimateTextTokens(message.Content) + estimateTextTokens(message.Name) + estimateTextTokens(message.ToolCallID)
		for _, call := range message.ToolCalls {
			total += estimateTextTokens(call.Name) + estimateTextTokens(call.Arguments)
		}
	}
	return total
}

// ASCII 文本大约四字符一个 Token；中文等非 ASCII 字符按一字符一个 Token。
// 这只是触发前估算，页面展示仍只使用 Provider 真实上报的 Usage。
func estimateTextTokens(value string) int {
	ascii, nonASCII := 0, 0
	for _, character := range value {
		if character <= 127 {
			ascii++
		} else {
			nonASCII++
		}
	}
	if !utf8.ValidString(value) {
		return (len(value) + 3) / 4
	}
	return (ascii+3)/4 + nonASCII
}

func compactionInput(previousSummary string, messages []store.Message) string {
	var result strings.Builder
	if strings.TrimSpace(previousSummary) != "" {
		result.WriteString("<previous_checkpoint>\n")
		result.WriteString(previousSummary)
		result.WriteString("\n</previous_checkpoint>\n\n")
	}
	result.WriteString("<conversation>\n")
	for _, message := range messages {
		fmt.Fprintf(&result, "[%s]", message.Role)
		if message.Name != "" {
			fmt.Fprintf(&result, "[%s]", message.Name)
		}
		result.WriteString(": ")
		result.WriteString(limitSummaryText(message.Content, 4000))
		for _, call := range message.ToolCalls {
			fmt.Fprintf(&result, "\n[tool_call %s]: %s", call.Name, limitSummaryText(call.Arguments, 2000))
		}
		result.WriteString("\n\n")
	}
	result.WriteString("</conversation>")
	return result.String()
}

func limitSummaryText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + fmt.Sprintf("\n[… 已截断 %d 字符]", len(runes)-limit)
}

func compactionMaxOutput(configured int) int {
	if configured <= 0 {
		return 1200
	}
	if configured < 800 {
		return 800
	}
	if configured > 1600 {
		return 1600
	}
	return configured
}

func toStoreUsage(value agent.Usage, duration time.Duration) store.Usage {
	total := value.TotalTokens
	if total == 0 {
		total = value.InputTokens + value.OutputTokens
	}
	return store.Usage{
		InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
		CachedTokens: value.CachedInputTokens, CacheWriteTokens: value.CacheWriteTokens,
		TotalTokens: total, ModelDurationMS: duration.Milliseconds(), ModelCalls: 1,
		CacheReported: value.CacheReported, CacheInputTokens: value.InputTokens,
	}
}

func addStoreUsage(total *store.Usage, value store.Usage) {
	total.InputTokens += value.InputTokens
	total.OutputTokens += value.OutputTokens
	total.CachedTokens += value.CachedTokens
	total.CacheWriteTokens += value.CacheWriteTokens
	total.TotalTokens += value.TotalTokens
	total.ModelDurationMS += value.ModelDurationMS
	total.ToolDurationMS += value.ToolDurationMS
	total.ModelCalls += value.ModelCalls
	total.ToolCalls += value.ToolCalls
	total.CacheReported = total.CacheReported || value.CacheReported
	total.CacheInputTokens += value.CacheInputTokens
}
