// Package store 定义 EasyAgent 的持久化模型并直接使用 SQLite。
// 会话、消息和 Trace 是独立表，不会再把整段运行历史塞进一个 JSON 文档。
package store

import "time"

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
	Turn             int       `json:"turn,omitempty"`
	Step             int       `json:"step"`
	Attempt          int       `json:"attempt,omitempty"`
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
	StatusCode       int       `json:"statusCode,omitempty"`
	HistoryMode      string    `json:"historyMode,omitempty"`
	RequestMessages  int       `json:"requestMessages,omitempty"`
	ToolDefinitions  int       `json:"toolDefinitions,omitempty"`
	DurationMS       int64     `json:"durationMs,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

// Compaction 是长会话的上下文检查点。原始消息仍保留在 ea_messages；运行时只用
// Summary 替代 ThroughMessageID 及以前的消息，再拼接最近原始消息。
type Compaction struct {
	ID               int64  `json:"id"`
	Summary          string `json:"summary"`
	ThroughMessageID int64  `json:"throughMessageId"`
	// SplitTurn 表示检查点切在一个用户轮次的 assistant/tool 后缀之前。
	// 重建上下文时需要用 user checkpoint 承接这个后缀，避免以 assistant 或
	// tool 消息开头而破坏 Provider 的消息协议。
	SplitTurn         bool      `json:"splitTurn,omitempty"`
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
	ID                string      `json:"id"`
	Title             string      `json:"title"`
	Status            string      `json:"status"`
	Error             string      `json:"error,omitempty"`
	Runtime           string      `json:"runtime"`
	Model             string      `json:"model,omitempty"`
	Workspace         string      `json:"workspace"`
	CreatedAt         time.Time   `json:"createdAt"`
	UpdatedAt         time.Time   `json:"updatedAt"`
	Messages          []Message   `json:"messages"`
	Events            []Event     `json:"events"`
	MessageCount      int         `json:"messageCount,omitempty"`
	EventCount        int         `json:"eventCount,omitempty"`
	UserTurnCount     int         `json:"userTurnCount,omitempty"`
	MessagesTruncated bool        `json:"messagesTruncated,omitempty"`
	EventsTruncated   bool        `json:"eventsTruncated,omitempty"`
	MessagesHasMore   bool        `json:"messagesHasMore,omitempty"`
	EventsHasMore     bool        `json:"eventsHasMore,omitempty"`
	Usage             Usage       `json:"usage"`
	Context           ContextInfo `json:"context"`
	// PartialOutput 只在模型流式生成期间由内存运行时填充，不写入 SQLite。
	PartialOutput string       `json:"partialOutput,omitempty"`
	Compactions   []Compaction `json:"-"`
	ResponseID    string       `json:"-"`
	ProviderKey   string       `json:"-"`
}
