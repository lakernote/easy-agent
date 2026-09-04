import type { Message, Session, ToolCall, TraceEvent } from '../types'

export type CapabilityPresentation = {
  kind: 'tool' | 'loader' | 'skill' | 'mcp'
  label: string
  name: string
  resultLabel?: string
}

const groupLabels: Record<string, string> = {
  execution: '执行', files: '文件', information: '信息', skills: 'Skills', web: 'Web',
}

export function describeToolCall(call: ToolCall): CapabilityPresentation {
  const input = parseArguments(call.arguments)
  if (call.activityKind === 'loader' || call.name === 'load_tools') {
    const groups = Array.isArray(input.groups) ? input.groups.map((value) => groupLabels[String(value)] || String(value)) : []
    return { kind: 'loader', label: '加载能力', resultLabel: '能力加载结果', name: groups.join(' / ') || call.displayName || '内置能力' }
  }
  if (call.activityKind === 'skill' || call.name === 'load_skill') {
    return { kind: 'skill', label: '使用 Skill', resultLabel: 'Skill 加载结果', name: String(input.name || call.displayName || 'Skill') }
  }
  if (call.activityKind === 'mcp_loader' || call.name === 'search_mcp_tools') {
    return { kind: 'mcp', label: '发现 MCP 工具', resultLabel: 'MCP 发现结果', name: formatSource(String(input.id || call.activitySource || 'MCP')) }
  }
  const legacyMCP = /^mcp__(.+?)__(.+)$/.exec(call.name)
  if (call.activityKind === 'mcp' || legacyMCP) {
    const source = formatSource(call.activitySource || legacyMCP?.[1] || 'MCP')
    const tool = call.displayName || legacyMCP?.[2] || call.name
    return { kind: 'mcp', label: '调用 MCP', resultLabel: 'MCP 返回结果', name: `${source} / ${tool}` }
  }
  return { kind: 'tool', label: '调用 Tool', resultLabel: 'Tool 返回结果', name: call.displayName || call.name }
}

export function capabilityResultLabel(call: ToolCall | undefined, fallbackName: string) {
  const item = call ? describeToolCall(call) : describeToolCall({ id: '', name: fallbackName, arguments: '' })
  return `${item.resultLabel || '能力返回结果'} · ${item.name}`
}

export function SelectedCapabilities({ message }: { message: Message }) {
  const matches = [...(message.content || '').matchAll(/@(skill|tool|mcp):([a-z0-9][a-z0-9._-]*)/gi)]
  const seen = new Set<string>()
  const values = matches.flatMap((match) => {
    const key = `${match[1].toLowerCase()}:${match[2].toLowerCase()}`
    if (seen.has(key)) return []
    seen.add(key)
    const kind = match[1].toLowerCase()
    const label = kind === 'skill' ? '应用 Skill' : kind === 'mcp' ? '选择 MCP' : '指定 Tool'
    return [{ key, kind, label, name: formatSource(match[2]) }]
  })
  if (values.length === 0) return null
  return <div className="selected-capabilities" aria-label="本轮选择的能力">{values.map((item) => <span className={item.kind} key={item.key}><b>{item.label}</b>{item.name}</span>)}</div>
}

export function codexConversationActivities(events: TraceEvent[]) {
  const selected = events.filter((event) => event.kind === 'codex_item' && (event.activityKind === 'tool' || event.activityKind === 'mcp' || ['commandExecution', 'fileChange', 'mcpToolCall', 'webSearch', 'imageView', 'dynamicToolCall'].includes(event.name || '')))
  const byID = new Map<string, TraceEvent>()
  const withoutID: TraceEvent[] = []
  for (const event of selected) {
    if (event.activityId) {
      const previous = byID.get(event.activityId)
      byID.set(event.activityId, previous && Date.parse(previous.createdAt) < Date.parse(event.createdAt) ? { ...event, createdAt: previous.createdAt } : event)
    }
    else if (event.status !== 'started') withoutID.push(event)
  }
  return [...byID.values(), ...withoutID].sort(compareCreatedAt)
}

export function CodexActivity({ event }: { event: TraceEvent }) {
  const isMCP = event.activityKind === 'mcp' || event.name === 'mcpToolCall'
  const source = formatSource(event.activitySource || (isMCP ? event.detail?.split('/')[0]?.trim() : 'Codex') || 'MCP')
  const files = event.name === 'fileChange' ? parseFileChanges(event.input) : []
  const fileStats = summarizeFileChanges(files)
  const command = event.name === 'commandExecution' ? compactCommand(event.input) : ''
  const display = files.length > 0 ? files.map((item) => item.path).join(' · ') : command ? `Shell · ${command}` : event.displayName || (isMCP ? event.detail?.split('/').slice(1).join('/').trim() : codexToolLabel(event.name || ''))
  const state = event.status === 'error' ? '失败' : event.status === 'started' ? '进行中' : '完成'
  return <div className={`conversation-activity ${isMCP ? 'mcp' : files.length > 0 ? 'files' : 'tool'} ${event.status}`} role="status" title={files.length > 0 ? files.map((item) => item.path).join('\n') : undefined}>
    <span>{isMCP ? 'MCP' : files.length > 0 ? 'Files' : 'Tool'}</span><strong>{isMCP ? `${source} / ${display || '工具'}` : display || 'Codex 工具'}</strong><small>{files.length > 0 ? `${files.length} 个文件 · +${fileStats.additions} −${fileStats.deletions}` : state}{event.durationMs ? ` · ${formatDuration(event.durationMs)}` : ''}</small>
  </div>
}

export function ExecutionProgress({ session }: { session: Session }) {
  const events = session.events
  const planEvent = [...events].reverse().find((event) => event.name === 'plan' && event.activityKind === 'plan')
  const plan = parsePlan(planEvent?.output)
  const planIndex = plan.findIndex((item) => item.status === 'inProgress')
  const pendingIndex = plan.findIndex((item) => item.status === 'pending')
  const currentIndex = planIndex >= 0 ? planIndex : pendingIndex >= 0 ? pendingIndex : Math.max(0, plan.length - 1)
  const agentStep = events.reduce((maximum, event) => Math.max(maximum, event.step || 0), 0)
  const files = summarizeSessionFileChanges(events)
  const businessCalls = countBusinessCalls(events)
  const label = plan.length > 0 ? `第 ${currentIndex + 1}/${plan.length} 步` : agentStep > 0 ? `第 ${agentStep} 步` : session.runtime === 'codex' ? 'Codex 准备中' : 'Agent 准备中'
  const detail = plan[currentIndex]?.step || session.runProgress?.replace(/^(Codex|EasyAgent)\s*·\s*/, '') || '正在处理任务'
  return <div className="execution-progress" role="status" aria-live="polite">
    <span className="execution-progress-spinner" aria-hidden="true" /><strong>{label}</strong><span>{detail}</span>
    {files.count > 0 ? <em><b>{files.count} 个文件</b><i>+{files.additions}</i><u>−{files.deletions}</u></em> : businessCalls > 0 ? <em><b>{businessCalls} 次能力调用</b></em> : null}
  </div>
}

type FileChangeKind = string | { type?: string; move_path?: string | null }
type FileChange = { path: string; kind: FileChangeKind; diff: string }
type PlanStep = { step: string; status: string }

function parsePlan(value?: string): PlanStep[] {
  try {
    const parsed = JSON.parse(value || '{}')
    if (!Array.isArray(parsed?.plan)) return []
    return parsed.plan.filter((item: unknown): item is PlanStep => Boolean(item && typeof item === 'object' && typeof (item as PlanStep).step === 'string'))
  } catch { return [] }
}

function parseFileChanges(value?: string): FileChange[] {
  try {
    const parsed = JSON.parse(value || '[]')
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item: unknown): item is FileChange => Boolean(item && typeof item === 'object' && typeof (item as FileChange).path === 'string')).map((item) => ({ path: item.path, kind: item.kind || 'update', diff: item.diff || '' }))
  } catch { return [] }
}

function summarizeFileChanges(files: FileChange[]) {
  let additions = 0
  let deletions = 0
  for (const file of files) {
    let fileAdditions = 0
    let fileDeletions = 0
    for (const line of file.diff.split('\n')) {
      if (line.startsWith('+') && !line.startsWith('+++')) fileAdditions++
      if (line.startsWith('-') && !line.startsWith('---')) fileDeletions++
    }
    const kind = typeof file.kind === 'string' ? file.kind : file.kind?.type || 'update'
    const contentLines = file.diff.split('\n').filter(Boolean).length
    additions += fileAdditions || (kind === 'add' ? contentLines : 0)
    deletions += fileDeletions || (kind === 'delete' ? contentLines : 0)
  }
  return { additions, deletions }
}

function summarizeSessionFileChanges(events: TraceEvent[]) {
  const byOperation = new Map<string, TraceEvent>()
  for (const event of events) {
    if (event.name !== 'fileChange') continue
    byOperation.set(event.activityId || String(event.id), event)
  }
  const changes = [...byOperation.values()].flatMap((event) => parseFileChanges(event.input))
  return { count: new Set(changes.map((file) => file.path)).size, ...summarizeFileChanges(changes) }
}

function countBusinessCalls(events: TraceEvent[]) {
  const ids = new Set<string>()
  let withoutID = 0
  for (const event of events) {
    const easyAgentCall = event.kind === 'tool_end' && !['loader', 'skill', 'mcp_loader'].includes(event.activityKind || '') && !['load_tools', 'load_skill', 'search_mcp_tools'].includes(event.name || '')
    const codexCall = event.kind === 'codex_item' && event.status !== 'started' && (event.activityKind === 'tool' || event.activityKind === 'mcp')
    if (!easyAgentCall && !codexCall) continue
    if (event.activityId) ids.add(event.activityId)
    else withoutID++
  }
  return ids.size + withoutID
}

function compactCommand(value?: string) {
  const normalized = (value || '').replace(/\s+/g, ' ').trim()
  if (!normalized) return ''
  return normalized.length > 88 ? `${normalized.slice(0, 85)}…` : normalized
}

function parseArguments(value: string): Record<string, unknown> {
  try {
    const parsed = JSON.parse(value || '{}')
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {}
  } catch { return {} }
}

function formatSource(value: string) {
  const known: Record<string, string> = { context7: 'Context7', github: 'GitHub', playwright: 'Playwright', 'openai-docs': 'OpenAI Docs' }
  return known[value.toLowerCase()] || value.replaceAll('_', '-')
}

function codexToolLabel(value: string) {
  const labels: Record<string, string> = { commandExecution: 'Shell', fileChange: '文件修改', webSearch: 'Web Search', imageView: '图片查看', dynamicToolCall: '动态工具' }
  return labels[value] || value
}

function formatDuration(ms: number) { return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms` }
function compareCreatedAt(left: TraceEvent, right: TraceEvent) { return Date.parse(left.createdAt) - Date.parse(right.createdAt) || left.id - right.id }
