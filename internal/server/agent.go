package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/openai"
	"github.com/lakernote/easy-agent/internal/builtin/prompt"
	builtintools "github.com/lakernote/easy-agent/internal/builtin/tools"
	"github.com/lakernote/easy-agent/internal/mcpclient"
	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) queue(id, userMessage string, model store.ModelSettings) error {
	if server.hasTask(id) {
		return errors.New("上一条任务正在结束，请稍后再发送")
	}
	if err := server.store.QueueSession(id, model.Model, time.Now()); err != nil {
		return err
	}
	if err := server.store.AppendMessage(id, store.Message{Role: "user", Content: userMessage, ToolCalls: []store.ToolCall{}, CreatedAt: time.Now()}); err != nil {
		_ = server.store.FailSession(id, err, store.Usage{}, time.Now())
		return err
	}
	taskContext, taskCancel := context.WithCancel(server.context)
	taskToken := newID()
	server.setTask(id, taskToken, taskCancel)
	server.wait.Add(1)
	go func() {
		defer server.wait.Done()
		defer server.clearTask(id, taskToken)
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
		usage := store.Usage{}
		// 不再给整轮 Agent 叠加一个固定总超时。每次模型请求和工具调用都有
		// 自己的超时，循环也有最大步数；用户还可以随时点击“停止”。固定总
		// 超时会让合法的多步任务在最后阶段被无故中断。
		if err := server.run(taskContext, id, model, &usage); err != nil {
			_ = server.store.FailSession(id, err, usage, time.Now())
		}
	}()
	return nil
}

func (server *Server) setTask(id, token string, cancel context.CancelFunc) {
	server.taskMu.Lock()
	defer server.taskMu.Unlock()
	server.tasks[id] = taskHandle{token: token, cancel: cancel}
}

func (server *Server) appendTaskPartial(id, delta string) {
	if delta == "" {
		return
	}
	server.taskMu.Lock()
	defer server.taskMu.Unlock()
	current, ok := server.tasks[id]
	if !ok {
		return
	}
	current.partial += delta
	server.tasks[id] = current
}

func (server *Server) taskPartial(id string) string {
	server.taskMu.Lock()
	defer server.taskMu.Unlock()
	return server.tasks[id].partial
}

func (server *Server) clearTask(id, token string) {
	server.taskMu.Lock()
	defer server.taskMu.Unlock()
	if current, ok := server.tasks[id]; ok && current.token == token {
		delete(server.tasks, id)
	}
}

func (server *Server) cancelTask(id string) {
	server.taskMu.Lock()
	current := server.tasks[id]
	server.taskMu.Unlock()
	if current.cancel != nil {
		current.cancel()
	}
}

func (server *Server) hasTask(id string) bool {
	server.taskMu.Lock()
	defer server.taskMu.Unlock()
	_, exists := server.tasks[id]
	return exists
}

func (server *Server) run(ctx context.Context, id string, settings store.ModelSettings, usage *store.Usage) error {
	settings = enrichOllamaContextWindow(ctx, settings)
	session, err := server.store.Session(id)
	if err != nil {
		return err
	}
	catalog, err := loadSkillCatalog(server.store)
	if err != nil {
		return err
	}
	skills := catalog.EnabledSkills()
	skillMeta := make([]prompt.SkillMeta, 0, len(skills))
	for _, skill := range skills {
		skillMeta = append(skillMeta, prompt.SkillMeta{Name: skill.Name, Description: skill.Description})
	}

	allTools := builtintools.Catalog(catalog)
	mcps, err := server.store.MCPs()
	if err != nil {
		return err
	}
	loader := mcpclient.NewLoader(mcps)
	defer loader.Close()
	mcpMeta := make([]prompt.MCPMeta, 0)
	for _, info := range loader.Servers() {
		mcpMeta = append(mcpMeta, prompt.MCPMeta{ID: info.ID, Name: info.Name, Description: info.Description})
	}
	if !loader.Empty() {
		allTools = append(allTools, loader.Tool())
	}
	apiKey := settings.APIKey
	if settings.APIKeyEnv != "" {
		apiKey = os.Getenv(settings.APIKeyEnv)
	}
	client, err := openai.New(openai.Config{
		BaseURL: settings.BaseURL, APIKey: apiKey, Protocol: openai.Protocol(settings.Protocol),
		DisableThinking: settings.Thinking == "disabled",
		Timeout:         time.Duration(settings.RequestTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return err
	}
	systemPrompt := prompt.Render(prompt.Context{
		Now: time.Now(), Skills: skillMeta, MCPs: mcpMeta,
	})
	didCompact, err := server.compactIfNeeded(ctx, &session, settings, client, systemPrompt, allTools, usage)
	if err != nil {
		return err
	}
	runner, err := agent.NewRunner(client, settings.Model, allTools)
	if err != nil {
		return err
	}
	loader.SetRegister(runner.AddTools)
	runner.MaxOutputTokens = settings.MaxOutputTokens
	runner.Observe = server.observer(id, usage)

	coreMessages := []agent.Message{{Role: agent.RoleSystem, Content: systemPrompt}}
	var compactedThrough int64
	if len(session.Compactions) > 0 {
		latest := session.Compactions[len(session.Compactions)-1]
		compactedThrough = latest.ThroughMessageID
		coreMessages = append(coreMessages, agent.Message{Role: agent.RoleSystem, Content: "此前会话的上下文检查点：\n\n" + latest.Summary})
	}
	for _, message := range session.Messages {
		if message.ID <= compactedThrough {
			continue
		}
		coreMessages = append(coreMessages, toCoreMessage(message))
	}
	if len(coreMessages) > 0 {
		last := &coreMessages[len(coreMessages)-1]
		last.Content = expandContinuation(last.Role, last.Content)
	}
	initialCount := len(coreMessages)
	providerKey := strings.Join([]string{settings.Provider, settings.Protocol, settings.BaseURL, settings.Model}, "|")
	previousID := ""
	// Ollama 的 Responses 兼容端点目前不支持 previous_response_id，
	// 因此继续发送完整 input；真正支持服务端会话的 Provider 才续接 ID。
	if !didCompact && settings.Protocol == "responses" && session.ProviderKey == providerKey && !settings.IsOllama() {
		previousID = session.ResponseID
	}
	newMessages := []agent.Message{coreMessages[len(coreMessages)-1]}
	result, err := runner.Run(ctx, agent.RunRequest{
		Messages: coreMessages, NewMessages: newMessages, PreviousResponseID: previousID,
		PromptCacheKey: promptCacheKey(settings),
		OnTextDelta:    func(delta string) { server.appendTaskPartial(id, delta) },
	})
	if err != nil {
		return err
	}
	for _, message := range result.Messages[initialCount:] {
		if err := server.store.AppendMessage(id, fromCoreMessage(message)); err != nil {
			return err
		}
	}
	return server.store.FinishSession(id, result.ResponseID, providerKey, *usage, time.Now())
}

// promptCacheKey 仅发送给已知支持该字段的 OpenAI 服务。缓存键跨会话稳定，
// 真正是否命中仍由请求前缀决定，并以 Provider 返回的 cached_tokens 为准。
func promptCacheKey(settings store.ModelSettings) string {
	if settings.IsOfficialOpenAI() {
		return "easyagent-core-v1"
	}
	return ""
}

func (server *Server) observer(id string, usage *store.Usage) agent.Observer {
	return func(event agent.Event) {
		value := store.Event{Kind: string(event.Kind), Step: event.Step, Status: "success", CreatedAt: time.Now(), DurationMS: event.Duration.Milliseconds()}
		if event.ToolCall != nil {
			value.Name = event.ToolCall.Name
			value.Input = string(event.ToolCall.Arguments)
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
			value.HistoryMode, value.RequestMessages, value.ToolDefinitions = modelRequestShape(event.Exchange)
		}
		if event.Kind == agent.EventToolEnd {
			usage.ToolCalls++
			usage.ToolDurationMS += event.Duration.Milliseconds()
			value.Output = event.Output
		}
		if event.Err != nil {
			value.Status, value.Detail = "error", event.Err.Error()
		}
		_ = server.store.AppendEvent(id, value)
	}
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

// decorateContext 将消息数、真实 Usage、压缩检查点和模型配置组合成页面账本。
func decorateContext(session *store.Session, settings store.ModelSettings) {
	threshold := compressionThreshold(settings)
	info := store.ContextInfo{
		HistoryMessages: len(session.Messages), ContextWindowTokens: settings.ContextWindowTokens,
		CompressionMode: "auto", CompressionThresholdPercent: threshold,
		RetainedMessages: len(session.Messages), HistoryMode: "full_history",
	}
	if threshold <= 0 {
		info.CompressionMode = "disabled"
	}
	if len(session.Compactions) > 0 {
		latest := session.Compactions[len(session.Compactions)-1]
		info.CompressionCount = len(session.Compactions)
		info.CompressedMessages = latest.CompactedMessages
		info.RetainedMessages = len(session.Messages) - latest.CompactedMessages
	}
	if settings.Protocol == string(openai.Responses) {
		info.HistoryMode = "responses_full_input"
	}
	for _, message := range session.Messages {
		if message.Role == string(agent.RoleUser) {
			info.UserTurns++
		}
	}
	for index := len(session.Events) - 1; index >= 0; index-- {
		event := session.Events[index]
		if event.Kind != string(agent.EventModelEnd) {
			continue
		}
		info.LastInputTokens = event.InputTokens
		info.LastCachedTokens = event.CachedTokens
		info.LastCacheWriteTokens = event.CacheWriteTokens
		info.CacheReported = event.CacheReported
		if event.HistoryMode != "" {
			info.HistoryMode = event.HistoryMode
		}
		info.RequestMessages = event.RequestMessages
		info.ToolDefinitions = event.ToolDefinitions
		break
	}
	session.Context = info
}

func toCoreMessage(value store.Message) agent.Message {
	message := agent.Message{Role: agent.Role(value.Role), Content: value.Content, ToolCallID: value.ToolCallID, Name: value.Name}
	for _, call := range value.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, agent.ToolCall{ID: call.ID, Name: call.Name, Arguments: json.RawMessage(call.Arguments)})
	}
	return message
}

func fromCoreMessage(value agent.Message) store.Message {
	message := store.Message{Role: string(value.Role), Content: value.Content, ToolCallID: value.ToolCallID, Name: value.Name, ToolCalls: []store.ToolCall{}, CreatedAt: time.Now()}
	for _, call := range value.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, store.ToolCall{ID: call.ID, Name: call.Name, Arguments: string(call.Arguments)})
	}
	return message
}

func newID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}

func makeTitle(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if utf8.RuneCountInString(message) <= 36 {
		return message
	}
	runes := []rune(message)
	return string(runes[:36]) + "…"
}

// expandContinuation 只改变本轮发给模型的短指令，不修改 SQLite 中的用户原话。
// 小模型容易把“继续”当成缺少目标；显式指出它引用紧邻历史，可稳定多轮体验。
func expandContinuation(role agent.Role, content string) string {
	if role != agent.RoleUser {
		return content
	}
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(content)), "。.!！ ")
	switch normalized {
	case "继续", "继续吧", "接着", "接着做", "go on", "continue":
		return content + "\n\n<continuation_instruction>这是对紧邻上一轮任务的延续。不要询问用户要继续什么，也不要重复已有内容；直接在上一条回答基础上补充下一层最有价值的具体内容。</continuation_instruction>"
	default:
		return content
	}
}
