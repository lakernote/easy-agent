import { useMemo, useState } from 'react'
import type { Session, TraceEvent } from './types'
import { formatDuration, historyModeLabel } from './format'
import { writeClipboardText } from './clipboard'
import { Payload } from './chat/Payload'
import { Metric } from './chat/Metrics'

type TraceFilter = 'all' | 'work' | 'model' | 'protocol' | 'error'

export function TracePanel({ session, onLoadOlder, onError, onClose }: { session: Session; onLoadOlder: (id: string, kind: 'messages' | 'events', before: number) => Promise<void>; onError: (value: string) => void; onClose: () => void }) {
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [filter, setFilter] = useState<TraceFilter>('all')
  const [query, setQuery] = useState('')
  const events = useMemo(() => withRecoveredCodexRequests(session, mergeLifecycleEvents(session.events)), [session])
  const counts = useMemo(() => ({
    all: events.length,
    work: events.filter((event) => traceCategory(event) === 'work').length,
    model: events.filter((event) => traceCategory(event) === 'model').length,
    protocol: events.filter((event) => traceCategory(event) === 'protocol').length,
    error: events.filter((event) => event.status === 'error').length,
  }), [events])
  const visibleEvents = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return events.filter((event) => {
      if (filter === 'error' ? event.status !== 'error' : filter !== 'all' && traceCategory(event) !== filter) return false
      if (!needle) return true
      return [traceEventTitle(event), event.detail, event.input, event.output, event.protocolMethod, event.rawPayload].some((value) => value?.toLowerCase().includes(needle))
    })
  }, [events, filter, query])
  const groups = useMemo(() => groupEventsByTurn(visibleEvents), [visibleEvents])
  const isCodexRuntime = session.runtime === 'codex'
  const runtimeName = isCodexRuntime ? 'Codex app-server' : 'EasyAgent Runtime'
  const totalTokenValue = formatTokenCount(session.usage.totalTokens)
  const inputTokenValue = formatTokenCount(session.usage.inputTokens)
  const outputTokenValue = formatTokenCount(session.usage.outputTokens)
  const cacheRate = session.usage.cacheReported && session.usage.cacheInputTokens > 0 ? `${Math.round(session.usage.cachedTokens / session.usage.cacheInputTokens * 100)}%` : '未上报'
  const compressionValue = session.context.compressionMode === 'auto' ? `自动 ${session.context.compressionThresholdPercent}%` : '未启用'
  const loadOlder = async () => {
    const first = session.events[0]
    if (!first || loadingOlder) return
    setLoadingOlder(true)
    try { await onLoadOlder(session.id, 'events', first.id) } catch (reason) { onError((reason as Error).message) }
    finally { setLoadingOlder(false) }
  }
  return <aside className="trace-panel" aria-label="Agent 轨迹">
    <div className="trace-head"><div><p className="eyebrow">运行记录</p><div className="trace-title-row"><h2>Agent 轨迹</h2><span>{formatTraceCount(events.length)} 步</span></div></div><button type="button" aria-label="关闭 Agent 轨迹" onClick={onClose}>×</button></div>
    <div className="trace-runtime-banner"><span className={`trace-live-dot ${session.status}`} aria-hidden="true" /><strong>{runtimeName}</strong><span>{traceStatusLabel(session.status, session.runProgress)}</span></div>
    <div className="trace-metric-group"><span className="trace-metric-group-label">用量</span><div className="metrics metrics-usage"><Metric label="总 Token" value={totalTokenValue} className="metric-primary" /><Metric label="输入" value={inputTokenValue} /><Metric label="输出" value={outputTokenValue} /><Metric label="缓存率" value={cacheRate} /><Metric label="压缩阈值" value={compressionValue} /></div></div>
    <div className="trace-metric-group"><span className="trace-metric-group-label">运行</span><div className="metrics metrics-runtime"><Metric label="模型调用" value={`${session.usage.modelCalls} 次`} className="metric-primary" /><Metric label="工具调用" value={`${session.usage.toolCalls} 次`} /><Metric label="模型耗时" value={formatDuration(session.usage.modelDurationMs)} /><Metric label="工具耗时" value={formatDuration(session.usage.toolDurationMs)} /></div></div>
    <div className="trace-toolbar"><div className="trace-filters" role="group" aria-label="筛选轨迹">{([['all', '全部'], ['work', '执行'], ['model', '模型'], ['protocol', '协议'], ['error', '失败']] as [TraceFilter, string][]).map(([value, label]) => <button type="button" className={filter === value ? 'active' : ''} aria-pressed={filter === value} onClick={() => setFilter(value)} key={value}>{label}<span title={`${counts[value].toLocaleString()} 条记录`}>{formatTraceCount(counts[value])}</span></button>)}</div><label className="trace-search"><span aria-hidden="true">⌕</span><input value={query} onChange={(event) => setQuery(event.target.value)} aria-label="搜索轨迹" placeholder="搜索命令、方法或输出" /></label></div>
    {session.eventsTruncated && <div className="trace-history-note">当前展示最近的运行记录，更早记录仍保存在 SQLite 中。</div>}
    {session.eventsHasMore && session.events.length > 0 && <button className="history-load-more" onClick={loadOlder} disabled={loadingOlder}>{loadingOlder ? '加载中…' : '加载更早记录'}</button>}
    <div className="trace-events">{groups.length === 0 && <div className="trace-empty">{query || filter !== 'all' ? '没有匹配的运行记录' : '还没有运行记录'}</div>}{groups.map((group) => <section className="trace-turn" key={group.key}><div className="trace-turn-head"><strong>{group.label}</strong><span>{formatTraceCount(group.events.length)} 步</span></div>{group.events.map((event) => <TraceRow key={event.id} event={event} />)}</section>)}</div>
  </aside>
}

export function TraceRow({ event }: { event: TraceEvent }) {
  const isModelResult = event.kind === 'model_end' || event.kind === 'compaction_end' || event.kind === 'codex_end' || event.kind === 'codex_usage'
  const showsUsage = event.kind === 'model_end' || event.kind === 'compaction_end' || event.kind === 'codex_usage'
  const title = traceEventTitle(event)
  const cacheRate = event.cacheReported && event.inputTokens ? Math.round((event.cachedTokens || 0) / event.inputTokens * 100) : 0
  const tokenMissing = event.status === 'error' && !event.totalTokens && !event.inputTokens && !event.outputTokens
  const location = `${event.step ? `第 ${event.step} 步` : '运行阶段'}${event.attempt ? ` · 尝试 ${event.attempt}` : ''}`
  const category = traceCategory(event)
  const summaryDetail = traceSummaryDetail(event)
  const isTurnRequest = event.kind === 'codex_rpc' && event.protocolMethod === 'turn/start'
  const hasDetails = Boolean(event.detail || event.input || event.output || event.rawPayload || showsUsage || event.protocolMethod)
  return <details className={`trace-row ${event.status} ${event.activityKind || ''}`} open={event.status === 'error'}>
    <summary aria-label={`${title}，${eventStatusLabel(event.status)}`}><span className="trace-node" /><div className="trace-summary-copy"><div><span className={`trace-kind ${category}`}>{traceCategoryLabel(category)}</span><strong>{title}</strong></div><small>{summaryDetail ? `${summaryDetail} · ` : ''}{location}{eventDurationLabel(event)}{event.totalTokens ? ` · ${formatTokenCount(event.totalTokens)} Token` : tokenMissing ? ' · Token 未上报' : ''}</small></div><div className="trace-summary-state"><time>{formatTraceTime(event.createdAt)}</time><em>{eventStatusLabel(event.status)}</em>{hasDetails && <span className="trace-chevron" aria-hidden="true" />}</div></summary>
    <div className="trace-row-body">
      <dl className="trace-meta"><div><dt>事件类型</dt><dd>{event.kind}</dd></div>{event.protocolMethod && <div><dt>JSON-RPC 方法</dt><dd><code>{event.protocolMethod}</code></dd></div>}{event.activityId && <div><dt>活动 ID</dt><dd><code>{event.activityId}</code></dd></div>}{event.protocol && <div><dt>协议</dt><dd><code>{event.protocol}</code></dd></div>}{event.statusCode ? <div><dt>HTTP 状态</dt><dd>{event.statusCode}</dd></div> : null}</dl>
      {isTurnRequest ? <CodexTurnRequestDetails event={event} /> : <>
        {event.detail && <p className={event.status === 'error' ? 'event-error' : 'event-detail'}>{event.detail}</p>}
        {showsUsage && <div className="event-usage"><span>本次输入 <b>{tokenMissing ? '未上报' : (event.inputTokens || 0).toLocaleString()}</b></span><span>本次输出 <b>{tokenMissing ? '未上报' : (event.outputTokens || 0).toLocaleString()}</b></span><span>缓存命中 <b>{event.cacheReported ? (event.cachedTokens || 0).toLocaleString() : '未上报'}</b></span><span>缓存写入 <b>{event.cacheReported ? (event.cacheWriteTokens || 0).toLocaleString() : '未上报'}</b></span><span>请求消息 <b>{historyModeLabel(event.historyMode || '')} · {event.requestMessages || 0} 项</b></span><span>工具定义 <b>{event.toolDefinitions || 0}</b></span><span>缓存率 <b>{event.cacheReported ? `${cacheRate}%` : '未上报'}</b></span></div>}
        {(event.input || event.output) && <div className={`trace-io ${event.input && event.output ? 'split' : ''}`}>{event.input && <TracePayload label={event.kind === 'codex_rpc' ? '请求参数' : isModelResult ? '模型请求 · 实际发送' : '输入'} value={event.input} />}{event.output && (isModelResult ? <ModelTraceResponse value={event.output} /> : <TracePayload label={event.kind === 'codex_rpc' ? '响应结果' : '输出'} value={event.output} />)}</div>}
        {event.rawPayload && <details className="trace-raw"><summary><span>{event.kind === 'codex_rpc' ? '原始 JSONL 请求' : '原始 JSONL 事件'}</span><code>{event.protocolMethod || event.kind}</code></summary><TracePayload value={event.rawPayload} copyLabel={event.kind === 'codex_rpc' ? '复制请求' : '复制事件'} /></details>}
      </>}
    </div>
  </details>
}

function CodexTurnRequestDetails({ event }: { event: TraceEvent }) {
  const params = parseEventInput(event.input)
  const items = Array.isArray(params.input) ? params.input.filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === 'object' && !Array.isArray(item)) : []
  const prompt = items.filter((item) => item.type === 'text' && typeof item.text === 'string').map((item) => String(item.text)).join('\n\n').trim()
  const skills = items.filter((item) => item.type === 'skill' && typeof item.name === 'string').map((item) => String(item.name))
  const sandbox = params.sandboxPolicy && typeof params.sandboxPolicy === 'object' && !Array.isArray(params.sandboxPolicy)
    ? String((params.sandboxPolicy as Record<string, unknown>).type || '') : String(params.sandboxPolicy || '')
  return <>
    {event.activitySource === 'history' && <p className="trace-history-note compact">旧记录未保存原始 JSONL，下面的 Prompt 从会话消息恢复。</p>}
    {prompt && <TracePayload label={event.activitySource === 'history' ? '用户 Prompt' : '实际发送的 Prompt'} value={prompt} />}
    <dl className="trace-request-fields">
      <div><dt>输入项</dt><dd>{items.length || 1}</dd></div>
      <div><dt>审批</dt><dd>{String(params.approvalPolicy || 'never')}</dd></div>
      <div><dt>沙箱</dt><dd>{sandbox || 'dangerFullAccess'}</dd></div>
      {skills.length > 0 && <div><dt>Skill</dt><dd>{skills.join(', ')}</dd></div>}
      {typeof params.cwd === 'string' && <div className="wide"><dt>工作目录</dt><dd>{params.cwd}</dd></div>}
    </dl>
    {event.input && event.activitySource !== 'history' && <details className="trace-raw"><summary><span>请求参数</span><code>params</code></summary><TracePayload value={event.input} copyLabel="复制参数" /></details>}
    {event.rawPayload && <details className="trace-raw"><summary><span>原始 JSONL 请求</span><code>stdin</code></summary><TracePayload value={event.rawPayload} copyLabel="复制请求" /></details>}
    {event.output && <details className="trace-raw"><summary><span>响应结果</span><code>stdout</code></summary><TracePayload value={event.output} copyLabel="复制响应" /></details>}
  </>
}

function TracePayload({ label, value, copyLabel }: { label?: string; value: string; copyLabel?: string }) {
  return <div className="trace-payload">{(label || copyLabel) && <div className="trace-payload-head">{label && <span>{label}</span>}<CopyButton value={value} label={copyLabel || `复制${label}`} /></div>}<Payload value={value} /></div>
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const copy = async () => {
    try {
      await writeClipboardText(value)
      setState('copied')
    } catch {
      setState('failed')
    }
    window.setTimeout(() => setState('idle'), 1600)
  }
  return <button type="button" onClick={() => void copy()} aria-label={label}>{state === 'copied' ? '已复制' : state === 'failed' ? '复制失败' : '复制'}</button>
}

function traceEventTitle(event: TraceEvent) {
  if (event.kind === 'model_start') return '模型请求'
  if (event.kind === 'model_end') return `模型响应 · ${event.name || '模型'}`
  if (event.kind === 'compaction_start') return '准备压缩上下文'
  if (event.kind === 'compaction_end') return `上下文检查点 · ${event.name || '模型'}`
  if (event.kind === 'codex_start') return '启动 Codex Runtime'
  if (event.kind === 'codex_end') return `Codex 最终响应 · ${event.name || 'Codex'}`
  if (event.kind === 'codex_usage') return '本轮 Token 用量'
  if (event.kind === 'codex_rpc') {
    if (event.protocolMethod === 'initialize') return '初始化 app-server'
    if (event.protocolMethod === 'thread/start') return '创建 Codex 会话'
    if (event.protocolMethod === 'thread/resume') return '恢复 Codex 会话'
    if (event.protocolMethod === 'turn/start') return '发送用户请求'
    return `协议请求 · ${event.protocolMethod || event.name}`
  }
  if (event.kind === 'codex_request') return `反向请求 · ${event.protocolMethod || event.name}`
  if (event.kind === 'codex_turn') return 'Codex Turn 生命周期'
  if (event.kind === 'agent_guidance') return 'Agent 校验 · 原始来源核验'
  if (event.kind === 'capability') {
    if (event.activityKind === 'skill') return `应用 Skill · ${event.displayName || event.name}`
    return `选择 MCP · ${formatActivitySource(event.activitySource || event.name || 'MCP')}`
  }
  if (event.kind === 'tool_start' || event.kind === 'tool_end') return toolEventTitle(event)
  if (event.kind === 'codex_item') {
    if (event.activityKind === 'mcp' || event.name === 'mcpToolCall') return `MCP · ${activityIdentity(event)}`
    return `${codexItemLabel(event.name || '')}${event.displayName && event.displayName !== codexItemLabel(event.name || '') ? ` · ${event.displayName}` : ''}`
  }
  if (event.kind === 'codex_progress') {
    if (event.activityKind === 'plan') return `计划更新${event.displayName ? ` · ${event.displayName}` : ''}`
    return `${codexProgressLabel(event.name || '')}${event.displayName ? ` · ${event.displayName}` : ''}`
  }
  return `${event.activitySource || 'Agent'} · ${event.name || event.kind}`
}

function toolEventTitle(event: TraceEvent) {
  const input = parseEventInput(event.input)
  if (event.activityKind === 'loader' || event.name === 'load_tools') {
    const labels: Record<string, string> = { execution: '执行', files: '文件', information: '信息', skills: 'Skills', web: 'Web' }
    const groups = Array.isArray(input.groups) ? input.groups.map((item) => labels[String(item)] || String(item)).join(' / ') : '内置能力'
    return `加载能力 · ${groups}`
  }
  if (event.activityKind === 'skill' || event.name === 'load_skill') return `加载 Skill · ${String(input.name || event.displayName || 'Skill')}`
  if (event.activityKind === 'mcp_loader' || event.name === 'search_mcp_tools') return `发现 MCP 工具 · ${formatActivitySource(String(input.id || event.activitySource || 'MCP'))}`
  if (event.activityKind === 'mcp' || event.name?.startsWith('mcp__')) return `MCP 调用 · ${activityIdentity(event)}`
  return `工具调用 · ${event.displayName || event.name || '工具'}`
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

function withRecoveredCodexRequests(session: Session, events: TraceEvent[]) {
  if (session.runtime !== 'codex') return events
  const userMessages = session.messages.filter((message) => message.role === 'user')
  if (userMessages.length === 0) return events
  const totalTurns = session.userTurnCount || session.context.userTurns || userMessages.length
  const firstLoadedTurn = Math.max(1, totalTurns - userMessages.length + 1)
  const requests = new Map<number, TraceEvent>()
  userMessages.forEach((message, index) => {
    const turn = firstLoadedTurn + index
    if (events.some((event) => event.turn === turn && event.kind === 'codex_rpc' && event.protocolMethod === 'turn/start')) return
    const input = [{ type: 'text', text: message.content || '' }, ...(message.attachments || []).map((attachment) => ({ type: attachment.kind, name: attachment.name, mimeType: attachment.mimeType, size: attachment.size }))]
    requests.set(turn, {
      id: -message.id,
      kind: 'codex_rpc', protocolMethod: 'turn/start', turn, step: 0,
      name: 'turn/start', status: 'success', activityKind: 'protocol', activitySource: 'history',
      detail: '旧记录 · Prompt 从会话消息恢复', input: JSON.stringify({ input }), cacheReported: false,
      createdAt: message.createdAt,
    })
  })
  if (requests.size === 0) return events
  const result = [...events]
  for (const [turn, request] of [...requests].sort(([left], [right]) => left - right)) {
    const lifecycleIndex = result.findIndex((event) => event.turn === turn && (event.kind === 'codex_turn' || (event.kind === 'codex_item' && event.name === 'userMessage')))
    const firstTurnIndex = result.findIndex((event) => event.turn === turn)
    const nextTurnIndex = result.findIndex((event) => Boolean(event.turn && event.turn > turn))
    const requestTime = Date.parse(request.createdAt)
    const chronologicalIndex = Number.isNaN(requestTime) ? -1 : result.findIndex((event) => {
      const eventTime = Date.parse(event.createdAt)
      return !Number.isNaN(eventTime) && eventTime > requestTime
    })
    const insertAt = lifecycleIndex >= 0 ? lifecycleIndex : firstTurnIndex >= 0 ? firstTurnIndex : nextTurnIndex >= 0 ? nextTurnIndex : chronologicalIndex >= 0 ? chronologicalIndex : result.length
    result.splice(insertAt, 0, request)
  }
  return result
}

function mergeLifecycleEvents(events: TraceEvent[]) {
  const result: TraceEvent[] = []
  const indexes = new Map<string, number>()
  for (const event of events) {
    const key = lifecycleKey(event)
    const previousIndex = key ? indexes.get(key) : undefined
    if (key && previousIndex !== undefined) {
      const previous = result[previousIndex]
      const terminal = event.status === 'success' || event.status === 'error' ? event : previous.status === 'success' || previous.status === 'error' ? previous : event
      result[previousIndex] = {
        ...previous, ...event, ...terminal,
        createdAt: previous.createdAt,
        detail: terminal.detail || event.detail || previous.detail,
        input: terminal.input || event.input || previous.input,
        output: terminal.output || event.output || previous.output,
        protocolMethod: mergeProtocolMethods(previous.protocolMethod, event.protocolMethod),
        rawPayload: mergeRawPayloads(previous.rawPayload, event.rawPayload),
      }
      if (isTerminalLifecycle(event)) indexes.delete(key)
      continue
    }
    if (key && !isTerminalLifecycle(event)) indexes.set(key, result.length)
    result.push(event)
  }
  return result
}

function isTerminalLifecycle(event: TraceEvent) {
  return event.status === 'success' || event.status === 'error'
}

function lifecycleKey(event: TraceEvent) {
  if ((event.kind === 'codex_item' || event.kind === 'codex_progress') && event.activityId && (event.activityKind === 'tool' || event.activityKind === 'mcp')) return `codex-activity:${event.activityId}`
  if (event.kind === 'codex_item' && event.activityId) return `codex-item:${event.activityId}`
  if ((event.kind === 'tool_start' || event.kind === 'tool_end') && event.activityId) return `tool:${event.activityId}`
  if (event.kind === 'model_start' || event.kind === 'model_end') return `model:${event.turn || 0}:${event.step}:${event.attempt || 0}`
  if (event.kind === 'compaction_start' || event.kind === 'compaction_end') return `compaction:${event.turn || 0}:${event.step}:${event.attempt || 0}`
  if (event.kind === 'codex_turn') return `codex-turn:${event.turn || 0}`
  return ''
}

function mergeProtocolMethods(first?: string, second?: string) {
  if (!first) return second
  if (!second || first === second) return first
  return `${first} → ${second}`
}

function mergeRawPayloads(first?: string, second?: string) {
  if (!first) return second
  if (!second || first === second) return first
  try { return JSON.stringify([JSON.parse(first), JSON.parse(second)]) } catch { return `${first}\n${second}` }
}

function groupEventsByTurn(events: TraceEvent[]) {
  const groups: { key: string; label: string; events: TraceEvent[] }[] = []
  for (const event of events) {
    const key = event.turn ? `turn-${event.turn}` : 'runtime'
    let group = groups.at(-1)
    if (!group || !group.key.startsWith(`${key}-`)) {
      group = { key: `${key}-${groups.length}`, label: event.turn ? `第 ${event.turn} 轮` : 'Runtime', events: [] }
      groups.push(group)
    }
    group.events.push(event)
  }
  return groups
}

function traceCategory(event: TraceEvent): Exclude<TraceFilter, 'all' | 'error'> {
  if (event.kind === 'model_start' || event.kind === 'model_end' || event.kind === 'compaction_start' || event.kind === 'compaction_end' || event.kind === 'codex_usage' || event.name === 'reasoning' || event.name === 'agentMessage') return 'model'
  if (event.kind === 'tool_start' || event.kind === 'tool_end' || event.kind === 'capability' || event.activityKind === 'tool' || event.activityKind === 'mcp' || event.activityKind === 'skill' || event.activityKind === 'loader' || event.activityKind === 'mcp_loader') return 'work'
  return 'protocol'
}

function traceCategoryLabel(value: ReturnType<typeof traceCategory>) { return value === 'work' ? '执行' : value === 'model' ? '模型' : '协议' }

function traceSummaryDetail(event: TraceEvent) {
  if (event.kind === 'codex_rpc' && event.protocolMethod === 'turn/start') {
    const params = parseEventInput(event.input)
    const items = Array.isArray(params.input) ? params.input : []
    const prompt = items.find((item) => item && typeof item === 'object' && !Array.isArray(item) && (item as Record<string, unknown>).type === 'text') as Record<string, unknown> | undefined
    const text = typeof prompt?.text === 'string' ? prompt.text.replace(/\s+/g, ' ').trim() : ''
    return text ? `Prompt: ${text.length > 72 ? `${text.slice(0, 72)}…` : text}` : `${items.length} 个输入项`
  }
  return event.detail && event.detail.length < 96 ? event.detail : ''
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
  if ((event.durationMs || 0) > 0) return ` · ${formatDuration(event.durationMs || 0)}`
  return event.kind === 'model_end' || event.kind === 'compaction_end' || event.kind === 'codex_end' ? ' · 耗时未上报' : ''
}

function formatTraceTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(date)
}

function formatTraceCount(value: number) {
  if (value < 1000) return value.toLocaleString()
  return new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

function formatTokenCount(value: number) {
  if (value <= 0) return '未上报'
  if (value < 10_000) return value.toLocaleString()
  if (value < 100_000_000) return `${trimUnitValue(value / 10_000)}万`
  return `${trimUnitValue(value / 100_000_000)}亿`
}

function trimUnitValue(value: number) {
  return value.toFixed(1).replace(/\.0$/, '')
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
    } catch { return null }
  }, [value])
  if (!streamed) return <TracePayload label="模型响应 · Provider 原始返回" value={value} />
  return <div className="model-trace-response"><TracePayload label="模型响应 · 最终聚合" value={JSON.stringify(streamed.final_response)} /><details className="raw-deltas"><summary><span>原始流式 Delta</span><em>{streamed.raw_chunks?.length || 0} 个 SSE Chunk</em></summary><Payload value={JSON.stringify(streamed.raw_chunks)} /></details></div>
}
