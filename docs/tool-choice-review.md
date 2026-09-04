# EasyAgent `tool_choice` 请求链路 Review

> 状态：已按方案 A 修复。以下链路保留为故障复盘，描述的是修复前行为。

## 1. Review 结论

当前 400 的主要根因是 EasyAgent Runner 的重试逻辑，而不是 Groq 不支持 `tool_choice`。

Groq API 支持 `auto`、`required`、`none` 和指定函数；`openai/gpt-oss-20b` 也支持 Tool Use 和 Function Calling。

本次失败链路中，真正有问题的是：

```text
auto → load_tools
required → 模型返回空响应
空响应重试被代码改成 none
none → 模型仍返回 shell Tool Call
Groq 返回 400
```

第三个请求不是独立的 Agent Step，而是第二个 Step 内部的 Attempt 2。

## 2. `tool_choice` 语义

| 值 | 语义 | EasyAgent 适用场景 |
|---|---|---|
| `auto` | 模型自行选择直接回答或调用工具 | 默认 Agent 循环 |
| `required` | 必须调用至少一个工具 | 明确要求必须调用工具的场景 |
| `none` | 禁止调用任何工具，只能返回文本 | 最终文本收敛 |
| 指定函数对象 | 强制调用指定函数 | 用户明确点名某个函数 |
| 省略 | 有工具时通常默认为 `auto`，无工具时通常默认为 `none` | 不建议依赖隐式行为 |

Groq 官方 API Reference：

<https://console.groq.com/docs/api-reference>

GPT-OSS-20B 官方模型说明：

<https://console.groq.com/docs/model/openai/gpt-oss-20b>

## 3. EasyAgent 当前请求链路

### 3.1 第一轮：`auto → load_tools`

Runner 初始有工具时设置：

```go
toolChoice = ToolChoice{Mode: ToolChoiceAuto}
```

位置：`internal/agent/runner.go:124-127`

模型返回 `load_tools` 后，Loader 将 Shell 等真实工具动态注册到 Runner。

这一步是正确的。

### 3.2 第二轮：`required → 空响应`

Loader 成功且没有真实工具调用后，Runner 设置：

```go
requireLoadedToolCall = calledLoader && !calledRealTool
```

位置：`internal/agent/runner.go:332`

下一轮会隐藏 Loader，并设置：

```go
toolChoice = ToolChoice{Mode: ToolChoiceRequired}
```

位置：`internal/agent/runner.go:136-142`

这时 GPT-OSS-20B 返回空响应。空响应可能是模型/Provider 在流式工具调用场景下的兼容性表现；但此时 Runner 还不应该关闭工具。

### 3.3 第二轮内部重试：错误改成 `none`

Runner 检测到空响应后执行：

```go
request = prepareEmptyResponseRetry(request)
```

位置：`internal/agent/runner.go:201-210`

`prepareEmptyResponseRetry` 当前逻辑是：

```go
if !containsToolResult(request.Messages) {
    return request
}

request.Tools = nil
request.ToolChoice = ToolChoice{Mode: ToolChoiceNone}
```

位置：`internal/agent/runner.go:346-356`

问题在于：`load_tools` 的执行结果也会被写成：

```text
role = tool
```

因此：

```go
containsToolResult(request.Messages) == true
```

但此时实际上只有 Loader 结果，没有真实 Shell 工具结果。代码把“能力加载完成”误判成“真实工具已经执行”。

### 3.4 第三次请求：`none → shell Tool Call → 400`

由于重试请求被改成：

```json
{
  "tools": [],
  "tool_choice": "none"
}
```

模型仍然生成：

```json
{
  "name": "shell",
  "arguments": {
    "cmd": ["git", "--version"]
  }
}
```

Groq 发现请求禁止工具、但模型返回了工具调用，于是返回：

```text
Tool choice is none, but model called a tool
```

Groq 的 400 是对非法模型输出的正确拒绝，不是 `tool_choice=none` 不支持。

## 4. 根因分层

| 层级 | 判断 |
|---|---|
| Groq API 能力 | 支持 `auto`、`required`、`none` |
| GPT-OSS-20B 能力 | 支持工具调用和 Function Calling |
| 第二轮空响应 | 模型/Provider 流式兼容性问题，或需要进一步查看 raw chunks 判断 Parser 是否漏解析 |
| 第三轮 400 | EasyAgent 将 Loader 结果误判为真实工具结果，错误进入 `none` 收敛 |
| Shell 是否执行 | 没有执行；400 发生在 Provider 返回模型结果阶段 |

## 5. 与 Pi / OpenCode 的对比

### Pi

Pi 的 Agent 核心循环主要是：

```text
请求模型
→ 有 Tool Call 就执行
→ 把 Tool Result 放回上下文
→ 再次请求模型
→ 没有 Tool Call 就结束
```

Pi 的核心 `ToolChoice` 类型主要是 `auto` 和 `none`，没有 EasyAgent 当前这种 Loader 后强制 `required` 的特殊切换。

源码：

<https://github.com/earendil-works/pi/blob/6aedd1066e540642165aa30fa7b4a1b863778aa7/packages/ai/src/types.ts#L82>

Agent Loop：

<https://github.com/earendil-works/pi/blob/6aedd1066e540642165aa30fa7b4a1b863778aa7/packages/agent/src/agent-loop.ts>

### OpenCode

OpenCode 的协议层支持：

```text
auto
none
required
tool(name)
```

但它把这些选择作为请求能力传给具体 Provider；正常工具循环并不需要每次在 Loader、Required、None 之间人为切换。

源码：

<https://github.com/anomalyco/opencode/blob/03cb6324352b5e09477e56324aaaefb9e149b298/packages/llm/src/schema/messages.ts#L241>

OpenAI Chat 映射：

<https://github.com/anomalyco/opencode/blob/03cb6324352b5e09477e56324aaaefb9e149b298/packages/llm/src/protocols/openai-chat.ts#L188-L193>

## 6. 推荐修改方案

### 方案 A：正常 Agent 循环统一使用 `auto`（推荐）

建议的请求链路：

```text
auto → load_tools
auto → shell
auto → shell 结果后的下一轮
auto → 模型返回文本，结束
```

具体修改：

1. Loader 成功后，将 `required` 改为 `auto`。
2. `containsToolResult` 改成只识别真实工具结果，排除 `load_tools`。
3. 空响应重试时，如果真实工具尚未执行，保留原有工具列表和 `auto`。
4. 只有真实工具已经执行后，才允许进入无工具收敛。
5. Runner 仍然根据实际 Tool Call 判断是否执行工具，不依赖 `required` 保证模型行为。

### 方案 B：保留 Loader 后的 `required`

如果希望强制 Loader 后必须调用真实工具，也可以保留 `required`，但必须修改空响应重试逻辑：

```text
Loader 后 required + 空响应
→ 只关闭流式
→ 保留真实工具和 required
→ 再重试一次
```

如果再次为空，则返回清晰错误，不要把请求改成 `none`。

### 方案 C：删除 `none` 收敛

不建议简单地把所有 `none` 删除。`none` 对最终回答仍有价值，可以防止模型在真实工具成功后继续调用工具。

但 `none` 只能用于真正的最终收敛阶段，不能因为存在任意 `role=tool` 消息就触发。

## 7. 建议新增的回归测试

新增测试覆盖：

```text
第 1 次：auto，返回 load_tools
第 2 次：required 或 auto，返回空响应
第 2 次 Attempt 2：不能变成 none
第 2 次 Attempt 2：必须仍保留真实工具
```

建议测试名称：

```text
TestRunnerDoesNotConvergeAfterLoaderOnlyResult
```

现有测试：

- `TestRunnerKeepsToolsAndFailsClosedAfterEmptyCompatibilityResponses`
- `TestRunnerConvergesEmptyResponseAfterSuccessfulToolResult`
- `TestRunnerRequiresRealToolAfterLoader`

现有测试覆盖了普通工具结果和 Loader 后正常工具调用，但没有覆盖“Loader 后空响应导致错误进入 none”的场景。

## 8. 最终判断

不建议把所有请求永远固定成 `auto`，但 EasyAgent 的默认 Agent 循环应该以 `auto` 为主：

```text
有工具时：auto
Loader 后：auto
真实工具执行后：通常仍然 auto
最终文本收敛：none 或移除工具
```

当前最应该修复的是 Loader 结果识别和空响应重试，不是更换 Groq 模型，也不是关闭工具调用。

本 Review 只做分析，未修改 Go 代码。
