export type StarterSuggestion = { category: string; title: string; prompt: string; attachment?: boolean }
export const starterSuggestions: StarterSuggestion[] = [
  { category: '天气查询', title: '查询合肥今天和明天天气', prompt: '查询合肥今天和明天的天气，简要说明温度、降雨概率和出行建议。' },
  { category: 'GitHub', title: 'EasyAgent 当前有多少 Star', prompt: '查询 GitHub 仓库 lakernote/easy-agent 当前的 Star 数量，并提供数据来源与查询时间。' },
  { category: '理解项目', title: '梳理结构、依赖与启动方式', prompt: '请读取当前项目的 README、目录结构和关键配置，概括它解决的问题、技术栈、启动方式与主要风险。' },
  { category: '代码审查', title: '检查改动与潜在回归风险', prompt: '请审查当前工作区未提交的改动，优先找真实缺陷、回归风险和缺少的测试，并给出有证据的结论。' },
  { category: '测试验证', title: '运行相关测试并修复失败', prompt: '请识别项目的测试入口，运行与当前改动最相关的测试；如果失败，定位根因并完成最小修复。' },
  { category: '故障排查', title: '分析日志并定位服务异常', prompt: '请根据下面的日志或错误信息建立候选根因，执行最有信息量的检查，并给出证据和处理建议：' },
]
