package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
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
}

func TestRunnerDisablesToolsOnLastStep(t *testing.T) {
	model := modelFunc(func(_ context.Context, request Request) (Response, error) {
		if len(request.Tools) != 0 {
			t.Fatalf("last step should not expose tools: %+v", request.Tools)
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
