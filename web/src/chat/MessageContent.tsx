import { lazy, Suspense } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Session } from '../types'
import { formatTokens, historyModeLabel } from '../format'
import { formatBytes } from '../attachments'
import { FileIcon, Logo } from '../ui'
import { markdownComponents } from '../markdownComponents'
import { Payload } from './Payload'

const MathMarkdown = lazy(() => import('../MathMarkdown'))
const hasMath = (value: string) => /\$\$[\s\S]+?\$\$|\$[^$\n]+?\$/.test(value)

export function MessageView({ message }: { message: Session['messages'][number] }) {
  if (message.role === 'tool') return <details className="tool-result" open={message.name === 'weather'}><summary><span>⌁</span>{message.name === 'weather' ? '天气预报' : message.name || '工具'} 返回结果</summary><ToolResult name={message.name || ''} value={message.content || ''} /></details>
  if (message.role === 'user') return <div className="user-row"><div className="user-message">{message.attachments?.length > 0 && <MessageAttachments attachments={message.attachments} />}{message.content && <div>{message.content}</div>}</div></div>
  if (message.role !== 'assistant') return null
  return <div className="assistant-row"><Avatar /><div className="assistant-message">{message.toolCalls?.length > 0 && <div className="tool-intent">{message.toolCalls.map((call) => <span key={call.id}>调用 {call.name}</span>)}</div>}{message.content && <div className="answer-text"><Markdown>{message.content}</Markdown></div>}</div></div>
}

type WeatherResult = {
  location?: { name?: string; admin1?: string; country?: string }
  observed_at?: string
  condition?: string
  temperature_c?: number
  feels_like_c?: number
  humidity_percent?: number
  wind_kmh?: number
  source?: string
  forecast?: Array<{ date?: string; condition?: string; temp_max_c?: number; temp_min_c?: number; precipitation_probability_percent?: number }>
}

function ToolResult({ name, value }: { name: string; value: string }) {
  if (name !== 'weather') return <Payload value={value} />
  let weather: WeatherResult
  try { weather = JSON.parse(value) as WeatherResult } catch { return <Payload value={value} /> }
  if (!weather.location || !Array.isArray(weather.forecast)) return <Payload value={value} />
  const place = [weather.location.name, weather.location.admin1, weather.location.country].filter(Boolean).join(' · ')
  return <div className="weather-result">
    <div className="weather-current"><div><strong>{place || '天气'}</strong><span>{weather.condition || '—'}</span></div><b>{typeof weather.temperature_c === 'number' ? `${weather.temperature_c}°C` : '—'}</b><small>体感 {formatWeatherNumber(weather.feels_like_c)}°C · 湿度 {formatWeatherNumber(weather.humidity_percent)}% · 风速 {formatWeatherNumber(weather.wind_kmh)} km/h</small><small>{weather.observed_at ? `观测于 ${weather.observed_at}` : ''}{weather.source ? ` · ${weather.source}` : ''}</small></div>
    <div className="weather-forecast" aria-label="未来天气预报">{weather.forecast.map((day, index) => <div className="weather-day" key={`${day.date || 'day'}-${index}`}><strong>{formatWeatherDate(day.date)}</strong><span>{day.condition || '—'}</span><b>{formatWeatherNumber(day.temp_max_c)}° / {formatWeatherNumber(day.temp_min_c)}°</b>{typeof day.precipitation_probability_percent === 'number' && <small>降水 {day.precipitation_probability_percent}%</small>}</div>)}</div>
    <details className="weather-raw"><summary>查看原始天气数据</summary><Payload value={value} /></details>
  </div>
}

function formatWeatherDate(value?: string) {
  if (!value) return '—'
  const date = new Date(`${value}T00:00:00`)
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', weekday: 'short' }).format(date)
}

function formatWeatherNumber(value?: number) {
  return typeof value === 'number' ? Number.isInteger(value) ? String(value) : value.toFixed(1) : '—'
}

function MessageAttachments({ attachments }: { attachments: Session['messages'][number]['attachments'] }) {
  return <div className="message-attachments">{attachments.map((attachment) => attachment.kind === 'image'
    ? <a key={attachment.id} className="message-image" href={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} target="_blank" rel="noreferrer" title={`查看 ${attachment.name}`}><img src={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} alt={attachment.name} loading="lazy" /><span>{attachment.name}</span></a>
    : <a key={attachment.id} className="message-file" href={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} target="_blank" rel="noreferrer" download={attachment.name}><FileIcon /><span><strong>{attachment.name}</strong><small>{attachment.kind === 'pdf' ? 'PDF' : '文本文件'} · {formatBytes(attachment.size)}</small></span></a>)}</div>
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
