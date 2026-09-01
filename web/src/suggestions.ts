export type StarterSuggestion = { category: string; title: string; prompt: string; attachment?: boolean }
export const starterSuggestions: StarterSuggestion[] = [
  { category: '实时查询', title: '今天星期几？', prompt: '今天星期几？请使用可用工具核验后回答。' },
  { category: '精确计算', title: '计算一个表达式', prompt: '请使用计算工具算出 (128*35+640)/8，并给出结果。' },
  { category: '本机操作', title: '看看当前工作目录', prompt: '请使用 shell 查看当前工作目录，并根据目录内容概括这是个什么项目。' },
  { category: '联网研究', title: '查找最新公开资料', prompt: '请搜索 EasyAgent 的 GitHub 仓库，概括它当前的定位、主要能力并附上来源。' },
  { category: '代码实现', title: '写一个最小 HTTP API', prompt: '请用 Go 设计一个带 /health 健康检查的最小 HTTP API，并解释如何运行。' },
  { category: '故障排查', title: '分析连接失败', prompt: '请分析这个错误的可能根因并给出排查顺序：dial tcp 127.0.0.1:5432: connect: connection refused' },
  { category: '文件理解', title: '总结 PDF 或代码文件', prompt: '请阅读我上传的文件，先概括核心内容，再提取风险、结论和待办事项。', attachment: true },
  { category: '图片分析', title: '分析报错截图', prompt: '请读取我上传的截图，提取其中的错误信息，分析最可能的原因并给出解决步骤。', attachment: true },
]
