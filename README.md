# EasyAgent

EasyAgent 是一个可以部署在个人电脑或 Linux 服务器上的轻量通用 Agent：一个 Go 二进制、一个 SQLite 数据库、一个 Web 页面。

它只有一个核心循环：

```text
用户消息 → 模型 → Tool / Skill / MCP → 模型 → 最终回答
```

不需要先创建项目，也不引入 Graph、多 Agent 编排或工作流 DSL。

## 功能

- 多轮对话；Chat Completions 支持流式显示，也支持 Responses 及兼容接口。
- 可使用 OpenAI、DeepSeek、Ollama 或其他兼容模型服务。
- 内置时间、天气、计算、Shell 和 Skill 加载工具。
- 页面创建、编辑、启停 Skill；按需加载正文，减少无效 Token。
- 接入 STDIO / HTTP MCP，支持 Bearer Token、用户名密码、Header 和环境变量。
- 完整 Agent Trace：模型和工具输入输出、Token、缓存命中、耗时与错误。
- 长会话自动生成上下文检查点；SQLite 始终保留全部原始消息。
- 单机任务排队、主动停止和 SQLite 持久化。

## 快速开始

需要 Go 1.26.7+ 和 Node.js 22+：

```bash
git clone https://github.com/lakernote/easy-agent.git
cd easy-agent
make build
./bin/easyagent
```

打开 <http://127.0.0.1:8080>。

默认数据库为 `./data/easyagent.db`。修改监听地址或数据库位置：

```bash
./bin/easyagent -listen 0.0.0.0:8080 -db /var/lib/easyagent/easyagent.db
```

### Docker

```bash
docker build -t easyagent .
docker run --rm -p 8080:8080 -v easyagent-data:/data easyagent
```

## 配置模型

打开「模型与工具」：

1. 使用 Ollama 时，先启动 Ollama 并下载模型，然后在页面点击「使用」。
2. 使用云模型时，填写 Provider、Base URL、模型名、协议、API Key 和请求超时。
3. API Key 可以直接保存到本机 SQLite，也可以填写环境变量名。

Ollama 示例：

```bash
ollama serve
ollama pull qwen3:8b
```

## 扩展能力

- **Tool**：编译进 Go 二进制的高频、确定性能力。
- **Skill**：可在页面编辑的任务方法，Agent 需要时通过 `load_skill` 读取。
- **MCP**：外部系统能力，启用前会验证认证、连接和工具列表，再按需加载。

服务器不需要安装 SQLite、Python 或 Git。但 `shell` 调用 Git、Python、Maven 等命令时，服务器仍需安装对应程序；STDIO MCP 也需要自己的启动运行时。

## 数据与安全

- 默认只监听 `127.0.0.1`，当前没有登录、RBAC 或多租户隔离，请不要直接暴露到公网。
- API Key、MCP 密钥、会话和 Trace 保存在本机 SQLite；数据库尚未静态加密。
- Shell 和 STDIO MCP 使用 EasyAgent 进程权限运行，不是安全沙箱。
- 生产部署建议使用低权限账号、限制数据库文件权限，并优先通过环境变量提供密钥。

更多说明见 [Security Policy](SECURITY.md)。

## 开发

```bash
make test          # 前端构建、依赖校验、vet、race test
make build         # 当前平台单二进制
make build-linux   # Linux amd64；LINUX_ARCH=arm64 可切换架构
```

代码结构与 Agent 循环见 [Agent Runtime](docs/agent-runtime.md)。

## 当前边界

- 单机、单进程、SQLite，默认同时运行一个模型任务。
- Responses 暂未流式显示；暂无登录系统、Webhook、Cron、项目工作区和自动创建 PR。
- 模型能力取决于所选模型、上下文、Skill 和工具质量。

## License

[MIT](LICENSE)
