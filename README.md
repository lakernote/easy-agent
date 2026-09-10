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

请在运行 EasyAgent 的服务器终端中手动安装 Codex CLI：

```bash
curl -fsSL https://chatgpt.com/codex/install.sh | sh
```

安装命令会下载并执行 OpenAI 官方安装脚本，请在执行前核对[官方安装说明](https://developers.openai.com/codex/cli)。EasyAgent 只检测 Codex CLI，不会下载安装或执行该脚本。`app-server` 是 Codex CLI 自带的子命令，不需要单独安装。两个 Runtime 共用任务队列、项目目录、worktree、Skills 和 MCP；设置中心的 Runtime 权限支持只读、工作区写入、完全访问和自定义模式，默认仍为完全访问以兼容已有安装。

无论选择哪个 Runtime，都使用相同的项目和任务系统：

- 项目可包含多个服务器源文件夹，第一个目录作为默认工作目录。
- 支持排队、暂停、继续、停止和重启恢复；默认并发 4、单轮最长 12 小时，均可在设置中调整。
- Git 项目可按会话创建 worktree；源仓库有未提交修改时不会自动隔离。Codex 会话还支持 thread 继续、读取和分支。

<p align="center">
  <img src="docs/images/model-and-tools.png" alt="EasyAgent 运行时与模型设置" width="920" />
</p>

## 内置能力

内置 Skills 聚焦项目理解、问题分析、代码审查、API 设计、测试与 E2E、事故 RCA、发布工程、文档维护、Git worktree 和网页研究。GitHub、Context7、Playwright、OpenAI Docs 等 MCP 可在设置页启用，供两个 Runtime 共用。

Skills 和大型工具组按需加载，减少无关上下文。网页研究会先发现候选，再读取原始来源后回答。

`web_research` 是模型唯一可见的联网入口。模型根据语义填写数据类型、查询对象、时间范围和研究深度；复杂问题还可给出 2–4 条互补检索式。Runtime 负责限制查询预算，并执行结构化数据读取、多源搜索、安全抓取、去重和引用整理。Tavily、SearXNG、Brave Search、Firecrawl、Reader 与 GitHub Token 可在 **设置 → 工具与 MCP → Web Research** 配置并执行真实连接测试，保存后下一轮立即生效；也可以继续使用环境变量部署。

### GitHub、GitLab 与 Git 凭据

- 查询公开 GitHub 仓库指标不要求登录。若要提高 `web_research` 的 GitHub API 限额，可在 **设置 → 工具与 MCP → Web Research** 填写 GitHub Token，或为服务进程设置 `GITHUB_TOKEN` / `GH_TOKEN`。页面配置优先于环境变量，保存后下一轮立即生效。
- 访问私有仓库、Issue、Pull Request 和 Actions，优先到 **设置 → 工具与 MCP → GitHub** 配置官方 GitHub MCP。Bearer Token 保存在 `~/.easyagent/easyagent.db`，接口和页面只返回已配置状态，不回传明文；数据库本身不是独立的密钥保险库，应继续依赖目录权限、磁盘加密和低权限服务账号。
- 本地 clone、diff、commit 等操作直接使用服务器的 `git`。HTTPS 凭据、SSH Key 和 `gh`/`glab` 登录态属于运行 EasyAgent 的系统账号，不属于模型配置；请以同一个服务账号执行 `gh auth login` 或 `glab auth login`。GitHub/GitLab CLI 未安装时，EasyAgent 不会自动安装。
- GitLab 官方远端 MCP 使用 OAuth 动态注册；EasyAgent 当前还没有这套交互式 OAuth 流程。可先使用公开网页研究、本地 `git`，或安装新版 `glab` 后把 `glab mcp serve` 配成自定义 stdio MCP。GitLab CLI MCP 仍是实验能力，生产环境应固定版本并限制权限。

Shell、Codex 和 stdio MCP 继承服务账号的系统权限，服务进程环境变量也会传给子进程。不要使用个人全权限 PAT；生产环境应使用专用账号、最小权限和可轮换凭据。

<p align="center">
  <img src="docs/images/skills.png" alt="EasyAgent Skills 能力库" width="920" />
</p>

## 微信远程

- 支持多人扫码绑定，并为每个人选择新会话的默认项目。
- 文字、图片、PDF、代码文件和带微信文字的语音进入与 Web 相同的任务队列；语音没有文字时提示补发说明，不下载音频，也不运行语音识别。
- “新会话”“状态”“停止”“项目列表”等控制指令不调用模型；微信回传状态和结果，完整 Trace 保留在 Web。

## 部署前注意

- 默认监听所有网卡，请立即修改默认密码；不要直接暴露到公网，建议使用防火墙、VPN 和 HTTPS。
- 当前是单机团队共享模式：一个管理员账号、一个 SQLite 数据库，不提供 RBAC 或多租户隔离。
- Shell、Codex 和 stdio MCP 使用 EasyAgent 服务进程的系统权限运行；建议使用专用的低权限账号。
- 只有任务需要调用 Git、Python、Node.js 等命令时，服务器才需要安装对应工具。

## License

[MIT](LICENSE)
