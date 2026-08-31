package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type modelFunc func(context.Context, Request) (Response, error)

func (function modelFunc) Generate(ctx context.Context, request Request) (Response, error) {
	return function(ctx, request)
}

func TestRunnerFeedsToolResultBackAsToolMessage(t *testing.T) {
	requests := []Request{}
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		requests = append(requests, request)
		if len(requests) == 1 {
			return Response{ID: "resp-1", Message: Message{ToolCalls: []ToolCall{{ID: "call-1", Name: "weather", Arguments: json.RawMessage(`{"city":"上海"}`)}}}}, nil
		}
		return Response{ID: "resp-2", Message: Message{Content: "上海今天晴。"}}, nil
	})
	runner, err := NewRunner(model, "fixture", []Tool{{
		Spec: ToolSpec{Name: "weather", Parameters: map[string]any{"type": "object"}},
		Run:  func(context.Context, json.RawMessage) (string, error) { return `{"weather":"sunny"}`, nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "上海天气"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "上海今天晴。" || result.Steps != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	second := requests[1]
	if second.PreviousResponseID != "resp-1" || len(second.NewMessages) != 1 || second.NewMessages[0].Role != RoleTool || second.NewMessages[0].ToolCallID != "call-1" {
		t.Fatalf("tool result was not preserved: %+v", second)
	}
	if requests[0].ToolChoice.Mode != ToolChoiceAuto || len(requests[0].Tools) != 1 {
		t.Fatalf("普通轮次应把稳定工具集交给模型自主选择: %+v", requests[0])
	}
}

func TestRunnerDisablesToolsOnLastStep(t *testing.T) {
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		if len(request.Tools) != 0 {
			t.Fatalf("last step should not expose tools: %+v", request.Tools)
		}
		if request.ToolChoice.Mode != ToolChoiceNone {
			t.Fatalf("最后收敛轮应明确禁用工具: %+v", request.ToolChoice)
		}
		return Response{Message: Message{Content: "已收敛"}}, nil
	})
	runner, err := NewRunner(model, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "回答"}}, MaxSteps: 1})
	if err != nil || result.Answer != "已收敛" {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}

func TestRunnerRetriesEmptyStreamWithoutStreaming(t *testing.T) {
	calls := 0
	events := []Event{}
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		calls++
		if calls == 1 {
			if request.OnTextDelta == nil {
				t.Fatal("首次请求应启用流式回调")
			}
			usage := Usage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13}
			return Response{Usage: usage, Exchange: Exchange{Model: "fixture", Usage: usage}}, ErrEmptyModelResponse
		}
		if request.OnTextDelta != nil {
			t.Fatal("空流重试必须关闭流式")
		}
		return Response{Message: Message{Content: "重试成功"}, Usage: Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}}, nil
	})
	runner, err := NewRunner(model, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	runner.Observe = func(event Event) { events = append(events, event) }
	result, err := runner.Run(context.Background(), RunRequest{
		Messages: []Message{{Role: RoleUser, Content: "回答"}}, OnTextDelta: func(string) {},
	})
	if err != nil || result.Answer != "重试成功" || result.Usage.TotalTokens != 25 {
		t.Fatalf("空流自动重试异常: result=%+v err=%v", result, err)
	}
	if calls != 2 || len(events) != 4 || events[1].Err == nil || events[3].Err != nil {
		t.Fatalf("两次真实模型调用必须分别进入 Trace: calls=%d events=%+v", calls, events)
	}
	if events[0].Step != 1 || events[1].Step != 1 || events[2].Step != 1 || events[3].Step != 1 ||
		events[0].Attempt != 1 || events[1].Attempt != 1 || events[2].Attempt != 2 || events[3].Attempt != 2 {
		t.Fatalf("协议重试应保留同一 Agent Step，并增加 Attempt: %+v", events)
	}
}

func TestRunnerMarksEmptyNonStreamingResponseAsError(t *testing.T) {
	var modelEnd Event
	runner, err := NewRunner(modelFunc(func(context.Context, Request) (Response, error) {
		return Response{Exchange: Exchange{Model: "fixture"}}, nil
	}), "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	runner.Observe = func(event Event) {
		if event.Kind == EventModelEnd {
			modelEnd = event
		}
	}
	_, err = runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "回答"}}})
	if !errors.Is(err, ErrEmptyModelResponse) || !errors.Is(modelEnd.Err, ErrEmptyModelResponse) {
		t.Fatalf("空的成功响应必须在 Trace 和任务状态中标记失败: run=%v event=%v", err, modelEnd.Err)
	}
}

func TestRunnerRetriesTransientProviderErrorInSameStep(t *testing.T) {
	calls := 0
	events := make([]Event, 0)
	runner, err := NewRunner(modelFunc(func(context.Context, Request) (Response, error) {
		calls++
		if calls == 1 {
			return Response{Exchange: Exchange{StatusCode: 503}}, &ModelError{StatusCode: 503, Message: "temporarily unavailable", RetryAfter: time.Millisecond}
		}
		return Response{Message: Message{Content: "恢复成功"}, Exchange: Exchange{StatusCode: 200}}, nil
	}), "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	runner.Observe = func(event Event) { events = append(events, event) }
	result, err := runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "回答"}}})
	if err != nil || result.Answer != "恢复成功" || calls != 2 {
		t.Fatalf("瞬时故障没有恢复: calls=%d result=%+v err=%v", calls, result, err)
	}
	if len(events) != 4 || events[0].Attempt != 1 || events[2].Attempt != 2 || events[1].Err == nil || events[3].Err != nil {
		t.Fatalf("重试 Trace 不完整: %+v", events)
	}
}

func TestRunnerDoesNotRetryDeterministicProviderError(t *testing.T) {
	calls := 0
	runner, err := NewRunner(modelFunc(func(context.Context, Request) (Response, error) {
		calls++
		return Response{}, &ModelError{StatusCode: 400, Message: "bad request"}
	}), "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "回答"}}})
	if err == nil || calls != 1 {
		t.Fatalf("确定性 4xx 不应重试: calls=%d err=%v", calls, err)
	}
}

func TestRunnerKeepsToolsAndFailsClosedAfterEmptyCompatibilityResponses(t *testing.T) {
	calls := 0
	var requests []Request
	events := make([]Event, 0)
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		calls++
		requests = append(requests, request)
		usage := Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}
		return Response{Usage: usage, Exchange: Exchange{Model: "fixture", Usage: usage}}, nil
	})
	runner, err := NewRunner(model, "fixture", []Tool{{
		Spec: ToolSpec{Name: "lookup"}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner.Observe = func(event Event) { events = append(events, event) }
	_, err = runner.Run(context.Background(), RunRequest{
		Messages: []Message{{Role: RoleUser, Content: "回答"}}, OnTextDelta: func(string) {},
	})
	if !errors.Is(err, ErrEmptyModelResponse) || calls != 2 {
		t.Fatalf("空响应必须在保留工具后失败: calls=%d err=%v", calls, err)
	}
	for index, request := range requests {
		if len(request.Tools) != 1 || request.Tools[0].Name != "lookup" || request.ToolChoice.Mode != ToolChoiceAuto {
			t.Fatalf("第 %d 次请求丢失工具能力: %+v", index+1, request)
		}
	}
	if requests[0].OnTextDelta == nil || requests[1].OnTextDelta != nil {
		t.Fatalf("兼容重试应只关闭流式，不应修改工具: %+v", requests)
	}
	if len(events) != 4 || events[1].Err == nil || events[3].Err == nil {
		t.Fatalf("空响应 Trace 不完整: %+v", events)
	}
}

func TestRunnerReturnsToolErrorsToModelForRecovery(t *testing.T) {
	requests := 0
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		requests++
		if requests == 1 {
			return Response{Message: Message{ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"id":"missing"}`)}}}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != RoleTool || !strings.Contains(last.Content, "没有找到") {
			t.Fatalf("工具错误没有作为观察结果返回模型: %+v", last)
		}
		return Response{Message: Message{Content: "没有找到该记录，请补充 ID。"}}, nil
	})
	runner, err := NewRunner(model, "fixture", []Tool{{
		Spec: ToolSpec{Name: "lookup"},
		Run:  func(context.Context, json.RawMessage) (string, error) { return "", errors.New("没有找到该记录") },
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "查记录"}}})
	if err != nil || result.Answer != "没有找到该记录，请补充 ID。" {
		t.Fatalf("Agent 没有从工具错误中恢复: %+v, %v", result, err)
	}
}

func TestRunnerConvergesAfterRepeatedFailedToolSteps(t *testing.T) {
	calls := 0
	toolRuns := 0
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		calls++
		if calls <= 2 {
			return Response{Message: Message{ToolCalls: []ToolCall{{ID: fmt.Sprintf("call-%d", calls), Name: "lookup", Arguments: json.RawMessage(`{"id":"missing"}`)}}}}, nil
		}
		if len(request.Tools) != 0 || request.ToolChoice.Mode != ToolChoiceNone {
			t.Fatalf("连续失败后应关闭工具收敛: %+v", request.ToolChoice)
		}
		return Response{Message: Message{Content: "两次查询都失败，请检查 ID。"}}, nil
	})
	runner, err := NewRunner(model, "fixture", []Tool{{
		Spec: ToolSpec{Name: "lookup"}, Run: func(context.Context, json.RawMessage) (string, error) {
			toolRuns++
			return "", errors.New("没有找到")
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "查记录"}}})
	if err != nil || calls != 3 || toolRuns != 1 || !strings.Contains(result.Answer, "失败") {
		t.Fatalf("重复失败收敛错误: modelCalls=%d toolRuns=%d result=%+v err=%v", calls, toolRuns, result, err)
	}
}

func TestToolErrorOutputIncludesRecoveryMetadata(t *testing.T) {
	output := toolErrorOutput(&ToolError{Code: "temporary", Message: "稍后重试", Hint: "等待一秒", Retryable: true})
	for _, expected := range []string{`"ok":false`, `"code":"temporary"`, `"retryable":true`, `"hint":"等待一秒"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("结构化错误缺少 %s: %s", expected, output)
		}
	}
}

func TestRunnerCanAddToolsAfterCreation(t *testing.T) {
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		if len(request.Tools) != 1 || request.Tools[0].Name != "loaded_tool" {
			t.Fatalf("动态工具没有进入下一轮模型请求: %+v", request.Tools)
		}
		return Response{Message: Message{Content: "已加载"}}, nil
	})
	runner, err := NewRunner(model, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.AddTools([]Tool{{Spec: ToolSpec{Name: "loaded_tool"}, Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}}); err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "继续"}}, MaxSteps: 2})
	if err != nil || result.Answer != "已加载" {
		t.Fatalf("动态工具运行异常: %+v, %v", result, err)
	}
}

func TestRunnerRejectsDynamicToolBatchAtomically(t *testing.T) {
	runner, err := NewRunner(modelFunc(func(context.Context, Request) (Response, error) { return Response{}, nil }), "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, json.RawMessage) (string, error) { return "ok", nil }
	err = runner.AddTools([]Tool{{Spec: ToolSpec{Name: "same"}, Run: run}, {Spec: ToolSpec{Name: "same"}, Run: run}})
	if err == nil || len(runner.Tools) != 0 {
		t.Fatalf("重复工具批次不应产生部分写入: tools=%d err=%v", len(runner.Tools), err)
	}
}

func TestToolErrorKeepsStructuredOutput(t *testing.T) {
	requests := 0
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		requests++
		if requests == 1 {
			return Response{Message: Message{ToolCalls: []ToolCall{{ID: "call-1", Name: "fixture", Arguments: json.RawMessage(`{}`)}}}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if !strings.Contains(last.Content, `"exit_code":7`) {
			t.Fatalf("工具错误的结构化输出丢失: %+v", last)
		}
		return Response{Message: Message{Content: "已处理失败"}}, nil
	})
	runner, err := NewRunner(model, "fixture", []Tool{{
		Spec: ToolSpec{Name: "fixture", Parameters: map[string]any{"type": "object"}},
		Run: func(context.Context, json.RawMessage) (string, error) {
			return `{"exit_code":7,"stderr":"failed"}`, errors.New("command failed")
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), RunRequest{Messages: []Message{{Role: RoleUser, Content: "run"}}})
	if err != nil || result.Answer != "已处理失败" {
		t.Fatalf("Agent 没有处理工具失败: %+v err=%v", result, err)
	}
}

func TestRunnerPersistsMessagesIncrementallyBeforeLaterModelFailure(t *testing.T) {
	persisted := []Message{}
	calls := 0
	runner, err := NewRunner(modelFunc(func(context.Context, Request) (Response, error) {
		calls++
		if calls == 1 {
			return Response{Message: Message{ToolCalls: []ToolCall{{Name: "lookup", Arguments: json.RawMessage(`{}`)}}}}, nil
		}
		return Response{}, errors.New("模型暂时不可用")
	}), "fixture", []Tool{{
		Spec: ToolSpec{Name: "lookup"},
		Run:  func(context.Context, json.RawMessage) (string, error) { return "结果", nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), RunRequest{
		Messages: []Message{{Role: RoleUser, Content: "查询"}},
		OnMessage: func(message Message) error {
			persisted = append(persisted, message)
			return nil
		},
	})
	if err == nil || calls != 2 || len(persisted) != 2 {
		t.Fatalf("中途失败前的消息没有增量保存: calls=%d persisted=%+v err=%v", calls, persisted, err)
	}
	if persisted[0].Role != RoleAssistant || len(persisted[0].ToolCalls) != 1 || persisted[0].ToolCalls[0].ID == "" || persisted[1].Role != RoleTool || persisted[1].ToolCallID != persisted[0].ToolCalls[0].ID {
		t.Fatalf("assistant/tool 消息顺序错误: %+v", persisted)
	}
}

func TestRunnerPersistsCompletedToolStepAsOneBatch(t *testing.T) {
	var batches [][]Message
	calls := 0
	runner, err := NewRunner(modelFunc(func(context.Context, Request) (Response, error) {
		calls++
		if calls == 1 {
			return Response{Message: Message{ToolCalls: []ToolCall{{Name: "lookup", Arguments: json.RawMessage(`{}`)}}}}, nil
		}
		return Response{}, errors.New("模型暂时不可用")
	}), "fixture", []Tool{{
		Spec: ToolSpec{Name: "lookup"},
		Run:  func(context.Context, json.RawMessage) (string, error) { return "结果", nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), RunRequest{
		Messages: []Message{{Role: RoleUser, Content: "查询"}},
		OnTurnMessages: func(messages []Message) error {
			batches = append(batches, append([]Message(nil), messages...))
			return nil
		},
	})
	if err == nil || calls != 2 || len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("完整工具 step 没有作为一个批次保存: calls=%d batches=%+v err=%v", calls, batches, err)
	}
	if batches[0][0].Role != RoleAssistant || len(batches[0][0].ToolCalls) != 1 || batches[0][1].Role != RoleTool || batches[0][1].ToolCallID != batches[0][0].ToolCalls[0].ID {
		t.Fatalf("原子工具批次顺序或配对错误: %+v", batches[0])
	}
}

func TestRunnerRetriesContextErrorAfterPreparation(t *testing.T) {
	modelCalls, prepareCalls := 0, 0
	runner, err := NewRunner(modelFunc(func(_ context.Context, request Request) (Response, error) {
		modelCalls++
		if modelCalls == 1 {
			return Response{}, errors.New("maximum context length exceeded")
		}
		if request.PreviousResponseID != "" || len(request.Messages) != 3 {
			t.Fatalf("压缩重试没有改用新的完整上下文: %+v", request)
		}
		return Response{Message: Message{Content: "重试成功"}}, nil
	}), "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), RunRequest{
		Messages: []Message{{Role: RoleSystem, Content: "system"}, {Role: RoleUser, Content: "问题"}},
		PrepareRequest: func(_ context.Context, request Request, force bool) (Request, bool, error) {
			prepareCalls++
			if !force {
				return request, false, nil
			}
			request.Messages = append(request.Messages, Message{Role: RoleSystem, Content: "压缩摘要"})
			request.NewMessages = request.Messages
			request.PreviousResponseID = ""
			return request, true, nil
		},
		IsContextError: func(err error) bool { return strings.Contains(err.Error(), "context length") },
	})
	if err != nil || result.Answer != "重试成功" || modelCalls != 2 || prepareCalls != 2 {
		t.Fatalf("上下文超限恢复异常: result=%+v modelCalls=%d prepareCalls=%d err=%v", result, modelCalls, prepareCalls, err)
	}
}

func TestRunnerDoesNotTraceModelStartBeforePreparation(t *testing.T) {
	modelCalls := 0
	events := []Event{}
	runner, err := NewRunner(modelFunc(func(context.Context, Request) (Response, error) {
		modelCalls++
		return Response{Message: Message{Content: "不应调用"}}, nil
	}), "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	runner.Observe = func(event Event) {
		events = append(events, event)
	}
	prepareErr := errors.New("压缩准备失败")
	_, err = runner.Run(context.Background(), RunRequest{
		Messages: []Message{{Role: RoleUser, Content: "问题"}},
		PrepareRequest: func(context.Context, Request, bool) (Request, bool, error) {
			return Request{}, false, prepareErr
		},
	})
	if !errors.Is(err, prepareErr) || modelCalls != 0 || len(events) != 0 {
		t.Fatalf("准备失败前不应写入 model_start: err=%v calls=%d events=%+v", err, modelCalls, events)
	}
}
