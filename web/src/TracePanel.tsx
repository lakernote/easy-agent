import { useEffect, useMemo, useState } from 'react'
import type { CodexSessionLogLine, CodexSessionLogPage, Session, TraceEvent } from './types'
import { api } from './api'
import { formatDuration, historyModeLabel } from './format'
import { writeClipboardText } from './clipboard'
import { Payload } from './chat/Payload'
import { Metric } from './chat/Metrics'

type TraceMode = 'timeline' | 'stats' | 'raw'
type TraceFilter = 'all' | 'input' | 'llm' | 'tool' | 'usage' | 'status' | 'error'
type RawTraceSource = 'session' | 'app-server' | 'easyagent'

export function TracePanel({ session, onLoadOlder, onError, onClose }: { session: Session; onLoadOlder: (id: string, kind: 'messages' | 'events', before: number) => Promise<void>; onError: (value: string) => void; onClose: () => void }) {
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [mode, setMode] = useState<TraceMode>('timeline')
  const [filter, setFilter] = useState<TraceFilter>('all')
  const [persistedModel, setPersistedModel] = useState('')
  const isCodexRuntime = session.runtime === 'codex'
  const events = useMemo(() => withRecoveredCodexRequests(session, mergeLifecycleEvents(session.events)), [session])
  const counts = useMemo(() => ({
    all: events.length,
    input: events.filter((event) => traceCategory(event) === 'input').length,
    llm: events.filter((event) => traceCategory(event) === 'llm').length,
    tool: events.filter((event) => traceCategory(event) === 'tool').length,
    usage: events.filter((event) => traceCategory(event) === 'usage').length,
    status: events.filter((event) => traceCategory(event) === 'status').length,
    error: events.filter((event) => event.status === 'error').length,
  }), [events])
  const visibleEvents = useMemo(() => {
    return events.filter((event) => {
      return filter === 'error' ? event.status === 'error' : filter === 'all' || traceCategory(event) === filter
    })
  }, [events, filter])
  const groups = useMemo(() => groupEventsByTurn(visibleEvents), [visibleEvents])
  const runtimeName = isCodexRuntime ? 'Codex app-server' : 'EasyAgent Runtime'
  const activeModel = session.model || persistedModel || (isCodexRuntime ? 'Codex 默认模型' : 'Runtime 默认模型')
  const rounds = useMemo(() => new Set(events.map((event) => event.turn).filter(Boolean)).size, [events])
  useEffect(() => {
    let cancelled = false
    setPersistedModel('')
    if (!isCodexRuntime) return () => { cancelled = true }
    void api.codexSessionLog(session.id, 0, 1).then((page) => {
      if (!cancelled) setPersistedModel(page.model || '')
    }).catch(() => undefined)
    return () => { cancelled = true }
  }, [isCodexRuntime, session.id])
  const loadOlder = async () => {
    const first = session.events[0]
    if (!first || loadingOlder) return
    setLoadingOlder(true)
    try { await onLoadOlder(session.id, 'events', first.id) } catch (reason) { onError((reason as Error).message) }
    finally { setLoadingOlder(false) }
  }
  return <aside className="trace-panel" aria-label="运行轨迹">
    <div className="trace-head"><div className="trace-head-copy"><div className="trace-title-row"><h2>运行轨迹</h2><span>{rounds} 回合 · {formatTraceCount(events.length)} 事件</span></div></div><div className="trace-head-actions"><div className="trace-mode-tabs" role="tablist" aria-label="轨迹视图">{([['timeline', '事件'], ['stats', '统计'], ['raw', '原始']] as [TraceMode, string][]).map(([value, label]) => <button type="button" role="tab" aria-selected={mode === value} className={mode === value ? 'active' : ''} onClick={() => setMode(value)} key={value}>{label}</button>)}</div><button type="button" className="trace-close" aria-label="关闭运行轨迹" onClick={onClose}>×</button></div></div>
    <div className="trace-overview"><div className="trace-runtime-banner"><span className={`trace-live-dot ${session.status}`} aria-hidden="true" /><strong>{runtimeName}</strong><span>{traceStatusLabel(session.status, session.runProgress)}</span></div><div className="trace-overview-model"><span>模型</span><strong>{activeModel}</strong></div></div>
    {mode === 'timeline' && <>
      <div className="trace-toolbar"><div className="trace-filters" role="group" aria-label="筛选轨迹">{([['all', '全部'], ['input', '输入'], ['llm', 'LLM'], ['tool', '工具'], ['usage', '用量'], ['status', '状态'], ['error', '失败']] as [TraceFilter, string][]).map(([value, label]) => <button type="button" className={filter === value ? 'active' : ''} aria-pressed={filter === value} onClick={() => setFilter(value)} key={value}>{label}<span title={`${counts[value].toLocaleString()} 条记录`}>{formatTraceCount(counts[value])}</span></button>)}</div></div>
      <TraceHistoryControls session={session} loading={loadingOlder} onLoadOlder={loadOlder} />
      <div className="trace-events">{groups.length === 0 && <div className="trace-empty">{filter !== 'all' ? '没有匹配的运行记录' : '还没有运行记录'}</div>}{groups.map((group) => <TraceTurn key={group.key} label={group.label} events={group.events} fallbackModel={activeModel} />)}</div>
    </>}
    {mode === 'stats' && <TraceStatistics session={session} events={events} />}
    {mode === 'raw' && <><TraceHistoryControls session={session} loading={loadingOlder} onLoadOlder={loadOlder} /><RawTraceRecords session={session} events={session.events} /></>}
  </aside>
}

function TraceHistoryControls({ session, loading, onLoadOlder }: { session: Session; loading: boolean; onLoadOlder: () => Promise<void> }) {
  return <>{session.eventsTruncated && <div className="trace-history-note">当前展示最近的运行记录，更早记录仍保存在 EasyAgent SQLite 中。</div>}{session.eventsHasMore && session.events.length > 0 && <button className="history-load-more" onClick={() => void onLoadOlder()} disabled={loading}>{loading ? '加载中…' : '加载更早记录'}</button>}</>
}

function TraceTurn({ label, events, fallbackModel }: { label: string; events: TraceEvent[]; fallbackModel: string }) {
  const usage = events.reduce((total, event) => total + (event.kind === 'codex_usage' || event.kind === 'model_end' ? event.totalTokens || 0 : 0), 0)
  const toolCalls = events.filter((event) => traceCategory(event) === 'tool' && event.status !== 'started').length
  const failed = events.some((event) => event.status === 'error')
  const model = [...events].reverse().find((event) => ['codex_usage', 'codex_end', 'model_end'].includes(event.kind))?.name
  return <section className={`trace-turn ${failed ? 'error' : ''}`}><div className="trace-turn-head"><div><strong>{label}</strong><small>{model || fallbackModel}</small></div><div>{usage > 0 && <span><b>{formatTokenCount(usage)}</b> Token</span>}{toolCalls > 0 && <span><b>{toolCalls}</b> 工具调用</span>}<em>{failed ? '有失败' : '已完成'}</em></div></div>{events.map((event) => <TraceRow key={event.id} event={event} />)}</section>
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

function TraceStatistics({ session, events }: { session: Session; events: TraceEvent[] }) {
  const cacheRate = session.usage.cacheReported && session.usage.cacheInputTokens > 0 ? `${Math.round(session.usage.cachedTokens / session.usage.cacheInputTokens * 100)}%` : '未上报'
  const calls = useMemo(() => aggregateTraceCalls(events), [events])
  return <div className="trace-stats-view">
    <section className="trace-stats-section"><header><div><span>本次会话</span><h3>用量与耗时</h3></div><small>来自 EasyAgent 保存的运行事件</small></header><div className="trace-stat-grid"><Metric label="总 Token" value={formatTokenCount(session.usage.totalTokens)} className="metric-primary" /><Metric label="输入 Token" value={formatTokenCount(session.usage.inputTokens)} /><Metric label="输出 Token" value={formatTokenCount(session.usage.outputTokens)} /><Metric label="缓存率" value={cacheRate} /><Metric label="模型耗时" value={formatDuration(session.usage.modelDurationMs)} /><Metric label="工具耗时" value={formatDuration(session.usage.toolDurationMs)} /></div></section>
    <section className="trace-stats-section"><header><div><span>调用明细</span><h3>按能力聚合</h3></div><small>{calls.length} 类调用</small></header>{calls.length === 0 ? <div className="trace-empty">还没有可统计的调用</div> : <div className="trace-call-table"><div className="trace-call-table-head"><span>调用</span><span>次数</span><span>失败</span><span>耗时</span></div>{calls.map((call) => <div className="trace-call-table-row" key={call.key}><div><span className={`trace-kind ${call.category}`}>{traceCategoryLabel(call.category)}</span><strong>{call.label}</strong></div><b>{call.count}</b><b className={call.failed ? 'failed' : ''}>{call.failed}</b><code>{call.duration > 0 ? formatDuration(call.duration) : '未上报'}</code></div>)}</div>}</section>
  </div>
}

function aggregateTraceCalls(events: TraceEvent[]) {
  const result = new Map<string, { key: string; label: string; category: ReturnType<typeof traceCategory>; count: number; failed: number; duration: number }>()
  for (const event of events) {
    const category = traceCategory(event)
    if (category === 'input' || category === 'status') continue
    const label = category === 'tool' ? activityIdentity(event) : category === 'usage' ? 'Token 用量' : event.name || '模型处理'
    const key = `${category}:${label}`
    const current = result.get(key) || { key, label, category, count: 0, failed: 0, duration: 0 }
    current.count++
    if (event.status === 'error') current.failed++
    current.duration += event.durationMs || 0
    result.set(key, current)
  }
  return [...result.values()].sort((left, right) => right.count - left.count || left.label.localeCompare(right.label, 'zh-CN'))
}

function RawTraceRecords({ session, events }: { session: Session; events: TraceEvent[] }) {
  const runtime = session.runtime
  const [source, setSource] = useState<RawTraceSource>(runtime === 'codex' ? 'session' : 'easyagent')
  const [sessionLog, setSessionLog] = useState<CodexSessionLogPage | null>(null)
  const [sessionLogLoading, setSessionLogLoading] = useState(false)
  const [sessionLogError, setSessionLogError] = useState('')
  const effectiveSource = runtime === 'codex' ? source : 'easyagent'
  const records = useMemo(() => events.filter((event) => effectiveSource === 'easyagent' || isAppServerEvent(event)), [events, effectiveSource])
  const loadSessionLog = async (cursor = 0, append = false) => {
    if (sessionLogLoading || runtime !== 'codex') return
    setSessionLogLoading(true)
    setSessionLogError('')
    try {
      const page = await api.codexSessionLog(session.id, cursor)
      setSessionLog((current) => append && current ? { ...page, lines: [...current.lines, ...page.lines] } : page)
    } catch (reason) {
      setSessionLogError((reason as Error).message || '读取 Codex Session JSONL 失败')
    } finally {
      setSessionLogLoading(false)
    }
  }
  useEffect(() => {
    setSource(runtime === 'codex' ? 'session' : 'easyagent')
    setSessionLog(null)
    setSessionLogError('')
  }, [runtime, session.id])
  useEffect(() => {
    if (effectiveSource === 'session' && !sessionLog && !sessionLogLoading && !sessionLogError) void loadSessionLog()
  }, [effectiveSource, sessionLog, sessionLogLoading, sessionLogError])
  return <div className="trace-raw-view">
    <div className="trace-source-tabs" role="tablist" aria-label="原始记录来源">{runtime === 'codex' && <button type="button" role="tab" aria-selected={effectiveSource === 'session'} className={effectiveSource === 'session' ? 'active' : ''} onClick={() => setSource('session')}>Session</button>}<button type="button" role="tab" aria-selected={effectiveSource === 'app-server'} className={effectiveSource === 'app-server' ? 'active' : ''} onClick={() => setSource('app-server')} disabled={runtime !== 'codex'}>App Server</button><button type="button" role="tab" aria-selected={effectiveSource === 'easyagent'} className={effectiveSource === 'easyagent' ? 'active' : ''} onClick={() => setSource('easyagent')}>EasyAgent</button></div>
    {effectiveSource === 'session' ? <CodexSessionLogRecords page={sessionLog} loading={sessionLogLoading} error={sessionLogError} onRefresh={() => { setSessionLog(null); setSessionLogError('') }} onLoadMore={() => void loadSessionLog(sessionLog?.nextCursor || 0, true)} /> : <><p className="trace-raw-note">{effectiveSource === 'app-server' ? 'Codex app-server 的 JSON-RPC 请求与事件；这些原始消息已由 EasyAgent 实时采集并保存在 SQLite。' : 'EasyAgent 的完整运行事件；没有原始协议包的记录会显示规范化后的事件 JSON。'}</p><div className="trace-raw-list">{records.length === 0 ? <div className="trace-empty">还没有原始记录</div> : records.map((event) => <RawTraceRow event={event} key={event.id} />)}</div></>}
  </div>
}

function CodexSessionLogRecords({ page, loading, error, onRefresh, onLoadMore }: { page: CodexSessionLogPage | null; loading: boolean; error: string; onRefresh: () => void; onLoadMore: () => void }) {
  if (!page && loading) return <div className="trace-session-loading"><span className="spinner" />正在读取 Codex 持久化会话…</div>
  if (!page && error) return <div className="trace-session-state error"><strong>Session JSONL 读取失败</strong><span>{error}</span><button type="button" onClick={onRefresh}>重试</button></div>
  if (!page) return null
  if (!page.available) return <div className="trace-session-state"><strong>没有可读取的 Session JSONL</strong><span>{page.message || '当前会话只有 EasyAgent 保存的运行事件。'}</span><button type="button" onClick={onRefresh}>重新检测</button></div>
  return <>
    <div className="trace-session-summary"><div><strong>Codex Session JSONL</strong><span>{page.path}</span></div><div>{page.model && <span>模型 <b>{page.model}</b></span>}{page.source && <span>来源 <b>{page.source}</b></span>}<button type="button" onClick={onRefresh} disabled={loading}>{loading ? '刷新中…' : '刷新'}</button></div></div>
    <p className="trace-raw-note">Codex 持久化的完整会话日志，服务重启后仍可读取。内容按行解析并隐藏凭证字段；L 表示原文件行号。</p>
    <div className="trace-session-progress"><span>已显示 {page.lines.length.toLocaleString()} / {page.totalLines.toLocaleString()} 行</span><span>{formatBytes(page.totalBytes)}</span></div>
    <div className="trace-raw-list">{page.lines.map((line) => <CodexSessionLogRow value={line} key={line.line} />)}</div>
    {page.nextCursor ? <button type="button" className="trace-session-more" onClick={onLoadMore} disabled={loading}>{loading ? '加载中…' : `继续加载（从 L${page.nextCursor + 1}）`}</button> : null}
  </>
}

function CodexSessionLogRow({ value }: { value: CodexSessionLogLine }) {
  const label = codexSessionLogLabel(value.type, value.subtype)
  return <details className="trace-raw-row session-line"><summary><code>L{value.line}</code><div><strong>{label}</strong><small>{value.type}{value.subtype ? ` · ${value.subtype}` : ''} · {formatBytes(value.bytes)}{value.truncated ? ' · 内容已截断' : ''}</small></div><time>{formatTraceTime(value.timestamp || '')}</time><span className="trace-chevron" aria-hidden="true" /></summary><div className="trace-raw-row-body"><TracePayload value={value.payload} copyLabel={`复制 L${value.line}`} /></div></details>
}

function codexSessionLogLabel(type: string, subtype?: string) {
  if (type === 'session_meta') return 'Session 元数据'
  if (type === 'world_state') return '运行环境快照'
  if (type === 'turn_context') return '回合上下文'
  if (type === 'token_usage_record') return 'Token 使用记录'
  if (type === 'event_msg') {
    const labels: Record<string, string> = { task_started: '任务开始', task_complete: '任务完成', token_count: 'Token 统计', item_completed: '项目完成' }
    return labels[subtype || ''] || '运行事件'
  }
  if (type === 'response_item') {
    const labels: Record<string, string> = { message: '消息', reasoning: '模型推理', custom_tool_call: '工具请求', custom_tool_call_output: '工具结果' }
    return labels[subtype || ''] || '响应项目'
  }
  return subtype || type || '未知记录'
}

function RawTraceRow({ event }: { event: TraceEvent }) {
  const payload = event.rawPayload || JSON.stringify(event)
  const bytes = new TextEncoder().encode(payload).length
  return <details className={`trace-raw-row ${event.status}`}><summary><code>E{event.id}</code><div><strong>{event.protocolMethod || event.name || event.kind}</strong><small>{event.kind} · {formatBytes(bytes)}</small></div><time>{formatTraceTime(event.createdAt)}</time><span className="trace-chevron" aria-hidden="true" /></summary><div className="trace-raw-row-body"><TracePayload value={payload} copyLabel="复制原始记录" /></div></details>
}

function isAppServerEvent(event: TraceEvent) {
  return Boolean(event.rawPayload && (event.kind.startsWith('codex_') || event.protocol === 'codex_app_server'))
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${trimUnitValue(value / 1024)} KB`
  return `${trimUnitValue(value / 1024 / 1024)} MB`
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
  if (event.kind === 'codex_usage') return 'usage'
  if ((event.kind === 'codex_rpc' && event.protocolMethod === 'turn/start') || (event.kind === 'codex_item' && event.name === 'userMessage')) return 'input'
  if (event.kind === 'model_start' || event.kind === 'model_end' || event.kind === 'compaction_start' || event.kind === 'compaction_end' || event.kind === 'codex_end' || event.name === 'reasoning' || event.name === 'agentMessage') return 'llm'
  if (event.kind === 'tool_start' || event.kind === 'tool_end' || event.kind === 'capability' || event.activityKind === 'tool' || event.activityKind === 'mcp' || event.activityKind === 'skill' || event.activityKind === 'loader' || event.activityKind === 'mcp_loader') return 'tool'
  return 'status'
}

function traceCategoryLabel(value: ReturnType<typeof traceCategory>) {
  const labels: Record<ReturnType<typeof traceCategory>, string> = { input: '输入', llm: 'LLM', tool: '工具', usage: '用量', status: '状态' }
  return labels[value]
}

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
