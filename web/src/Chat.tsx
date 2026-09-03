import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import type { ChangeEvent, KeyboardEvent } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { api } from './api'
import type { Bootstrap, Session } from './types'
import { isActive, mergeSessionSnapshot } from './sessionState'
import { formatTokens, historyModeLabel } from './format'
import { formatBytes, attachmentAccept, attachmentTypeLabel, encodeAttachment, supportedAttachment, type PendingAttachment } from './attachments'
import { capabilityKindLabel, capabilityMention, capabilityOptions, hasCapabilityToken, type CapabilityOption } from './capabilities'
import { starterSuggestions, type StarterSuggestion } from './suggestions'
import { AttachIcon, CloseIcon, FileIcon, Logo, Payload, SendIcon } from './ui'
import { CapabilityPicker } from './CapabilityPicker'
import { RunError } from './dialogs'

const MathMarkdown = lazy(() => import('./MathMarkdown'))
const hasMath = (value: string) => /\$\$[\s\S]+?\$\$|\$[^$\n]+?\$/.test(value)

export function Chat({ session, data, onSession, onRefresh, onError, onLoadOlder, onOpenSkills, onOpenCapabilities }: { session: Session | null; data: Bootstrap; onSession: (session: Session) => void; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void; onLoadOlder: (id: string, kind: 'messages' | 'events', before: number) => Promise<void>; onOpenSkills: () => void; onOpenCapabilities: () => void }) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [attachments, setAttachments] = useState<PendingAttachment[]>([])
  const [attachmentError, setAttachmentError] = useState('')
  const [dragging, setDragging] = useState(false)
  const [capabilityOpen, setCapabilityOpen] = useState(false)
  const [capabilityQuery, setCapabilityQuery] = useState('')
  const [capabilityIndex, setCapabilityIndex] = useState(0)
  const [capabilityRange, setCapabilityRange] = useState<{ start: number; end: number } | null>(null)
  const [selectedProfileId, setSelectedProfileId] = useState(data.activeModelProfileId)
  const [workspace, setWorkspace] = useState(session?.workspace || data.runtime.workspace)
  const endRef = useRef<HTMLDivElement>(null)
  const conversationRef = useRef<HTMLDivElement>(null)
  const loadingOlderRef = useRef(false)
  const composerRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const stickToBottomRef = useRef(true)
  const previousSessionIDRef = useRef<string | undefined>(undefined)
  const capabilitySearchRef = useRef<HTMLInputElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const attachmentRef = useRef<PendingAttachment[]>([])
  const runtime = session?.runtime || data.model.runtime
  const isCodexRuntime = runtime === 'codex'
  const capabilities = useMemo(() => isCodexRuntime ? [] : capabilityOptions(data), [data, isCodexRuntime])
  const visibleCapabilities = useMemo(() => {
    const keyword = capabilityQuery.trim().toLocaleLowerCase()
    return capabilities.filter((item) => !keyword || item.name.toLocaleLowerCase().includes(keyword) || item.description.toLocaleLowerCase().includes(keyword) || item.token.slice(1).toLocaleLowerCase().includes(keyword) || capabilityKindLabel(item.kind).toLocaleLowerCase().includes(keyword))
  }, [capabilities, capabilityQuery])
  const selectedCapabilities = useMemo(() => capabilities.filter((item) => hasCapabilityToken(draft, item.token)), [capabilities, draft])
  const enabledCapabilityCount = capabilities.filter((item) => item.enabled).length
  const enabledSkillCount = data.skills.filter((item) => item.enabled).length
  const enabledMCPCount = data.mcps.filter((item) => item.enabled).length
  const profileOptions = useMemo(() => data.modelProfiles.filter((item) => item.settings.runtime === runtime), [data.modelProfiles, runtime])
  const workspaceOptions = useMemo(() => Array.from(new Set([
    data.runtime.workspace,
    ...data.sessions.map((item) => item.workspace).filter(Boolean),
  ])), [data.runtime.workspace, data.sessions])
  // 只有用户当前就在底部时才跟随新消息/流式输出；用户向上阅读历史时不抢夺滚动位置。
  useEffect(() => {
    const node = conversationRef.current
    if (!node) return
    if (!session) {
      node.scrollTo({ top: 0, behavior: 'auto' })
      stickToBottomRef.current = false
      return
    }
    const sessionChanged = previousSessionIDRef.current !== session?.id
    previousSessionIDRef.current = session?.id
    if (sessionChanged) {
      node.scrollTo({ top: node.scrollHeight, behavior: 'auto' })
      stickToBottomRef.current = true
      return
    }
    if (stickToBottomRef.current) endRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [session?.id, session?.messages.at(-1)?.id, session?.status, session?.partialOutput])
  useEffect(() => {
    const node = conversationRef.current
    if (!node) return
    const updateStickiness = () => {
      stickToBottomRef.current = node.scrollHeight - node.scrollTop - node.clientHeight < 96
    }
    node.addEventListener('scroll', updateStickiness, { passive: true })
    return () => node.removeEventListener('scroll', updateStickiness)
  }, [])
  useEffect(() => {
    const node = conversationRef.current
    if (!node || !session?.messagesHasMore) return
    const loadOlder = async () => {
      if (loadingOlderRef.current || node.scrollTop > 120) return
      const first = session.messages[0]
      if (!first) return
      loadingOlderRef.current = true
      const previousHeight = node.scrollHeight
      const previousTop = node.scrollTop
      try {
        await onLoadOlder(session.id, 'messages', first.id)
        window.requestAnimationFrame(() => { node.scrollTop = node.scrollHeight - previousHeight + previousTop })
      } catch (reason) {
        onError((reason as Error).message)
      } finally {
        loadingOlderRef.current = false
      }
    }
    node.addEventListener('scroll', loadOlder)
    return () => node.removeEventListener('scroll', loadOlder)
  }, [session?.id, session?.messagesHasMore, session?.messages.length, onLoadOlder, onError])
  useEffect(() => {
    if (!textareaRef.current) return
    textareaRef.current.style.height = 'auto'
    textareaRef.current.style.height = `${Math.min(textareaRef.current.scrollHeight, 180)}px`
  }, [draft])
  useEffect(() => { attachmentRef.current = attachments }, [attachments])
  useEffect(() => () => attachmentRef.current.forEach((item) => item.preview && URL.revokeObjectURL(item.preview)), [])
  useEffect(() => { setCapabilityIndex(0) }, [capabilityQuery])
  useEffect(() => { setWorkspace(session?.workspace || data.runtime.workspace) }, [session?.id, session?.workspace, data.runtime.workspace])
  useEffect(() => { if (!session) setSelectedProfileId(data.activeModelProfileId) }, [data.activeModelProfileId, session?.id])
  useEffect(() => {
    if (!capabilityOpen) return
    const close = (event: PointerEvent) => {
      if (!composerRef.current?.contains(event.target as Node)) setCapabilityOpen(false)
    }
    document.addEventListener('pointerdown', close)
    return () => document.removeEventListener('pointerdown', close)
  }, [capabilityOpen])

  const addFiles = (files: FileList | File[]) => {
    if (sending || isActive(session?.status)) return
    const incoming = Array.from(files)
    if (!incoming.length) return
    setAttachmentError('')
    setAttachments((current) => {
      const next = [...current]
      let total = current.reduce((sum, item) => sum + item.file.size, 0)
      for (const file of incoming) {
        if (next.length >= 5) { setAttachmentError('每条消息最多添加 5 个附件'); break }
        if (file.size === 0) { setAttachmentError(`${file.name} 是空文件`); continue }
        if (file.size > 5 * 1024 * 1024) { setAttachmentError(`${file.name} 超过 5 MiB`); continue }
        if (!supportedAttachment(file)) { setAttachmentError(`${file.name} 暂不支持；请选择图片、UTF-8 文本/代码或 PDF`); continue }
        if (total + file.size > 10 * 1024 * 1024) { setAttachmentError('本条消息的附件总大小不能超过 10 MiB'); break }
        if (next.some((item) => item.file.name === file.name && item.file.size === file.size && item.file.lastModified === file.lastModified)) continue
        total += file.size
        next.push({ id: `${file.name}-${file.lastModified}-${crypto.randomUUID()}`, file, preview: file.type.startsWith('image/') ? URL.createObjectURL(file) : '' })
      }
      return next
    })
  }

  const removeAttachment = (id: string) => setAttachments((current) => current.filter((item) => {
    if (item.id !== id) return true
    if (item.preview) URL.revokeObjectURL(item.preview)
    return false
  }))

  const closeCapabilityPicker = () => {
    setCapabilityOpen(false)
    setCapabilityQuery('')
    setCapabilityRange(null)
  }

  const openCapabilityPicker = () => {
    if (sending || isActive(session?.status)) return
    const textarea = textareaRef.current
    setCapabilityRange({ start: textarea?.selectionStart ?? draft.length, end: textarea?.selectionEnd ?? draft.length })
    setCapabilityQuery('')
    setCapabilityOpen(true)
    window.setTimeout(() => capabilitySearchRef.current?.focus(), 0)
  }

  const insertCapability = (item: CapabilityOption) => {
    if (!item.enabled) return
    if (hasCapabilityToken(draft, item.token)) {
      closeCapabilityPicker()
      textareaRef.current?.focus()
      return
    }
    const range = capabilityRange || { start: draft.length, end: draft.length }
    const before = draft.slice(0, range.start)
    const after = draft.slice(range.end)
    const prefix = before && !/\s$/.test(before) ? ' ' : ''
    const suffix = after && /^\s/.test(after) ? '' : ' '
    const inserted = `${prefix}${item.token}${suffix}`
    const next = before + inserted + after
    const caret = before.length + inserted.length
    setDraft(next)
    closeCapabilityPicker()
    window.requestAnimationFrame(() => {
      textareaRef.current?.focus()
      textareaRef.current?.setSelectionRange(caret, caret)
    })
  }

  const removeCapability = (item: CapabilityOption) => {
    setDraft((current) => current.replace(item.token, '').replace(/ {2,}/g, ' ').trimStart())
  }

  const handleCapabilityKey = (event: KeyboardEvent) => {
    if (!capabilityOpen) return false
    if (event.key === 'Escape') {
      event.preventDefault()
      closeCapabilityPicker()
      textareaRef.current?.focus()
      return true
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const direction = event.key === 'ArrowDown' ? 1 : -1
      setCapabilityIndex((current) => visibleCapabilities.length ? (current + direction + visibleCapabilities.length) % visibleCapabilities.length : 0)
      return true
    }
    if (event.key === 'Enter' && visibleCapabilities[capabilityIndex]?.enabled) {
      event.preventDefault()
      insertCapability(visibleCapabilities[capabilityIndex])
      return true
    }
    return false
  }

  const updateDraft = (event: ChangeEvent<HTMLTextAreaElement>) => {
    const value = event.target.value
    const caret = event.target.selectionStart
    setDraft(value)
    const mention = capabilityMention(value, caret)
    if (mention) {
      setCapabilityRange({ start: mention.start, end: caret })
      setCapabilityQuery(mention.query)
      setCapabilityOpen(true)
    } else if (capabilityRange && document.activeElement === event.target) {
      closeCapabilityPicker()
    }
  }

  const send = async (preset?: string) => {
    const message = (preset ?? draft).trim()
    if ((!message && attachments.length === 0) || sending || isActive(session?.status)) return
    setSending(true); onError(''); setAttachmentError('')
    try {
      const payload = await Promise.all(attachments.map(encodeAttachment))
      const next = session ? await api.sendMessage(session.id, message, payload) : await api.createSession(message, payload, workspace.trim(), selectedProfileId)
      onSession(session ? mergeSessionSnapshot(session, next) : next); setDraft(''); closeCapabilityPicker(); attachments.forEach((item) => item.preview && URL.revokeObjectURL(item.preview)); setAttachments([]); await onRefresh()
    } catch (reason) {
      const message = (reason as Error).message
      if (/附件|Base64|MiB|格式/.test(message)) setAttachmentError(message)
      else onError(message)
    } finally { setSending(false) }
  }

  const startSuggestion = (suggestion: StarterSuggestion) => {
    if (suggestion.attachment) {
      setDraft(suggestion.prompt)
      textareaRef.current?.focus()
      window.setTimeout(() => fileInputRef.current?.click(), 0)
      return
    }
    send(suggestion.prompt)
  }
  return <section className="chat-page">
    <div className="conversation">
      <div ref={conversationRef} className="conversation-content">
      {!session && <div className="welcome"><div className="agent-orb"><Logo /></div><p className="eyebrow">一个核心 Agent · 能力按需加载</p><h1>想解决什么问题？</h1><p>直接描述目标，也可以添加代码、日志、图片或 PDF；输入 <code>@</code> 可明确指定 Tool、Skill 或 MCP。</p><div className="suggestion-heading"><strong>从一个场景开始</strong><span>点击即可运行；文件场景会先请你选择附件</span></div><div className="suggestions">{starterSuggestions.map((suggestion) => <button key={suggestion.category} onClick={() => startSuggestion(suggestion)} aria-label={`${suggestion.category}：${suggestion.title}`}><span className="suggestion-copy"><em>{suggestion.category}</em><strong>{suggestion.title}</strong></span><span className="suggestion-arrow">{suggestion.attachment ? '+' : '↗'}</span></button>)}</div></div>}
      {session && <ContextBar session={session} />}
      {session?.messagesTruncated && <div className="history-window-note">当前显示最近一段消息；向上滚动加载更早记录。原始历史仍保存在本地数据库，并参与 Agent 上下文处理。</div>}
      {session?.messages.map((message) => <MessageView key={message.id} message={message} />)}
      {session?.status === 'queued' && <div className="assistant-row"><Avatar /><div className="thinking queued" role="status" aria-live="polite"><i /><i /><i /><span>{session.runProgress || `${isCodexRuntime ? 'Codex' : 'EasyAgent'} · 任务排队中`}</span></div></div>}
      {session?.status === 'running' && (session.partialOutput
        ? <div className="assistant-row"><Avatar /><div className="assistant-message streaming-message"><div className="answer-text"><Markdown>{session.partialOutput}</Markdown></div></div></div>
        : <div className="assistant-row"><Avatar /><div className="thinking" role="status" aria-live="polite"><i /><i /><i /><span>{session.runProgress || `${isCodexRuntime ? 'Codex' : 'EasyAgent'} · 正在处理任务`}</span></div></div>)}
      {session?.status === 'failed' && <RunError error={session.error} ollamaRunning={data.ollama.running} retrying={sending} onRetry={() => {
        const lastUserMessage = session.messages.slice().reverse().find((message) => message.role === 'user')
        if (lastUserMessage) send(lastUserMessage.attachments?.length ? '请重新完成上一条包含附件的请求。' : lastUserMessage.content)
      }} onOpenCapabilities={onOpenCapabilities} />}
      {session?.status === 'canceled' && <div className="run-error canceled"><div className="run-error-mark" aria-hidden="true">■</div><div className="run-error-copy"><strong>任务已停止</strong><span>你可以继续发送新消息。</span></div></div>}
      <div ref={endRef} />
      </div>
    </div>
    <div className="composer-wrap"><div ref={composerRef} className={`composer ${dragging ? 'dragging' : ''}`} onDragEnter={(event) => { event.preventDefault(); setDragging(true) }} onDragOver={(event) => event.preventDefault()} onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setDragging(false) }} onDrop={(event) => { event.preventDefault(); setDragging(false); addFiles(event.dataTransfer.files) }}>
      {capabilityOpen && <CapabilityPicker items={visibleCapabilities} activeIndex={capabilityIndex} query={capabilityQuery} searchRef={capabilitySearchRef} onQuery={setCapabilityQuery} onKeyDown={handleCapabilityKey} onPick={insertCapability} onOpenSkills={onOpenSkills} onOpenCapabilities={onOpenCapabilities} />}
      {!session && profileOptions.length > 0 && <label className="profile-control" title="新会话会使用所选模型配置；已创建会话不会被切换影响">
        <span>模型</span>
        <select value={selectedProfileId} onChange={(event) => setSelectedProfileId(event.target.value)} disabled={sending} aria-label="新会话模型配置">
          {profileOptions.map((item) => <option key={item.id} value={item.id}>{item.name}{item.settings.model ? ` · ${item.settings.model}` : ''}</option>)}
        </select>
        <em>新会话</em>
      </label>}
      <label className={`workspace-control ${session ? 'locked' : ''}`} title={session ? '工作区在创建会话后固定；如需切换请新建会话' : '选择最近使用的工作区，或输入服务器上已经存在的目录'}>
        <span>工作区</span>
        <input list="easyagent-workspaces" value={workspace} readOnly={Boolean(session)} disabled={sending || isActive(session?.status)} onChange={(event) => setWorkspace(event.target.value)} placeholder={data.runtime.workspace} aria-label="会话工作区" />
        <em>{session ? '本会话已固定' : '新会话'}</em>
      </label>
      <datalist id="easyagent-workspaces">{workspaceOptions.map((item) => <option key={item} value={item} />)}</datalist>
      {attachments.length > 0 && <div className="attachment-preview-list" aria-label="待发送附件">{attachments.map((item) => <div className="attachment-preview" key={item.id}>{item.preview ? <img src={item.preview} alt={item.file.name} /> : <span className="attachment-file-icon"><FileIcon /></span>}<span><strong title={item.file.name}>{item.file.name}</strong><small>{attachmentTypeLabel(item.file)} · {formatBytes(item.file.size)}</small></span><button type="button" disabled={sending || isActive(session?.status)} aria-label={`移除附件 ${item.file.name}`} onClick={() => removeAttachment(item.id)}><CloseIcon /></button></div>)}</div>}
      {selectedCapabilities.length > 0 && <div className="selected-capabilities" aria-label="已指定能力">{selectedCapabilities.map((item) => <span key={item.key}><b>{capabilityKindLabel(item.kind)}</b>{item.name}<button type="button" aria-label={`移除 ${item.name}`} onClick={() => removeCapability(item)}>×</button></span>)}</div>}
      <textarea ref={textareaRef} value={draft} onChange={updateDraft} aria-label="消息内容" aria-describedby="composer-help attachment-error" placeholder={attachments.length ? '描述希望 Agent 如何处理这些附件…' : `${isCodexRuntime ? '给 Codex' : '给 EasyAgent'} 发消息…${isCodexRuntime ? '' : ' 输入 @ 选择能力'}`} rows={1} onPaste={(event) => { const files = Array.from(event.clipboardData.files); if (files.length) { event.preventDefault(); addFiles(files) } }} onKeyDown={(event) => { if (handleCapabilityKey(event)) return; if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }} />
      <div className="composer-toolbar"><div className="composer-tools"><button type="button" className="attach-button" disabled={sending || isActive(session?.status)} aria-label="添加文件或图片" onClick={() => fileInputRef.current?.click()}><AttachIcon /><span>附件</span></button>{!isCodexRuntime && <button type="button" className={`capability-button ${capabilityOpen ? 'active' : ''}`} disabled={sending || isActive(session?.status)} aria-label={`选择 Agent 能力，共 ${capabilities.length} 项，${enabledCapabilityCount} 项已启用`} aria-expanded={capabilityOpen} aria-haspopup="listbox" onClick={() => capabilityOpen ? closeCapabilityPicker() : openCapabilityPicker()}><span aria-hidden="true">@</span><strong>能力</strong><small>{capabilities.length}</small></button>}<small>{isCodexRuntime ? 'Codex app-server · 工具、Skill、沙箱由 Codex 管理' : `${enabledSkillCount}/${data.skills.length} Skills · ${data.builtinTools.length} Tools · ${enabledMCPCount}/${data.mcps.length} MCP`}</small><input ref={fileInputRef} className="visually-hidden" type="file" multiple tabIndex={-1} aria-hidden="true" accept={attachmentAccept} onChange={(event) => { if (event.target.files) addFiles(event.target.files); event.target.value = '' }} /></div><button type="button" className="send-button" aria-label={sending ? '正在发送' : '发送消息'} disabled={(!draft.trim() && attachments.length === 0) || sending || isActive(session?.status)} onClick={() => send()}>{sending ? <span className="send-spinner" /> : <SendIcon />}</button></div>
      {attachmentError && <div id="attachment-error" className="composer-error" role="alert">{attachmentError}</div>}
    </div><small id="composer-help" className="composer-hint">Enter 发送 · Shift + Enter 换行 · 可拖入或粘贴 · 单文件最大 5 MiB · 图片/PDF 需要当前模型支持多模态</small></div>
  </section>
}

export function MessageView({ message }: { message: Session['messages'][number] }) {
  if (message.role === 'tool') return <details className="tool-result" open={message.name === 'weather'}><summary><span>⌁</span>{message.name === 'weather' ? '天气预报' : message.name || '工具'} 返回结果</summary><ToolResult name={message.name || ''} value={message.content || ''} /></details>
  if (message.role === 'user') return <div className="user-row"><div className="user-message">{message.attachments?.length > 0 && <MessageAttachments attachments={message.attachments} />}{message.content && <div>{message.content}</div>}</div></div>
  if (message.role !== 'assistant') return null
  return <div className="assistant-row"><Avatar /><div className="assistant-message">{message.toolCalls?.length > 0 && <div className="tool-intent">{message.toolCalls.map((call) => <span key={call.id}>调用 {call.name}</span>)}</div>}{message.content && <div className="answer-text"><Markdown>{message.content}</Markdown></div>}</div></div>
}

type WeatherResult = {
  location?: { name?: string; admin1?: string; country?: string }
  observed_at?: string
  timezone?: string
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
  try {
    weather = JSON.parse(value) as WeatherResult
  } catch {
    return <Payload value={value} />
  }
  if (!weather.location || !Array.isArray(weather.forecast)) return <Payload value={value} />
  const place = [weather.location.name, weather.location.admin1, weather.location.country].filter(Boolean).join(' · ')
  return <div className="weather-result">
    <div className="weather-current">
      <div><strong>{place || '天气'}</strong><span>{weather.condition || '—'}</span></div>
      <b>{typeof weather.temperature_c === 'number' ? `${weather.temperature_c}°C` : '—'}</b>
      <small>体感 {formatWeatherNumber(weather.feels_like_c)}°C · 湿度 {formatWeatherNumber(weather.humidity_percent)}% · 风速 {formatWeatherNumber(weather.wind_kmh)} km/h</small>
      <small>{weather.observed_at ? `观测于 ${weather.observed_at}` : ''}{weather.source ? ` · ${weather.source}` : ''}</small>
    </div>
    <div className="weather-forecast" aria-label="未来天气预报">
      {weather.forecast.map((day, index) => <div className="weather-day" key={`${day.date || 'day'}-${index}`}><strong>{formatWeatherDate(day.date)}</strong><span>{day.condition || '—'}</span><b>{formatWeatherNumber(day.temp_max_c)}° / {formatWeatherNumber(day.temp_min_c)}°</b>{typeof day.precipitation_probability_percent === 'number' && <small>降水 {day.precipitation_probability_percent}%</small>}</div>)}
    </div>
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

export function MessageAttachments({ attachments }: { attachments: Session['messages'][number]['attachments'] }) {
  return <div className="message-attachments">{attachments.map((attachment) => attachment.kind === 'image'
    ? <a key={attachment.id} className="message-image" href={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} target="_blank" rel="noreferrer" title={`查看 ${attachment.name}`}><img src={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} alt={attachment.name} loading="lazy" /><span>{attachment.name}</span></a>
    : <a key={attachment.id} className="message-file" href={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} target="_blank" rel="noreferrer" download={attachment.name}><FileIcon /><span><strong>{attachment.name}</strong><small>{attachment.kind === 'pdf' ? 'PDF' : '文本文件'} · {formatBytes(attachment.size)}</small></span></a>)}</div>
}

export function Avatar() { return <div className="avatar"><Logo /></div> }
export function Markdown({ children }: { children: string }) {
  if (hasMath(children)) return <Suspense fallback={<ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>}><MathMarkdown>{children}</MathMarkdown></Suspense>
  return <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
}

export function ContextBar({ session }: { session: Session }) {
  const context = session.context
  const isCodexRuntime = session.runtime === 'codex'
  const tokenStatus = context.lastInputTokens > 0 ? formatTokens(context.lastInputTokens) : session.status === 'failed' ? '本轮未上报' : '等待模型上报'
  const cacheRate = context.cacheReported && context.lastInputTokens > 0 ? Math.round(context.lastCachedTokens / context.lastInputTokens * 100) : 0
  const utilization = context.contextWindowTokens > 0 && context.lastInputTokens > 0 ? Math.round(context.lastInputTokens / context.contextWindowTokens * 100) : 0
  const pressure = utilization >= 85 ? 'danger' : utilization >= 65 ? 'warning' : ''
  return <details className={`context-bar ${pressure}`}>
    <summary>
      <strong>上下文</strong>
      <span>{tokenStatus}{context.contextWindowTokens > 0 ? ` / ${formatTokens(context.contextWindowTokens)}` : ''}</span>
      <span>{context.userTurns} 个用户轮次 · {context.historyMessages} 条消息</span>
      <span>{historyModeLabel(context.historyMode)}</span>
      <span>{context.cacheReported ? `缓存 ${cacheRate}%` : isCodexRuntime ? 'Codex 缓存未上报' : '缓存未上报'}</span>
      <span className="context-workspace" title={session.workspace}>工作区 {workspaceName(session.workspace)}</span>
      <em>{context.compressionCount > 0 ? `已压缩 ${context.compressionCount} 次` : context.compressionMode === 'auto' ? `自动 ${context.compressionThresholdPercent}%` : '压缩停用'}</em>
    </summary>
    <div className="context-details">
      <ContextDatum label="最近一次模型输入" value={context.lastInputTokens > 0 ? `${context.lastInputTokens.toLocaleString()} Token` : session.status === 'failed' ? '本轮 Token 未上报' : '尚无数据'} hint={context.contextWindowTokens > 0 ? `模型窗口 ${context.contextWindowTokens.toLocaleString()} · 使用 ${utilization}%` : '模型没有提供窗口上限，请在“模型与工具”中填写'} />
      <ContextDatum label="会话历史" value={`${context.userTurns} 个用户轮次 · ${context.historyMessages} 条消息`} hint={`最近请求发送 ${context.requestMessages || '—'} 条消息项 · ${context.toolDefinitions || 0} 个工具定义`} />
      <ContextDatum label="缓存" value={context.cacheReported ? `命中 ${context.lastCachedTokens.toLocaleString()} · ${cacheRate}%` : isCodexRuntime ? 'Codex 未提供' : 'Provider 未上报'} hint={context.cacheReported ? `本次写入 ${context.lastCacheWriteTokens.toLocaleString()} Token` : isCodexRuntime ? 'Codex app-server 未返回 thread/tokenUsage/updated 的缓存字段' : '不等于确认没有缓存，只表示响应中没有缓存字段'} />
      <ContextDatum label="上下文压缩" value={context.compressionCount > 0 ? `${context.compressionCount} 次 · 摘要代表 ${context.compressedMessages} 条` : context.compressionMode === 'auto' ? `自动 · ${context.compressionThresholdPercent}% 触发` : '已停用'} hint={context.compressionCount > 0 ? `最近 ${context.retainedMessages} 条仍原样发送；SQLite 永久保留全部 ${context.historyMessages} 条消息` : '达到阈值后生成结构化检查点，并保留最近原始轮次；不会静默删除历史'} />
      <ContextDatum label="会话工作区" value={session.workspace || '默认工作区'} hint={session.workspace ? '文件、Shell 和 stdio MCP 都在这个目录中运行；切换工作区需要新建会话' : '该会话使用 EasyAgent 默认工作区'} />
    </div>
  </details>
}

function workspaceName(value: string) {
  const parts = value.replace(/[\\/]+$/, '').split(/[\\/]/)
  return parts[parts.length - 1] || '默认'
}

export function ContextDatum({ label, value, hint }: { label: string; value: string; hint: string }) {
  return <div><span>{label}</span><strong>{value}</strong><small>{hint}</small></div>
}
