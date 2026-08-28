# 贡献指南

EasyAgent 的核心边界是：**消息描述目标，上下文提供事实，Skill 定义方法，Tool/MCP 提供能力，一个小型 Runtime 决定下一步。**

## 本地开发

```bash
make build
./bin/easyagent -db ./data/easyagent.db
```

修改后至少运行：

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
cd web && npm run build
```

## 代码约定

- Go 代码保持小函数、显式错误和中文注释；注释重点解释 Java 开发者不熟悉的 Go 语义与设计原因。
- 不增加固定业务任务字段。项目差异应进入 Prompt、Skill 或可插拔 Tool/MCP。
- 新入口应转换为会话消息和可选上下文，再复用 `internal/agent.Runner`；不要复制一套 Agent 循环。
- 新模型协议实现 `agent.Model`；OpenAI-compatible 服务优先复用 Chat Completions 或 Responses 适配器。新外部能力优先实现 Tool/MCP。
- UI 的启用、停用、失败和空状态必须清晰；避免重新引入多步向导。
- Agent Trace 必须覆盖新增的 LLM、工具和脚本调用。

提交 Pull Request 时说明行为变化、验证命令以及是否影响 SQLite schema。
