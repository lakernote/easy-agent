<div align="center">
  <img src="web/public/logo.svg" alt="EasyAgent Logo" width="96" />
  <h1>EasyAgent</h1>
  <p>一个可部署在个人电脑或 Linux 服务器上的轻量通用 Agent</p>
  <p><code>用户消息 → 模型 → Tool / Skill / MCP → 模型 → 最终回答</code></p>
</div>

打开页面即可直接对话；需要更多能力时，再按需添加上下文、Skill 或 MCP。系统不引入 Graph、多 Agent 编排或工作流 DSL，模型通过原生 Function Calling 自己决定是否调用工具，Go 代码不根据用户关键词做业务路由。

## 能做什么

- 多轮对话，支持流式显示、图片、文本、代码和 PDF。
- 连接 OpenAI、DeepSeek、Ollama 以及 OpenAI 兼容模型服务。
- 内置文件读写搜索、Shell、网页搜索/读取、时间、天气、计算和 Skill 加载工具。
- 在页面管理 Skill 与 MCP，任务需要时才加载。
- 查看完整 Agent Trace：真实请求响应、工具输入输出、Token、缓存、耗时与错误。
- 长会话自动生成上下文检查点，原始消息始终保存在 SQLite。

## 界面预览

对话、运行时和能力管理使用同一套工作区视觉语言；新会话创建时固定运行时与模型配置，已有会话不会被后续修改影响。

<p align="center">
  <img src="docs/images/conversation.png" alt="EasyAgent 对话工作区" width="920" />
</p>

<table>
  <tr>
    <td align="center" width="50%">
      <strong>多 Runtime 与运行时配置</strong><br />
      <img src="docs/images/model-and-tools.png" alt="多 Runtime 设置" width="440" />
    </td>
    <td align="center" width="50%">
      <strong>Skills 按需加载与编辑</strong><br />
      <img src="docs/images/skills.png" alt="Skills 管理" width="440" />
    </td>
  </tr>
</table>

## 快速开始

从 [Releases](https://github.com/lakernote/easy-agent/releases) 下载对应平台的单文件：

```bash
chmod +x easyagent
./easyagent
```

启动后打开 <http://127.0.0.1:8080>（远程服务器请把 `127.0.0.1` 换成服务器 IP），进入「设置 → 模型配置」完成配置后即可对话。发布包不要求安装 Go、Node.js、Python、Git 或 SQLite；只有 Agent 通过 Shell 执行这些程序时，服务器才需要安装对应程序。

从源码构建：

```bash
git clone https://github.com/lakernote/easy-agent.git
cd easy-agent
make build
./bin/easyagent
```

启动参数只有监听地址和 SQLite 路径，而且都有默认值：

```bash
./easyagent -listen 0.0.0.0:8080 -db /var/lib/easyagent/easyagent.db
```

不传参数时监听 `0.0.0.0:8080`，数据库位于 `~/.easyagent/easyagent.db`。服务器上如果端口已被占用，先用 `ss -ltnp | grep :8080` 找到进程，再停止旧实例或换端口，例如 `./easyagent -listen 0.0.0.0:8081`。
EasyAgent 自动管理 `~/.easyagent` 和默认工作区
`~/.easyagent/workspaces/default`，不依赖服务进程从哪个目录启动。

工作区不属于启动参数：新建会话时可在输入框上方选择最近使用的目录，或输入
服务器上已存在的目录；留空就使用默认工作区。工作区保存到会话中，后续多轮的
文件、Shell 和 stdio MCP 始终使用同一个目录。Playwright 预设安装在私有
`runtime/mcp` 目录，不修改项目或全局 npm 依赖。EasyAgent 只安装和卸载
MCP 自己的固定版本包；Node.js、Python、Java 等宿主运行时由服务器管理员或
项目环境提供，页面只负责检测，不执行系统级安装或升级。

### 在 Linux 服务器启用 Codex Runtime

Codex Runtime 需要安装在运行 EasyAgent 的服务器上。服务器只安装 Codex CLI，
不需要安装 ChatGPT Desktop；`codex app-server` 是 CLI 自带的子命令。Ubuntu
或其他支持的 Linux 主机可按 Codex 官方安装说明执行：

```bash
curl -fsSL https://chatgpt.com/codex/install.sh | sh
codex --version
codex app-server --help
```

安装完成后进入 EasyAgent「设置 → 执行引擎」，页面会自动检测；也可以直接点击
「在服务器安装 Codex CLI」。进入「模型配置」编辑 Codex 配置，即可填写 Provider、
Base URL、默认模型、推理强度和 API Key。API Key 不要写入 `env_key`：`env_key`
只填写变量名，例如 `GROQ_API_KEY`；EasyAgent 会把密钥保存到当前服务用户的
私有文件，并在启动 app-server 时注入。EasyAgent 不会把本机 ChatGPT Desktop
的登录状态复制到远程服务器。若服务器是多人共享部署，需要明确使用一个服务账号，
或为每个用户隔离 Unix 用户、HOME 和 Codex 配置；不要把某个用户的 `~/.codex`
凭据复制给所有用户。

「设置 → 模型配置」支持为 EasyAgent 和 Codex 分别保存多套配置。设置页保存的是当前
profile；首页新会话可以选择同一 Runtime 下的 profile。会话创建后会固定它的
profile，之后修改或删除其他 profile 不会改变已有会话；仍被会话使用的 profile
不能删除。

## 三种扩展

| 能力 | 适合放什么 | 如何使用 |
| --- | --- | --- |
| Tool | 高频、确定性的本机操作 | 首轮常驻少量核心工具，其余按需加载并调用 |
| Skill | 任务方法、团队规范和领域经验 | 页面编辑，模型按需读取 |
| MCP | GitHub、浏览器、数据库等外部系统 | 页面配置，模型按需连接 |

<details>
<summary>免费 OpenAI-compatible LLM Provider</summary>

下面 4 个 Provider 都可以接入 EasyAgent，但“免费”不等于无限量，也不代表免费政策永久不变。模型名单、速率限制和账号资格应以各平台当前控制台为准。

| Provider | 地区与定位 | 当前可用的免费模型示例 | 免费额度 / 限制 | OpenAI-compatible 接入 | Agent / Tool Calling 备注 |
| --- | --- | --- | --- | --- | --- |
| [智谱 BigModel](https://docs.bigmodel.cn/cn/guide/models/free/glm-4.7-flash) | 中国大陆；智谱 GLM 官方模型平台 | `glm-4.7-flash` | 官方价格页当前标记输入、输出均免费；具体并发和速率以账号权益为准 | `https://open.bigmodel.cn/api/paas/v4` | `glm-4.7-flash` 官方支持 Function Calling、MCP、结构化输出，适合作为国内主力 |
| [SiliconFlow 硅基流动](https://siliconflow.cn/pricing) | 中国大陆；开源模型托管与聚合平台 | 当前价格页中标记为 ¥0 的模型，例如 `THUDM/GLM-Z1-9B-0414`、`tencent/Hunyuan-MT-7B` | 免费模型名单动态调整；按账号和模型限流，Qwen / DeepSeek 等大模型通常不是免费模型 | `https://api.siliconflow.cn/v1` | 支持 OpenAI SDK 和 Function Calling；具体免费模型是否支持 tools 要以模型详情页为准 |
| [Groq](https://console.groq.com/docs/rate-limits) | 海外；使用 LPU 提供高速推理 | `openai/gpt-oss-120b`、`openai/gpt-oss-20b` | Free Plan 当前对 `gpt-oss-120b` 约为 30 RPM、1,000 RPD、8K TPM、200K TPD | `https://api.groq.com/openai/v1` | 官方支持 Function Calling、并行工具调用和结构化输出；适合作为高速 Agent Provider |
| [Cerebras Inference](https://inference-docs.cerebras.ai/resources/openai) | 海外；使用 Cerebras 芯片提供高速推理 | `gpt-oss-120b` | Free 层当前约为 5 RPM、30K TPM、1M TPH、1M TPD；具体限制以账号页面为准 | `https://api.cerebras.ai/v1` | 兼容 OpenAI SDK，适合作为 Groq 的高速备用；Provider 特有参数不要写死到通用 Agent 逻辑 |

推荐的 fallback 顺序：智谱 `glm-4.7-flash` → SiliconFlow 当前 ¥0 模型 → Groq `openai/gpt-oss-120b` → Cerebras `gpt-oss-120b`。生产代码应对 429、超时、模型下线和网络不可达做重试、熔断和降级。

</details>

## 文档

- [设计说明：场景、术语、边界和伪代码](docs/design.md)
- [运行时细节：消息、上下文、Tool、Skill、MCP 和 Trace](docs/agent-runtime.md)
- [工程复盘：遇到的问题、错误方案、修正与 Review 清单](docs/engineering-notes.md)
- [安全说明](SECURITY.md)
- [参与贡献](CONTRIBUTING.md)

## 当前边界

EasyAgent 目前是单机、单进程、SQLite 应用，没有登录、RBAC 或多租户隔离，请勿直接暴露到公网。Agent 的效果取决于所选模型、上下文和可用能力。

## 许可证

[MIT](LICENSE)
