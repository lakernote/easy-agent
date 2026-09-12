import { lazy, Suspense } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import type { Session } from '../types'
import { formatBytes } from '../attachments'
import { FileIcon, Logo } from '../ui'
import { markdownComponents } from '../markdownComponents'
import { remarkRepairModelMarkdown } from '../remarkRepairModelMarkdown'
import { Payload } from './Payload'
import { capabilityResultLabel, describeToolCall, SelectedCapabilities } from './CapabilityActivity'

const MathMarkdown = lazy(() => import('../MathMarkdown'))
const hasMath = (value: string) => /\$\$[\s\S]+?\$\$|\$[^$\n]+?\$/.test(value)

export function MessageView({ message, relatedCall, researchCitations = [] }: { message: Session['messages'][number]; relatedCall?: Session['messages'][number]['toolCalls'][number]; researchCitations?: ResearchCitation[] }) {
	if (message.role === 'tool') return <details className={`tool-result ${relatedCall ? describeToolCall(relatedCall).kind : ''}`}><summary><span>⌁</span>{capabilityResultLabel(relatedCall, message.name || '工具')}</summary><ToolResult name={message.name || ''} value={message.content || ''} result={message.toolResult} /></details>
  if (message.role === 'user') return <div className="user-row"><div className="user-message">{message.attachments?.length > 0 && <MessageAttachments attachments={message.attachments} />}<SelectedCapabilities message={message} />{message.content && <div>{message.content}</div>}</div></div>
  if (message.role !== 'assistant') return null
  return <div className="assistant-row"><Avatar /><div className="assistant-message">{message.toolCalls?.length > 0 && <div className="tool-intent">{message.toolCalls.map((call) => { const item = describeToolCall(call); return <span className={item.kind} key={call.id}><b>{item.label}</b>{item.name}</span> })}</div>}{message.content && <div className="answer-text"><Markdown researchCitations={researchCitations}>{message.content}</Markdown></div>}</div></div>
}

type ResearchSource = { id?: string; title?: string; url?: string; domain?: string; provider?: string; kind?: string; content?: string; discovered_by?: string[] }
type ResearchResult = { ok?: boolean; depth?: string; source_scope?: string; domain_constraints?: string[]; evidence_status?: string; independent_domain_count?: number; source_count?: number; sources?: ResearchSource[]; limitations?: string[]; executed_search_queries?: string[] }
export type ResearchCitation = { id: string; url: string }

// web_research 的 S1/S2 只在单个用户轮次内有效。模型偶尔会保留编号却漏掉
// Markdown URL，因此页面从真实 Tool 结果确定性地补上引用定义；不修改会话原文，
// 也不会把上一个用户轮次的来源错误地带到下一轮。同一轮多次调用若复用了
// 相同编号但 URL 不同，则放弃该编号的自动补链，避免把引用链到错误来源。
export function collectResearchCitations(messages: Session['messages']) {
  const byMessageID = new Map<number, ResearchCitation[]>()
  let active = new Map<string, string | null>()
  for (const message of messages) {
    if (message.role === 'user') {
      active = new Map()
      continue
    }
    if (message.role === 'tool' && message.name === 'web_research') {
      for (const citation of parseResearchCitations(message.content || '')) {
        const previous = active.get(citation.id)
        if (previous === undefined) active.set(citation.id, citation.url)
        else if (previous !== citation.url) active.set(citation.id, null)
      }
      continue
    }
    if (message.role === 'assistant' && message.content && active.size > 0) byMessageID.set(message.id, unambiguousResearchCitations(active))
  }
  return { byMessageID, active: unambiguousResearchCitations(active) }
}

function unambiguousResearchCitations(values: Map<string, string | null>): ResearchCitation[] {
  return Array.from(values.entries()).flatMap(([id, url]) => url ? [{ id, url }] : [])
}

function parseResearchCitations(value: string): ResearchCitation[] {
  let research: ResearchResult
  try { research = JSON.parse(value) as ResearchResult } catch { return [] }
  if (!research.ok || !Array.isArray(research.sources)) return []
  const seen = new Set<string>()
  return research.sources.flatMap((source, index) => {
    const id = String(source.id || `S${index + 1}`).toUpperCase()
    const url = normalizedResearchURL(source.url)
    if (!/^S\d+$/.test(id) || !url || seen.has(id)) return []
    seen.add(id)
    return [{ id, url }]
  })
}

function ToolResult({ name, value, result }: { name: string; value: string; result?: Session['messages'][number]['toolResult'] }) {
  if (name !== 'web_research') return result ? <TypedToolResult result={result} fallback={value} /> : <Payload value={value} />
  let research: ResearchResult
  try { research = JSON.parse(value) as ResearchResult } catch { return <Payload value={value} /> }
  if (!research.ok || !Array.isArray(research.sources)) return <Payload value={value} />
  return <div className="research-result">
    <div className="research-result-head"><strong>{research.source_count ?? research.sources.length} 个已读取来源</strong><span>{research.depth || 'normal'} · {researchScopeLabel(research)}{researchEvidenceLabel(research)}{Array.isArray(research.executed_search_queries) ? ` · ${research.executed_search_queries.length} 条检索式` : ''} · S=来源编号</span></div>
    <div className="research-source-list">{research.sources.map((source, index) => <details className="research-source" key={`${source.id || index}-${source.url || ''}`}>
      <summary><b title="本次研究的来源编号，不代表质量或可信度排名">{source.id || `S${index + 1}`}</b><span>{source.title || source.domain || '来源'}</span><small>{source.provider || source.kind || ''}</small></summary>
      {safeResearchLink(source.url) && <a href={source.url} target="_blank" rel="noopener noreferrer">{source.domain || source.url}</a>}
      {Array.isArray(source.discovered_by) && source.discovered_by.length > 0 && <p className="research-source-query">发现于：{source.discovered_by.join(' · ')}</p>}
      {source.content && <pre>{source.content}</pre>}
    </details>)}</div>
    {Array.isArray(research.limitations) && research.limitations.length > 0 && <p className="research-limitations">{research.limitations.join('；')}</p>}
  </div>
}

type TypedResult = NonNullable<Session['messages'][number]['toolResult']>

function TypedToolResult({ result, fallback }: { result: TypedResult; fallback: string }) {
  const blocks = Array.isArray(result.content) ? result.content : []
  const hasStructured = Object.prototype.hasOwnProperty.call(result, 'structuredContent')
  return <div className="typed-tool-result">
    {result.error && <div className="tool-result-error"><strong>{result.error.code || 'tool_error'}</strong><span>{result.error.message}</span>{result.error.hint && <small>{result.error.hint}</small>}</div>}
    {result.truncation && <div className="tool-result-truncation">模型上下文已按 {result.truncation.strategy} 压缩：保留 {result.truncation.retainedTokens} / {result.truncation.originalTokens} Token</div>}
    {hasStructured && <Payload value={JSON.stringify(result.structuredContent)} />}
    {blocks.map((block, index) => <ToolResultBlock block={block} key={`${block.artifactId || block.uri || block.name || block.type}-${index}`} />)}
    {!hasStructured && blocks.length === 0 && !result.error && <Payload value={fallback} />}
  </div>
}

function ToolResultBlock({ block }: { block: NonNullable<TypedResult['content']>[number] }) {
  if (block.type === 'text') return <Payload value={block.text || ''} />
  if (block.type === 'json') return <Payload value={JSON.stringify(block.json)} />
  if (block.artifactId) {
    const source = `/api/v1/attachments/${encodeURIComponent(block.artifactId)}`
    if (block.type === 'image') return <figure className="tool-result-artifact"><img src={source} alt={block.name || '工具返回图片'} loading="lazy" /><figcaption>{block.name || block.mimeType || '图片'}</figcaption></figure>
    if (block.type === 'audio') return <div className="tool-result-artifact"><audio controls preload="none" src={source} /><span>{block.name || block.mimeType || '音频'}</span></div>
    return <a className="tool-result-artifact-link" href={source} target="_blank" rel="noopener noreferrer">{block.name || block.mimeType || '打开工具产物'}</a>
  }
  if (block.uri && normalizedResearchURL(block.uri)) return <a href={block.uri} target="_blank" rel="noopener noreferrer">{block.name || block.uri}</a>
  return <Payload value={JSON.stringify(block)} />
}

function researchScopeLabel(research: ResearchResult) {
  if (Array.isArray(research.domain_constraints) && research.domain_constraints.length > 0) return `限定 ${research.domain_constraints.join(', ')} · `
  if (research.source_scope === 'official') return '官方范围 · '
  return ''
}

function researchEvidenceLabel(research: ResearchResult) {
  if (research.evidence_status === 'multiple_independent_sources_retrieved') return `${research.independent_domain_count || 2} 个网站域名`
  if (research.evidence_status === 'multiple_sources_same_domain') return '同站多页面'
  if (research.evidence_status === 'multiple_sources_retrieved') return '多源证据'
  return '单一来源'
}

function safeResearchLink(value?: string) { return normalizedResearchURL(value) !== '' }

function normalizedResearchURL(value?: string) {
  if (!value) return ''
  try {
    const parsed = new URL(value)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.toString() : ''
  } catch {
    return ''
  }
}

function MessageAttachments({ attachments }: { attachments: Session['messages'][number]['attachments'] }) {
  return <div className="message-attachments">{attachments.map((attachment) => {
    const source = `/api/v1/attachments/${encodeURIComponent(attachment.id)}`
    if (attachment.kind === 'image') return <a key={attachment.id} className="message-image" href={source} target="_blank" rel="noreferrer" title={`查看 ${attachment.name}`}><img src={source} alt={attachment.name} loading="lazy" /><span>{attachment.name}</span></a>
    if (attachment.kind === 'audio') return <div key={attachment.id} className="message-audio"><div><strong>{attachment.name}</strong><small>微信语音 · {formatBytes(attachment.size)}</small></div><audio controls preload="metadata" src={source}>浏览器不支持播放音频。</audio></div>
    return <a key={attachment.id} className="message-file" href={source} target="_blank" rel="noreferrer" download={attachment.name}><FileIcon /><span><strong>{attachment.name}</strong><small>{attachment.kind === 'pdf' ? 'PDF' : '文本文件'} · {formatBytes(attachment.size)}</small></span></a>
  })}</div>
}

export function Avatar() { return <div className="avatar"><Logo /></div> }

export function Markdown({ children, researchCitations = [] }: { children: string; researchCitations?: ResearchCitation[] }) {
  const content = addResearchCitationDefinitions(children, researchCitations)
  if (hasMath(content)) return <Suspense fallback={<ReactMarkdown remarkPlugins={[remarkGfm, remarkMath, remarkRepairModelMarkdown]} components={markdownComponents}>{content}</ReactMarkdown>}><MathMarkdown>{content}</MathMarkdown></Suspense>
  return <ReactMarkdown remarkPlugins={[remarkGfm, remarkRepairModelMarkdown]} components={markdownComponents}>{content}</ReactMarkdown>
}

function addResearchCitationDefinitions(content: string, citations: ResearchCitation[]) {
  const definitions = citations.flatMap((citation) => {
    const escapedID = citation.id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    if (!new RegExp(`\\[${escapedID}\\]`, 'i').test(content)) return []
    if (new RegExp(`^\\s*\\[${escapedID}\\]:`, 'im').test(content)) return []
    return [`[${citation.id}]: <${citation.url}>`]
  })
  return definitions.length > 0 ? `${content.trimEnd()}\n\n${definitions.join('\n')}` : content
}
