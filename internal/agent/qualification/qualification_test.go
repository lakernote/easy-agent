package qualification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lakernote/easy-agent/internal/agent"
)

type modelFunc func(context.Context, agent.Request) (agent.Response, error)

func (function modelFunc) Generate(ctx context.Context, request agent.Request) (agent.Response, error) {
	return function(ctx, request)
}

func TestRunVerifiesNativeToolRoundTripAndUsage(t *testing.T) {
	calls := 0
	result, err := Run(context.Background(), modelFunc(func(_ context.Context, request agent.Request) (agent.Response, error) {
		calls++
		if calls == 1 {
			if request.ToolChoice.Mode != agent.ToolChoiceAuto || request.OnTextDelta == nil {
				t.Fatalf("first request = %+v", request)
			}
			return agent.Response{
				Message: agent.Message{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: ToolName, Arguments: json.RawMessage(`{"text":"ping"}`)}}},
				Usage:   agent.Usage{InputTokens: 10, OutputTokens: 3},
			}, nil
		}
		if request.ToolChoice.Mode != agent.ToolChoiceNone || len(request.Messages) != 4 || request.Messages[3].ToolResult == nil {
			t.Fatalf("second request = %+v", request)
		}
		request.OnTextDelta("EASY")
		return agent.Response{Message: agent.Message{Content: "EASYAGENT_OK"}, Usage: agent.Usage{InputTokens: 16, OutputTokens: 2}}, nil
	}), "fixture")
	if err != nil || calls != 2 || result.ToolCall != ToolName || result.Answer != "EASYAGENT_OK" || result.InputTokens != 26 || result.OutputTokens != 5 || result.Duration <= 0 || result.FirstTokenDuration <= 0 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestRunRejectsTextToolImitationAndKeepsPartialMetrics(t *testing.T) {
	result, err := Run(context.Background(), modelFunc(func(_ context.Context, request agent.Request) (agent.Response, error) {
		request.OnTextDelta(`{"name":"easyagent_diagnostic_echo"}`)
		return agent.Response{
			Message: agent.Message{Content: `{"name":"easyagent_diagnostic_echo"}`},
			Usage:   agent.Usage{InputTokens: 7, OutputTokens: 4},
		}, nil
	}), "fixture")
	if err == nil || !strings.Contains(err.Error(), "没有返回协议原生 Function Call") || result.InputTokens != 7 || result.OutputTokens != 4 || result.Duration <= 0 || result.FirstTokenDuration <= 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
