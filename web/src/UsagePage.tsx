import { useEffect, useMemo, useState } from 'react'
import { api } from './api'
import { formatDuration, formatTokens } from './format'
import type { Bootstrap, UsageAggregate, UsageReport } from './types'

type UsagePeriod = UsageReport['period']

const periodOptions: { value: UsagePeriod; label: string; range: string }[] = [
  { value: 'day', label: '按天', range: '最近 30 天' },
  { value: 'week', label: '按周', range: '最近 12 周' },
  { value: 'month', label: '按月', range: '最近 12 个月' },
]

export function UsagePage({ data }: { data: Bootstrap }) {
  const [period, setPeriod] = useState<UsagePeriod>('day')
  const [report, setReport] = useState<UsageReport | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    api.usage(period).then((next) => {
      if (!cancelled) setReport(next)
    }).catch((reason) => {
      if (!cancelled) setError((reason as Error).message)
    }).finally(() => {
      if (!cancelled) setLoading(false)
    })
    return () => { cancelled = true }
  }, [period])

  const items = report?.items || []
  const totals = useMemo(() => items.reduce((result, item) => ({
    totalTokens: result.totalTokens + item.totalTokens,
    modelCalls: result.modelCalls + item.modelCalls,
    toolCalls: result.toolCalls + item.toolCalls,
    sessions: result.sessions + item.sessions,
  }), { totalTokens: 0, modelCalls: 0, toolCalls: 0, sessions: 0 }), [items])

  const periods = useMemo(() => {
    const grouped = new Map<string, { periodStart: string; totalTokens: number; modelCalls: number; toolCalls: number }>()
    items.forEach((item) => {
      const current = grouped.get(item.periodStart) || { periodStart: item.periodStart, totalTokens: 0, modelCalls: 0, toolCalls: 0 }
      current.totalTokens += item.totalTokens
      current.modelCalls += item.modelCalls
      current.toolCalls += item.toolCalls
      grouped.set(item.periodStart, current)
    })
    return Array.from(grouped.values()).sort((left, right) => left.periodStart.localeCompare(right.periodStart))
  }, [items])

  const models = useMemo(() => {
    const grouped = new Map<string, UsageAggregate>()
    items.forEach((item) => {
      // 用量按真实 Runtime + 模型合并；同一模型被多个配置使用时不要拆成几行。
      const key = `${item.runtime}:${item.model}`
      const current = grouped.get(key)
      if (!current) { grouped.set(key, { ...item }); return }
      if (current.profileId !== item.profileId) current.profileId = undefined
      current.sessions += item.sessions
      current.inputTokens += item.inputTokens
      current.outputTokens += item.outputTokens
      current.cachedTokens += item.cachedTokens
      current.cacheWriteTokens += item.cacheWriteTokens
      current.totalTokens += item.totalTokens
      current.modelCalls += item.modelCalls
      current.toolCalls += item.toolCalls
      current.modelDurationMs += item.modelDurationMs
      current.toolDurationMs += item.toolDurationMs
      current.cacheReported = current.cacheReported || item.cacheReported
    })
    return Array.from(grouped.values()).sort((left, right) => right.totalTokens - left.totalTokens)
  }, [items])

  const maxPeriodTokens = Math.max(...periods.map((item) => item.totalTokens), 1)
  const modelName = (item: UsageAggregate) => data.modelProfiles.find((profile) => profile.id === item.profileId)?.name || item.model
  const formatPeriod = (value: string) => new Date(value).toLocaleDateString('zh-CN', period === 'month' ? { year: 'numeric', month: 'long' } : { month: 'short', day: 'numeric' })

  return <section className="usage-page">
    <div className="page-intro"><p className="eyebrow">运行统计</p><h1>用量</h1><p>按时间和模型查看已经落库的实际调用量。只统计模型和工具完成事件，不根据页面状态估算。</p></div>
    <div className="usage-toolbar"><div className="usage-period-tabs" role="tablist" aria-label="用量统计周期">{periodOptions.map((option) => <button key={option.value} className={period === option.value ? 'active' : ''} type="button" role="tab" aria-selected={period === option.value} onClick={() => setPeriod(option.value)}><strong>{option.label}</strong><small>{option.range}</small></button>)}</div><span className="usage-updated">{loading ? '正在读取…' : report ? `更新于 ${new Date(report.generatedAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}` : '—'}</span></div>
    {error && <div className="usage-error" role="alert">{error}</div>}
    <div className="usage-kpis" aria-label="用量摘要"><div><span>总 Token</span><strong>{formatTokens(totals.totalTokens)}</strong><small>输入 {formatTokens(items.reduce((sum, item) => sum + item.inputTokens, 0))} · 输出 {formatTokens(items.reduce((sum, item) => sum + item.outputTokens, 0))}</small></div><div><span>模型调用</span><strong>{totals.modelCalls.toLocaleString()}</strong><small>{totals.sessions.toLocaleString()} 个会话参与统计</small></div><div><span>工具调用</span><strong>{totals.toolCalls.toLocaleString()}</strong><small>EasyAgent 工具和 MCP</small></div><div><span>模型数</span><strong>{models.length}</strong><small>按 Runtime 和模型拆分</small></div></div>
    <div className="usage-grid">
      <section className="usage-panel"><div className="usage-panel-head"><div><p className="eyebrow">趋势</p><h2>调用量变化</h2></div><span>{periodOptions.find((option) => option.value === period)?.range}</span></div>{loading && <div className="usage-empty">正在读取用量…</div>}{!loading && periods.length === 0 && <div className="usage-empty"><strong>暂无用量记录</strong><span>完成一次 EasyAgent 或 Codex turn 后，这里会显示真实统计。</span></div>}{!loading && periods.length > 0 && <div className="usage-period-list">{periods.map((item) => <div className="usage-period-row" key={item.periodStart}><div className="usage-period-label"><strong>{formatPeriod(item.periodStart)}</strong><small>{item.modelCalls} 次模型调用 · {item.toolCalls} 次工具调用</small></div><div className="usage-period-track" aria-label={`${formatPeriod(item.periodStart)} ${formatTokens(item.totalTokens)}`}><span style={{ width: `${Math.max(3, item.totalTokens / maxPeriodTokens * 100)}%` }} /></div><strong className="usage-period-value">{formatTokens(item.totalTokens)}</strong></div>)}</div>}</section>
      <section className="usage-panel"><div className="usage-panel-head"><div><p className="eyebrow">按模型</p><h2>模型明细</h2></div><span>{models.length} 个模型</span></div>{!loading && models.length === 0 && <div className="usage-empty"><strong>还没有模型数据</strong><span>统计会随实际完成的请求自动产生。</span></div>}{models.length > 0 && <div className="usage-model-list">{models.map((item) => <div className="usage-model-row" key={`${item.runtime}:${item.model}`}><div className="usage-model-title"><span className={`usage-runtime-dot ${item.runtime}`} /><div><strong>{modelName(item)}</strong><small>{item.runtime === 'codex' ? 'Codex Runtime' : 'EasyAgent Runtime'} · {item.model}</small></div></div><div className="usage-model-numbers"><strong>{formatTokens(item.totalTokens)}</strong><span>{item.modelCalls} 次调用 · {item.toolCalls} 个工具</span></div><div className="usage-model-meta"><span>输入 {formatTokens(item.inputTokens)} · 输出 {formatTokens(item.outputTokens)}</span><span>{item.cacheReported ? `缓存 ${formatTokens(item.cachedTokens)} · 写入 ${formatTokens(item.cacheWriteTokens)}` : '缓存未上报'}</span><span>模型耗时 {formatDuration(item.modelDurationMs)}</span></div></div>)}</div>}</section>
    </div>
    <p className="usage-footnote">用量来自本地 SQLite 事件记录；删除会话会同时删除该会话的统计。Codex 是否包含缓存字段取决于 app-server 返回内容。</p>
  </section>
}
