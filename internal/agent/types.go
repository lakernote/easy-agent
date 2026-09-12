// Package agent 实现 EasyAgent 最小、与模型厂商无关的运行时。
//
// 这个包只认识消息、工具和模型三个概念。Git、MCP、Skill、网页以及具体业务
// 都在外层组装，避免核心循环逐渐变成一个无法理解的“大控制器”。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrEmptyModelResponse 表示 Provider 完成了一次请求并上报 Usage，却没有返回
// 可见文本或 Tool Call。Runner 会把这次真实调用写入 Trace，再关闭流式重试一次。
var ErrEmptyModelResponse = errors.New("模型没有返回回答或工具调用")

// StopReason 是 Provider 对一次生成为何结束的统一描述。只有 stop 和
// tool_use 表示响应完整；length/incomplete/interrupted/error 都不能继续执行
// 响应里可能存在的工具调用，避免把被截断的参数当成有效指令。
type StopReason string

const (
	StopReasonStop        StopReason = "stop"
	StopReasonToolUse     StopReason = "tool_use"
	StopReasonLength      StopReason = "length"
	StopReasonIncomplete  StopReason = "incomplete"
	StopReasonInterrupted StopReason = "interrupted"
	StopReasonError       StopReason = "error"
)

// IncompleteModelResponseError 表示模型返回了可解析但并未完整结束的响应。
// 它不是瞬时网络错误，Runner 不会自动重放，更不会执行其中的工具调用。
type IncompleteModelResponseError struct {
	StopReason StopReason
	Reason     string
}

func (failure *IncompleteModelResponseError) Error() string {
	if failure == nil {
		return ""
	}
	if strings.TrimSpace(failure.Reason) != "" {
		return fmt.Sprintf("模型响应未完整结束（%s）：%s", failure.StopReason, failure.Reason)
	}
	return fmt.Sprintf("模型响应未完整结束（%s）", failure.StopReason)
}

// ModelError 保存 Provider 返回的 HTTP 失败。Runner 只重试 429 和 5xx；
// 认证、参数和不存在的模型等 4xx 属于确定性错误，不应浪费 Token 重放。
type ModelError struct {
	StatusCode            int
	Message               string
	RetryAfter            time.Duration
	RetryWithoutStreaming bool
}

func (failure *ModelError) Error() string {
	if failure == nil {
		return ""
	}
	return failure.Message
}

func (failure *ModelError) Retryable() bool {
	return failure != nil && (failure.StatusCode == 429 || failure.StatusCode >= 500)
}

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
	Role    Role
	Content string
	// Parts 是协议无关的多模态内容。Content 继续作为常用纯文本快捷字段；
	// Adapter 在 Parts 非空时按自身协议编码，而不是要求核心伪装成某种 wire format。
	Parts       []ContentBlock
	Attachments []Attachment
	ToolCalls   []ToolCall
	ToolCallID  string
	Name        string
	ToolResult  *ToolResult
	// Reasoning 与 ReasoningDetails 只用于同一轮工具调用之间保留兼容
	// Provider 返回的推理上下文，不写入 Trace，也不作为可见思维过程展示。
	Reasoning        string
	ReasoningDetails json.RawMessage
}

// ContentBlock 是模型输入与工具输出共用的内容原语。JSON 与二进制数据保持
// 类型信息，只有目标协议确实只接受字符串时才在 Adapter 边界降级。
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	JSON     json.RawMessage `json:"json,omitempty"`
	Name     string          `json:"name,omitempty"`
	MIMEType string          `json:"mimeType,omitempty"`
	Data     []byte          `json:"data,omitempty"`
	// ArtifactID points at separately persisted binary data. Data is populated
	// only while executing or rebuilding model context and is never required in
	// the session JSON returned to the browser.
	ArtifactID string `json:"artifactId,omitempty"`
	URI        string `json:"uri,omitempty"`
}

const (
	// Tool-result ingestion limits protect process and SQLite availability. They
	// are deliberately separate from request-time token compaction: accepted
	// results are preserved in full, while oversized results fail explicitly.
	MaxToolResultBlocks     = 256
	MaxToolResultBlockBytes = 8 * 1024 * 1024
	MaxToolResultTotalBytes = 16 * 1024 * 1024
)

type ToolResultError struct {
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

type ToolResultTruncation struct {
	Strategy       string `json:"strategy"`
	OriginalTokens int    `json:"originalTokens"`
	RetainedTokens int    `json:"retainedTokens"`
	OmittedItems   int    `json:"omittedItems,omitempty"`
}

// ToolResult preserves typed MCP/native-tool output, error semantics and any
// request-only compaction metadata. StructuredContent is preferred by adapters
// that support native JSON function responses.
type ToolResult struct {
	Content           []ContentBlock        `json:"content,omitempty"`
	StructuredContent json.RawMessage       `json:"structuredContent,omitempty"`
	IsError           bool                  `json:"isError,omitempty"`
	Error             *ToolResultError      `json:"error,omitempty"`
	Truncation        *ToolResultTruncation `json:"truncation,omitempty"`
}

func NewToolResult(value string) ToolResult {
	trimmed := strings.TrimSpace(value)
	if json.Valid([]byte(trimmed)) {
		return ToolResult{StructuredContent: append(json.RawMessage(nil), []byte(trimmed)...)}
	}
	return ToolResult{Content: []ContentBlock{{Type: "text", Text: value}}}
}

func (result ToolResult) Empty() bool {
	return len(result.StructuredContent) == 0 && len(result.Content) == 0 && result.Error == nil
}

func ValidateToolResult(result ToolResult) error {
	if len(result.Content) > MaxToolResultBlocks {
		return fmt.Errorf("工具结果包含 %d 个内容块，超过 %d 个上限", len(result.Content), MaxToolResultBlocks)
	}
	total := len(result.StructuredContent)
	if total > MaxToolResultBlockBytes {
		return fmt.Errorf("工具结构化结果超过 %d MiB", MaxToolResultBlockBytes/(1024*1024))
	}
	if result.Error != nil {
		size := len(result.Error.Code) + len(result.Error.Message) + len(result.Error.Hint)
		if size > MaxToolResultBlockBytes {
			return fmt.Errorf("工具错误信息超过 %d MiB", MaxToolResultBlockBytes/(1024*1024))
		}
		total += size
	}
	if result.Truncation != nil {
		total += len(result.Truncation.Strategy)
	}
	if total > MaxToolResultTotalBytes {
		return fmt.Errorf("工具结果总大小超过 %d MiB", MaxToolResultTotalBytes/(1024*1024))
	}
	for index, block := range result.Content {
		size := len(block.Type) + len(block.Text) + len(block.JSON) + len(block.Data) + len(block.Name) + len(block.MIMEType) + len(block.ArtifactID) + len(block.URI)
		if size > MaxToolResultBlockBytes {
			return fmt.Errorf("工具结果第 %d 个内容块超过 %d MiB", index+1, MaxToolResultBlockBytes/(1024*1024))
		}
		total += size
		if total > MaxToolResultTotalBytes {
			return fmt.Errorf("工具结果总大小超过 %d MiB", MaxToolResultTotalBytes/(1024*1024))
		}
	}
	return nil
}

// ModelText is the deterministic textual projection for string-only protocols
// and message history. Non-text blocks become explicit metadata instead of
// disappearing from the model context.
func (result ToolResult) ModelText() string {
	if len(result.StructuredContent) > 0 {
		if result.IsError {
			var value any
			if json.Unmarshal(result.StructuredContent, &value) != nil {
				value = string(result.StructuredContent)
			}
			encoded, _ := json.Marshal(map[string]any{"ok": false, "error": result.Error, "result": value})
			return string(encoded)
		}
		return string(result.StructuredContent)
	}
	parts := make([]string, 0, len(result.Content)+1)
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "json":
			parts = append(parts, string(block.JSON))
		default:
			value := map[string]any{"type": block.Type}
			if block.Name != "" {
				value["name"] = block.Name
			}
			if block.MIMEType != "" {
				value["mimeType"] = block.MIMEType
			}
			if block.URI != "" {
				value["uri"] = block.URI
			}
			if len(block.Data) > 0 {
				value["bytes"] = len(block.Data)
			}
			encoded, _ := json.Marshal(value)
			parts = append(parts, string(encoded))
		}
	}
	if len(parts) == 0 && result.Error != nil {
		encoded, _ := json.Marshal(map[string]any{"ok": false, "error": result.Error})
		return string(encoded)
	}
	if result.IsError && result.Error != nil {
		encoded, _ := json.Marshal(map[string]any{"ok": false, "error": result.Error})
		parts = append(parts, string(encoded))
	}
	return strings.Join(parts, "\n")
}

// Attachment 是模型无关的用户附件。Kind 目前支持 text、image 和 pdf；
// 各 Provider 适配器负责转换成自己的多模态内容块。
type Attachment struct {
	Name     string
	MIMEType string
	Kind     string
	Data     []byte
}

// ToolCall 是模型希望运行的一次函数调用。Arguments 保留原始 JSON，直到真正
// 执行工具时才解析，既避免丢失字段，也便于 Trace 展示模型的原始输入。
type ToolCall struct {
	ID             string
	Name           string
	Arguments      json.RawMessage
	ActivityKind   string
	ActivitySource string
	DisplayName    string
}

// ToolSpec 是暴露给模型的工具契约。Parameters 使用 JSON Schema。
type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
	// ActivityKind、ActivitySource 和 DisplayName 只用于 Trace 与页面展示，
	// 不发送给 Provider。它们让内置 Tool、Loader、Skill 和 MCP 在两个
	// Runtime 中使用同一套可观测语义。
	ActivityKind   string
	ActivitySource string
	DisplayName    string
	// Group 和 GroupDescription 是 EasyAgent 渐进加载用的本地元数据。
	// Provider 适配器只编码上面三个标准字段，因此它们不会污染 Function Schema。
	Group            string
	GroupDescription string
	// Loader 表示这个工具只负责把真实工具加入下一轮，成功本身不构成任务证据。
	// Runner 会在下一步临时隐藏 Loader，并验证模型至少调用一个真实工具。
	Loader bool
	// DiscoveryOnly 表示成功结果只提供候选线索，不能单独支撑外部事实。
	// Runner 不读取用户文本，只在模型准备基于这类结果直接结束时提醒一次，
	// 让模型使用任意非 Discovery 工具读取原始来源；该字段不会发送给 Provider。
	DiscoveryOnly bool
}

// Tool 把声明与实际执行函数绑定起来。
type Tool struct {
	Spec ToolSpec
	// Execute is the typed path. Run remains as a compatibility bridge for
	// existing built-ins and third-party integrations while they migrate.
	Execute func(context.Context, json.RawMessage) (ToolResult, error)
	Run     func(context.Context, json.RawMessage) (string, error)
}

// ToolError 是工具向模型返回的结构化失败。Retryable 只表示使用完全相同的
// 参数重试仍可能成功（例如临时超时）；参数、路径和权限错误必须为 false。
type ToolError struct {
	Code      string
	Message   string
	Hint      string
	Retryable bool
	Cause     error
}

func (failure *ToolError) Error() string {
	if failure == nil {
		return ""
	}
	if failure.Message != "" {
		return failure.Message
	}
	if failure.Cause != nil {
		return failure.Cause.Error()
	}
	return "工具执行失败"
}

func (failure *ToolError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

// ToolChoice 描述模型如何选择工具。默认运行使用 auto，让模型根据语义决定
// 直接回答还是调用工具；none 用于最后收敛轮，required/name 留给确定性策略。
// 不把协议 JSON 放进核心类型，因为 Chat Completions 与 Responses 的指定工具
// 写法不同，具体编码由各自适配器完成。
type ToolChoice struct {
	Mode string
	Name string
}

const (
	ToolChoiceAuto     = "auto"
	ToolChoiceNone     = "none"
	ToolChoiceRequired = "required"
)

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
	// Request shape is reported by the adapter because only the adapter knows
	// whether history was sent in full or continued by the provider.
	HistoryMode      string
	RequestMessages  int
	ToolDefinitions  int
	StopReason       StopReason
	IncompleteReason string
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
	ToolChoice         ToolChoice
	PromptCacheKey     string
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
	ID               string
	Message          Message
	Usage            Usage
	Exchange         Exchange
	StopReason       StopReason
	IncompleteReason string
}

// Model 是 EasyAgent 依赖的唯一模型接口。不同厂商和协议只需要实现 Generate。
type Model interface {
	Generate(context.Context, Request) (Response, error)
}

type ModelCapabilities struct {
	Provider    string
	Protocol    string
	Streaming   bool
	NativeTools bool
	// StructuredToolResults means the adapter preserves typed tool-result
	// content supported by its wire protocol instead of flattening everything.
	StructuredToolResults bool
	ServerContinuation    bool
	MultimodalInput       bool
}

type CapabilityModel interface {
	Model
	Capabilities() ModelCapabilities
}

func CapabilitiesOf(model Model) ModelCapabilities {
	if value, ok := model.(CapabilityModel); ok {
		return value.Capabilities()
	}
	return ModelCapabilities{}
}

// EventKind 是运行时向页面 Trace 发出的公开事件类型。
type EventKind string

const (
	EventModelStart EventKind = "model_start"
	EventModelEnd   EventKind = "model_end"
	EventToolStart  EventKind = "tool_start"
	EventToolEnd    EventKind = "tool_end"
	EventToolReject EventKind = "tool_rejected"
	EventGuidance   EventKind = "agent_guidance"
)

// Event 只包含可审计的操作信息，不包含模型的私有思维过程。
type Event struct {
	Kind EventKind
	Step int
	// Attempt 是同一个 Agent Step 内实际发出的模型请求次数。正常为 1；
	// 只有协议降级或瞬时重试才会增加，不能把它误算成新的推理步骤。
	Attempt   int
	ToolCall  *ToolCall
	Result    *ToolResult
	Output    string
	Err       error
	Exchange  Exchange
	StartedAt time.Time
	Duration  time.Duration
	Name      string
	Detail    string
}

// Observer 接收每一步模型和工具事件。传 nil 即关闭 Trace，不影响 Agent 行为。
type Observer func(Event)

// RunRequest 描述一次 Agent 运行。Messages 至少应包含一条用户消息。
type RunRequest struct {
	Messages []Message
	// RequiredToolNames 是用户通过 @tool:name 明确指定的本轮工具作用域。
	// Runner 在整轮所有模型请求中只暴露这些工具，并验证首轮真实调用，
	// 不会把未执行工具的正文当成成功回答。
	RequiredToolNames []string
	// NewMessages 是已有会话在本轮新增的消息。Responses 使用它配合
	// PreviousResponseID 续接服务端上下文；首次运行可留空。
	NewMessages        []Message
	PreviousResponseID string
	// PromptCacheKey 只是一致的 Provider 缓存路由键，不参与 Agent 决策。
	// 不支持该字段的兼容 Provider 应由外层留空。
	PromptCacheKey string
	MaxSteps       int
	ToolTimeout    time.Duration
	OnTextDelta    func(string)
	// OnToolApproval is called immediately before a tool that requires user
	// confirmation runs. A non-nil error denies the call.
	OnToolApproval func(context.Context, ToolCall) error
	// OnToolLifecycle 在真实工具执行前同步收到 tool_start，在执行结束后收到
	// tool_end。start 回调失败时工具不会运行；end 回调失败时本轮立即失败，
	// 让外层可以用持久化账本守住副作用的未知结果窗口。
	OnToolLifecycle func(Event) error
	// OnTextReset 清除不应成为最终回答的临时流式正文，例如模型只看了搜索
	// 摘要就提前作答、Runner 要求其继续读取原始来源时。
	OnTextReset func()
	// PrepareRequest 在每次真实模型调用前执行。它可以对当前内存中的消息做
	// 微压缩，或在 force=true 时处理 Provider 返回的上下文超限。changed=true
	// 表示请求历史已经改变，Responses 适配器不应继续复用旧的 response ID。
	PrepareRequest func(context.Context, Request, bool) (request Request, changed bool, err error)
	// OnMessage 在 Agent 产生 assistant 或 tool 消息后调用。它保留逐条回调
	// 兼容性；需要保证工具链原子性的外层应优先使用 OnTurnMessages。
	OnMessage func(Message) error
	// OnTurnMessages 在一个 Agent step 的 assistant 及其全部 tool result 都
	// 准备好后一次调用。它适合在一个数据库事务中保存完整工具链；与 OnMessage
	// 同时设置时优先使用 OnTurnMessages。
	OnTurnMessages func([]Message) error
	// IsContextError 判断 Provider 是否因为上下文过大拒绝了请求。配合
	// PrepareRequest 可对当前 Step 自动压缩并重试一次。
	IsContextError func(error) bool
}

// RunResult 返回最终回答和可继续多轮会话的完整消息。
type RunResult struct {
	Answer     string
	Messages   []Message
	Usage      Usage
	ResponseID string
	Steps      int
}
