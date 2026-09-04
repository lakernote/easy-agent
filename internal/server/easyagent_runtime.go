package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/openai"
	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/builtin/prompt"
	builtintools "github.com/lakernote/easy-agent/internal/builtin/tools"
	"github.com/lakernote/easy-agent/internal/mcpclient"
	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) runEasyAgentTurn(ctx context.Context, id string, session store.Session, settings store.ModelSettings, runEnvironment *appenv.Environment, usage *store.Usage) error {
	settings = enrichOllamaContextWindow(ctx, settings)
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
	// 只把少量高频工具常驻首轮；文件、网页和 Skill 等较大能力
	// 仍只发送精简目录，避免“全量 Schema”在每个模型回合重复计费。
	selectedToolNamesForTurn := selectedToolNames(session.Messages)
	activeTools := toolLoader.PreloadCore()
	activeTools = append(activeTools, toolLoader.Tool())
	activeTools = append(activeTools, toolLoader.Preload(selectedToolNamesForTurn)...)
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
		Now: time.Now(), Workspace: runEnvironment.Workspace(), Skills: skillMeta, MCPs: mcpMeta, SelectedSkills: selectedSkills(session.Messages, catalog), SelectedTools: selectedToolNamesForTurn,
	})
	coreMessages := coreMessagesForSession(session, systemPrompt)
	// 会话跨轮时，保持历史 function call 与当前 tools Schema 一致。
	// Preload 只会恢复仍在内置目录中的工具，且会自动去重。
	activeTools = append(activeTools, toolLoader.Preload(historicalToolNames(coreMessages))...)
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

	coreMessages = coreMessagesForSession(session, systemPrompt)
	// @tool 是 UI 控制标记，不是业务问题的一部分。保留它在数据库和页面中
	// 便于审计，但不要让模型把标记当成普通文本而绕过 function calling。
	for index := range coreMessages {
		if coreMessages[index].Role == agent.RoleUser {
			coreMessages[index].Content = stripToolMentions(coreMessages[index].Content)
		}
	}
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
		RequiredToolNames: selectedToolNamesForTurn,
		PromptCacheKey:    promptCacheKey(settings),
		OnTextDelta:       func(delta string) { server.tasks.appendPartial(id, delta) },
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
