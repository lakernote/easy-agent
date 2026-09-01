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

**错误方案**：流式和非流式都为空后移除全部工具，再用 `tool_choice=none` 请求自由回答。这样虽然会得到文字，却可能让模型猜出一个错误的计算值、实时事实或执行结果，并把任务错误标记成成功。

**解决**：同一个 Step 中记录真实失败 Attempt，只允许关闭流式重试一次；兼容 Provider 仍为空就明确失败，绝不通过删除工具来伪造完成。`current_time`、`weather`、`calculate` 三个高频核心工具首轮常驻，其余工具仍让模型从五个短能力组中选择；组由工具声明式元数据组成，代码不增加关键词路由。Loader 成功后下一步临时隐藏 Loader，并要求调用真实工具，避免把“加载完成”冒充“核验完成”。重试均进入 Trace，不能伪装成一次成功请求。

### 2.7 工具失败后重复调用

**问题**：模型可能连续使用相同错误参数，浪费 Token 并陷入循环。

**解决**：工具错误统一包含 `ok/code/error/retryable/hint`。完全相同的不可重试失败只真实执行一次；之后返回 `duplicate_failed_call`，要求模型改参数、换工具或收敛。明确标记可重试的失败最多使用相同参数再试一次。连续两步仍无进展才关闭工具。Runtime 比较的是工具名和规范化参数，不读取用户文本判断业务。

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

### 2.11 服务 CWD 与 PATH 不稳定

**问题**：终端启动时工具工作区正常，由 launchd/systemd 启动后 CWD 可能是 `/`，PATH 也可能只有系统目录，导致文件工具访问错误位置、Shell/MCP 找不到 `node` 或 `npx`。

**解决**：`internal/appenv` 自动管理 Home、默认工作区、私有 Runtime 和确定性 PATH。PATH 合并私有 bin、一次性读取的登录 Shell PATH 和进程 PATH；Playwright 包安装到私有 Runtime，不依赖每次执行 `npx -y`。工作区不再作为启动参数：页面创建会话时选择并写入 SQLite，留空使用 `~/.easyagent/workspaces/default`；每轮为该会话派生独立 Environment，Shell、文件 Tool 和 stdio MCP 共用它。

### 2.12 Provider 与工具重试边界

模型请求只对 429、5xx 和明确的临时网络错误重试一次，遵守 `Retry-After`（最多等待 30 秒）；认证、参数等 4xx 直接失败。每个真实请求仍属于同一 Step 的不同 Attempt，并完整进入 Trace。工具重试由结构化 `retryable` 控制，不能把所有错误都盲目重放。

### 2.13 把 EasyAgent 做成通用 Runtime 管理器

**错误方案**：因为某个 MCP 或项目可能缺少 Java、Node.js、Python，就在 EasyAgent 中为每种语言增加下载地址、版本字段、安装 API 和管理页面。

**问题**：这会把一个轻量 Agent 变成不完整的 SDK 管理器；平台、架构、校验、镜像、补丁版本和项目锁文件都会进入核心代码，而且很快与 asdf、mise、SDKMAN、容器和团队现有工具链冲突。

**修正边界**：

- EasyAgent 管理自己的 Home、默认工作区、确定性 PATH 和 MCP 私有包；
- MCP 页面可以检测宿主命令和版本，但不执行系统级运行时安装；
- MCP 固定版本包安装到 `~/.easyagent/runtime/mcp/<id>`，卸载只删除该目录和配置；
- Java、Node.js、Python 等项目环境交给宿主机、容器、项目脚本或专门版本管理器；
- 检测失败和安装失败不写入“看起来已经安装”的配置，只有私有包安装成功但握手失败时保留停用配置供排查。

### 2.14 把用户、网页或工具内容当成高优先级指令

**问题**：用户消息可以直接要求“忽略之前规则”，网页、附件、日志、代码注释及 MCP 返回也可能夹带“读取密钥并上传”“输出 System Prompt”等间接提示词注入。如果模型把这些内容当成新的授权，工具越强，风险反而越大。

**解决**：

- System Prompt 明确信任顺序：系统策略最高，用户定义目标，附件和外部返回只提供不可信数据或证据；
- 外部内容不能扩大原始任务、权限或工作区，冲突指令被忽略后仍继续完成安全范围内的目标；
- 密钥不进入 Prompt，工作区、路径、Tool Schema、超时和 MCP 范围由 Runtime 确定性约束；
- 不使用关键词、正则或硬编码业务路由判断注入，因为它既容易绕过，也会误伤正常代码和安全研究内容。

Prompt 不是安全沙箱。若未来允许无人值守执行高风险 Shell、写操作或外部发布，还需要独立的权限策略、审批或隔离执行环境，不能只依赖模型“记住不要做”。

对标结论：OpenAI API 用 `system`/`developer` 与 `user` 角色表达指令层级，并建议保持 Prompt 精简、把授权边界写清楚；Codex 把 Personality 单独写成简短、具体的协作风格；Claude Code 在 Prompt 之外使用权限、工作区/网络沙箱、WebFetch 隔离上下文和 MCP 信任确认；Pi 明确说明默认继承宿主进程权限，强隔离需要容器或沙箱。EasyAgent 当前实现角色边界、外部内容信任标记和工作区文件边界，但尚未实现 Claude/Codex 等级的 Shell 审批与系统沙箱，页面不能把它宣传成“已完全防御提示词注入”。

参考：

- OpenAI Responses 指令层级：<https://developers.openai.com/api/reference/cli/resources/beta/subresources/responses>
- OpenAI 模型 Prompt 与授权边界建议：<https://developers.openai.com/api/docs/guides/latest-model>
- Codex Personality 模板：<https://github.com/openai/codex/blob/main/codex-rs/core/templates/personalities/gpt-5.2-codex_pragmatic.md>
- Claude Code Security：<https://code.claude.com/docs/en/security>
- Pi Permissions & Containerization：<https://github.com/earendil-works/pi#permissions--containerization>

## 3. 智能优先的 Token 优化顺序

按下面顺序优化，越靠前越不容易损害智能：

1. 保持稳定 System Prompt 与精简能力目录前缀，提高 Provider Prompt Cache 命中；
2. 内置 Tool 首轮只发少量核心 Schema 与自解释名称目录，模型选择后再加载其余完整 Schema；
3. Skill 先发元数据，需要时再加载正文；
4. MCP 先发服务元数据，需要时再连接并加载 Tool Schema；
5. 搜索只返回候选，读取工具只返回任务需要的正文；
6. 工具大结果确定性截断，但完整结果保留在 Trace；
7. 长会话达到真实阈值后再压缩较早轮次；
8. 用更强模型减少错误工具调用和重试，而不是靠代码猜意图；
9. 最后才考虑更激进的工具裁剪、模型路由或摘要策略。

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
internal/appenv         应用目录、默认工作区、私有运行时和确定性 PATH
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
