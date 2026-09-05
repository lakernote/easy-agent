<div align="center">
  <img src="web/public/logo.svg" alt="EasyAgent Logo" width="88" />
  <h1>EasyAgent</h1>
  <p><strong>部署在自己服务器上的远程 AI Agent 工作台。</strong></p>
  <p>通过浏览器或个人微信，把研发、测试和运维任务交给服务器持续执行。</p>
</div>

## EasyAgent 能做什么

| 场景 | 可以交给 Agent 的任务 |
| --- | --- |
| 研发 | 理解代码库、实现需求、修改文件、代码审查、API 设计、查询依赖文档 |
| 测试 | 编写测试、执行回归、浏览器 E2E、复现问题、验证修复结果 |
| 运维 | 检查服务和日志、定位故障、整理 RCA、核对发布流程与风险 |
| 远程协作 | 从浏览器或微信提交任务、查看状态、停止任务并接收结果 |

它主要解决这些问题：

- **任务不必绑在个人电脑上**：Agent 在团队服务器运行，关闭浏览器也可以继续执行。
- **多个任务可控并发**：默认同时运行 4 个任务；Git 项目可用 worktree 隔离，共享目录自动排队，避免互相覆盖。
- **长任务有记录、可恢复**：会话、队列和运行状态写入 SQLite；服务重启后恢复排队任务，并明确标记被中断的任务。
- **过程看得见**：通过 SSE 实时显示模型、Tool、Skill、MCP、Token、缓存、耗时和错误，网络重连后可继续 Trace。
- **团队共用一套能力**：模型配置、服务器项目、Skills 和 MCP 统一管理，同时提供给 EasyAgent 与 Codex Runtime。

<p align="center">
  <img src="docs/images/conversation.png" alt="EasyAgent 对话工作区" width="920" />
</p>

## 快速体验

前往 [Releases](https://github.com/lakernote/easy-agent/releases/latest)，按系统和架构下载：

| 系统 | x64 / Intel | ARM64 / Apple Silicon |
| --- | --- | --- |
| Windows | `easyagent_*_windows_amd64.zip` | `easyagent_*_windows_arm64.zip` |
| macOS | `easyagent_*_darwin_amd64.tar.gz` | `easyagent_*_darwin_arm64.tar.gz` |
| Linux | `easyagent_*_linux_amd64.tar.gz` | `easyagent_*_linux_arm64.tar.gz` |

解压后运行 `easyagent.exe`（Windows）或 `./easyagent`（macOS/Linux）。发布包是包含 Web UI 的单个二进制，不需要安装 Go、Node.js 或 SQLite。

服务默认监听 `0.0.0.0:8080`。启动后访问 `http://服务器IP:8080`，使用默认账号 `admin / admin` 登录，并立即在 **设置 → 账户安全** 修改密码，然后到 **模型配置** 添加模型。

macOS 和 Windows 发布包暂未代码签名；如果首次运行被系统拦截，请在系统安全设置中确认。

### Linux x64 下载并后台启动

进入准备存放 EasyAgent 的目录后执行：

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

```bash
tail -f easyagent.log       # 查看日志
kill "$(cat easyagent.pid)" # 停止服务
```

发布包已带可执行权限。数据库、默认工作区和运行状态保存在 `~/.easyagent/`。

## 两种 Runtime

| Runtime | 适合场景 | 要求 |
| --- | --- | --- |
| EasyAgent | 使用 Ollama、OpenAI、Groq 等 OpenAI-compatible 模型和 EasyAgent 工具循环 | 在页面配置模型 |
| Codex | 使用 Codex thread、原生工具、Skill 和沙箱处理代码任务 | 服务器安装 Codex CLI |

安装 Codex CLI：

```bash
curl -fsSL https://chatgpt.com/codex/install.sh | sh
```

`app-server` 是 Codex CLI 自带的子命令，不需要单独安装。EasyAgent 默认以完全访问模式运行 Codex，两个 Runtime 共用任务队列、项目目录、worktree、Skills 和 MCP。

无论选择哪个 Runtime，都使用相同的项目和任务系统：

- 项目可包含多个服务器源文件夹，第一个目录作为默认工作目录。
- 支持排队、暂停、继续、停止和重启恢复；默认并发 4、单轮最长 12 小时，均可在设置中调整。
- Git 项目可按会话创建 worktree；源仓库有未提交修改时不会自动隔离。Codex 会话还支持 thread 继续、读取和分支。

## 内置能力

内置 Skills 聚焦项目理解、问题分析、代码审查、API 设计、测试与 E2E、事故 RCA、发布工程、文档维护、Git worktree 和网页研究。GitHub、Context7、Playwright、OpenAI Docs 等 MCP 可在设置页启用，供两个 Runtime 共用。

Skills 和大型工具组按需加载，减少无关上下文。网页研究会先发现候选，再读取原始来源后回答。

## 微信远程

- 支持多人扫码绑定，并为每个人选择新会话的默认项目。
- 文字、图片、PDF、代码文件和语音消息进入与 Web 相同的任务队列；无语音文本时保存音频并提示补发说明，不额外运行语音识别。
- “新会话”“状态”“停止”“项目列表”等控制指令不调用模型；微信回传状态和结果，完整 Trace 保留在 Web。

## 部署前注意

- 默认监听所有网卡，请立即修改默认密码；不要直接暴露到公网，建议使用防火墙、VPN 和 HTTPS。
- 当前是单机团队共享模式：一个管理员账号、一个 SQLite 数据库，不提供 RBAC 或多租户隔离。
- Shell、Codex 和 stdio MCP 使用 EasyAgent 服务进程的系统权限运行；建议使用专用的低权限账号。
- 只有任务需要调用 Git、Python、Node.js 等命令时，服务器才需要安装对应工具。

## License

[MIT](LICENSE) · [Third-party notices](THIRD_PARTY_NOTICES.md)
