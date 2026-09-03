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
  return <aside className="trace-panel"><div className="trace-head"><div><p className="eyebrow">运行记录</p><h2>Agent 轨迹</h2></div><button aria-label="关闭 Agent 轨迹" onClick={onClose}>×</button></div><div className="trace-runtime-banner"><strong>{runtimeName}</strong><span>{session.status === 'running' || session.status === 'queued' ? session.runProgress || '正在处理…' : session.status === 'failed' ? '本轮失败' : '本轮已完成'}</span></div><div className="metrics"><Metric label="模型调用" value={`${session.usage.modelCalls} 次`} sub={formatDuration(session.usage.modelDurationMs)} /><Metric label="工具调用" value={`${session.usage.toolCalls} 次`} sub={formatDuration(session.usage.toolDurationMs)} /><Metric label="Token" value={tokenValue} sub={tokenSub} /><Metric label="缓存" value={session.usage.cacheReported ? `${cacheRate}%` : isCodexRuntime ? 'Codex 未提供' : '未上报'} sub={session.usage.cacheReported ? `命中 ${session.usage.cachedTokens} · 写入 ${session.usage.cacheWriteTokens}` : isCodexRuntime ? '等待 thread/tokenUsage/updated' : 'Provider 未返回缓存字段'} /></div><div className="context-ledger"><div><span>最近上下文</span><strong>{context.lastInputTokens ? formatTokens(context.lastInputTokens) : isCodexRuntime ? 'Codex 未上报' : session.status === 'failed' ? '未上报' : '—'}{context.contextWindowTokens ? ` / ${formatTokens(context.contextWindowTokens)}` : ''}</strong></div><div><span>会话历史</span><strong>{isCodexRuntime ? `Codex thread · ${context.userTurns} 个用户轮次` : `${context.userTurns} 个用户轮次 · ${context.historyMessages} 条消息${session.messagesTruncated ? ' · 历史窗口' : ''}`}</strong></div><div><span>发送方式</span><strong>{isCodexRuntime ? 'Codex thread/resume' : historyModeLabel(context.historyMode)}</strong></div><div><span>压缩</span><strong>{isCodexRuntime ? 'Codex 管理' : context.compressionCount > 0 ? `${context.compressionCount} 次 · ${context.compressedMessages} 条` : context.compressionMode === 'auto' ? `自动 ${context.compressionThresholdPercent}%` : '已停用'}</strong></div></div>{session.eventsTruncated && <div className="trace-empty">运行记录较多；点击下方按钮加载更早记录</div>}{session.eventsHasMore && session.events.length > 0 && <button className="history-load-more" onClick={loadOlder} disabled={loadingOlder}>{loadingOlder ? '加载中…' : '加载更早记录'}</button>}<div className="trace-events">{session.events.length === 0 && <div className="trace-empty">还没有运行记录</div>}{session.events.map((event) => <TraceRow key={event.id} event={event} />)}</div></aside>
}

export function TraceRow({ event }: { event: TraceEvent }) {
  const isModelResult = event.kind === 'model_end' || event.kind === 'compaction_end' || event.kind === 'codex_end' || event.kind === 'codex_usage'
  const showsUsage = event.kind === 'model_end' || event.kind === 'compaction_end' || event.kind === 'codex_usage'
  const title = event.kind === 'model_start' ? '模型请求开始' : event.kind === 'model_end' ? `模型响应 · ${event.name || '模型'}` : event.kind === 'compaction_start' ? '准备压缩上下文' : event.kind === 'compaction_end' ? `上下文检查点 · ${event.name || '模型'}` : event.kind === 'codex_start' ? 'Codex Runtime 开始' : event.kind === 'codex_end' ? `Codex Runtime 响应 · ${event.name || 'Codex'}` : event.kind === 'codex_usage' ? 'Codex · 本轮用量' : event.kind === 'codex_turn' ? `Codex Turn · ${event.status === 'started' ? '开始' : '完成'}` : event.kind === 'tool_start' ? `工具开始 · ${event.name}` : event.kind === 'tool_end' ? `工具结果 · ${event.name}` : event.kind === 'codex_item' ? `Codex · ${codexItemLabel(event.name || '')}` : `MCP · ${event.name}`
  const cacheRate = event.cacheReported && event.inputTokens ? Math.round((event.cachedTokens || 0) / event.inputTokens * 100) : 0
  const tokenMissing = event.status === 'error' && !event.totalTokens && !event.inputTokens && !event.outputTokens
  const location = `${event.turn ? `第 ${event.turn} 轮 · ` : ''}${event.step ? `第 ${event.step} 步` : '独立阶段'}${event.attempt ? ` · 尝试 ${event.attempt}` : ''}`
  return <details className={`trace-row ${event.status}`} open={isModelResult && event.status === 'error'}><summary><span className="trace-node" /><div><strong>{title}</strong><small>{location} {event.statusCode ? `· HTTP ${event.statusCode} ` : ''}{eventDurationLabel(event)} {event.totalTokens ? `· ${event.totalTokens} tokens` : tokenMissing ? '· Token 未上报' : ''}{showsUsage ? ` · ${event.cacheReported ? `缓存 ${cacheRate}%` : '缓存未上报'}` : ''}</small></div><em>{eventStatusLabel(event.status)}</em></summary>{event.detail && <p className="event-error">{event.detail}</p>}{showsUsage && <div className="event-usage"><span>输入 <b>{tokenMissing ? '未上报' : (event.inputTokens || 0).toLocaleString()}</b></span><span>输出 <b>{tokenMissing ? '未上报' : (event.outputTokens || 0).toLocaleString()}</b></span><span>缓存命中 <b>{event.cacheReported ? (event.cachedTokens || 0).toLocaleString() : '未上报'}</b></span><span>缓存写入 <b>{event.cacheReported ? (event.cacheWriteTokens || 0).toLocaleString() : '未上报'}</b></span><span>历史 <b>{historyModeLabel(event.historyMode || '')} · {event.requestMessages || 0} 项</b></span><span>工具定义 <b>{event.toolDefinitions || 0}</b></span></div>}{event.input && <div><p className="trace-label">{isModelResult ? '模型请求 · 实际发送' : '工具输入'}</p><Payload value={event.input} /></div>}{event.output && (isModelResult ? <ModelTraceResponse value={event.output} /> : <div><p className="trace-label">工具响应 · 原始返回</p><Payload value={event.output} /></div>)}</details>
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

export function eventStatusLabel(status: string) { return status === 'started' ? '开始' : status === 'success' ? '成功' : status === 'error' ? '失败' : status }

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
