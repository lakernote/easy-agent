package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	defaultMaxSteps = 12
	// Shell 自己默认 60 秒、最多 300 秒。Runner 的外层上限必须允许 Shell 的
	// 显式长任务，否则页面写 300 秒而实际 60 秒就会被提前取消。
	defaultToolTimeout = 5 * time.Minute
)

// Runner 是 EasyAgent 的核心。它只负责一个很小的循环：
//
//	模型 -> 工具调用 -> 工具结果 -> 模型 -> 最终回答
//
// Skill 通过提示词指导模型，MCP 只需注册成 Tool；二者都不需要在这里增加分支。
type Runner struct {
	Model           Model
	ModelName       string
	Tools           []Tool
	Temperature     float64
	MaxOutputTokens int
	ReasoningEffort string
	Observe         Observer
	modelToolSpecs  []ToolSpec
	toolsByName     map[string]Tool
}

// NewRunner 校验并索引工具。工具名必须唯一，避免模型调用时产生歧义。
func NewRunner(model Model, modelName string, tools []Tool) (*Runner, error) {
	if model == nil {
		return nil, errors.New("Agent 缺少模型")
	}
	// Temperature 为 0 时适配器会省略该字段。OpenAI 的部分推理模型不接受
	// temperature，而 Ollama/DeepSeek 也不需要 Runtime 强行覆盖模型默认值。
	runner := &Runner{Model: model, ModelName: strings.TrimSpace(modelName), MaxOutputTokens: 1600}
	runner.toolsByName = make(map[string]Tool, len(tools))
	runner.modelToolSpecs = make([]ToolSpec, 0, len(tools))
	if err := runner.AddTools(tools); err != nil {
		return nil, err
	}
	return runner, nil
}

// AddTools 在一次运行中动态追加工具。它让工具组或 MCP 搜索先暴露一个很小的入口，
// 只有模型确认需要某项能力时，才把少量真实工具加入下一轮模型请求。
// 整批工具会先完成校验再写入，避免校验失败后只追加了一半。
func (runner *Runner) AddTools(tools []Tool) error {
	if runner == nil {
		return errors.New("Agent 尚未初始化")
	}
	prepared := make([]Tool, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Spec.Name)
		if name == "" || tool.Run == nil {
			return errors.New("Agent 工具缺少名称或执行器")
		}
		if _, exists := runner.toolsByName[name]; exists {
			return fmt.Errorf("Agent 工具 %q 重复", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("Agent 工具 %q 重复", name)
		}
		seen[name] = struct{}{}
		tool.Spec.Name = name
		if tool.Spec.Parameters == nil {
			tool.Spec.Parameters = map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		}
		prepared = append(prepared, tool)
	}
	for _, tool := range prepared {
		name := tool.Spec.Name
		runner.toolsByName[name] = tool
		runner.modelToolSpecs = append(runner.modelToolSpecs, tool.Spec)
		runner.Tools = append(runner.Tools, tool)
	}
	return nil
}

// Run 执行一次任务。最后一步会停止暴露工具，要求模型基于已有结果收敛回答。
func (runner *Runner) Run(ctx context.Context, input RunRequest) (RunResult, error) {
	if runner == nil || runner.Model == nil {
		return RunResult{}, errors.New("Agent 尚未初始化")
	}
	if len(input.Messages) == 0 {
		return RunResult{}, errors.New("Agent 至少需要一条消息")
	}
	maxSteps := input.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	toolTimeout := input.ToolTimeout
	if toolTimeout <= 0 {
		toolTimeout = defaultToolTimeout
	}

	messages := append([]Message(nil), input.Messages...)
	pending := append([]Message(nil), input.NewMessages...)
	if len(pending) == 0 {
		pending = append(pending, input.Messages...)
	}
	previousResponseID := strings.TrimSpace(input.PreviousResponseID)
	totalUsage := Usage{}
	consecutiveFailedToolSteps := 0
	forceConverge := false
	requireLoadedToolCall := false
	requiredToolNames := availableRequiredTools(runner.modelToolSpecs, input.RequiredToolNames)
	failedCalls := make(map[string]failedToolCall)

	for step := 1; step <= maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return RunResult{}, err
		}
		tools := runner.modelToolSpecs
		toolChoice := ToolChoice{Mode: ToolChoiceNone}
		if len(tools) > 0 {
			toolChoice = ToolChoice{Mode: ToolChoiceAuto}
		}
		if step == 1 && len(requiredToolNames) > 0 {
			// 只暴露用户明确选择的工具。部分 Ollama 模型会忽略 required
			// tool_choice，因此这里使用 auto 配合 System Prompt 约束，仍由
			// 下面的 Runner 校验保证未调用时不会被当成成功。
			tools = filterToolsByName(tools, requiredToolNames)
			toolChoice = ToolChoice{Mode: ToolChoiceAuto}
		}
		if requireLoadedToolCall {
			// Loader 的结果只代表“能力已可用”，不是用户任务的证据。下一轮临时
			// 隐藏所有 Loader，并要求模型从真实工具中选择至少一个调用。
			tools = nonLoaderTools(tools)
			if len(tools) > 0 {
				toolChoice = ToolChoice{Mode: ToolChoiceRequired}
			}
		}
		if forceConverge {
			tools = nil
			toolChoice = ToolChoice{Mode: ToolChoiceNone}
		}
		if step == maxSteps {
			tools = nil
			toolChoice = ToolChoice{Mode: ToolChoiceNone}
		}
		attempt := 1
		request := Request{
			Model: runner.ModelName, Messages: messages, NewMessages: pending, Tools: tools,
			ToolChoice: toolChoice, PromptCacheKey: input.PromptCacheKey,
			Temperature: runner.Temperature, MaxOutputTokens: runner.MaxOutputTokens,
			ReasoningEffort: runner.ReasoningEffort, PreviousResponseID: previousResponseID,
			OnTextDelta: input.OnTextDelta,
		}
		if input.PrepareRequest != nil {
			prepared, changed, err := input.PrepareRequest(ctx, request, false)
			if err != nil {
				return RunResult{}, err
			}
			request = prepared
			if changed {
				messages = append([]Message(nil), request.Messages...)
				pending = append([]Message(nil), request.NewMessages...)
				previousResponseID = strings.TrimSpace(request.PreviousResponseID)
			}
			tools = request.Tools
		}
		// PrepareRequest 可能触发压缩、改写历史或直接失败；只有准备成功后
		// 才记录 model_start，保证 Trace 代表真实即将发出的模型请求。
		runner.emit(Event{Kind: EventModelStart, Step: step, Attempt: attempt, StartedAt: time.Now()})
		response, err := runner.Model.Generate(ctx, request)
		if err == nil && strings.TrimSpace(response.Message.Content) == "" && len(response.Message.ToolCalls) == 0 {
			err = ErrEmptyModelResponse
		}
		runner.emit(Event{Kind: EventModelEnd, Step: step, Attempt: attempt, Exchange: response.Exchange, Err: err, Duration: response.Exchange.Duration})
		// 与成熟 CLI Agent 一致，只重试明确的瞬时故障：429、5xx 或网络临时
		// 错误。4xx、工具参数和模型内容错误都不会在这里盲目重放。
		if err != nil && retryableModelError(err) {
			addUsage(&totalUsage, response.Usage)
			if waitErr := waitForRetry(ctx, modelRetryDelay(err, attempt)); waitErr != nil {
				return RunResult{}, waitErr
			}
			attempt++
			runner.emit(Event{Kind: EventModelStart, Step: step, Attempt: attempt, StartedAt: time.Now()})
			response, err = runner.Model.Generate(ctx, request)
			if err == nil && strings.TrimSpace(response.Message.Content) == "" && len(response.Message.ToolCalls) == 0 {
				err = ErrEmptyModelResponse
			}
			runner.emit(Event{Kind: EventModelEnd, Step: step, Attempt: attempt, Exchange: response.Exchange, Err: err, Duration: response.Exchange.Duration})
		}
		// 少数兼容 Provider 会在 stream + tools 组合下只生成隐藏推理并返回空正文。
		// 首次调用已经完整进入 Trace；关闭流式重试一次，避免把空回答伪装成成功。
		// 如果本轮已经取得真实工具结果，则同时进入无工具收敛轮，明确要求模型把
		// 结论写入可见 content。不能直接展示 reasoning 字段，因为它可能包含模型
		// 的隐藏推理过程，而不只是最终答案。
		if err != nil && errors.Is(err, ErrEmptyModelResponse) && request.OnTextDelta != nil {
			addUsage(&totalUsage, response.Usage)
			request = prepareEmptyResponseRetry(request)
			attempt++
			runner.emit(Event{Kind: EventModelStart, Step: step, Attempt: attempt, StartedAt: time.Now()})
			response, err = runner.Model.Generate(ctx, request)
			if err == nil && strings.TrimSpace(response.Message.Content) == "" && len(response.Message.ToolCalls) == 0 {
				err = ErrEmptyModelResponse
			}
			runner.emit(Event{Kind: EventModelEnd, Step: step, Attempt: attempt, Exchange: response.Exchange, Err: err, Duration: response.Exchange.Duration})
		}
		if err != nil && input.PrepareRequest != nil && input.IsContextError != nil && input.IsContextError(err) {
			prepared, changed, prepareErr := input.PrepareRequest(ctx, request, true)
			if prepareErr != nil {
				return RunResult{}, fmt.Errorf("%w；自动压缩失败: %v", err, prepareErr)
			}
			if changed {
				request = prepared
				messages = append([]Message(nil), request.Messages...)
				pending = append([]Message(nil), request.NewMessages...)
				previousResponseID = strings.TrimSpace(request.PreviousResponseID)
				attempt++
				runner.emit(Event{Kind: EventModelStart, Step: step, Attempt: attempt, StartedAt: time.Now()})
				response, err = runner.Model.Generate(ctx, request)
				if err == nil && strings.TrimSpace(response.Message.Content) == "" && len(response.Message.ToolCalls) == 0 {
					err = ErrEmptyModelResponse
				}
				runner.emit(Event{Kind: EventModelEnd, Step: step, Attempt: attempt, Exchange: response.Exchange, Err: err, Duration: response.Exchange.Duration})
			}
		}
		if err != nil {
			return RunResult{}, err
		}
		addUsage(&totalUsage, response.Usage)
		if response.ID != "" {
			previousResponseID = response.ID
		}
		assistant := response.Message
		assistant.Role = RoleAssistant
		for index, call := range assistant.ToolCalls {
			if strings.TrimSpace(call.ID) == "" {
				assistant.ToolCalls[index].ID = fmt.Sprintf("call_%d_%d", step, index+1)
			}
		}
		messages = append(messages, assistant)
		turnMessages := []Message{assistant}
		if input.OnTurnMessages == nil && input.OnMessage != nil {
			if err := input.OnMessage(assistant); err != nil {
				return RunResult{}, err
			}
		}

		if len(assistant.ToolCalls) == 0 {
			if step == 1 && len(requiredToolNames) > 0 {
				return RunResult{}, fmt.Errorf("用户明确指定了工具 %s，但模型没有发起工具调用", strings.Join(requiredToolNames, ", "))
			}
			if requireLoadedToolCall {
				return RunResult{}, errors.New("模型在能力加载后没有调用真实工具")
			}
			if input.OnTurnMessages != nil {
				if err := input.OnTurnMessages(turnMessages); err != nil {
					return RunResult{}, err
				}
			}
			answer := strings.TrimSpace(assistant.Content)
			if answer == "" {
				return RunResult{}, errors.New("模型既没有返回回答，也没有调用工具")
			}
			return RunResult{Answer: answer, Messages: messages, Usage: totalUsage, ResponseID: previousResponseID, Steps: step}, nil
		}
		if len(tools) == 0 {
			return RunResult{}, errors.New("本步已禁用工具，但模型仍在请求工具")
		}

		pending = pending[:0]
		failedToolCalls := 0
		calledLoader := false
		calledRealTool := false
		for _, call := range assistant.ToolCalls {
			key := toolCallKey(call)
			previous, repeated := failedCalls[key]
			var output string
			var toolErr error
			var duration time.Duration
			if repeated && (!previous.retryable || previous.attempts >= 2) {
				startedAt := time.Now()
				runner.emit(Event{Kind: EventToolStart, Step: step, ToolCall: &call, StartedAt: startedAt})
				toolErr = &ToolError{
					Code: "duplicate_failed_call", Message: fmt.Sprintf("相同的工具调用 %s 已失败，不会重复执行", call.Name),
					Hint: "检查上一条错误，修改参数、换用其他工具，或基于已有证据回答", Retryable: false,
				}
				output = toolErrorOutput(toolErr)
				duration = time.Since(startedAt)
			} else {
				output, toolErr, duration = runner.runTool(ctx, step, call, toolTimeout)
			}
			if toolErr != nil {
				failedToolCalls++
				retryable := false
				var failure *ToolError
				if errors.As(toolErr, &failure) {
					retryable = failure.Retryable
				}
				failedCalls[key] = failedToolCall{attempts: previous.attempts + 1, retryable: retryable}
			} else {
				delete(failedCalls, key)
				if tool, exists := runner.toolsByName[call.Name]; exists && tool.Spec.Loader {
					calledLoader = true
				} else {
					calledRealTool = true
				}
			}
			toolMessage := Message{Role: RoleTool, Name: call.Name, ToolCallID: call.ID, Content: output}
			messages = append(messages, toolMessage)
			pending = append(pending, toolMessage)
			turnMessages = append(turnMessages, toolMessage)
			if input.OnTurnMessages == nil && input.OnMessage != nil {
				if err := input.OnMessage(toolMessage); err != nil {
					return RunResult{}, err
				}
			}
			runner.emit(Event{Kind: EventToolEnd, Step: step, ToolCall: &call, Output: output, Err: toolErr, Duration: duration})
		}
		if failedToolCalls == len(assistant.ToolCalls) {
			consecutiveFailedToolSteps++
		} else {
			consecutiveFailedToolSteps = 0
		}
		// 连续两步所有工具都失败说明 Agent 没有取得新证据。停止继续暴露工具，
		// 让模型根据现有错误收敛，而不是用略有不同的参数无限重复同一尝试。
		forceConverge = consecutiveFailedToolSteps >= 2
		requireLoadedToolCall = calledLoader && !calledRealTool
		if input.OnTurnMessages != nil {
			if err := input.OnTurnMessages(turnMessages); err != nil {
				return RunResult{}, err
			}
		}
	}
	return RunResult{}, errors.New("Agent 达到最大步数")
}

const visibleAnswerReminder = "上一响应没有可展示的最终正文。不要重复调用已经成功的工具；请根据已有工具结果，在 assistant content 中直接给出最终答案。"

// prepareEmptyResponseRetry 只在已有工具结果时强制收敛。首次工具选择轮如果
// 返回空消息，仍保留原工具集并失败关闭，避免模型跳过用户要求的真实执行。
func prepareEmptyResponseRetry(request Request) Request {
	request.OnTextDelta = nil
	if !containsToolResult(request.Messages) {
		return request
	}
	request.Tools = nil
	request.ToolChoice = ToolChoice{Mode: ToolChoiceNone}
	reminder := Message{Role: RoleSystem, Content: visibleAnswerReminder}
	request.Messages = append(append([]Message(nil), request.Messages...), reminder)
	request.NewMessages = append(append([]Message(nil), request.NewMessages...), reminder)
	return request
}

func containsToolResult(messages []Message) bool {
	for _, message := range messages {
		if message.Role == RoleTool {
			return true
		}
	}
	return false
}

func nonLoaderTools(tools []ToolSpec) []ToolSpec {
	result := make([]ToolSpec, 0, len(tools))
	for _, tool := range tools {
		if !tool.Loader {
			result = append(result, tool)
		}
	}
	return result
}

func availableRequiredTools(tools []ToolSpec, names []string) []string {
	available := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if !tool.Loader {
			available[tool.Name] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := available[name]; !ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func filterToolsByName(tools []ToolSpec, names []string) []ToolSpec {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	result := make([]ToolSpec, 0, len(names))
	for _, tool := range tools {
		if _, ok := allowed[tool.Name]; ok {
			result = append(result, tool)
		}
	}
	return result
}

func (runner *Runner) runTool(ctx context.Context, step int, call ToolCall, timeout time.Duration) (string, error, time.Duration) {
	startedAt := time.Now()
	runner.emit(Event{Kind: EventToolStart, Step: step, ToolCall: &call, StartedAt: startedAt})
	tool, ok := runner.toolsByName[call.Name]
	if !ok {
		err := fmt.Errorf("模型请求了未知工具 %q", call.Name)
		return toolErrorOutput(err), err, time.Since(startedAt)
	}
	arguments := call.Arguments
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	if !json.Valid(arguments) {
		err := fmt.Errorf("工具 %q 的参数不是有效 JSON", call.Name)
		return toolErrorOutput(err), err, time.Since(startedAt)
	}
	toolContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := tool.Run(toolContext, arguments)
	if err != nil {
		// 有些工具（例如 Shell、MCP）失败时仍能返回退出码、stderr 等重要证据。
		// 保留这部分结构化输出，只有工具完全没有结果时才生成统一错误 JSON。
		if strings.TrimSpace(output) == "" {
			output = toolErrorOutput(err)
		}
		return output, err, time.Since(startedAt)
	}
	return output, nil, time.Since(startedAt)
}

func toolErrorOutput(err error) string {
	value := map[string]any{"ok": false, "code": "tool_error", "error": err.Error(), "retryable": false}
	var failure *ToolError
	if errors.As(err, &failure) {
		if strings.TrimSpace(failure.Code) != "" {
			value["code"] = failure.Code
		}
		value["retryable"] = failure.Retryable
		if strings.TrimSpace(failure.Hint) != "" {
			value["hint"] = failure.Hint
		}
	}
	body, _ := json.Marshal(value)
	return string(body)
}

type failedToolCall struct {
	attempts  int
	retryable bool
}

func toolCallKey(call ToolCall) string {
	arguments := call.Arguments
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, arguments); err != nil {
		return call.Name + "\x00" + string(arguments)
	}
	return call.Name + "\x00" + compact.String()
}

func (runner *Runner) emit(event Event) {
	if runner.Observe != nil {
		runner.Observe(event)
	}
}

func addUsage(total *Usage, current Usage) {
	total.InputTokens += current.InputTokens
	total.OutputTokens += current.OutputTokens
	total.CachedInputTokens += current.CachedInputTokens
	total.CacheWriteTokens += current.CacheWriteTokens
	total.TotalTokens += current.TotalTokens
	total.CacheReported = total.CacheReported || current.CacheReported
	if current.TotalTokens == 0 {
		total.TotalTokens += current.InputTokens + current.OutputTokens
	}
}

func retryableModelError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var provider *ModelError
	if errors.As(err, &provider) {
		return provider.Retryable()
	}
	var network net.Error
	return errors.As(err, &network) && (network.Timeout() || network.Temporary())
}

func modelRetryDelay(err error, attempt int) time.Duration {
	var provider *ModelError
	if errors.As(err, &provider) && provider.RetryAfter > 0 {
		if provider.RetryAfter > 30*time.Second {
			return 30 * time.Second
		}
		return provider.RetryAfter
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := 250 * time.Millisecond * time.Duration(1<<(attempt-1))
	if delay > 2*time.Second {
		return 2 * time.Second
	}
	return delay
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
