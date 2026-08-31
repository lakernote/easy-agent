# EasyAgent 工程复盘与学习记录

这份文档记录项目从早期原型到当前 EasyAgent Runtime 遇到的主要问题、错误方案和修正原则。它不是发布日志；目标是帮助后续 Review 时判断一段代码是在实现确定性协议，还是在替模型做脆弱的语义决策。

## 1. 当前结论

EasyAgent 保持一个核心 Agent：

```text
用户消息
   ↓
System Prompt + 会话历史 + Tool Schema + Skill/MCP 元数据
   ↓
模型自主决定：直接回答 / 调用 Tool / 加载 Skill / 加载 MCP
   ↓
Runtime 校验并执行 → 结果回给模型 → 最终回答
```

智能优先，Token、延迟和成本优化排在正确性之后。任何优化都必须先通过相同的端到端 Case，再比较 Token、耗时和费用；不能因为调用次数变少就认定效果更好。

## 2. 已遇到的问题与解决方案

### 2.1 把具体业务写进 Runtime

**问题**：早期设计包含评测、根因、责任人、修复 PR 等固定字段和流程，通用 Agent 逐渐变成特定业务系统。

**原因**：把“用户想做什么”和“Runtime 如何运行模型”混在了一起。

**解决**：

- `internal/agent` 只保留 Message、Model、Tool 和循环；
- 任务目标放进用户 Prompt；
- 任务方法放进 Skill；
- GitHub、浏览器、日志系统等外部能力放进 MCP；
- 审批、Webhook、Cron 等确定性触发放在 Agent 外层。

### 2.2 用关键词或正则表达式选择工具

**错误示例**：

```text
if contains(userText, "天气") then call weather
if contains(userText, "GitHub") then load github
if matches(userText, "bug|修复") then load repair skill
```

**问题**：自然语言表达无限，代码规则和模型会形成两套冲突的决策系统。

**解决**：把 Tool 的名称、用途和 JSON Schema 交给模型，通过原生 Function Calling 选择。Go 代码只处理协议、校验、超时、权限、执行和失败收敛。

`@skill:name`、`@tool:name`、`@mcp:name` 是用户明确选择能力的输入协议，不是根据语义猜测用户意图。

### 2.3 `toolCategory(name)` 按名字二次分类

**问题文件**：`internal/builtin/tools/catalog.go`

**旧方案**：注册工具后，再用 `switch toolName` 推导“文件、执行、信息、扩展”等页面分类。

这段代码不会影响模型决策，因此不属于语义路由；但分类与注册分散在两处，新增或改名时容易漏改。

**当前方案**：工具注册时同时声明 UI 分类：

```text
entry {
    tool: currentTimeTool(),
    category: information
}
```

模型仍只接收 `ToolSpec`，Category 只返回页面展示。

### 2.4 通用 `web_search` 内置 GitHub 特判

**问题文件**：`internal/builtin/tools/web_search.go`

**旧方案**：搜索结果指向 GitHub 时，`web_search` 自动拼 GitHub API 地址并补充 star、fork 等字段。

**为什么不合适**：它不是按用户文本路由，但破坏了通用搜索的单一职责。以后继续加入 GitLab、Jira、Stack Overflow 会演变成网站特判集合。

**当前方案**：

- `web_search` 只发现候选网址；
- `web_fetch` 读取已知网页证据；
- GitHub 精确结构化数据使用 GitHub MCP；
- 其他平台使用对应 MCP 或独立、明确命名的 Tool。

搜索实现当前使用 DuckDuckGo HTML 作为零配置后端。这是可替换的基础设施实现，不是业务语义判断；如果稳定性成为问题，应抽象 Search Provider，而不是添加网站特判。

### 2.5 把日期、星期和精确时间混为一谈

**旧问题**：每次都调用时间工具会增加一次模型循环；完全依赖 Prompt 又可能把精确时间说错。

**解决**：运行时上下文提供本轮稳定的日期、星期和时区；询问精确到时分秒的“现在几点”时，模型调用 `current_time`。是否调用仍由模型根据 Tool 描述决定，代码不扫描用户问题。

### 2.6 小模型在带 Tool Schema 时返回空内容

**现象**：Ollama/Qwen 在某些请求中既不返回文本，也不返回 Tool Call。

**解决**：同一个 Step 中记录真实失败 Attempt，再关闭流式重试；兼容 Provider 仍为空时，最后用 `tool_choice=none` 请求模型基于已有上下文收敛。重试均进入 Trace，不能伪装成一次成功请求。

### 2.7 工具失败后重复调用

**问题**：模型可能连续使用相同错误参数，浪费 Token 并陷入循环。

**解决**：Runtime 不判断业务原因，只统计连续无进展的结构化失败。达到阈值后关闭工具，让模型基于现有证据给出清楚的失败说明。

### 2.8 上下文过早压缩

**旧问题**：为理论最大输出预留过多 Token，短对话也会触发摘要，既花钱又损失细节。

**解决**：

- 以 Provider 实际 Input Token 为准；
- 只保留小比例动态安全边界；
- 每次请求按剩余窗口收紧本次最大输出；
- 工具大结果先确定性保留头尾；
- 真正达到阈值后才生成检查点；
- SQLite 永远保存原始消息和完整 Trace。

### 2.9 把 SSE Delta 当成多个 Step

**问题**：流式片段、模型调用、Agent Step 概念混乱，页面统计失真。

**解决**：

- Turn：一条用户消息触发的完整运行；
- Step：模型基于一次观察作出的决策；
- Attempt：同一 Step 的协议重试；
- Delta：同一个 Attempt 的传输片段；
- Event：模型或工具开始/结束的审计记录。

页面默认显示聚合响应，Trace 可展开原始 Delta。

### 2.10 Cache Token 显示为 0%

**问题**：有些 Provider 没上报缓存字段，显示 0% 会误导用户以为明确没有命中。

**解决**：使用 `CacheReported` 区分“明确上报 0”和“未上报”；只有真实字段存在时才计算缓存率。

## 3. 智能优先的 Token 优化顺序

按下面顺序优化，越靠前越不容易损害智能：

1. 保持稳定 System Prompt 和 Tool Schema 前缀，提高 Provider Prompt Cache 命中；
2. Skill 先发元数据，需要时再加载正文；
3. MCP 先发服务元数据，需要时再连接并加载 Tool Schema；
4. 搜索只返回候选，读取工具只返回任务需要的正文；
5. 工具大结果确定性截断，但完整结果保留在 Trace；
6. 长会话达到真实阈值后再压缩较早轮次；
7. 用更强模型减少错误工具调用和重试，而不是靠代码猜意图；
8. 最后才考虑更激进的工具裁剪、模型路由或摘要策略。

每次优化至少比较：任务成功率、事实正确性、工具选择正确率、最终答案完整度、Input/Output/Cache Token、模型调用数、工具调用数、耗时和费用。

## 4. 模型 Provider 边界

Ollama 是本地开发和协议兼容测试 Provider，适合验证：

- Chat Completions 流式解析；
- Function Calling 工具循环；
- 多轮历史、上下文压缩和 Trace；
- 弱模型失败时 Runtime 是否能如实处理。

生产效果应使用经过真实 Case 评测的云模型。EasyAgent 的 `internal/agent/openai` 同时实现 Chat Completions 和 Responses，页面可配置任意 OpenAI 兼容 Base URL、模型和 API Key，因此可接 OpenAI、DeepSeek、OpenRouter、Groq、Gemini 兼容端点等。Provider 配置与 Agent Runtime 分离，切模型不应修改 Runner。

### Codex 与 GitHub Copilot

- **OpenAI 模型 API**：EasyAgent 当前通过正式 API Key 调用 Responses 或 Chat Completions。这条路径由 EasyAgent 自己负责 Tool、Skill、MCP、上下文和 Trace。
- **Codex SDK**：OpenAI 已提供 `@openai/codex-sdk`，可以在服务端启动、继续和恢复本地 Codex thread；Codex 本身支持 ChatGPT 订阅登录或 API Key 登录。它是一套完整的编码 Agent，不是 OpenAI-compatible 模型 URL。
- **Codex 集成边界**：不能把 Codex 桌面应用的缓存凭证读取出来塞进 EasyAgent 的 API Key 字段。若采用 Codex SDK、App Server 或 MCP Server，应作为一个明确的可选 Runtime/外部能力，并单独映射事件与 Trace。
- **GitHub Copilot SDK**：GitHub 已提供正式 Copilot SDK，可在后端连接 Copilot CLI，支持 GitHub OAuth、Copilot 订阅计费或 BYOK。它返回的是一套 Agent Session/JSON-RPC 能力，不是 IntelliJ 插件公开出来的 OpenAI Chat Completions 地址。
- **IntelliJ Copilot 插件**：不要读取插件缓存、Token 或内部接口。若采用 Copilot，应使用官方 Copilot SDK，在 EasyAgent 外增加独立适配层，并明确它和现有核心 Agent 谁负责 Tool 循环，避免“双 Agent”重复规划和重复计费。

因此有两条清晰路线：

```text
路线 A（当前默认）：EasyAgent Runtime + OpenAI/DeepSeek 等模型 API
路线 B（以后可选）：EasyAgent UI/任务入口 + Codex SDK 或 Copilot SDK Runtime
```

路线 A 最轻、协议统一、Trace 最完整；路线 B 的代码能力可能更强，但会增加 CLI/Node 等部署依赖，并且需要重新适配会话、权限、工具事件、Token 和取消。不要在同一个 Turn 中让 EasyAgent Runner 与外部 Agent 同时自主规划。

参考资料：

- OpenAI Responses API：<https://developers.openai.com/api/reference/cli/resources/responses/methods/create>
- OpenAI 模型指南：<https://developers.openai.com/api/docs/guides/latest-model>
- Codex SDK：<https://developers.openai.com/codex/sdk>
- Codex 认证：<https://developers.openai.com/codex/auth>
- GitHub Copilot SDK：<https://docs.github.com/en/copilot/how-tos/copilot-sdk>
- Copilot 后端部署：<https://docs.github.com/en/copilot/how-tos/copilot-sdk/setup/backend-services>
- Copilot BYOK：<https://docs.github.com/en/copilot/how-tos/copilot-sdk/auth/byok>

## 5. 分包与依赖 Review

```text
internal/agent          核心类型和唯一 Runner
internal/agent/openai   模型协议适配
internal/builtin        内置 Prompt、Tool、Skill、MCP 和模型模板
internal/mcpclient      外部 MCP 转 agent.Tool
internal/server         HTTP、会话、运行时组装和队列
internal/store          SQLite
web                     页面
```

正确依赖方向：

```text
server → agent/openai, builtin, mcpclient, store
builtin/tools → agent
mcpclient → agent
agent → Go 标准库
```

`agent` 不能反向 import `server`、`store`、`builtin` 或 `mcpclient`。不要为了“看起来分层”添加没有独立职责的 Manager、Service、Graph 或多个 Agent 类。

## 6. 新代码 Review 清单

- 是否读取用户文本来决定调用哪个 Tool、Skill 或 MCP？如果是，通常应删除。
- 是否在通用工具里出现特定网站、仓库或业务字段？如果是，应移到专用 Tool/MCP。
- Tool 描述和 Schema 是否足够让模型自己选择？
- 确定性失败是否结构化并进入 Trace？
- 是否区分 Turn、Step、Attempt、Event 和 SSE Delta？
- Token 与 Cache 是否来自 Provider 真实上报？
- 上下文优化是否用相同 Case 验证过答案质量？
- 新 Provider 是否只修改适配层，而没有侵入 Runner？
- 新能力更适合 Tool、Skill 还是 MCP？
- 配置、密钥和用户路径是否可能进入日志、截图或 Git 历史？
