import type { Components } from 'react-markdown'

// 对话中的链接不能替换正在运行的工作台页面。统一在 Markdown 渲染层打开
// 新 Tab，确保普通回答、流式回答和数学公式回答保持一致。
export const markdownComponents: Components = {
  a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer" />,
}
