import type { Session } from './types'
export function formatTime(value: string) { const date = new Date(value); return date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' }) + ' ' + date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }
export function statusLabel(value: Session['status']) { return value === 'idle' ? '完成' : value === 'queued' ? '排队中' : value === 'running' ? '运行中' : value === 'failed' ? '失败' : '已停止' }
export function formatDuration(ms: number) { return ms > 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms` }
export function formatTokens(value: number) { return value >= 1000 ? `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)}k` : value.toLocaleString() }
export function historyModeLabel(value: string) { return value === 'full_history' ? '完整历史' : value === 'provider_continuation' ? 'Provider 续接' : value === 'responses_full_input' ? 'Responses 全量' : value === 'summary_history' ? '摘要 + 最近历史' : '等待识别' }
export function recordLines(value: Record<string, string>) { return Object.entries(value || {}).map(([key, item]) => `${key}=${item}`).join('\n') }
export function parseRecord(value: string) { const result: Record<string, string> = {}; value.split('\n').forEach((line) => { const index = line.indexOf('='); if (index > 0) result[line.slice(0, index).trim()] = line.slice(index + 1) }); return result }
