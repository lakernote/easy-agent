<div align="center">
  <img src="web/public/logo.svg" alt="EasyAgent Logo" width="88" />
  <h1>EasyAgent</h1>
  <p>面向研发、测试和运维团队的私有远程 AI Agent 工作台。</p>
  <p><strong>部署在自己的服务器，通过浏览器或个人微信下发任务；支持任务队列、停止、Trace、会话恢复与 Git worktree 隔离。</strong></p>
</div>

## 解决什么问题

- **随时远程下发任务**：通过浏览器或个人微信，把代码修改、测试、发布检查和故障排查交给自己的服务器执行。
- **团队共享一套能力**：成员通过浏览器共用模型、项目目录、Skills、MCP 和任务记录。
- **多任务安全并发**：干净的 Git 项目按会话创建 worktree；共享目录自动互斥，避免同时写坏文件。
- **服务重启不丢状态**：队列和任务状态持久化，重启后恢复待执行任务，并明确标记被中断的任务。
- **执行过程可追溯**：SSE 实时展示模型、工具、Token、缓存、耗时和错误，断线后按事件序号续传。

## 适合的工作流

| 角色 | 常见任务 |
| --- | --- |
| 研发 | 理解项目、实现需求、代码审查、API 设计、依赖文档查询 |
| 测试 | 生成测试、执行回归、浏览器 E2E、定位失败与验证修复 |
| 运维 | 分析日志、排查服务异常、形成事故 RCA、检查发布风险 |

## 核心能力

- 单个 Go 二进制内置 Web UI 和 SQLite，默认监听 `0.0.0.0:8080`。
- EasyAgent Runtime 支持 OpenAI、Ollama 和 OpenAI-compatible 模型；Codex Runtime 通过 Codex CLI 自带的 `app-server` 执行 Codex thread。
- 默认并发 4 个任务、单轮最长 12 小时；支持排队、暂停、继续、停止和重启恢复。
- 支持 Codex thread 列表、详情、继续和分支；分支可复用目录或创建独立 worktree。
- Skills 和 MCP 只配置一次，同时供两个 Runtime 使用；大能力按需加载，减少无关上下文。
- 网页查询采用“发现候选 → 读取原始来源 → 回答”的证据链；模型只看搜索摘要就结束时会获得一次有界纠偏。
- 可选微信 ClawBot 通道，支持多人扫码绑定、中文快捷指令、进度查询和结果回传；完整 Trace 只保留在 Web 工作台。
- 微信的“新会话”“状态”“停止”等控制命令由服务端直接处理，不消耗模型调用；普通消息进入与 Web 相同的任务队列、worktree 和 Runtime。

### 内置 Skills

- **理解与研发**：`project-onboarding`、`problem-analysis`、`code-review`、`api-design`
- **测试与验证**：`test-and-e2e`、`browser-validation`
- **运维与交付**：`incident-rca`、`release-engineering`、`docs-maintenance`
- **协作与研究**：`git-worktree-workflow`、`web-research`

### MCP 核心预设

| MCP | 用途 | 启用要求 |
| --- | --- | --- |
| GitHub | 仓库、Issue、Pull Request、Actions | PAT 或 App Token |
| Context7 | 最新依赖库与框架文档 | 可直接连接，API Key 可选 |
| Playwright | 浏览器复现与 E2E 验证 | Node.js 20+，页面一键安装 |
| OpenAI Docs | OpenAI API 与 Codex 官方文档 | 可直接连接 |

EasyAgent Runtime 首轮提供当前时间、Shell、只读文件检索、网页搜索和原始来源读取，文件写入、Skill、天气与计算按需加载；Codex Runtime 保留自己的原生工具。默认 MCP 聚焦代码、依赖文档和浏览器验证，不接入与研发交付无关的办公服务。

<p align="center">
  <img src="docs/images/conversation.png" alt="EasyAgent 对话工作区" width="920" />
</p>

## 快速下载

打开 [最新版本下载页](https://github.com/lakernote/easy-agent/releases/latest)，根据系统和架构选择文件：

| 系统 | x64 / Intel | ARM64 / Apple Silicon |
| --- | --- | --- |
| Windows | `easyagent_*_windows_amd64.zip` | `easyagent_*_windows_arm64.zip` |
| macOS | `easyagent_*_darwin_amd64.tar.gz` | `easyagent_*_darwin_arm64.tar.gz` |
| Linux | `easyagent_*_linux_amd64.tar.gz` | `easyagent_*_linux_arm64.tar.gz` |

[查看全部 Releases](https://github.com/lakernote/easy-agent/releases) · [最新版 SHA-256 校验文件](https://github.com/lakernote/easy-agent/releases/latest/download/checksums.txt)

下载并解压后运行：

- Windows：双击或执行 `easyagent.exe`
- macOS / Linux：执行 `./easyagent`

macOS/Windows 发布包暂未代码签名；如果系统首次拦截，请在系统安全设置中确认运行。

服务默认监听 `0.0.0.0:8080`。启动后访问 <http://127.0.0.1:8080>；部署在服务器时，将地址换成服务器 IP。

首次登录用户名和密码均为 `admin`。登录后先到 **设置 → 账户安全** 修改密码，再到 **设置 → 模型配置** 添加模型。

### Linux x64 一键启动

进入准备存放 EasyAgent 的空目录，执行：

```bash
(
set -euo pipefail
release_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' \
  https://github.com/lakernote/easy-agent/releases/latest)"
tag="${release_url##*/}"
version="${tag#v}"
curl -fL "https://github.com/lakernote/easy-agent/releases/download/${tag}/easyagent_${version}_linux_amd64.tar.gz" \
  | tar -xz --strip-components=1
nohup ./easyagent >easyagent.log 2>&1 </dev/null &
printf '%s\n' "$!" >easyagent.pid
)
```

- 查看日志：`tail -f easyagent.log`
- 停止服务：`kill "$(cat easyagent.pid)"`

发布包已包含可执行权限，不需要额外运行 `chmod +x`。程序、日志和 PID 文件位于当前目录；数据库和默认工作区保存在 `~/.easyagent/`。

## Runtime 怎么选

| Runtime | 适合场景 | 额外要求 |
| --- | --- | --- |
| EasyAgent Runtime | 可控的模型接入、内置工具与团队自定义 Agent | 无，配置模型即可 |
| Codex Runtime | 代码任务、Codex thread、原生 Codex 工具 | 服务器需要安装 Codex CLI |

安装 Codex CLI：

```bash
curl -fsSL https://chatgpt.com/codex/install.sh | sh
```

`app-server` 已包含在 Codex CLI 中，无需单独安装。EasyAgent 默认以完全访问模式运行 Codex 任务，因此 Codex 可以使用 EasyAgent 服务账号有权访问的文件和命令。

新建会话时可在输入框上方选择服务器项目目录。EasyAgent Runtime 与 Codex Runtime 使用同一套调度和 worktree 规则：Git worktree 之间可以并行，共享同一目录的任务会排队串行。为避免遗漏本地改动，源目录存在未提交文件时不会自动创建 worktree。

## 使用前注意

- 默认监听所有网卡，首次登录密码为 `admin`；服务器部署后请立即修改密码。
- 不要直接暴露到公网。建议使用防火墙或 VPN 限制来源，并通过反向代理启用 HTTPS。
- 当前定位是单机共享服务：一个管理员账号、一个 SQLite 数据库，不提供 RBAC 或多租户隔离。
- Shell 和 stdio MCP 使用服务进程权限运行，不是安全沙箱；建议使用低权限系统账号。
- 发布包无需 Go、Node.js 或系统 SQLite。只有任务调用 Git、Python 等命令时，服务器才需要安装对应程序。

## License

[MIT](LICENSE)
