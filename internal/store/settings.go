package store

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

var ErrSessionNotRunning = errors.New("任务已不在运行状态")

// 模型配置默认值集中在这里，避免 Store、HTTP API 和 Agent Runtime
// 各自维护一份数字，后续调整时出现行为不一致。
const (
	RuntimeEasyAgent                   = "easyagent"
	RuntimeCodex                       = "codex"
	ChannelWeb                         = "web"
	ChannelWeixin                      = "weixin"
	ChannelAutomation                  = "automation"
	DefaultModelProtocol               = "chat_completions"
	DefaultOllamaProtocol              = "ollama_chat"
	DefaultOllamaBaseURL               = "http://127.0.0.1:11434"
	DefaultMaxOutputTokens             = 4096
	DefaultMaxSteps                    = 32
	MinMaxSteps                        = 4
	MaxMaxSteps                        = 64
	DefaultRequestTimeoutSeconds       = 300
	MinRequestTimeoutSeconds           = 30
	MaxRequestTimeoutSeconds           = 600
	DefaultCodexTurnTimeoutSeconds     = DefaultTurnTimeoutSeconds
	MinCodexTurnTimeoutSeconds         = 300
	MaxCodexTurnTimeoutSeconds         = 86400
	DefaultCompressionThresholdPercent = 75
	MinCompressionThresholdPercent     = 50
	MaxCompressionThresholdPercent     = 90
)

// ModelSettings 是持久化的模型连接和上下文预算设置。
type ModelSettings struct {
	ProfileID       string `json:"profileId,omitempty"`
	ProfileName     string `json:"profileName,omitempty"`
	Runtime         string `json:"runtime"`
	Provider        string `json:"provider"`
	Protocol        string `json:"protocol"`
	BaseURL         string `json:"baseUrl"`
	Model           string `json:"model"`
	APIKey          string `json:"apiKey,omitempty"`
	APIKeyEnv       string `json:"apiKeyEnv,omitempty"`
	Thinking        string `json:"thinking,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
	MaxSteps        int    `json:"maxSteps,omitempty"`
	// RequestTimeoutSeconds is the timeout for one provider request in EasyAgent.
	// Codex uses its own TurnTimeoutSeconds because app-server owns the inner
	// model/tool requests.
	RequestTimeoutSeconds       int  `json:"requestTimeoutSeconds,omitempty"`
	TurnTimeoutSeconds          int  `json:"turnTimeoutSeconds,omitempty"`
	ContextWindowTokens         int  `json:"contextWindowTokens,omitempty"`
	CompressionThresholdPercent int  `json:"compressionThresholdPercent,omitempty"`
	SecretConfigured            bool `json:"secretConfigured,omitempty"`
}

// ModelProfile 是一套可复用的 Runtime 配置。Codex profile 只保存 EasyAgent
// 传给 app-server 的 override/超时等参数，认证和 Provider 仍由 Codex 自己管理。
type ModelProfile struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Settings ModelSettings `json:"settings"`
}

// DefaultModelSettings 返回一个“尚未选择模型”的本地配置。
// 不预设具体模型名，因为用户机器上不一定下载了某个固定模型；页面会从
// Ollama 的真实模型列表中选择，也可以直接改成任意 OpenAI 兼容服务。
func DefaultModelSettings() ModelSettings {
	return ModelSettings{
		Provider:                    "ollama",
		Protocol:                    DefaultOllamaProtocol,
		BaseURL:                     DefaultOllamaBaseURL,
		Thinking:                    "disabled",
		MaxOutputTokens:             DefaultMaxOutputTokens,
		MaxSteps:                    DefaultMaxSteps,
		RequestTimeoutSeconds:       DefaultRequestTimeoutSeconds,
		CompressionThresholdPercent: DefaultCompressionThresholdPercent,
	}
}

// WithDefaults 只补齐旧记录或不完整请求中可以安全推导的运行参数，
// 不替用户猜测模型名称、密钥或上下文窗口。
func (value ModelSettings) WithDefaults() ModelSettings {
	if value.Runtime != RuntimeCodex {
		value.Runtime = RuntimeEasyAgent
		// EasyAgent 的认证只保存在自己的模型配置 DB 中；旧版本留下的
		// APIKeyEnv 不再参与运行，避免环境变量和 DB 密钥产生歧义。
		value.APIKeyEnv = ""
	}
	if value.Runtime == RuntimeCodex && strings.EqualFold(strings.TrimSpace(value.Provider), "codex") {
		// 旧版本用 codex 表示“跟随官方登录”；空值是更清晰的持久化表示。
		value.Provider = ""
	}
	if value.Protocol == "" {
		value.Protocol = DefaultModelProtocol
		if value.IsOllama() {
			value.Protocol = DefaultOllamaProtocol
		}
	}
	if value.MaxOutputTokens == 0 {
		value.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if value.MaxSteps <= 0 {
		value.MaxSteps = DefaultMaxSteps
	}
	if value.RequestTimeoutSeconds == 0 {
		value.RequestTimeoutSeconds = DefaultRequestTimeoutSeconds
	}
	if value.Runtime == RuntimeCodex && value.TurnTimeoutSeconds == 0 {
		value.TurnTimeoutSeconds = DefaultCodexTurnTimeoutSeconds
	}
	if value.CompressionThresholdPercent == 0 {
		value.CompressionThresholdPercent = DefaultCompressionThresholdPercent
	}
	return value
}

// IsOllama 只用于处理 Ollama 自身的协议差异，不参与工具选择。
// 工具仍全部交给模型原生 function calling 决定。
func (value ModelSettings) IsOllama() bool {
	if strings.EqualFold(strings.TrimSpace(value.Provider), "ollama") {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(value.BaseURL))
	return err == nil && parsed.Port() == "11434"
}

// IsOfficialOpenAI 判断请求是否真正发往 OpenAI 官方 API。
// prompt_cache_key 是厂商扩展字段，不能只根据可编辑的 Provider 名称发送。
func (value ModelSettings) IsOfficialOpenAI() bool {
	parsed, err := url.Parse(strings.TrimSpace(value.BaseURL))
	return err == nil && strings.EqualFold(parsed.Hostname(), "api.openai.com")
}

func (value Session) QueuedModelSettings() (ModelSettings, bool, error) {
	if len(value.PendingModel) == 0 {
		return ModelSettings{}, false, nil
	}
	var settings ModelSettings
	if err := json.Unmarshal(value.PendingModel, &settings); err != nil {
		return ModelSettings{}, false, err
	}
	return settings.WithDefaults(), true, nil
}
