// Package agent 实现 EasyAgent 最小、与模型厂商无关的运行时。
//
// 这个包只认识消息、工具和模型三个概念。Git、MCP、Skill、网页以及具体业务
// 都在外层组装，避免核心循环逐渐变成一个无法理解的“大控制器”。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrEmptyModelResponse 表示 Provider 完成了一次请求并上报 Usage，却没有返回
// 可见文本或 Tool Call。Runner 会把这次真实调用写入 Trace，再关闭流式重试一次。
var ErrEmptyModelResponse = errors.New("模型没有返回回答或工具调用")

// Role 对应模型会话中的消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是 Chat Completions、Responses 和其他模型协议之间的公共消息格式。
// ToolCallID 把工具结果与模型发起的调用一一对应，不能把工具输出伪装成用户消息。
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

// ToolCall 是模型希望运行的一次函数调用。Arguments 保留原始 JSON，直到真正
// 执行工具时才解析，既避免丢失字段，也便于 Trace 展示模型的原始输入。
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolSpec 是暴露给模型的工具契约。Parameters 使用 JSON Schema。
type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Tool 把声明与实际执行函数绑定起来。
type Tool struct {
	Spec ToolSpec
	Run  func(context.Context, json.RawMessage) (string, error)
}

// Usage 统一不同模型协议的 Token 数据。CachedInputTokens 用于页面计算缓存率。
type Usage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	CacheWriteTokens  int
	TotalTokens       int
	// CacheReported 区分“Provider 明确上报 0”与“Provider 没有返回缓存字段”。
	// 例如 Ollama 的 Chat Completions 目前只返回输入/输出 Token，
	// 此时页面应显示“未上报”，而不是误导性的 0% 命中率。
	CacheReported bool
}

// Exchange 保存一次真实模型 HTTP 调用的审计信息。
type Exchange struct {
	Model      string
	Protocol   string
	Request    string
	Response   string
	Usage      Usage
	Duration   time.Duration
	StatusCode int
}

// Request 是核心 Agent 发给模型适配器的标准请求。
//
// Messages 保存完整会话，适合 Chat Completions；NewMessages 只保存上次响应后
// 新增的消息，Responses 可配合 PreviousResponseID 避免重复发送整段上下文。
type Request struct {
	Model              string
	Messages           []Message
	NewMessages        []Message
	Tools              []ToolSpec
	Temperature        float64
	MaxOutputTokens    int
	ReasoningEffort    string
	PreviousResponseID string
	// OnTextDelta 非空时，支持流式的模型适配器会逐段回传可见回答。
	// 回调只用于当前进程的实时展示；最终完整消息仍按原流程持久化。
	OnTextDelta func(string)
}

// Response 是模型适配器归一化后的结果。
type Response struct {
	ID       string
	Message  Message
	Usage    Usage
	Exchange Exchange
}

// Model 是 EasyAgent 依赖的唯一模型接口。不同厂商和协议只需要实现 Generate。
type Model interface {
	Generate(context.Context, Request) (Response, error)
}

// EventKind 是运行时向页面 Trace 发出的公开事件类型。
type EventKind string

const (
	EventModelStart EventKind = "model_start"
	EventModelEnd   EventKind = "model_end"
	EventToolStart  EventKind = "tool_start"
	EventToolEnd    EventKind = "tool_end"
)

// Event 只包含可审计的操作信息，不包含模型的私有思维过程。
type Event struct {
	Kind      EventKind
	Step      int
	ToolCall  *ToolCall
	Output    string
	Err       error
	Exchange  Exchange
	StartedAt time.Time
	Duration  time.Duration
}

// Observer 接收每一步模型和工具事件。传 nil 即关闭 Trace，不影响 Agent 行为。
type Observer func(Event)

// RunRequest 描述一次 Agent 运行。Messages 至少应包含一条用户消息。
type RunRequest struct {
	Messages []Message
	// NewMessages 是已有会话在本轮新增的消息。Responses 使用它配合
	// PreviousResponseID 续接服务端上下文；首次运行可留空。
	NewMessages        []Message
	PreviousResponseID string
	MaxSteps           int
	ToolTimeout        time.Duration
	OnTextDelta        func(string)
}

// RunResult 返回最终回答和可继续多轮会话的完整消息。
type RunResult struct {
	Answer     string
	Messages   []Message
	Usage      Usage
	ResponseID string
	Steps      int
}
