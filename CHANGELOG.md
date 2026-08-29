# 更新日志

## 未发布

- 模型默认值与校验范围集中管理，新数据库不再猜测用户已经下载某个 Ollama 模型。
- 增加 OpenRouter、Gemini 和 Groq 免费额度配置模板，并支持 `EASYAGENT_OLLAMA_URL`。
- 移除整轮 Agent 的固定十分钟超时；模型与工具继续使用各自超时和主动取消。
- 切换模型服务时不再沿用旧 API Key，避免跨 Provider 误发密钥。
- 增加模型 Agent 能力测试，真实验证原生 Function Calling 和工具结果回传。

## 0.1.0

- 重构为 EasyAgent：一个核心 Agent，不使用 Graph、多 Agent 或固定业务状态机。
- 支持 OpenAI Chat Completions、OpenAI Responses 和兼容模型服务。
- 支持 SQLite 多轮会话、消息历史和完整 Agent Trace。
- 将 System Prompt、内置 Skills、内置 Tools、MCP 预设拆成独立 Go packages。
- 支持在页面编辑和启停 Skills，添加、认证、测试和启停 MCP。
- 修复聊天 Markdown 渲染，增加高频内置 Skills 和 Playwright MCP 的依赖检测、自动安装、连接测试与启用流程。
- 新增对话式 Web UI、Token/缓存率/耗时汇总和 JSON 输入输出查看。
- 新增 Provider 缓存 Token 兼容、上下文账本和可配置的自动会话压缩；原始消息完整保留，压缩调用进入 Trace。
- 参考 Pi/Codex 将基础 System Prompt 精简分层，并为上下文检查点使用独立 Prompt。
- Skill 改为页面内创建；MCP 启用前强制完成认证和连接校验，并通过 `load_mcp` 按需加载远端工具。
- 新增任务排队状态和运行中取消，取消信号会传播到模型与工具调用。
- 新增无需外部解释器的 `calculate` 和可审计、可取消、带超时与输出截断的 `shell`。
- MCP 元数据增加用途描述，GitHub 预设限制为常用 Toolsets；Playwright 预设在安装前检查当前运行时实际要求的 Node.js 20+。
- Chat Completions 新增 SSE 流式读取和页面增量展示，并兼容忽略流式参数的普通 JSON Provider。
- 模型请求超时改为页面可配置，默认 300 秒；Ollama 禁用推理改用标准 `reasoning_effort=none`。
- Ollama 上下文窗口读取当前 `/api/ps` 的真实运行值，不再把理论上限用于压缩阈值。
- 失败和取消的模型调用也会保存次数、耗时与 Trace；修复 Trace 主键丢失及 Token 未上报被误显示为 0。
- 新增 `api-design` 高频 Skill，要求合法 JSON、完整接口契约和可运行验证。
- 模型始终看到稳定的常驻工具集合并通过原生 `tool_choice=auto` 自主选择；空流会记录原调用后关闭流式重试一次。
- “继续 / 接着做”类短指令只在模型输入中补充续写语义，SQLite 与页面仍保留用户原话。
- 删除旧的项目、事故、归因、修复、发布和兼容代码。
