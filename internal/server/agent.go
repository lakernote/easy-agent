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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/openai"
	"github.com/lakernote/easy-agent/internal/builtin/prompt"
	builtintools "github.com/lakernote/easy-agent/internal/builtin/tools"
	"github.com/lakernote/easy-agent/internal/mcpclient"
	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) enqueueTurn(id, userMessage string, attachments []store.Attachment, model store.ModelSettings) error {
	if server.hasTask(id) {
		return errors.New("上一条任务正在结束，请稍后再发送")
	}
	if err := server.store.QueueSession(id, model.Model, time.Now()); err != nil {
		return err
	}
	if err := server.store.AppendMessage(id, store.Message{Role: "user", Content: userMessage, Attachments: attachments, ToolCalls: []store.ToolCall{}, CreatedAt: time.Now()}); err != nil {
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
		if err := server.runAgentTurn(taskContext, id, model, &usage); err != nil {
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

func (server *Server) runAgentTurn(ctx context.Context, id string, settings store.ModelSettings, usage *store.Usage) error {
	settings = enrichOllamaContextWindow(ctx, settings)
	session, err := server.store.RuntimeSession(id)
	if err != nil {
		return err
	}
	// 工作区属于会话，而不是服务进程。旧会话的空值自动落到默认工作区；新会话
	// 使用页面创建时选择并保存的绝对目录。
	runEnvironment, err := server.env.WithWorkspace(session.Workspace)
	if err != nil {
		return fmt.Errorf("打开会话工作区: %w", err)
	}
	turn := session.UserTurnCount
	catalog, err := loadSkillCatalog(server.store)
	if err != nil {
		return err
	}
	skills := catalog.EnabledSkills()
	skillMeta := make([]prompt.SkillMeta, 0, len(skills))
	for _, skill := range skills {
		skillMeta = append(skillMeta, prompt.SkillMeta{Name: skill.Name, Description: skill.Description})
	}

	toolCatalog := builtintools.Catalog(runEnvironment, catalog)
	toolLoader, err := builtintools.NewLoader(toolCatalog)
	if err != nil {
		return err
	}
	// 只把极少数高频、低风险工具常驻首轮；文件、Shell、网页和 Skill 等较大
	// 能力仍只发送精简目录，避免“全量 Schema”在每个模型回合重复计费。
	activeTools := toolLoader.PreloadCore()
	activeTools = append(activeTools, toolLoader.Tool())
	activeTools = append(activeTools, toolLoader.Preload(selectedToolNames(session.Messages))...)
	mcps, err := server.store.ListMCPConfigs()
	if err != nil {
		return err
	}
	mcpLoader := mcpclient.NewLoader(runEnvironment, mcpClientConfigs(mcps))
	defer mcpLoader.Close()
	selectedMCPTools, err := mcpLoader.Preload(ctx, selectedMCPIDs(session.Messages))
	if err != nil {
		return err
	}
	mcpMeta := make([]prompt.MCPMeta, 0)
	for _, info := range mcpLoader.Servers() {
		mcpMeta = append(mcpMeta, prompt.MCPMeta{ID: info.ID, Name: info.Name, Description: info.Description})
	}
	if !mcpLoader.Empty() {
		activeTools = append(activeTools, selectedMCPTools...)
		activeTools = append(activeTools, mcpLoader.Tool())
	}
	apiKey := settings.APIKey
	if settings.APIKeyEnv != "" {
		apiKey = os.Getenv(settings.APIKeyEnv)
	}
	client, err := openai.New(openai.Config{
		BaseURL: settings.BaseURL, APIKey: apiKey, Protocol: openai.Protocol(settings.Protocol),
		DisableThinking:      settings.Thinking == "disabled",
		KeepThinkingForTools: settings.IsOllama(),
		Timeout:              time.Duration(settings.RequestTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return err
	}
	systemPrompt := prompt.Render(prompt.Context{
		Now: time.Now(), Workspace: runEnvironment.Workspace(), Skills: skillMeta, MCPs: mcpMeta, SelectedSkills: selectedSkills(session.Messages, catalog),
	})
	didCompact, err := server.compactIfNeeded(ctx, &session, settings, client, systemPrompt, activeTools, turn, usage, runtimeCompactionThreshold(settings), false)
	if err != nil {
		return err
	}
	runner, err := agent.NewRunner(client, settings.Model, activeTools)
	if err != nil {
		return err
	}
	mcpLoader.SetRegister(runner.AddTools)
	toolLoader.SetRegister(runner.AddTools)
	runner.MaxOutputTokens = settings.MaxOutputTokens
	var traceErr error
	var traceMu sync.Mutex
	recordTraceError := func(err error) {
		traceMu.Lock()
		defer traceMu.Unlock()
		if traceErr == nil {
			traceErr = err
		}
	}
	runner.Observe = server.newTraceObserver(id, turn, usage, recordTraceError)

	coreMessages := coreMessagesForSession(session, systemPrompt)
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
		OnTurnMessages: func(messages []agent.Message) error {
			values := make([]store.Message, 0, len(messages))
			for _, message := range messages {
				values = append(values, fromCoreMessage(message))
			}
			return server.store.AppendMessages(id, values)
		},
		PrepareRequest: func(ctx context.Context, request agent.Request, force bool) (agent.Request, bool, error) {
			return server.prepareRuntimeRequest(ctx, id, settings, systemPrompt, turn, usage, client, runner, request, force)
		},
		IsContextError: isContextLengthError,
	})
	if err != nil {
		return err
	}
	traceMu.Lock()
	savedTraceErr := traceErr
	traceMu.Unlock()
	if savedTraceErr != nil {
		return fmt.Errorf("保存 Agent Trace 失败: %w", savedTraceErr)
	}
	return server.store.FinishSession(id, result.ResponseID, providerKey, *usage, time.Now())
}

func coreMessagesForSession(session store.Session, systemPrompt string) []agent.Message {
	coreMessages := []agent.Message{{Role: agent.RoleSystem, Content: systemPrompt}}
	var compactedThrough int64
	if len(session.Compactions) > 0 {
		latest := session.Compactions[len(session.Compactions)-1]
		compactedThrough = latest.ThroughMessageID
		checkpointRole := agent.RoleSystem
		checkpointText := "此前会话的上下文检查点：\n\n" + latest.Summary
		if latest.SplitTurn {
			// split-turn 的后缀可能从 assistant/tool 开始。用一个合成的
			// user checkpoint 承接它，保证 Chat Completions 和 Responses
			// 都不会收到孤立的 tool result 或不完整的消息轮次。
			checkpointRole = agent.RoleUser
			checkpointText = "此前会话上下文检查点（当前轮次的前半段已压缩）：\n\n" + latest.Summary + "\n\n请继续处理下面保留的当前轮次内容。"
		}
		coreMessages = append(coreMessages, agent.Message{Role: checkpointRole, Content: checkpointText})
	}
	activeMessages := make([]store.Message, 0, len(session.Messages))
	for _, message := range session.Messages {
		if message.ID > compactedThrough {
			activeMessages = append(activeMessages, message)
		}
	}
	for _, message := range protocolSafeStoredMessages(activeMessages) {
		coreMessages = append(coreMessages, toCoreMessage(message))
	}
	return coreMessages
}

// protocolSafeStoredMessages 只过滤历史中的未闭合工具链，不修改 SQLite。
// 这样旧版本或异常退出留下的 assistant(tool_call) 不会在下一轮再次发送给
// Provider；完整的 Assistant + Tool Results 则保持原有顺序。
func protocolSafeStoredMessages(messages []store.Message) []store.Message {
	result := make([]store.Message, 0, len(messages))
	for index := 0; index < len(messages); {
		message := messages[index]
		if message.Role == string(agent.RoleTool) {
			// tool result 没有位于它前面的、已确认完整的 assistant 调用。
			break
		}
		if message.Role != string(agent.RoleAssistant) || len(message.ToolCalls) == 0 {
			result = append(result, message)
			index++
			continue
		}

		callIDs := make(map[string]struct{}, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			id := strings.TrimSpace(call.ID)
			if id == "" {
				return result
			}
			callIDs[id] = struct{}{}
		}
		toolResults := 0
		for next := index + 1; next < len(messages) && messages[next].Role == string(agent.RoleTool); next++ {
			id := strings.TrimSpace(messages[next].ToolCallID)
			if _, ok := callIDs[id]; !ok {
				return result
			}
			toolResults++
		}
		if toolResults != len(callIDs) {
			return result
		}
		result = append(result, message)
		result = append(result, messages[index+1:index+1+toolResults]...)
		index += 1 + toolResults
	}
	return result
}

// promptCacheKey 仅发送给已知支持该字段的 OpenAI 服务。缓存键跨会话稳定，
// 真正是否命中仍由请求前缀决定，并以 Provider 返回的 cached_tokens 为准。
func promptCacheKey(settings store.ModelSettings) string {
	if settings.IsOfficialOpenAI() {
		return "easyagent-core-v1"
	}
	return ""
}

func (server *Server) newTraceObserver(id string, turn int, usage *store.Usage, onTraceError func(error)) agent.Observer {
	return func(event agent.Event) {
		value := store.Event{Kind: string(event.Kind), Turn: turn, Step: event.Step, Attempt: event.Attempt, Status: "success", CreatedAt: time.Now(), DurationMS: event.Duration.Milliseconds()}
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
			value.StatusCode = event.Exchange.StatusCode
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
		// Trace 面向本机运行审计，必须保留工具真实返回的工作目录和文件路径，
		// 否则用户无法根据 Trace 复现问题。附件二进制仍只保留结构和 MIME。
		value.Input = redactTraceAttachmentData(value.Input)
		value.Output = redactTraceAttachmentData(value.Output)
		if err := server.store.AppendEvent(id, value); err != nil && onTraceError != nil {
			onTraceError(err)
		}
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
	for _, attachment := range value.Attachments {
		message.Attachments = append(message.Attachments, agent.Attachment{Name: attachment.Name, MIMEType: attachment.MIMEType, Kind: attachment.Kind, Data: attachment.Data})
	}
	for _, call := range value.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, agent.ToolCall{ID: call.ID, Name: call.Name, Arguments: json.RawMessage(call.Arguments)})
	}
	return message
}

func fromCoreMessage(value agent.Message) store.Message {
	message := store.Message{Role: string(value.Role), Content: value.Content, Attachments: []store.Attachment{}, ToolCallID: value.ToolCallID, Name: value.Name, ToolCalls: []store.ToolCall{}, CreatedAt: time.Now()}
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
