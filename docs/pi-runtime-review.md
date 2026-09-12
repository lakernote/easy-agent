# PI 对照复审：搜索边界与 Agent 测试版图

复审基线：PI 仓库快照 `71dca871bc80b6bc97be37f0ca3189399d651fff`（2026-09-11），并于 2026-09-12 对照当前官方仓库 `earendil-works/pi` 复查。本文只比较可迁移的 Runtime 设计，不把 PI 当作唯一标准。

## 结论

PI 最值得借鉴的不是某个搜索实现，而是三层边界：Agent loop 只处理模型、事件和工具循环；默认 coding tools 保持少而稳定；搜索、浏览器和外部服务通过 Skill 或 Extension 增加。PI 默认只给模型 `read`、`write`、`edit`、`bash`，Brave Search 是按需加载的 Skill，也可以通过 `pi.registerTool()` 注册自定义搜索工具。

EasyAgent 不应照搬成 shell 脚本搜索。它是常驻 Web 服务，已经提供 Research Provider 配置、密钥保护、来源读取和引用 UI，因此保留高层 `web_research` 更合适。需要学习 PI 的是“可替换能力边界”：

- Runner 只认识协议无关的 `ToolSpec` / `ToolResult`，不知道 Tavily、Brave、SearXNG 或 Firecrawl。
- `web_research` 是一个高层能力，不把 `web_search`、`web_fetch`、天气、行情等供应商细节泄漏给模型。
- Provider 发现、页面读取、证据整理和引用编号属于工具实现；什么时候研究、怎样引用属于 Skill。
- 搜索后端必须可注册、可测试、可替换；不能把某家 API 的字段扩散到 Agent loop。

当前 EasyAgent 的方向基本正确：`web-research` Skill 负责使用规则，`web_research` 工具负责执行，Research Provider registry 负责后端。后续应进一步把研究工具做成独立 package contract，并为每个 adapter 跑同一套 conformance suite。

## PI、OpenCode 与 EasyAgent 的取舍

| Runtime | 默认工具策略 | 搜索与扩展 | 对 EasyAgent 的启发 |
| --- | --- | --- | --- |
| PI | 默认 `read`、`bash`、`edit`、`write` 四个 coding tools | Skill、Extension、Package 按需增加；未注册专用检索工具时可由 `bash` 使用系统命令 | 小而稳定的首轮 Schema 对中小模型更友好；扩展留在 Agent loop 外 |
| OpenCode | 默认启用全部 built-in tools，并通过 allow/ask/deny 权限控制 | 内置 `webfetch`、条件启用的 `websearch`、Skill、LSP、Todo 等 | 功能完整、交互丰富，但不是 EasyAgent“简单轻量”的默认模板 |
| EasyAgent | 可写模式默认 `read`、`shell`、`edit`、`write`，只读模式默认文件探索工具；存在 Skill 时直接给 `load_skill` | `load_tools` 渐进加载，联网统一走高层 `web_research`，外部系统走 MCP | 保留 PI 的小核心，同时保留服务端来源治理、引用、权限和可观测性 |

因此参考 PI 是当前默认工具面的最佳起点，但不是完整产品设计的唯一来源。EasyAgent 不加入 PI 明确省略的 Planner、Graph、Todo 或 Subagent；也不照搬 OpenCode 的全量工具暴露。大型模型仍可通过 Loader、Skill 和 MCP 获得完整能力，中模型则少承担无关 Schema。

## Token 与效果怎样取舍

工具 Schema 不是免费的：它在每次模型调用中重复占用 Input Token，工具越多，中模型选错或生成无效调用的概率也越高；但延迟加载同样不是免费的，它通常多出一次“发现/加载”模型往返。默认策略应按下面的不等式判断，而不是追求工具数越少越好：

```text
常驻成本 = 每个模型步骤重复发送的 Schema Token
按需成本 = 使用概率 ×（额外模型往返 + Loader 结果 + 失败重试风险）
```

只有常驻成本长期小于按需成本时，工具才应进入默认核心。还要把中模型成功率作为硬约束：如果一个工具虽然低频，但 Loader 会让目标模型频繁产生空响应或错误名称，应先简化目录与 Prompt；仍不稳定时再考虑常驻，而不是加入自然语言关键词路由。

2026-09-12 使用本机 `qwen3:14b-16k` 的同配置实测：旧的 9 个默认 Tool 请求约 3,189 Input Token；新的 4 个 PI 风格核心加 `load_tools` 为 1,517 Input Token，首轮减少约 52%。普通回答一次完成；动态计算经 `load_tools → calculate → 最终回答` 共 4,878 Token；动态联网经 `load_tools → web_research → 最终回答` 共 7,114 Token。这个结果支持以下默认边界：

- `read`、`shell`、`edit`、`write` 是高频、互补、Schema 相对稳定的 coding 核心；只读权限改用 `read`、`grep`、`find`、`ls`。
- 有 Skill 时直接提供小型 `load_skill`，因为再经 `load_tools` 会无意义地增加一次往返；没有 Skill 时完全省略。
- `web_research` Schema 大、结果也大，而且联网任务本身已有 I/O 延迟，默认按需最划算。
- `calculate`、`current_time` Schema 小，但在 coding 工作负载中频率不高；先按需并持续从 Trace 统计真实命中率。如果二者合计出现在约 15%–20% 以上的用户轮次，再用固定评测比较“常驻小工具”是否更省总 Token 和失败重试。
- 用户通过 `@tool:name` 明确选择时直接预加载，绕过 Loader；这是可靠的显式控制，不是 Runtime 猜意图。
- 工具总数进入数十个或接入大型 MCP 时，继续使用检索后只注册最多 5 个 Schema；不要把全部远端工具塞入首轮。

Anthropic 的官方 Tool Search 采用同样的延迟加载原则，并建议主要在 20 个以上工具时使用；其文档给出的多服务示例可把约 55k Tool 定义减少 85% 以上，同时指出工具超过约 30–50 个后选择准确率会下降。EasyAgent 当前内置目录规模远小于这个量级，因此一个确定性的短分组 Loader 比向量检索、BM25 或脚本化多工具调用更简单，也更适合模型无关目标。未来只有 MCP/插件目录显著扩大时，才需要把 Loader 升级成真正的 Tool Search。

## 为什么不是只保留 Shell

成熟 coding agent 普遍保留专用文件工具。PI 默认就是 `read`、`bash`、`edit`、`write`；Claude Code 将 `Read`、`Edit`、`Write`、`Glob`、`Grep` 与 `Bash` 分开，并据此区分只读和有副作用操作；Codex 更偏向 Shell，但仍保留结构化 `apply_patch`，OpenAI 的模型指南称命名的 Patch 工具在测试中降低了 35% 的补丁失败率。它们共同说明“执行通用命令”和“可靠修改文件”是两个不同契约。

EasyAgent 保留 `read`、`edit`、`write` 的具体理由是：`read` 能分页并控制大文件输出；`edit` 做唯一精确替换，失败时不会模糊修改；`write` 区分创建和覆盖，覆盖已有文件前要求本轮读取且版本未变。三者还统一执行工作区与符号链接边界校验，返回结构化结果。若只提供 `shell`，中模型必须反复生成 `cat`、`sed`、重定向或 heredoc，输出预算、转义、覆盖保护和审计都会退化。

`ls`、`grep`、`find` 的情况不同：有 Shell 的可写模式可以用系统命令完成，因此它们不进入首轮核心，只在模型需要结构化检索时按需加载；只读模式为了彻底移除 Shell 的执行面，才直接提供这三个探索工具。这个不对称是权限和可靠性的结果，不是重复设计。

## PI 的测试领域

PI 当前三个相关包约有 473 个 `*.test.ts` 文件：Agent 55、AI Provider 146、Coding Agent 272。数量不是质量排名，但覆盖面很值得作为 EasyAgent 测试目录的检查表。

| 层 | PI 重点覆盖 | EasyAgent 应对应覆盖 |
| --- | --- | --- |
| Agent loop | 事件顺序、工具循环、上下文变换、终止、retry、deferred retry、restore、reconcile、lane/progress | `Runner` 状态机不变量；每个 start 必须有 end/rejected；重试前清除 partial；取消后不提交结果 |
| Provider protocol | SSE、terminal event、partial JSON、tool ID、无结果 tool call、跨 Provider handoff、overflow、abort、retry、reasoning/cache/token usage | OpenAI Chat、Responses、Anthropic Messages、Ollama Chat 共用流式 conformance fixtures；原生 Function Call 正反例 |
| Tool result | 图片结果、工具输入流、截断、严格 schema、工具名单过滤 | typed `ToolResult`、Artifact 外置、大小上限、协议降级、`isError`、图片/音频/资源 UI |
| Session/context | compaction、branch summary、JSONL migration、storage conformance、session tree、fork | SQLite migration、压缩边界、split turn、长会话、历史分页、队列配置快照、fork 附件 |
| Concurrency/recovery | concurrent queue、file mutation queue、late output、process exit、signal shutdown、parallel preflight abort | 同工作区串行、worktree 并行、取消/完成竞态、服务重启、未知副作用账本、并行同名工具关联 |
| Extensibility | dynamic tools/providers、extensions discovery/reload、tool allowlist、skills collision/trust | Loader/MCP 动态目录、Skill 优先级与信任、Provider registry、配置热更新边界 |
| Product surface | RPC、TUI、selector、rendering、clipboard/image、XSS/export | HTTP/SSE 鉴权、设置行为、Trace、typed result renderer、真实浏览器 E2E |
| Regression corpus | 每个线上 bug 建独立编号测试 | 修复 bug 时保留最小 fixture；按协议、恢复、并发、上下文分类维护 |

## 本次复审后已经落实

- EasyAgent 模型必须完成两段式原生 Function Calling 测试；测试指纹绑定 Provider、协议、URL、模型、密钥、推理和上下文配置。Codex Runtime 使用自己的 app-server 协议。
- 所有模型调用使用流式 API；重试会先撤回旧 attempt 的 partial output。SSE/NDJSON Trace 统一为 `transport + raw_events + final_response`，失败也保留已收到事件。
- `ToolResult` 保留结构化内容、类型块、错误和压缩元数据；二进制作为 Artifact 单独存储，页面窗口只返回元数据，Runtime 重建上下文时才加载字节。
- 入队时冻结完整 `ModelSettings`，服务重启不会读取后来被修改的 Profile。
- EasyAgent 工具账本标记 `pre_effect`；Codex 只能标记 `observed`。缺少稳定 ID 且存在多个并行同名调用时标记 `uncorrelated`，不再用 FIFO 猜配。
- 取消/完成使用条件更新；迟到的完成不能把 canceled 覆盖为 idle。
- 四种内置 Provider Adapter 已共用同一套原生流式 Function Calling 资格契约；`cmd/easyagent-eval` 可对多个模型重复运行并比较通过率、Token 与耗时。
- 默认内置工具面收敛为 PI 风格的小核心；时间、计算、联网和额外检索按需加载，禁用全部 Skill 时不再发送无效的 `load_skill`。
- 请求 Token 估算只计算 Provider 实际收到的 Tool 字段，不再把 UI/Loader 元数据计入压缩阈值。
- 同一 Ollama endpoint 被视为一个本地推理资源，即使会话工作区不同也会串行，避免本机并发争抢导致首字延迟和内存抖动；远端模型仍按全局并发运行。

## 下一阶段建议的测试顺序

1. 扩展 Provider conformance suite：当前同一资格 fixture 已跑四种协议；继续加入畸形 JSON、半途断流、usage 变体和错误元数据的共享 fixture。
2. 建立 Runtime event invariant suite：自动检查事件配对、attempt reset、消息工具链原子性和终态唯一性。
3. 建立 crash-point suite：在入队提交、模型返回、工具 pre-effect、工具完成、消息提交、FinishSession 六个边界模拟进程退出。
4. 建立 Research adapter conformance：候选去重、域名白名单、SSRF 防护、正文读取失败、同域多页、引用只来自已读取来源。
5. 增加浏览器行为测试：模型测试后才能启用、Ollama 兼容协议警告、typed ToolResult/Artifact 展示、SSE 断线续传和取消竞态。
6. 建立本地模型资格回归：固定无副作用 Function Call、参数 schema、工具结果回传、流式首字延迟、完成率和 token/耗时，结果按模型版本与配置指纹保存。

## 官方参考

- [PI README：默认四个工具与扩展入口](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md)
- [PI 默认工具与只读工具注册表](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/tools/index.ts)
- [PI System Prompt：工具缺失时使用 bash 与 Skill 注入](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/system-prompt.ts)
- [PI Skills：渐进加载与 Brave Search 示例](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/skills.md)
- [PI Extensions：registerTool 与文件变更队列](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md)
- [PI Agent Runtime tests](https://github.com/earendil-works/pi/tree/main/packages/agent/test)
- [PI Provider tests](https://github.com/earendil-works/pi/tree/main/packages/ai/test)
- [PI Coding Agent tests](https://github.com/earendil-works/pi/tree/main/packages/coding-agent/test)
- [OpenCode built-in tools](https://opencode.ai/docs/tools/)
- [OpenCode tool permissions](https://opencode.ai/docs/permissions/)
- [Anthropic Tool Search：延迟加载、Token 与选择准确率](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool)
- [Anthropic 工具上下文取舍](https://platform.claude.com/docs/en/agents-and-tools/tool-use/manage-tool-context)
- [Claude Code Agent Loop：文件、搜索与 Bash 工具分层](https://code.claude.com/docs/en/agent-sdk/agent-loop)
- [OpenAI 模型指南：Shell 与 Apply Patch](https://developers.openai.com/api/docs/guides/latest-model)
- [Codex apply_patch 实现](https://github.com/openai/codex/blob/main/codex-rs/core/src/tools/handlers/apply_patch.rs)
