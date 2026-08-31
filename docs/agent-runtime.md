# EasyAgent 运行原理

EasyAgent 只有一个核心 Agent，`Runner` 只实现以下循环：

```text
Model.Generate(messages, tools)
     ├── 无 tool_calls → 返回最终回答
     └── 有 tool_calls → Tool.Run(arguments)
                            ↓
                       role=tool 消息
                            └── 回到 Model.Generate
```

## 分层

```text
internal/agent/
├── types.go                 # Message、Tool、Model、Usage、Event
├── runner.go                # 唯一 Agent 循环
└── openai/
    ├── client.go            # HTTP、认证、原始请求/响应
    ├── chat.go              # Chat Completions
    └── responses.go         # Responses

internal/builtin/
├── models/                   # 可替换的免费模型配置模板
├── prompt/system.md         # 常规 Agent 的稳定基础 Prompt
├── prompt/compaction.md     # 独立的上下文检查点 Prompt
├── skills/definitions/*     # 一个目录一个 SKILL.md
├── tools/                   # 文件、网页、时间、天气、计算、Shell 和 Skill 加载
└── mcp/                     # 页面一键安装预设

internal/mcpclient/          # 把远端 MCP Tool 转为 agent.Tool
internal/server/             # 配置、会话、API 和运行时组装
internal/store/              # SQLite
```

依赖方向保持单向：`agent` 不 import `server`、`store`、`builtin` 或 `mcpclient`。增加 Skill、MCP 或 Tool 不需要修改 `Runner`。

## 多轮对话

SQLite 中每条消息保留标准角色和 Tool Call：

- `system`：每轮重新渲染，不保存到用户会话；
- `user`：用户消息；
- `assistant`：模型文本或模型发起的 Tool Calls；
- `tool`：与 `tool_call_id` 对应的真实结果。

Chat Completions 每轮发送完整历史。Responses 在 Provider 配置没有变化且服务支持有状态续接时保存 `response_id`，下一轮使用 `previous_response_id` 和新增消息续接。Ollama Responses 不支持该续接方式，因此仍发送完整 input。

用户消息可以携带附件。文本和代码附件会转成文本内容块；图片会转成 Chat Completions 的 `image_url` 或 Responses 的 `input_image`；PDF 会转成 `file` 或 `input_file`。附件原文保存在 `ea_attachments`，会话接口只返回元数据，页面需要内容时再通过附件接口读取，避免列表接口携带大段 Base64。

页面的“上下文”使用 Provider 最近一次真实上报的 Input Token，而不是用字符数伪造精确 Token。模型窗口可在配置中填写；Ollama 会从 `/api/ps` 读取当前已加载模型真正使用的窗口，避免把理论上限误当成运行值。

Chat Completions 使用标准 SSE 流式读取，可见回答在内存中增量展示，完成后仍保存为一条标准 Assistant 消息。Trace 默认展示 EasyAgent 聚合后的完整响应，并在折叠区保留 Provider 原始 JSON Chunks，Usage 和耗时与本次模型请求一一对应。若兼容 Provider 忽略 `stream=true` 并返回普通 JSON，适配器会自动降级。

Trace 保留 Shell 和工具真实返回的绝对工作目录与文件路径，便于直接复现问题；图片和 PDF 的 Base64 原文不进入 Trace，只保留 MIME 类型和请求结构。

OpenRouter 等兼容网关可能在 Tool Call 旁返回 `reasoning` 或 `reasoning_details`。适配器会在同一轮工具循环中原样保留并回传这些字段，避免工具结果回来后丢失推理上下文。聚合响应和对话不展示这些字段；“原始流式 Delta”是 Provider 原始审计数据，会保留 Provider 实际返回的完整字段。

达到配置阈值（默认窗口的 75%）后，Server 会在普通 Agent 循环前调用同一个模型生成结构化检查点：

```text
完整 SQLite 历史
     ↓ 达到阈值
较早完整轮次 ──→ compaction.md ──→ 结构化摘要
                                      + 最近的完整原始轮次
                                      + 本轮重新渲染的 System Prompt
                                                ↓
                                           普通 Agent 循环
```

检查点保存到 `ea_compactions`，包含摘要、消息边界、原始消息数量、Usage 和时间。`ea_messages` 不删除任何原文。再次压缩时会把旧检查点和新增长历史一起更新；压缩调用作为 `compaction_start` / `compaction_end` 进入 Trace，Token 和耗时计入会话总账。触发前可以参考最近一次真实 Input Token 加上新消息估算，但页面仍只把 Provider 上报值显示为真实 Token。

单次工具结果过大时先做确定性的头尾保留，完整内容仍留在 SQLite 和 Trace；只有输入本身达到阈值时才调用模型生成检查点。每次请求还会按剩余上下文动态收紧该次 `max_output_tokens`，不会因为模型配置了较大的理论输出上限就提前压缩一个很短的工具链。

## Prompt 与 Skill

基础 Prompt 在 `internal/builtin/prompt/system.md`，只保存身份、工作方式、信任边界、能力使用和回答契约。运行时事实、Skill 元数据和 MCP 元数据放在尾部，便于保持公共前缀稳定；不会把 MCP 启动参数或密钥写进 Prompt。用户消息可以定义任务目标，但不能覆盖 System Prompt 或授权越权操作；附件、网页、代码、日志以及 Tool/MCP 返回按不可信数据处理，只能提供证据。压缩使用独立的 `internal/builtin/prompt/compaction.md`，不携带常规工具规则，也不给模型任何 Tool，避免摘要过程继续执行用户任务。

Prompt 规则只能降低模型服从直接或间接注入的概率，不能替代 Runtime 约束。工作区边界、Tool Schema、路径校验、超时和 MCP 连接范围仍由 Go 代码确定性执行；不能用关键词黑名单判断“是否注入”，也不能把网页或工具结果当成用户授权。

网页搜索、网页读取和文本附件会携带 `content_trust=untrusted_external` 或 `trust=untrusted_user_content` 元数据，使模型能稳定区分任务指令与待分析资料。该标记不删除原文，也不依赖扫描“忽略规则”等关键词，因此日志、安全样例和代码仍可正常分析。

Skill 默认使用渐进式加载：模型先看到简短元数据；任务相关时加载 `load_skill`，再读取指定 Skill 全文。用户明确 `@skill:name` 时，运行时直接把已选正文注入本轮 Prompt。

内置 Tool 首轮只注册 `load_tools`：其描述包含稳定、自解释的名称目录，但不重复 13 份说明和参数 Schema。模型自主选出当前需要的最少工具，Runtime 动态注册后下一轮直接调用。`@tool:name` 是用户显式预加载，不是语义路由。MCP 则通过 `load_mcp` 按服务加载。这能降低小上下文模型的首轮 Token，同时保留模型选择权；Prompt Cache 依赖稳定的 System Prompt 与精简目录前缀。若 Provider 在保留工具时返回空响应，Runtime 只允许关闭流式重试一次；仍为空就明确失败，不会移除工具后让模型猜答案。

工作区文件能力直接编译进 Go 二进制：`read` 分段读取文本，`grep` 搜索内容，`find` 查找文件，`ls` 查看目录，`edit` 做唯一精确替换，`write` 创建文件或在已读取版本未变化时完整覆盖。默认工作区固定为 `~/.easyagent/workspaces/default`，不使用进程 CWD。用户在页面创建会话时可以选择服务器上已存在的目录；绝对路径保存在会话中，后续多轮固定使用它。每轮从会话派生独立 Environment，文件、Shell 和 stdio MCP 共用该工作区，路径解析会校验真实符号链接目标并拒绝越界。

`calculate` 使用 Go 标准库执行常见数学表达式，不要求服务器安装 Python。`shell` 使用服务器自带的 `/bin/sh`，只负责构建、测试、Git、脚本、CLI 和安装任务，支持工作目录与最长 300 秒超时；标准输出和错误输出分别限制为 64 KiB，并保留开头和结尾。任务取消时会终止整个命令进程组。Agent 和页面 Trace 都保留工具真实返回的命令、工作目录和文件路径，保证结果可复现。

页面对内置 Skill 的修改作为 SQLite 覆盖保存；删除覆盖即可恢复编译进二进制的版本。自定义 Skill 也保存在同一张表。

## MCP

`internal/mcpclient` 使用官方 Go SDK 连接 STDIO 或 Streamable HTTP Server，调用 `tools/list`，再转换为：

```text
mcp__<server_id>__<remote_tool_name>
```

每轮开始时不会连接全部 MCP。模型先看到一个 `load_mcp(id)` 工具和服务元数据；只有调用它时，`Loader` 才连接指定服务，并通过 `Runner.AddTools` 把真实工具加入下一轮模型请求。这样普通问答不承担远端工具 Schema 的 Token 成本。

认证、进程环境和连接生命周期都停留在 MCP 适配层。stdio MCP 与 Shell 共用启动时冻结的 PATH 和会话工作区；服务管理器 PATH 较短时会一次性读取登录 Shell PATH。Playwright 预设固定版本安装到 `~/.easyagent/runtime/mcp`，不写全局 npm 或项目 `node_modules`。EasyAgent 只管理这个 MCP 私有包及其配置，不成为通用语言运行时管理器：Node.js、Python、Java 等由宿主机、容器或项目工具链提供，MCP 页面只检测 PATH 和版本。启用前必须通过握手和 `tools/list`，任务结束后关闭本轮连接。

## 排队与取消

单机默认只有一个执行槽。消息先进入 `queued`，拿到槽位后才进入 `running`；页面会分别展示这两个状态。用户停止任务时，Server 取消对应 `context.Context`，模型 HTTP 请求、内置 Tool 和 MCP Tool 会收到同一个取消信号，SQLite 保留 `canceled` 状态和已经发生的 Trace。

整轮 Agent 不设置隐藏的固定总超时：每次模型请求使用页面配置的超时，每次 Tool 自己声明超时，Runner 还有最大循环步数，用户也能主动停止。这样长任务不会被一个与页面配置无关的总计时器提前杀掉。

模型默认值和校验范围集中在 `internal/store/model.go`。免费模型端点与型号集中在 `internal/builtin/models`，它们只是帮助填表的可更新目录，不参与 Agent 的工具路由。切换 Provider 或 Base URL 时不会继承旧服务的 API Key。

页面的“测试当前模型”会执行一次无副作用的两阶段诊断：要求模型返回原生 `tool_calls`，回传固定工具结果，再确认模型能读取结果并生成最终文本。把工具调用 JSON 写在普通回答里的模型会被明确判定为不适合，而不会显示成“连接成功”。

## Trace

`Runner.Observer` 公开四种核心循环事件：

- `model_start`
- `model_end`
- `tool_start`
- `tool_end`

Server 另外记录 `compaction_start` 和 `compaction_end`，使独立的摘要模型调用同样可审计。

Trace 使用三个不同层级，不能混为一谈：

- **Turn**：用户发送一次消息触发的一整轮 Agent 运行；
- **Step**：模型做出一次回答或工具选择，工具结果回来后才进入下一步；
- **Attempt**：同一 Step 内真实模型请求的次数，例如流式兼容降级重试。

模型请求的开始和结束是两个 Event，但属于同一个 Step/Attempt。流式 Delta 是一次模型响应的传输片段，也不会增加 Step。

应用层把真实模型请求、响应、工具参数、结果、错误、耗时和 Token 保存到 `ea_events`。页面默认显示汇总，展开事件后格式化 JSON。附件 Base64 在写入 Trace 前会替换为省略标记，只保留名称、类型和大小，避免把二进制内容重复写入审计记录。Trace 不依赖“根因”“处理人”或“修复”这些业务字段，所以能用于任意任务。

缓存 Token 仅在 Provider 响应真实包含缓存字段时统计。已兼容 OpenAI `prompt_tokens_details.cached_tokens` / `input_tokens_details.cached_tokens`、DeepSeek `prompt_cache_hit_tokens` 以及常见的 cache read/write 兼容字段。“未上报”与“已上报 0”是两种不同状态。OpenAI 请求使用稳定 `prompt_cache_key`；其他兼容服务不发送厂商专属字段。

## 为什么不用 Graph

Graph 适合预先知道节点和转移条件的稳定业务流程。EasyAgent 的目标是通用对话和动态工具选择；模型每一步根据最新观察决定下一步即可。固定 Graph 会带来两套决策来源、更多状态和更高理解成本。

如果未来某项业务确实需要审批或确定性 Job，它应在 Agent 外层做触发与权限控制，仍然调用同一个 Agent，而不是复制一套 Agent Runtime。
