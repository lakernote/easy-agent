export type StarterSuggestion = { category: string; title: string; prompt: string; attachment?: boolean }
export const starterSuggestions: StarterSuggestion[] = [
  { category: '理解项目', title: '梳理当前项目与运行方式', prompt: '请先读取当前项目的 README、目录结构和关键配置，概括它解决的问题、技术栈、启动方式与主要风险。' },
  { category: '代码审查', title: '审查当前未提交改动', prompt: '@skill:code-review 请审查当前工作区未提交改动，优先找真实缺陷、回归风险和缺少的测试；只报告有证据的问题。' },
  { category: '测试验证', title: '运行测试并定位失败', prompt: '@skill:test-and-e2e 请识别项目的测试入口，运行与当前改动最相关的测试；如果失败，定位根因并提出最小修复方案。' },
  { category: '故障排查', title: '从日志定位服务异常', prompt: '@skill:problem-analysis 请根据我接下来提供的日志或错误信息建立候选根因，执行最有信息量的检查，并给出证据和处理建议。' },
  { category: '发布检查', title: '检查项目是否可以发布', prompt: '@skill:release-engineering 请检查当前项目的 Git 状态、测试、构建和发布流程，列出阻塞发布的问题；不要创建 Tag 或发布版本。' },
  { category: '文档维护', title: '让 README 与代码保持一致', prompt: '@skill:docs-maintenance 请根据当前代码和配置检查 README，删除过时或重复内容，并补齐用户真正需要的启动与使用说明。' },
]
