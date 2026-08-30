<img src="web/public/logo.svg" alt="EasyAgent Logo" width="80" />

# EasyAgent

一个可部署在个人电脑或 Linux 服务器上的轻量通用 Agent：一个 Go 二进制、一个 SQLite 数据库、一个 Web 页面。

```text
用户消息 → 模型 → Tool / Skill / MCP → 模型 → 最终回答
```

打开页面即可直接对话；需要更多能力时，再按需添加上下文、Skill 或 MCP。系统不引入 Graph、多 Agent 编排或工作流 DSL，模型通过原生 Function Calling 自己决定是否调用工具，Go 代码不根据用户关键词做业务路由。

## 能做什么

- 多轮对话，支持流式显示、图片、文本、代码和 PDF。
- 连接 OpenAI、DeepSeek、Ollama 以及 OpenAI 兼容模型服务。
- 内置文件读写搜索、Shell、时间、天气、计算和 Skill 加载工具。
- 在页面管理 Skill 与 MCP，任务需要时才加载。
- 查看完整 Agent Trace：真实请求响应、工具输入输出、Token、缓存、耗时与错误。
- 长会话自动生成上下文检查点，原始消息始终保存在 SQLite。

![EasyAgent 对话界面](docs/images/conversation.png)

## 快速开始

从 [Releases](https://github.com/lakernote/easy-agent/releases) 下载对应平台的单文件：

```bash
chmod +x easyagent
./easyagent
```

打开 <http://127.0.0.1:8080>，进入「模型与工具」配置模型后即可对话。发布包不要求安装 Go、Node.js、Python、Git 或 SQLite；只有 Agent 通过 Shell 执行这些程序时，服务器才需要安装对应程序。

从源码构建：

```bash
git clone https://github.com/lakernote/easy-agent.git
cd easy-agent
make build
./bin/easyagent
```

修改监听地址或数据库位置：

```bash
./easyagent -listen 0.0.0.0:8080 -db /var/lib/easyagent/easyagent.db
```

## 三种扩展

| 能力 | 适合放什么 | 如何使用 |
| --- | --- | --- |
| Tool | 高频、确定性的本机操作 | 编译进 Go 二进制，模型直接调用 |
| Skill | 任务方法、团队规范和领域经验 | 页面编辑，模型按需读取 |
| MCP | GitHub、浏览器、数据库等外部系统 | 页面配置，模型按需连接 |

## 文档

- [设计说明：场景、术语、边界和伪代码](docs/design.md)
- [运行时细节：消息、上下文、Tool、Skill、MCP 和 Trace](docs/agent-runtime.md)
- [安全说明](SECURITY.md)
- [参与贡献](CONTRIBUTING.md)

## 当前边界

EasyAgent 目前是单机、单进程、SQLite 应用，没有登录、RBAC 或多租户隔离，请勿直接暴露到公网。Agent 的效果取决于所选模型、上下文和可用能力。

## 许可证

[MIT](LICENSE)
