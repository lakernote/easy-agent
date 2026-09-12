package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent/qualification"
	"github.com/lakernote/easy-agent/internal/codexruntime"
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
func prepareModelInput(input, current store.ModelSettings, clearAPIKey bool) (store.ModelSettings, error) {
	input = input.WithDefaults()
	if clearAPIKey && input.APIKey != "" {
		return store.ModelSettings{}, errors.New("不能同时填写新 API Key 和清除已保存的 API Key")
	}
	if input.Runtime == store.RuntimeCodex {
		// Codex app-server 自己读取 ~/.codex 配置并负责认证、Provider 和协议。
		// 当前 Codex 配置可以为空（跟随官方 ChatGPT 登录），也可以保存一个
		// Provider ID，由 app-server 的 -c model_provider=... 在本次运行中覆盖。
		input.Protocol = "app_server"
		input.BaseURL = ""
		input.APIKey = ""
		input.APIKeyEnv = ""
		input.Thinking = ""
		input.ContextWindowTokens = 0
		input.CompressionThresholdPercent = 0
	}
	if input.Runtime != store.RuntimeCodex {
		// EasyAgent 只使用 DB 中的 APIKey；环境变量入口仅为旧版本兼容
		// 保留在结构体中，不再接受或读取。
		input.APIKeyEnv = ""
	}
	if input.Runtime != store.RuntimeCodex && input.APIKey == "" {
		switch {
		case clearAPIKey:
			input.APIKey = ""
		case current.APIKey == "":
		case sameModelEndpoint(input, current):
			input.APIKey = current.APIKey
		default:
			return store.ModelSettings{}, errors.New("Provider 或 Base URL 已改变；请填写新 API Key，或勾选清除已保存的 API Key")
		}
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
	current := server.modelSettingsForInput(input)
	settings, err := prepareModelInput(input, current, false)
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
		if !status.AppServerAvailable {
			writeError(response, http.StatusBadGateway, status.Message)
			return
		}
		runtimeSettings, err := server.store.GetRuntimeSettings()
		if err != nil {
			writeError(response, http.StatusInternalServerError, err.Error())
			return
		}
		turnTimeoutSeconds := runtimeSettings.TurnTimeoutSeconds
		_, capabilityEnv, syncErr := server.syncCodexCapabilities()
		if syncErr != nil {
			writeError(response, http.StatusBadGateway, syncErr.Error())
			return
		}
		environment, environmentErr := server.codexEnvironmentWith(capabilityEnv)
		if environmentErr != nil {
			writeError(response, http.StatusBadGateway, environmentErr.Error())
			return
		}
		result, runErr := codexruntime.RunMessage(request.Context(), codexruntime.Config{
			Path: status.Path, Workspace: server.env.Workspace(), Model: settings.Model, Provider: settings.Provider,
			Timeout: time.Duration(turnTimeoutSeconds) * time.Second, Env: environment, Permissions: runtimeSettings.Permissions,
		}, "只回复 CODEX_RUNTIME_TEST_OK，不要调用工具。")
		if runErr != nil {
			writeError(response, http.StatusBadGateway, "Codex app-server 能启动，但实际会话测试失败："+runErr.Error())
			return
		}
		writeJSON(response, http.StatusOK, modelTestResult{OK: true, Model: "Codex Runtime", Answer: result.Answer, DurationMS: result.Duration.Milliseconds()})
		return
	}
	result, err := runModelTest(request, settings)
	if err != nil {
		writeError(response, http.StatusBadGateway, "模型能力测试失败："+err.Error())
		return
	}
	if err := server.store.RecordModelCapabilityTest(modelCapabilityFingerprint(settings), time.Now()); err != nil {
		writeError(response, http.StatusInternalServerError, "保存模型能力验证失败："+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func modelCapabilityFingerprint(settings store.ModelSettings) string {
	settings = settings.WithDefaults()
	secretHash := sha256.Sum256([]byte(settings.APIKey))
	payload := struct {
		Provider, Protocol, BaseURL, Model, Thinking, Secret string
		ContextWindowTokens, MaxOutputTokens                 int
	}{
		Provider: strings.ToLower(strings.TrimSpace(settings.Provider)),
		Protocol: strings.TrimSpace(settings.Protocol), BaseURL: strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/"),
		Model: strings.TrimSpace(settings.Model), Thinking: strings.TrimSpace(settings.Thinking),
		Secret: fmt.Sprintf("%x", secretHash[:]), ContextWindowTokens: settings.ContextWindowTokens, MaxOutputTokens: settings.MaxOutputTokens,
	}
	data, _ := json.Marshal(payload)
	fingerprint := sha256.Sum256(data)
	return fmt.Sprintf("%x", fingerprint[:])
}

func (server *Server) requireVerifiedEasyAgent(settings store.ModelSettings) error {
	if settings.Runtime == store.RuntimeCodex {
		return nil
	}
	verified, err := server.store.HasModelCapabilityTest(modelCapabilityFingerprint(settings))
	if err != nil {
		return fmt.Errorf("读取模型能力验证: %w", err)
	}
	if !verified {
		return errors.New("当前模型配置尚未通过原生 Function Calling 能力测试；请先在设置中测试当前模型。EasyAgent 不会解析文本伪工具调用")
	}
	return nil
}

func runModelTest(request *http.Request, settings store.ModelSettings) (modelTestResult, error) {
	client, err := newModelAdapter(settings)
	if err != nil {
		return modelTestResult{}, err
	}
	result, err := qualification.Run(request.Context(), client, settings.Model)
	value := modelTestResult{
		OK: err == nil, Model: result.Model, ToolCall: result.ToolCall, Answer: result.Answer,
		InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, DurationMS: result.Duration.Milliseconds(),
	}
	if err != nil {
		return value, err
	}
	return modelTestResult{
		OK: true, Model: result.Model, ToolCall: result.ToolCall, Answer: result.Answer,
		InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, DurationMS: result.Duration.Milliseconds(),
	}, nil
}
