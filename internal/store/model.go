// Package store 定义 EasyAgent 的持久化模型并直接使用 SQLite。
// 会话、消息和 Trace 是独立表，不会再把整段运行历史塞进一个 JSON 文档。
package store

import (
	"net/url"
	"strings"
	"time"
)

// 模型配置默认值集中在这里，避免 Store、HTTP API 和 Agent Runtime
// 各自维护一份数字，后续调整时出现行为不一致。
const (
	DefaultModelProtocol               = "chat_completions"
	DefaultOllamaBaseURL               = "http://127.0.0.1:11434/v1"
	DefaultMaxOutputTokens             = 1600
	DefaultRequestTimeoutSeconds       = 300
	MinRequestTimeoutSeconds           = 30
	MaxRequestTimeoutSeconds           = 600
	DefaultCompressionThresholdPercent = 75
	MinCompressionThresholdPercent     = 50
	MaxCompressionThresholdPercent     = 90
)

type ModelSettings struct {
	Provider                    string `json:"provider"`
	Protocol                    string `json:"protocol"`
	BaseURL                     string `json:"baseUrl"`
	Model                       string `json:"model"`
	APIKey                      string `json:"apiKey,omitempty"`
	APIKeyEnv                   string `json:"apiKeyEnv,omitempty"`
	Thinking                    string `json:"thinking,omitempty"`
	MaxOutputTokens             int    `json:"maxOutputTokens,omitempty"`
	RequestTimeoutSeconds       int    `json:"requestTimeoutSeconds,omitempty"`
	ContextWindowTokens         int    `json:"contextWindowTokens,omitempty"`
	CompressionThresholdPercent int    `json:"compressionThresholdPercent,omitempty"`
	SecretConfigured            bool   `json:"secretConfigured,omitempty"`
}

// DefaultModelSettings 返回一个“尚未选择模型”的本地配置。
// 不预设具体模型名，因为用户机器上不一定下载了某个固定模型；页面会从
// Ollama 的真实模型列表中选择，也可以直接改成任意 OpenAI 兼容服务。
func DefaultModelSettings() ModelSettings {
	return ModelSettings{
		Provider:                    "ollama",
		Protocol:                    DefaultModelProtocol,
		BaseURL:                     DefaultOllamaBaseURL,
		Thinking:                    "disabled",
		MaxOutputTokens:             DefaultMaxOutputTokens,
		RequestTimeoutSeconds:       DefaultRequestTimeoutSeconds,
		CompressionThresholdPercent: DefaultCompressionThresholdPercent,
	}
}

// WithDefaults 只补齐旧记录或不完整请求中可以安全推导的运行参数，
// 不替用户猜测模型名称、密钥或上下文窗口。
func (value ModelSettings) WithDefaults() ModelSettings {
	if value.Protocol == "" {
		value.Protocol = DefaultModelProtocol
	}
	if value.MaxOutputTokens == 0 {
		value.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if value.RequestTimeoutSeconds == 0 {
		value.RequestTimeoutSeconds = DefaultRequestTimeoutSeconds
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

type MCPConfig struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Enabled          bool              `json:"enabled"`
	Transport        string            `json:"transport"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args"`
	Endpoint         string            `json:"endpoint,omitempty"`
	AuthType         string            `json:"authType,omitempty"`
	Token            string            `json:"token,omitempty"`
	Username         string            `json:"username,omitempty"`
	Password         string            `json:"password,omitempty"`
	Headers          map[string]string `json:"headers"`
	Environment      map[string]string `json:"environment"`
	SecretConfigured bool              `json:"secretConfigured,omitempty"`
}

type SkillOverride struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Enabled     bool   `json:"enabled"`
	Builtin     bool   `json:"builtin"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Attachment 是一条用户消息携带的文件或图片。Data 只在 Agent 运行和附件
// 下载接口内部使用，不进入会话 JSON，避免页面轮询时反复传输 Base64 数据。
type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Data     []byte `json:"-"`
}

type Message struct {
	ID          int64        `json:"id"`
	Role        string       `json:"role"`
	Content     string       `json:"content,omitempty"`
	Attachments []Attachment `json:"attachments"`
	ToolCalls   []ToolCall   `json:"toolCalls"`
	ToolCallID  string       `json:"toolCallId,omitempty"`
	Name        string       `json:"name,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
}

type Usage struct {
	InputTokens      int   `json:"inputTokens"`
	OutputTokens     int   `json:"outputTokens"`
	CachedTokens     int   `json:"cachedTokens"`
	CacheWriteTokens int   `json:"cacheWriteTokens"`
	TotalTokens      int   `json:"totalTokens"`
	ModelDurationMS  int64 `json:"modelDurationMs"`
	ToolDurationMS   int64 `json:"toolDurationMs"`
	ModelCalls       int   `json:"modelCalls"`
	ToolCalls        int   `json:"toolCalls"`
	CacheReported    bool  `json:"cacheReported"`
	CacheInputTokens int   `json:"cacheInputTokens"`
}

type Event struct {
	ID               int64     `json:"id"`
	Kind             string    `json:"kind"`
	Step             int       `json:"step"`
	Name             string    `json:"name,omitempty"`
	Status           string    `json:"status"`
	Detail           string    `json:"detail,omitempty"`
	Input            string    `json:"input,omitempty"`
	Output           string    `json:"output,omitempty"`
	InputTokens      int       `json:"inputTokens,omitempty"`
	OutputTokens     int       `json:"outputTokens,omitempty"`
	CachedTokens     int       `json:"cachedTokens,omitempty"`
	CacheWriteTokens int       `json:"cacheWriteTokens,omitempty"`
	CacheReported    bool      `json:"cacheReported"`
	TotalTokens      int       `json:"totalTokens,omitempty"`
	Protocol         string    `json:"protocol,omitempty"`
	HistoryMode      string    `json:"historyMode,omitempty"`
	RequestMessages  int       `json:"requestMessages,omitempty"`
	ToolDefinitions  int       `json:"toolDefinitions,omitempty"`
	DurationMS       int64     `json:"durationMs,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

// Compaction 是长会话的上下文检查点。原始消息仍保留在 ea_messages；运行时只用
// Summary 替代 ThroughMessageID 及以前的消息，再拼接最近原始消息。
type Compaction struct {
	ID                int64     `json:"id"`
	Summary           string    `json:"summary"`
	ThroughMessageID  int64     `json:"throughMessageId"`
	SourceMessages    int       `json:"sourceMessages"`
	CompactedMessages int       `json:"compactedMessages"`
	Usage             Usage     `json:"usage"`
	CreatedAt         time.Time `json:"createdAt"`
}

// ContextInfo 是页面展示的上下文账本，全部由已保存的消息和最近一次
// 真实模型 Usage 推导，不伪造 Provider 没有上报的 Token 数。
type ContextInfo struct {
	HistoryMessages             int    `json:"historyMessages"`
	UserTurns                   int    `json:"userTurns"`
	LastInputTokens             int    `json:"lastInputTokens"`
	ContextWindowTokens         int    `json:"contextWindowTokens"`
	HistoryMode                 string `json:"historyMode"`
	RequestMessages             int    `json:"requestMessages"`
	ToolDefinitions             int    `json:"toolDefinitions"`
	CompressionMode             string `json:"compressionMode"`
	CompressionThresholdPercent int    `json:"compressionThresholdPercent"`
	CompressionCount            int    `json:"compressionCount"`
	CompressedMessages          int    `json:"compressedMessages"`
	RetainedMessages            int    `json:"retainedMessages"`
	CacheReported               bool   `json:"cacheReported"`
	LastCachedTokens            int    `json:"lastCachedTokens"`
	LastCacheWriteTokens        int    `json:"lastCacheWriteTokens"`
}

type Session struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Status    string      `json:"status"`
	Error     string      `json:"error,omitempty"`
	Model     string      `json:"model,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Messages  []Message   `json:"messages"`
	Events    []Event     `json:"events"`
	Usage     Usage       `json:"usage"`
	Context   ContextInfo `json:"context"`
	// PartialOutput 只在模型流式生成期间由内存运行时填充，不写入 SQLite。
	PartialOutput string       `json:"partialOutput,omitempty"`
	Compactions   []Compaction `json:"-"`
	ResponseID    string       `json:"-"`
	ProviderKey   string       `json:"-"`
}
