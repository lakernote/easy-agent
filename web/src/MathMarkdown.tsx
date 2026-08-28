import ReactMarkdown from 'react-markdown'
import rehypeKatex from 'rehype-katex'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import 'katex/dist/katex.min.css'

// 数学渲染单独打包。普通对话不会加载 KaTeX，只有内容包含 $...$ 或 $$...$$ 时
// App 才动态加载这个组件，避免为了低频公式增加首页体积。
export default function MathMarkdown({ children }: { children: string }) {
  return <ReactMarkdown remarkPlugins={[remarkGfm, remarkMath]} rehypePlugins={[rehypeKatex]}>{children}</ReactMarkdown>
}
