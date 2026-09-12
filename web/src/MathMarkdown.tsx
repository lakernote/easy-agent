import ReactMarkdown from 'react-markdown'
import rehypeKatex from 'rehype-katex'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import 'katex/dist/katex.min.css'
import { markdownComponents } from './markdownComponents'
import { remarkRepairModelMarkdown } from './remarkRepairModelMarkdown'

// KaTeX 渲染器和样式单独打包。轻量的公式语法解析器与 fallback 共享，只有
// 内容包含 $...$ 或 $$...$$ 时才动态加载体积较大的 KaTeX。
export default function MathMarkdown({ children }: { children: string }) {
  return <ReactMarkdown remarkPlugins={[remarkGfm, remarkMath, remarkRepairModelMarkdown]} rehypePlugins={[rehypeKatex]} components={markdownComponents}>{children}</ReactMarkdown>
}
