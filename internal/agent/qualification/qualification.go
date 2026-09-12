// Package qualification implements the provider-independent minimum model
// qualification used by both the EasyAgent server and the local eval command.
package qualification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
)

const ToolName = "easyagent_diagnostic_echo"

type Result struct {
	Model              string
	ToolCall           string
	Answer             string
	InputTokens        int
	OutputTokens       int
	Duration           time.Duration
	FirstTokenDuration time.Duration
}

// Run verifies the contract EasyAgent actually depends on: the model must emit
// a protocol-native function call with schema-valid arguments, accept a typed
// tool result, and then produce visible streamed text with tools disabled.
func Run(ctx context.Context, model agent.Model, modelName string) (Result, error) {
	result := Result{Model: strings.TrimSpace(modelName)}
	startedAt := time.Now()
	var firstTokenOnce sync.Once
	markFirstToken := func(delta string) {
		if delta == "" {
			return
		}
		firstTokenOnce.Do(func() { result.FirstTokenDuration = time.Since(startedAt) })
	}
	finish := func(err error) (Result, error) {
		result.Duration = time.Since(startedAt)
		return result, err
	}

	tool := agent.ToolSpec{
		Name:        ToolName,
		Description: "EasyAgent 模型能力测试工具；收到要求时必须调用。",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
			"required":   []string{"text"},
		},
	}
	messages := []agent.Message{
		{Role: agent.RoleSystem, Content: "这是 EasyAgent Function Calling 能力测试。必须先调用提供的工具，参数 text 必须为 ping；拿到工具结果后，只回答结果中的 answer。"},
		{Role: agent.RoleUser, Content: "开始测试。"},
	}
	first, err := model.Generate(ctx, agent.Request{
		Model: result.Model, Messages: messages, Tools: []agent.ToolSpec{tool},
		ToolChoice: agent.ToolChoice{Mode: agent.ToolChoiceAuto}, MaxOutputTokens: 128,
		OnTextDelta: markFirstToken,
	})
	result.InputTokens += first.Usage.InputTokens
	result.OutputTokens += first.Usage.OutputTokens
	if err == nil {
		err = agent.ValidateModelResponse(&first)
	}
	if err != nil {
		return finish(err)
	}
	if len(first.Message.ToolCalls) != 1 || first.Message.ToolCalls[0].Name != tool.Name {
		return finish(fmt.Errorf("没有返回协议原生 Function Call，而是返回了普通文本 %q；EasyAgent 不会解析文本伪工具调用", strings.TrimSpace(first.Message.Content)))
	}
	var arguments struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(first.Message.ToolCalls[0].Arguments, &arguments) != nil || arguments.Text != "ping" {
		return finish(fmt.Errorf("工具参数不符合 JSON Schema：%s", string(first.Message.ToolCalls[0].Arguments)))
	}
	call := first.Message.ToolCalls[0]
	result.ToolCall = call.Name
	toolResult := agent.NewToolResult(`{"answer":"EASYAGENT_OK"}`)
	messages = append(messages, first.Message, agent.Message{
		Role: agent.RoleTool, Name: tool.Name, ToolCallID: call.ID, Content: toolResult.ModelText(), ToolResult: &toolResult,
	})
	second, err := model.Generate(ctx, agent.Request{
		Model: result.Model, Messages: messages, Tools: []agent.ToolSpec{tool},
		ToolChoice: agent.ToolChoice{Mode: agent.ToolChoiceNone}, MaxOutputTokens: 64,
		OnTextDelta: markFirstToken,
	})
	result.InputTokens += second.Usage.InputTokens
	result.OutputTokens += second.Usage.OutputTokens
	if err == nil {
		err = agent.ValidateModelResponse(&second)
	}
	if err != nil {
		return finish(err)
	}
	result.Answer = strings.TrimSpace(second.Message.Content)
	if !strings.Contains(result.Answer, "EASYAGENT_OK") {
		return finish(fmt.Errorf("模型没有正确使用工具结果：%q", result.Answer))
	}
	return finish(nil)
}
