# EasyAgent 设计说明

这份文档面向代码 Review：说明 EasyAgent 解决什么问题、核心概念如何计算，以及哪些判断可以写在 Go 代码里、哪些必须交给模型。

## 1. 产品定位与场景

EasyAgent 是一个服务器上的轻量通用 Agent Runtime，不是某个固定业务流程。

典型场景：

1. **直接问答**：解释、写作、规划，不需要工具时模型直接回答。
2. **处理上下文**：把日志、代码、截图、PDF 或其他文件作为消息附件交给模型。
3. **操作工作区**：模型按需调用 `read`、`grep`、`find`、`edit`、`shell` 等工具完成任务并验证结果。
4. **连接外部系统**：通过 MCP 访问 GitHub、浏览器、数据库或团队内部服务。
5. **沉淀方法**：把项目约定、排查方法和高频流程写成 Skill，模型需要时再读取。

上述场景共用同一个 Agent 循环，不在 Runtime 中分别实现“代码模式”“日志模式”或“修复模式”。用户目标放在消息和 Skill 中，外部能力放在 Tool/MCP 中。

## 2. 核心概念

### Turn：用户轮次

用户发送一条消息，就开始一个 Turn。这个 Turn 可以包含多次模型请求和工具调用，直到得到最终回答或失败。下一条用户消息开始下一个 Turn。

页面中的“2 个用户轮次 · 4 条消息”通常表示：两条 `user` 消息和两条最终 `assistant` 消息。工具循环还会增加 `assistant(tool_calls)` 与 `tool` 消息，因此消息数不一定等于轮次的两倍。

### Step：Agent 步骤

一个 Step 表示模型基于当前上下文做出一次决策：

- 没有 `tool_calls`：当前 Turn 完成；
- 有 `tool_calls`：执行工具，把结果作为 `role=tool` 消息放回上下文，然后进入下一个 Step。

一次 Step 可以并列执行模型本次返回的多个 Tool Call。Step 不是 HTTP 请求次数，也不是 Trace 事件序号。

### Attempt：模型尝试

Attempt 表示同一 Step 内实际请求模型服务的次数。正常请求为 Attempt 1；如果流式兼容失败后关闭流式重试，重试仍属于同一个 Step，但显示为 Attempt 2。

### Event：审计事件

一次操作通常有开始和结束两个 Event，例如 `model_start/model_end`。Event 是时间线记录，不等于 Step。开始和结束事件使用相同的 Step、Attempt。

```text
Turn 1
├── Step 1 · Attempt 1：模型要求调用 read
├── Step 1             ：read 返回结果
└── Step 2 · Attempt 1：模型给出最终回答

Turn 2
├── Step 1 · Attempt 1：流式响应为空
└── Step 1 · Attempt 2：关闭流式重试并得到最终回答
```

## 3. 核心循环伪代码

Runtime 只根据模型返回的结构和运行状态推进，不读取用户文本做意图分类。

```text
function run(turnMessages, tools):
    messages = turnMessages

    for step from 1 to maxSteps:
        request = {
            messages: messages,
            tools: tools,
            tool_choice: "auto"
        }

        response = callModel(request, step, attempt=1)

        if response is empty because streaming is incompatible:
            response = callModel(request without streaming, step, attempt=2)

        append response.assistantMessage to messages

        if response.toolCalls is empty:
            return response.text

        for call in response.toolCalls:
            result = executeRegisteredTool(call.name, call.arguments)
            append role=tool result to messages

    fail("达到最大步骤")
```

最后一个 Step 不再暴露工具，要求模型基于已有证据收敛回答。它是明确的运行上限，不是业务路由。

## 4. 一次请求如何组装

```text
HTTP 消息/附件
    ↓ 保存 user 消息
SQLite 会话历史 ──→ 可选上下文检查点
    ↓
System Prompt + 历史消息 + Tool Schema
    ↓
Runner（唯一循环）
    ├── Model Adapter：Chat Completions / Responses
    ├── Built-in Tool
    ├── load_skill → Skill 正文
    └── search_mcp_tools → 少量相关 MCP Tool
    ↓
保存 assistant/tool 消息与 Trace
```

对应的 Server 伪代码：

```text
function handleUserMessage(sessionId, content, attachments):
    saveUserMessage(sessionId, content, attachments)
    enqueue(sessionId)

function runSession(sessionId):
    settings = loadModelSettings()
    history  = loadSessionHistory(sessionId)
    prompt   = renderStableSystemPrompt(skillMetadata, mcpMetadata)
    history  = compactIfContextIsFull(history, settings)
    tools    = loadToolsCatalog + optional(explicitlySelectedTool) + optional(loadMcp)

    result = Runner(modelAdapter(settings), tools).run(prompt + history)

    saveNewMessages(result.messages)
    saveUsageAndTrace(result)
```

## 5. 决策边界：什么能硬编码

### 允许写在代码里的确定性规则

- OpenAI/Responses/SSE/MCP 等协议解析；
- JSON Schema、URL、路径、认证和参数校验；
- 超时、取消、最大 Step、并发与重试策略；
- 工具注册、权限边界和真实工具执行；
- Trace、Token、缓存和 SQLite 持久化；
- 页面搜索、排序、状态展示等 UI 行为。

这些规则处理的是协议和运行状态，结果可验证。

### 不允许写在代码里的语义路由

```text
if userText contains "天气": call weather
if regex matches "修复|bug": load repair skill
if userText == "继续": rewrite the user prompt
if userText contains "GitHub": force GitHub MCP
```

这些判断会与模型产生两套决策来源，也无法覆盖真实语言表达。正确做法是：

```text
模型看到：用户消息 + Tool 描述/Schema + Skill/MCP 元数据
模型输出：普通回答，或原生 function call
Runtime：只校验并执行模型明确选择的能力
```

当前代码中的 `Contains/Regex` 只可用于协议识别、数据清理、输入校验和模型明确调用后的文件搜索，不能用于判断用户想做什么。新增类似逻辑时应在 Review 中拒绝。

## 6. Tool、Skill 与 MCP 的职责

```text
Tool  = 能执行什么（确定性函数）
Skill = 某类任务应该怎么做（按需读取的方法）
MCP   = 外部系统提供什么能力（按需连接的 Tool）
```

选择原则：

- 高频、稳定、本机即可完成的能力做成内置 Tool；
- 会随项目和团队变化的方法写成 Skill；
- 需要外部服务、账号或独立生命周期的能力接 MCP。

EasyAgent 只管理 MCP 自己的私有包和连接配置，不管理项目语言运行时。MCP 所需的 Node.js、Python 或 Java 从服务器 PATH 检测；缺少时给出明确提示，由宿主机、容器或项目工具链提供。这样扩展能力不会演变成另一套 SDK 管理器。

内置 Tool 也使用渐进披露：首轮只有 `load_tools` 的五个自解释能力组和一个很小的选择 Schema；模型按任务选择组后，Runtime 才把组内真实 Tool 说明与 Schema 加入下一轮。组是工具自身的声明式元数据，代码不读取用户自然语言做关键词或正则路由。Loader 成功后，下一步会临时隐藏 Loader 并要求模型至少调用一个真实工具，避免把“已经加载”误当成“已经核验”。用户在输入框明确 `@tool:name` 时仍只按准确名称预加载。工具模式的空响应可以从流式切到非流式重试一次，但不能删除工具后降级为自由回答，否则模型可能把本应执行的结果猜成成功。

Skill 和 MCP 同样先提供简短元数据：模型调用 `load_skill` 后读取正文，调用 `search_mcp_tools` 后才连接服务并按任务语义注册最多 5 个远端 Tool Schema。用户明确 `@skill:name` 时，该 Skill 正文直接注入本轮上下文。三类能力使用同一条“先目录、后正文/Schema”的原则，避免小模型首轮承受全部动态能力。

## 7. Trace 的 Review 标准

每次真实模型调用必须能回答：

- 属于哪个 Turn、Step 和 Attempt；
- 实际发送了什么请求，Provider 返回了什么；
- HTTP 状态、耗时和 Token 是否真实上报；
- Prompt Cache 是命中 0 还是 Provider 未上报；
- 调用了哪个工具，参数、工作目录、输出和错误是什么；
- 是否发生上下文压缩，以及哪些历史由检查点代表。

流式响应保存两层数据：页面默认展示聚合后的完整响应，展开后查看原始 SSE Delta。两者属于同一次 Attempt，不能拆成多个 Step。

## 8. 保持简单的约束

- 一个 Agent、一个 Runner、一个会话消息序列；
- 不增加 Graph、多 Agent 调度器或业务工作流 DSL；
- 不复制 Tool/Skill/MCP 的第二套调用协议；
- 新功能优先作为 Tool、Skill、MCP 或 Agent 外层触发器实现；
- Runtime 只因协议能力或运行状态分支，不因用户关键词分支。

更底层的消息格式、上下文压缩和协议适配见 [Agent 运行原理](agent-runtime.md)。
