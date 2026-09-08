import { lazy, Suspense } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Session } from '../types'
import { formatTokens, historyModeLabel } from '../format'
import { formatBytes } from '../attachments'
import { FileIcon, Logo } from '../ui'
import { markdownComponents } from '../markdownComponents'
import { Payload } from './Payload'
import { capabilityResultLabel, describeToolCall, SelectedCapabilities } from './CapabilityActivity'

const MathMarkdown = lazy(() => import('../MathMarkdown'))
const hasMath = (value: string) => /\$\$[\s\S]+?\$\$|\$[^$\n]+?\$/.test(value)

export function MessageView({ message, relatedCall }: { message: Session['messages'][number]; relatedCall?: Session['messages'][number]['toolCalls'][number] }) {
	if (message.role === 'tool') return <details className={`tool-result ${relatedCall ? describeToolCall(relatedCall).kind : ''}`}><summary><span>⌁</span>{capabilityResultLabel(relatedCall, message.name || '工具')}</summary><ToolResult name={message.name || ''} value={message.content || ''} /></details>
  if (message.role === 'user') return <div className="user-row"><div className="user-message">{message.attachments?.length > 0 && <MessageAttachments attachments={message.attachments} />}<SelectedCapabilities message={message} />{message.content && <div>{message.content}</div>}</div></div>
  if (message.role !== 'assistant') return null
  return <div className="assistant-row"><Avatar /><div className="assistant-message">{message.toolCalls?.length > 0 && <div className="tool-intent">{message.toolCalls.map((call) => { const item = describeToolCall(call); return <span className={item.kind} key={call.id}><b>{item.label}</b>{item.name}</span> })}</div>}{message.content && <div className="answer-text"><Markdown>{message.content}</Markdown></div>}</div></div>
}

type ResearchSource = { id?: string; title?: string; url?: string; domain?: string; provider?: string; kind?: string; content?: string }
type ResearchResult = { ok?: boolean; depth?: string; evidence_status?: string; independent_domain_count?: number; source_count?: number; sources?: ResearchSource[]; limitations?: string[] }

function ToolResult({ name, value }: { name: string; value: string }) {
  if (name !== 'web_research') return <Payload value={value} />
  let research: ResearchResult
  try { research = JSON.parse(value) as ResearchResult } catch { return <Payload value={value} /> }
  if (!research.ok || !Array.isArray(research.sources)) return <Payload value={value} />
  return <div className="research-result">
    <div className="research-result-head"><strong>{research.source_count ?? research.sources.length} 个已读取来源</strong><span>{research.depth || 'normal'} · {researchEvidenceLabel(research)} · S=来源编号</span></div>
    <div className="research-source-list">{research.sources.map((source, index) => <details className="research-source" key={`${source.id || index}-${source.url || ''}`}>
      <summary><b title="本次研究的来源编号，不代表质量或可信度排名">{source.id || `S${index + 1}`}</b><span>{source.title || source.domain || '来源'}</span><small>{source.provider || source.kind || ''}</small></summary>
      {safeResearchLink(source.url) && <a href={source.url} target="_blank" rel="noopener noreferrer">{source.domain || source.url}</a>}
      {source.content && <pre>{source.content}</pre>}
    </details>)}</div>
    {Array.isArray(research.limitations) && research.limitations.length > 0 && <p className="research-limitations">{research.limitations.join('；')}</p>}
  </div>
}

function researchEvidenceLabel(research: ResearchResult) {
  if (research.evidence_status === 'multiple_independent_sources_retrieved') return `${research.independent_domain_count || 2} 个独立网站`
  if (research.evidence_status === 'multiple_sources_same_domain') return '同站多页面'
  if (research.evidence_status === 'multiple_sources_retrieved') return '多源证据'
  return '单一来源'
}

function safeResearchLink(value?: string) { return !!value && /^https?:\/\//i.test(value) }

function MessageAttachments({ attachments }: { attachments: Session['messages'][number]['attachments'] }) {
  return <div className="message-attachments">{attachments.map((attachment) => {
    const source = `/api/v1/attachments/${encodeURIComponent(attachment.id)}`
    if (attachment.kind === 'image') return <a key={attachment.id} className="message-image" href={source} target="_blank" rel="noreferrer" title={`查看 ${attachment.name}`}><img src={source} alt={attachment.name} loading="lazy" /><span>{attachment.name}</span></a>
    if (attachment.kind === 'audio') return <div key={attachment.id} className="message-audio"><div><strong>{attachment.name}</strong><small>微信语音 · {formatBytes(attachment.size)}</small></div><audio controls preload="metadata" src={source}>浏览器不支持播放音频。</audio></div>
    return <a key={attachment.id} className="message-file" href={source} target="_blank" rel="noreferrer" download={attachment.name}><FileIcon /><span><strong>{attachment.name}</strong><small>{attachment.kind === 'pdf' ? 'PDF' : '文本文件'} · {formatBytes(attachment.size)}</small></span></a>
  })}</div>
}

export function Avatar() { return <div className="avatar"><Logo /></div> }

export function Markdown({ children }: { children: string }) {
  if (hasMath(children)) return <Suspense fallback={<ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>{children}</ReactMarkdown>}><MathMarkdown>{children}</MathMarkdown></Suspense>
  return <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>{children}</ReactMarkdown>
}

export function ContextBar({ session }: { session: Session }) {
  const context = session.context
  const isCodexRuntime = session.runtime === 'codex'
  const tokenStatus = context.lastInputTokens > 0 ? formatTokens(context.lastInputTokens) : session.status === 'failed' ? '本轮未上报' : '等待模型上报'
  const cacheRate = context.cacheReported && context.lastInputTokens > 0 ? Math.round(context.lastCachedTokens / context.lastInputTokens * 100) : 0
  const utilization = context.contextWindowTokens > 0 && context.lastInputTokens > 0 ? Math.round(context.lastInputTokens / context.contextWindowTokens * 100) : 0
  const pressure = utilization >= 85 ? 'danger' : utilization >= 65 ? 'warning' : ''
  const isolation = session.workspaceIsolation || (session.worktreeBranch ? `Git worktree · ${session.worktreeBranch}` : '工作区互斥')
  return <details className={`context-bar ${pressure}`}>
    <summary><strong>上下文</strong><span>{tokenStatus}{context.contextWindowTokens > 0 ? ` / ${formatTokens(context.contextWindowTokens)}` : ''}</span><span>{context.userTurns} 个用户轮次 · {context.historyMessages} 条消息</span><span>{historyModeLabel(context.historyMode)}</span><span>{context.cacheReported ? `缓存 ${cacheRate}%` : isCodexRuntime ? 'Codex 缓存未上报' : '缓存未上报'}</span><span className="context-workspace" title={session.workspace}>工作区 {workspaceName(session.workspace)}</span><em>{context.compressionCount > 0 ? `已压缩 ${context.compressionCount} 次` : context.compressionMode === 'auto' ? `自动 ${context.compressionThresholdPercent}%` : '压缩停用'}</em></summary>
    <div className="context-details">
      <ContextDatum label="最近一次模型输入" value={context.lastInputTokens > 0 ? `${context.lastInputTokens.toLocaleString()} Token` : session.status === 'failed' ? '本轮 Token 未上报' : '尚无数据'} hint={context.contextWindowTokens > 0 ? `模型窗口 ${context.contextWindowTokens.toLocaleString()} · 使用 ${utilization}%` : '模型没有提供窗口上限，请在“模型与工具”中填写'} />
      <ContextDatum label="会话历史" value={`${context.userTurns} 个用户轮次 · ${context.historyMessages} 条消息`} hint={`最近请求发送 ${context.requestMessages || '—'} 条消息项 · ${context.toolDefinitions || 0} 个工具定义`} />
      <ContextDatum label="缓存" value={context.cacheReported ? `命中 ${context.lastCachedTokens.toLocaleString()} · ${cacheRate}%` : isCodexRuntime ? 'Codex 未提供' : 'Provider 未上报'} hint={context.cacheReported ? `本次写入 ${context.lastCacheWriteTokens.toLocaleString()} Token` : isCodexRuntime ? 'Codex app-server 未返回 thread/tokenUsage/updated 的缓存字段' : '不等于确认没有缓存，只表示响应中没有缓存字段'} />
      <ContextDatum label="上下文压缩" value={context.compressionCount > 0 ? `${context.compressionCount} 次 · 摘要代表 ${context.compressedMessages} 条` : context.compressionMode === 'auto' ? `自动 · ${context.compressionThresholdPercent}% 触发` : '已停用'} hint={context.compressionCount > 0 ? `最近 ${context.retainedMessages} 条仍原样发送；SQLite 永久保留全部 ${context.historyMessages} 条消息` : '达到阈值后生成结构化检查点，并保留最近原始轮次；不会静默删除历史'} />
      <ContextDatum label="执行目录" value={session.workspace || '默认工作区'} hint={session.workspace ? '文件、Shell 和 stdio MCP 都在这个目录中运行；切换项目需要新建会话' : '该会话使用 EasyAgent 默认工作区'} />
      <ContextDatum label="项目隔离" value={isolation} hint={session.workspaceNotice || (session.worktreeBranch ? `源项目 ${session.sourceWorkspace || '未记录'}` : '同一项目的任务会串行，避免并发修改冲突')} />
    </div>
  </details>
}

function workspaceName(value: string) {
  const parts = value.replace(/[\\/]+$/, '').split(/[\\/]/)
  return parts[parts.length - 1] || '默认'
}

function ContextDatum({ label, value, hint }: { label: string; value: string; hint: string }) {
  return <div><span>{label}</span><strong>{value}</strong><small>{hint}</small></div>
}
