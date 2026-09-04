import { useMemo, useState } from 'react'
import type { Session, TraceEvent } from './types'
import { formatDuration, formatTokens, historyModeLabel } from './format'
import { Payload } from './chat/Payload'
import { Metric } from './chat/Metrics'
export function TracePanel({ session, onLoadOlder, onError, onClose }: { session: Session; onLoadOlder: (id: string, kind: 'messages' | 'events', before: number) => Promise<void>; onError: (value: string) => void; onClose: () => void }) {
  const [loadingOlder, setLoadingOlder] = useState(false)
  const cacheRate = session.usage.cacheReported && session.usage.cacheInputTokens ? Math.round(session.usage.cachedTokens / session.usage.cacheInputTokens * 100) : 0
  const context = session.context
  const isCodexRuntime = session.runtime === 'codex'
  const runtimeName = isCodexRuntime ? 'Codex app-server' : 'EasyAgent Runtime'
  const tokenValue = session.usage.totalTokens > 0 ? session.usage.totalTokens.toLocaleString() : '未上报'
  const tokenSub = session.usage.totalTokens > 0 ? `入 ${session.usage.inputTokens} · 出 ${session.usage.outputTokens}` : isCodexRuntime ? 'Codex 未提供 thread/tokenUsage' : 'Provider 未返回 usage'
  const loadOlder = async () => {
    const first = session.events[0]
    if (!first || loadingOlder) return
    setLoadingOlder(true)
    try { await onLoadOlder(session.id, 'events', first.id) } catch (reason) { onError((reason as Error).message) }
    finally { setLoadingOlder(false) }
  }
  return <aside className="trace-panel"><div className="trace-head"><div><p className="eyebrow">运行记录</p><h2>Agent 轨迹</h2></div><button aria-label="关闭 Agent 轨迹" onClick={onClose}>×</button></div><div className="trace-runtime-banner"><strong>{runtimeName}</strong><span>{traceStatusLabel(session.status, session.runProgress)}</span></div><div className="metrics"><Metric label="模型调用" value={`${session.usage.modelCalls} 次`} sub={formatDuration(session.usage.modelDurationMs)} /><Metric label="工具调用" value={`${session.usage.toolCalls} 次`} sub={formatDuration(session.usage.toolDurationMs)} /><Metric label="Token" value={tokenValue} sub={tokenSub} /><Metric label="缓存" value={session.usage.cacheReported ? `${cacheRate}%` : isCodexRuntime ? 'Codex 未提供' : '未上报'} sub={session.usage.cacheReported ? `命中 ${session.usage.cachedTokens} · 写入 ${session.usage.cacheWriteTokens}` : isCodexRuntime ? '等待 thread/tokenUsage/updated' : 'Provider 未返回缓存字段'} /></div><div className="context-ledger"><div><span>最近上下文</span><strong>{context.lastInputTokens ? formatTokens(context.lastInputTokens) : isCodexRuntime ? 'Codex 未上报' : session.status === 'failed' ? '未上报' : '—'}{context.contextWindowTokens ? ` / ${formatTokens(context.contextWindowTokens)}` : ''}</strong></div><div><span>会话历史</span><strong>{isCodexRuntime ? `Codex thread · ${context.userTurns} 个用户轮次` : `${context.userTurns} 个用户轮次 · ${context.historyMessages} 条消息${session.messagesTruncated ? ' · 历史窗口' : ''}`}</strong></div><div><span>发送方式</span><strong>{isCodexRuntime ? 'Codex thread/resume' : historyModeLabel(context.historyMode)}</strong></div><div><span>压缩</span><strong>{isCodexRuntime ? 'Codex 管理' : context.compressionCount > 0 ? `${context.compressionCount} 次 · ${context.compressedMessages} 条` : context.compressionMode === 'auto' ? `自动 ${context.compressionThresholdPercent}%` : '已停用'}</strong></div></div>{session.eventsTruncated && <div className="trace-empty">运行记录较多；点击下方按钮加载更早记录</div>}{session.eventsHasMore && session.events.length > 0 && <button className="history-load-more" onClick={loadOlder} disabled={loadingOlder}>{loadingOlder ? '加载中…' : '加载更早记录'}</button>}<div className="trace-events">{session.events.length === 0 && <div className="trace-empty">还没有运行记录</div>}{session.events.map((event) => <TraceRow key={event.id} event={event} />)}</div></aside>
}

export function TraceRow({ event }: { event: TraceEvent }) {
  const isModelResult = event.kind === 'model_end' || event.kind === 'compaction_end' || event.kind === 'codex_end' || event.kind === 'codex_usage'
  const showsUsage = event.kind === 'model_end' || event.kind === 'compaction_end' || event.kind === 'codex_usage'
  const title = traceEventTitle(event)
  const cacheRate = event.cacheReported && event.inputTokens ? Math.round((event.cachedTokens || 0) / event.inputTokens * 100) : 0
  const tokenMissing = event.status === 'error' && !event.totalTokens && !event.inputTokens && !event.outputTokens
  const location = `${event.turn ? `第 ${event.turn} 轮 · ` : ''}${event.step ? `第 ${event.step} 步` : '独立阶段'}${event.attempt ? ` · 尝试 ${event.attempt}` : ''}`
  return <details className={`trace-row ${event.status} ${event.activityKind || ''}`} open={isModelResult && event.status === 'error'}><summary><span className="trace-node" /><div><strong>{title}</strong><small>{location} {event.statusCode ? `· HTTP ${event.statusCode} ` : ''}{eventDurationLabel(event)} {event.totalTokens ? `· ${event.totalTokens} tokens` : tokenMissing ? '· Token 未上报' : ''}{showsUsage ? ` · ${event.cacheReported ? `缓存 ${cacheRate}%` : '缓存未上报'}` : ''}</small></div><em>{eventStatusLabel(event.status)}</em></summary>{event.detail && <p className={event.status === 'error' ? 'event-error' : 'event-detail'}>{event.detail}</p>}{showsUsage && <div className="event-usage"><span>输入 <b>{tokenMissing ? '未上报' : (event.inputTokens || 0).toLocaleString()}</b></span><span>输出 <b>{tokenMissing ? '未上报' : (event.outputTokens || 0).toLocaleString()}</b></span><span>缓存命中 <b>{event.cacheReported ? (event.cachedTokens || 0).toLocaleString() : '未上报'}</b></span><span>缓存写入 <b>{event.cacheReported ? (event.cacheWriteTokens || 0).toLocaleString() : '未上报'}</b></span><span>历史 <b>{historyModeLabel(event.historyMode || '')} · {event.requestMessages || 0} 项</b></span><span>工具定义 <b>{event.toolDefinitions || 0}</b></span></div>}{event.input && <div><p className="trace-label">{isModelResult ? '模型请求 · 实际发送' : '能力输入'}</p><Payload value={event.input} /></div>}{event.output && (isModelResult ? <ModelTraceResponse value={event.output} /> : <div><p className="trace-label">能力响应 · 原始返回</p><Payload value={event.output} /></div>)}</details>
}

function traceEventTitle(event: TraceEvent) {
  if (event.kind === 'model_start') return '模型请求开始'
  if (event.kind === 'model_end') return `模型响应 · ${event.name || '模型'}`
  if (event.kind === 'compaction_start') return '准备压缩上下文'
  if (event.kind === 'compaction_end') return `上下文检查点 · ${event.name || '模型'}`
  if (event.kind === 'codex_start') return 'Codex Runtime 开始'
  if (event.kind === 'codex_end') return `Codex Runtime 响应 · ${event.name || 'Codex'}`
  if (event.kind === 'codex_usage') return 'Codex · 本轮用量'
  if (event.kind === 'codex_turn') return `Codex Turn · ${event.status === 'started' ? '开始' : '完成'}`
  if (event.kind === 'capability') {
    if (event.activityKind === 'skill') return `应用 Skill · ${event.displayName || event.name}`
    return `选择 MCP · ${formatActivitySource(event.activitySource || event.name || 'MCP')}`
  }
  if (event.kind === 'tool_start' || event.kind === 'tool_end') return toolEventTitle(event)
  if (event.kind === 'codex_item') {
    if (event.activityKind === 'mcp' || event.name === 'mcpToolCall') return `Codex · MCP · ${activityIdentity(event)}`
    return `Codex · ${codexItemLabel(event.name || '')}${event.displayName ? ` · ${event.displayName}` : ''}`
  }
  if (event.kind === 'codex_progress') {
    if (event.activityKind === 'plan') return `Codex · 计划更新${event.detail ? ` · ${event.detail}` : ''}`
    return `Codex · ${codexProgressLabel(event.name || '')}${event.displayName ? ` · ${event.displayName}` : ''}`
  }
  return `MCP · ${event.name}`
}

function toolEventTitle(event: TraceEvent) {
  const completed = event.kind === 'tool_end'
  const input = parseEventInput(event.input)
  if (event.activityKind === 'loader' || event.name === 'load_tools') {
    const labels: Record<string, string> = { execution: '执行', files: '文件', information: '信息', skills: 'Skills', web: 'Web' }
    const groups = Array.isArray(input.groups) ? input.groups.map((item) => labels[String(item)] || String(item)).join(' / ') : '内置能力'
    return `${completed ? '能力加载结果' : '加载能力'} · ${groups}`
  }
  if (event.activityKind === 'skill' || event.name === 'load_skill') return `${completed ? 'Skill 加载结果' : '加载 Skill'} · ${String(input.name || event.displayName || 'Skill')}`
  if (event.activityKind === 'mcp_loader' || event.name === 'search_mcp_tools') return `${completed ? 'MCP 发现结果' : '发现 MCP 工具'} · ${formatActivitySource(String(input.id || event.activitySource || 'MCP'))}`
  if (event.activityKind === 'mcp' || event.name?.startsWith('mcp__')) return `MCP ${completed ? '返回' : '调用'} · ${activityIdentity(event)}`
  return `Tool ${completed ? '结果' : '调用'} · ${event.displayName || event.name || '工具'}`
}

function activityIdentity(event: TraceEvent) {
  const legacy = event.name ? /^mcp__(.+?)__(.+)$/.exec(event.name) : null
  const source = formatActivitySource(event.activitySource || legacy?.[1] || event.detail?.split('/')[0]?.trim() || 'MCP')
  const name = event.displayName || legacy?.[2] || event.detail?.split('/').slice(1).join('/').trim() || event.name || '工具'
  return `${source} / ${name}`
}

function parseEventInput(value?: string): Record<string, unknown> {
  try {
    const parsed = JSON.parse(value || '{}')
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {}
  } catch { return {} }
}

function formatActivitySource(value: string) {
  const labels: Record<string, string> = { context7: 'Context7', github: 'GitHub', playwright: 'Playwright', 'openai-docs': 'OpenAI Docs' }
  return labels[value.toLowerCase()] || value.replaceAll('_', '-')
}

function traceStatusLabel(status: Session['status'], progress?: string) {
  if (status === 'running' || status === 'queued') return progress || '正在处理…'
  if (status === 'paused') return '排队任务已暂停'
  if (status === 'failed') return '本轮失败'
  if (status === 'canceled') return '本轮已停止'
  return '本轮已完成'
}

function eventDurationLabel(event: TraceEvent) {
  if (event.status === 'started') return ''
  if ((event.durationMs || 0) > 0) return `· ${event.durationMs} ms`
  return event.kind === 'model_end' || event.kind === 'compaction_end' || event.kind === 'codex_end' ? '· 耗时未上报' : ''
}

function codexItemLabel(value: string) {
  const labels: Record<string, string> = {
    agentMessage: '生成回答', reasoning: '分析任务', commandExecution: '执行命令',
    fileChange: '修改文件', mcpToolCall: '调用 MCP 工具', dynamicToolCall: '调用动态工具',
    plan: '制定计划', contextCompaction: '压缩上下文', userMessage: '接收消息',
    webSearch: '联网搜索', imageView: '查看图片', enteredReviewMode: '进入审查模式',
    exitedReviewMode: '退出审查模式', collabToolCall: '协作 Agent',
  }
  return labels[value] || value
}

export function eventStatusLabel(status: string) { return status === 'started' ? '开始' : status === 'success' ? '成功' : status === 'error' ? '失败' : status === 'progress' ? '进行中' : status === 'updated' ? '已更新' : status === 'resolved' ? '已处理' : status }

function codexProgressLabel(value: string) {
  const labels: Record<string, string> = { plan: '更新计划', commandExecution: '命令输出', fileChange: '文件变更', mcpToolCall: 'MCP 进度', reasoning: '思考摘要', thread: '线程状态', serverRequest: '请求状态' }
  return labels[value] || value
}

type StreamTracePayload = { stream?: boolean; final_response?: unknown; raw_chunks?: unknown[] }

export function ModelTraceResponse({ value }: { value: string }) {
  const streamed = useMemo<StreamTracePayload | null>(() => {
    try {
      const parsed = JSON.parse(value)
      return parsed && parsed.stream === true && parsed.final_response && Array.isArray(parsed.raw_chunks) ? parsed : null
    } catch {
      return null
    }
  }, [value])
  if (!streamed) return <div><p className="trace-label">模型响应 · Provider 原始返回</p><Payload value={value} /></div>
  return <div className="model-trace-response"><p className="trace-label">模型响应 · 最终聚合</p><Payload value={JSON.stringify(streamed.final_response)} /><details className="raw-deltas"><summary><span>原始流式 Delta</span><em>{streamed.raw_chunks?.length || 0} 个 SSE Chunk</em></summary><Payload value={JSON.stringify(streamed.raw_chunks)} /></details></div>
}
