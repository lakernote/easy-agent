package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/openai"
	"github.com/lakernote/easy-agent/internal/store"
)

type modelTestResult struct {
	OK           bool   `json:"ok"`
	Model        string `json:"model"`
	ToolCall     string `json:"toolCall"`
	Answer       string `json:"answer"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	DurationMS   int64  `json:"durationMs"`
}

// prepareModelInput 统一保存和连接测试的默认值、旧密钥恢复及校验逻辑。
// 不允许两个入口各自解释“空 API Key”，否则容易再次跨 Provider 复用旧密钥。
func prepareModelInput(input, current store.ModelSettings) (store.ModelSettings, error) {
	input = input.WithDefaults()
	if input.APIKey == "" && sameModelEndpoint(input, current) {
		input.APIKey = current.APIKey
	}
	if input.APIKeyEnv != "" && strings.TrimSpace(os.Getenv(input.APIKeyEnv)) == "" {
		return store.ModelSettings{}, errors.New("环境变量 " + input.APIKeyEnv + " 不存在或为空")
	}
	if err := validateModel(input); err != nil {
		return store.ModelSettings{}, err
	}
	return input, nil
}

// testModel 做一次完整但无副作用的最小 Agent 往返：
// 模型发起原生 Function Call -> EasyAgent 返回工具结果 -> 模型给出最终文本。
// 这比只请求一句“你好”更能判断模型是否真的适合 EasyAgent。
func (server *Server) testModel(response http.ResponseWriter, request *http.Request) {
	var input store.ModelSettings
	if !decodeJSON(response, request, &input) {
		return
	}
	current, _ := server.store.GetModelSettings()
	settings, err := prepareModelInput(input, current)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if settings.Runtime == store.RuntimeCodex {
		status := server.detectCodex(request.Context())
		if !status.Installed {
			writeError(response, http.StatusBadGateway, status.Message)
			return
		}
		writeJSON(response, http.StatusOK, modelTestResult{OK: true, Model: "Codex Runtime", Answer: status.Version})
		return
	}
	result, err := runModelTest(request, settings)
	if err != nil {
		writeError(response, http.StatusBadGateway, "模型能力测试失败："+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func runModelTest(request *http.Request, settings store.ModelSettings) (modelTestResult, error) {
	apiKey := settings.APIKey
	if settings.APIKeyEnv != "" {
		apiKey = os.Getenv(settings.APIKeyEnv)
	}
	client, err := openai.New(openai.Config{
		BaseURL: settings.BaseURL, APIKey: apiKey, Protocol: openai.Protocol(settings.Protocol),
		DisableThinking: settings.Thinking == "disabled", KeepThinkingForTools: settings.IsOllama(),
		Timeout: time.Duration(settings.RequestTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return modelTestResult{}, err
	}
	tool := agent.ToolSpec{
		Name:        "easyagent_diagnostic_echo",
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
	startedAt := time.Now()
	first, err := client.Generate(request.Context(), agent.Request{
		Model: settings.Model, Messages: messages, Tools: []agent.ToolSpec{tool},
		ToolChoice: agent.ToolChoice{Mode: agent.ToolChoiceAuto}, MaxOutputTokens: 128,
	})
	if err != nil {
		return modelTestResult{}, err
	}
	if len(first.Message.ToolCalls) != 1 || first.Message.ToolCalls[0].Name != tool.Name {
		return modelTestResult{}, fmt.Errorf("没有返回原生 tool_calls，而是返回了普通文本 %q", strings.TrimSpace(first.Message.Content))
	}
	var arguments struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(first.Message.ToolCalls[0].Arguments, &arguments) != nil || arguments.Text != "ping" {
		return modelTestResult{}, fmt.Errorf("工具参数不符合 JSON Schema：%s", string(first.Message.ToolCalls[0].Arguments))
	}
	call := first.Message.ToolCalls[0]
	messages = append(messages, first.Message, agent.Message{
		Role: agent.RoleTool, Name: tool.Name, ToolCallID: call.ID, Content: `{"answer":"EASYAGENT_OK"}`,
	})
	second, err := client.Generate(request.Context(), agent.Request{
		Model: settings.Model, Messages: messages, Tools: []agent.ToolSpec{tool},
		ToolChoice: agent.ToolChoice{Mode: agent.ToolChoiceNone}, MaxOutputTokens: 64,
	})
	if err != nil {
		return modelTestResult{}, err
	}
	answer := strings.TrimSpace(second.Message.Content)
	if !strings.Contains(answer, "EASYAGENT_OK") {
		return modelTestResult{}, fmt.Errorf("模型没有正确使用工具结果：%q", answer)
	}
	return modelTestResult{
		OK: true, Model: settings.Model, ToolCall: tool.Name, Answer: answer,
		InputTokens:  first.Usage.InputTokens + second.Usage.InputTokens,
		OutputTokens: first.Usage.OutputTokens + second.Usage.OutputTokens,
		DurationMS:   time.Since(startedAt).Milliseconds(),
	}, nil
}
