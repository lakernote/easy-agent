# Web research E2E — 2026-09-08

环境：本机 `http://localhost:8080`，EasyAgent Runtime，`qwen3:14b-16k`，Asia/Shanghai。测试通过真实浏览器创建会话，结果从 SQLite 会话记录复核。会话 ID 只记录前 8 位。

## 修复前基线（commit `cc23032`）

| 场景 | 会话 | Tool / 模型耗时 | 实际来源 | 结论 |
|---|---:|---:|---|---|
| 合肥今天和明天天气 | `9c2df481` | 2.7s / 99.5s | Open-Meteo 结构化来源 | 日期、温度、降水概率正确；模型在 6% 降水概率下自行添加“傍晚可能转阴、带雨具”，超出证据。 |
| GitHub 仓库指标 | `4e002c33` | 1.1s / 43.8s | GitHub 官方仓库页降级 | API 限流后读取到 star=2、fork=0；open issue 和更新时间缺失，模型正确声明缺失。 |
| Cisco 股票 | `85bed06b` | 0.7s / 53.1s | Yahoo Finance 结构化来源 | 数值正确；来源只给 `market_time_utc`，模型错误标成“美国东部时间”。 |
| “Laker 是谁”消歧 | `386aec5a` | 22.8s / 87.6s | 5 个网站、10 个候选抓取失败 | 能识别歧义，但来源排序混入低质量词典/内容站，引用了较弱的球队名称解释。 |
| Kafka 原理（只要官方） | `c69fae3c` | 2.7s / 123.1s | Kafka、Confluent、GitHub | 回答基本正确，但“只引用 Apache Kafka 官方文档”没有形成强约束，仍返回并引用 Confluent。 |
| 最近 7 天 Codex 官方更新 | `18be25f0` | 6.0s / 92.7s | 5 个非官方网站 | 模型传了 `domains=[openai.com]`，运行时却把它当排序偏好；结果完全越出指定域名。这是严重正确性问题。 |

基线结论：工具选择已经稳定（6/6 都调用一次 `web_research`），主要风险在来源约束、来源质量语义和结构化字段可复述性，不在 Ollama 是否会调用工具。

## 本轮修复目标

- `domains` 改为抓取前后均校验的硬白名单，包含重定向越域拒绝与候选补位。
- 增加显式 `source_scope=official`；未给官网域名时只保留实体域名和匹配实体的官方代码仓库候选。
- 配置型 Tavily / SearXNG / Brave 作为主搜索层；DuckDuckGo / Bing HTML 仅在候选不足时降级。
- 天气来源给出确定性的 `travel_advice`；行情同时给 UTC 与交易所当地时间；GitHub 网页降级补 open issue、创建时间和缺失字段列表。
- UI 明示 `S=来源编号`、域名/官方范围，并把“独立网站”改为更准确的“网站域名”。

## 修复后回归

| 场景 | 会话 | Tool / 模型耗时 | 实际来源 | 结论 |
|---|---:|---:|---|---|
| 合肥今天和明天天气 | `b1c0b32e` | 2.9s / 89.7s | Open-Meteo 结构化来源 | 日期、温度、降水概率正确；回答原样采用确定性 `travel_advice`，没有再把 6% 降水扩写成带雨具建议。 |
| GitHub 仓库指标 | `30e3293d` | 1.1s / 52.6s | GitHub 官方仓库页降级 | `domains=[github.com]` 生效；API 限流时仍读到 star=2、fork=0、open issue=0、创建时间，并明确 updated/pushed 缺失。 |
| Cisco 股票 | `48e2b889` | 1.4s / 77.7s | Yahoo Finance 结构化来源 | 正确区分 `NasdaqGS`、UTC 与 `America/New_York` 当地时间，并明确纽约时区不等于 NYSE。 |
| “Laker 是谁”消歧 | `76990639` | 10.6s / 89.6s | 5 个来源 | 不再认定唯一人物，区分普通词义与球队含义；随后加入精确拼写、meaning/biography 扩展和网站域名多样性排序，联网引擎回归可同时找到不同人物候选与词义来源。 |
| Kafka 原理（只要官方） | `8fdc49e8` | 2.2s / 112.2s | 2 个 Kafka 官方页面 | `domains=[kafka.apache.org]` 全程生效，5 个候选压缩为 2 个证据页；回答对未被正文覆盖的协作关系明确声明缺失，没有再声称消费者可从任意副本读取。 |
| 最近 7 天 Codex 官方更新 | `e358310a` | 3.6s / 103.7s | 5 个 OpenAI 域名页面 | 只返回 `openai.com` 及其子域，第三方候选全部排除；模型采用 `freshness=week` 并排除范围外社区事件。 |

真实浏览器回归之外，`EASYAGENT_LIVE_RESEARCH=1` 覆盖天气、GitHub、行情、人物与 Kafka 官方文档。零配置 HTML provider 在连续调用后曾触发 DuckDuckGo 人机验证；新增的白名单内 `sitemap.xml` 降级仍成功恢复 Kafka 当前文档，并通过断言确认证据同时包含 `replica` 与 `offset`。这说明 HTML 搜索适合作为开发/降级路径，不应作为生产唯一 provider。

## 生产部署建议

1. 模型只看到一个 `web_research`；天气、行情、GitHub 等结构化 adapter 继续作为内部确定性数据源，不恢复多个模型可见工具。
2. 生产主 provider 优先 Tavily；有自托管和隐私要求时使用 SearXNG，Brave 作为第二 API 搜索源。DuckDuckGo/Bing HTML 仅做零配置和故障降级。
3. `EASYAGENT_READER_URL` 用于 PDF、正文提取失败和确实依赖 JavaScript 的公开页面。需要登录、点击、无限滚动或视觉核验时再接独立 browser/CDP worker；不要把 Playwright 当通用搜索后端。
4. 质量门槛持续记录：工具选择率、可读取来源率、官方域名遵从率、字段覆盖率、引用完整率、事实支持率、P50/P95 tool latency，以及 provider 降级原因。

已知限制：本地 `qwen3:14b-16k` 偶尔只输出 `[S1]` 而不复制 Markdown citation，且可能把常识补入有引用的句子。当前通过更强证据规则与“字段缺失就声明缺失”显著收敛，但真正的逐句语义蕴含校验需要单独的回答 verifier/eval，不应伪装成搜索 provider 能完全解决的问题。
