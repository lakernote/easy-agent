import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { api } from './api'
import type { AttachmentInput, Bootstrap, MCPConfig, ModelSettings, Session, Skill, TraceEvent } from './types'

type Page = 'chat' | 'skills' | 'capabilities'
type CapabilityKind = 'skill' | 'tool' | 'mcp'
type CapabilityOption = {
  key: string
  kind: CapabilityKind
  name: string
  description: string
  enabled: boolean
  token: string
}

type StarterSuggestion = {
  category: string
  title: string
  prompt: string
  attachment?: boolean
}

const starterSuggestions: StarterSuggestion[] = [
  { category: '实时查询', title: '今天星期几？', prompt: '今天星期几？请使用可用工具核验后回答。' },
  { category: '精确计算', title: '计算一个表达式', prompt: '请使用计算工具算出 (128*35+640)/8，并给出结果。' },
  { category: '本机操作', title: '看看当前工作目录', prompt: '请使用 shell 查看当前工作目录，并根据目录内容概括这是个什么项目。' },
  { category: '联网研究', title: '查找最新公开资料', prompt: '请搜索 EasyAgent 的 GitHub 仓库，概括它当前的定位、主要能力并附上来源。' },
  { category: '代码实现', title: '写一个最小 HTTP API', prompt: '请用 Go 设计一个带 /health 健康检查的最小 HTTP API，并解释如何运行。' },
  { category: '故障排查', title: '分析连接失败', prompt: '请分析这个错误的可能根因并给出排查顺序：dial tcp 127.0.0.1:5432: connect: connection refused' },
  { category: '文件理解', title: '总结 PDF 或代码文件', prompt: '请阅读我上传的文件，先概括核心内容，再提取风险、结论和待办事项。', attachment: true },
  { category: '图片分析', title: '分析报错截图', prompt: '请读取我上传的截图，提取其中的错误信息，分析最可能的原因并给出解决步骤。', attachment: true },
]

const isActive = (status?: Session['status']) => status === 'queued' || status === 'running'
const MathMarkdown = lazy(() => import('./MathMarkdown'))
const hasMath = (value: string) => /\$\$[\s\S]+?\$\$|\$[^$\n]+?\$/.test(value)

function App() {
  const [data, setData] = useState<Bootstrap | null>(null)
  const [session, setSession] = useState<Session | null>(null)
  const [page, setPage] = useState<Page>('chat')
  const [traceOpen, setTraceOpen] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    const next = await api.bootstrap()
    setData(next)
    return next
  }, [])

  useEffect(() => {
    refresh().catch((reason) => setError(reason.message)).finally(() => setLoading(false))
  }, [refresh])

  const openSession = useCallback(async (id: string) => {
    setPage('chat')
    setError('')
    try { setSession(await api.session(id)) } catch (reason) { setError((reason as Error).message) }
  }, [])

  useEffect(() => {
    if (!session || !isActive(session.status)) return
    const timer = window.setInterval(async () => {
      try {
        const current = await api.session(session.id)
        setSession(current)
        if (!isActive(current.status)) await refresh()
      } catch (reason) { setError((reason as Error).message) }
    }, 800)
    return () => window.clearInterval(timer)
  }, [session?.id, session?.status, refresh])

  const newChat = () => { setSession(null); setPage('chat'); setTraceOpen(false); setError('') }
  const stopSession = async () => {
    if (!session || !isActive(session.status)) return
    try {
      setSession(await api.cancelSession(session.id))
      await refresh()
    } catch (reason) { setError((reason as Error).message) }
  }

  if (loading) return <div className="boot"><span className="spinner" />正在启动 EasyAgent…</div>
  if (!data) return <div className="boot error-page">无法读取服务：{error || '未知错误'}</div>

  const usesOllama = data.model.provider === 'ollama' || data.model.baseUrl.includes(':11434')
  const modelReady = Boolean(data.model.model) && (!usesOllama || data.ollama.running)
  const modelLabel = !data.model.model ? '未配置模型' : usesOllama && !data.ollama.running ? 'Ollama 未运行' : data.model.model

  return <div className="app-shell">
    <Sidebar page={page} data={data} session={session} onPage={setPage} onOpen={openSession} onNew={newChat} onRefresh={refresh} onError={setError} />
    <main className="main-canvas">
      <header className="topbar">
        <div className="mobile-brand"><Logo /></div>
        <div className="topbar-title">{page === 'chat' ? (session?.title || '新会话') : page === 'skills' ? 'Skills' : '模型与工具'}</div>
        <div className="topbar-actions"><span className={`model-dot ${modelReady ? 'ready' : ''}`} /><span className="model-name">{modelLabel}</span>{page === 'chat' && session && isActive(session.status) && <button className="stop-button" onClick={stopSession}>停止</button>}{page === 'chat' && session && <button className="ghost-button" onClick={() => setTraceOpen(!traceOpen)}>Trace · {session.events.length}</button>}</div>
      </header>
      {error && <div className="toast" role="alert"><span>{friendlyError(error)}</span><button aria-label="关闭错误提示" onClick={() => setError('')}>×</button></div>}
      {page === 'chat' && <Chat session={session} data={data} onSession={setSession} onRefresh={refresh} onError={setError} onOpenSkills={() => setPage('skills')} onOpenCapabilities={() => setPage('capabilities')} />}
      {page === 'skills' && <Skills data={data} onRefresh={refresh} onError={setError} />}
      {page === 'capabilities' && <Capabilities data={data} onRefresh={refresh} onError={setError} />}
    </main>
    {traceOpen && session && <TracePanel session={session} onClose={() => setTraceOpen(false)} />}
  </div>
}

function Sidebar({ page, data, session, onPage, onOpen, onNew, onRefresh, onError }: { page: Page; data: Bootstrap; session: Session | null; onPage: (page: Page) => void; onOpen: (id: string) => void; onNew: () => void; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<'newest' | 'oldest'>('newest')
  const [managing, setManaging] = useState(false)
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [deleting, setDeleting] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<Session[]>([])
  const [feedback, setFeedback] = useState('')

  const visibleSessions = useMemo(() => {
    const keyword = query.trim().toLocaleLowerCase()
    return data.sessions
      .filter((item) => !keyword || item.title.toLocaleLowerCase().includes(keyword) || (item.model || '').toLocaleLowerCase().includes(keyword))
      .slice()
      .sort((left, right) => {
        const difference = new Date(left.updatedAt).getTime() - new Date(right.updatedAt).getTime()
        return sort === 'newest' ? -difference : difference
      })
  }, [data.sessions, query, sort])

  const selectableSessions = visibleSessions.filter((item) => !isActive(item.status))
  const selectedCount = selectableSessions.filter((item) => selectedIds.has(item.id)).length
  const allSelected = selectableSessions.length > 0 && selectableSessions.every((item) => selectedIds.has(item.id))

  const showFeedback = (value: string) => {
    setFeedback(value)
    window.setTimeout(() => setFeedback(''), 2400)
  }

  const requestRemove = (item: Session) => setPendingDelete([item])

  const toggleSelected = (id: string) => setSelectedIds((current) => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })

  const toggleAll = () => setSelectedIds((current) => {
    const next = new Set(current)
    if (allSelected) selectableSessions.forEach((item) => next.delete(item.id))
    else selectableSessions.forEach((item) => next.add(item.id))
    return next
  })

  const requestRemoveSelected = () => {
    const targets = selectableSessions.filter((item) => selectedIds.has(item.id))
    if (targets.length) setPendingDelete(targets)
  }

  const confirmRemove = async () => {
    if (!pendingDelete.length || deleting) return
    setDeleting(true); onError('')
    let removed = 0
    try {
      for (const item of pendingDelete) { await api.deleteSession(item.id); removed += 1 }
      if (session && pendingDelete.some((item) => item.id === session.id)) onNew()
      setSelectedIds(new Set()); setManaging(false)
      await onRefresh()
      showFeedback(removed === 1 ? '会话已删除' : `已删除 ${removed} 条会话`)
    } catch (reason) {
      await onRefresh().catch(() => undefined)
      onError(`${removed ? `已删除 ${removed} 条；` : ''}${(reason as Error).message}`)
    } finally { setDeleting(false); setPendingDelete([]) }
  }

  const leaveManaging = () => { setManaging(false); setSelectedIds(new Set()) }
  return <aside className="sidebar">
    <div className="brand"><div className="brand-mark"><Logo /></div><div><strong>EasyAgent</strong><small>轻量 · 自托管 · 可扩展</small></div></div>
    <button className="new-chat" onClick={onNew}><span>＋</span> 新会话 <kbd>⌘ K</kbd></button>
    <nav className="primary-nav">
      <button className={page === 'chat' ? 'active' : ''} onClick={() => onPage('chat')}><Icon name="chat" />对话</button>
      <button className={page === 'skills' ? 'active' : ''} onClick={() => onPage('skills')}><Icon name="skill" />Skills <em title={`已启用 ${data.skills.filter((item) => item.enabled).length} / 共 ${data.skills.length} 个`}>{data.skills.filter((item) => item.enabled).length}/{data.skills.length}</em></button>
      <button className={page === 'capabilities' ? 'active' : ''} onClick={() => onPage('capabilities')}><Icon name="plug" />模型与工具</button>
    </nav>
    <div className="session-label"><span>会话 <small>{data.sessions.length}</small></span><div><button onClick={managing ? leaveManaging : () => setManaging(true)}>{managing ? '完成' : '管理'}</button><button aria-label="刷新会话" title="刷新会话" onClick={() => onRefresh().catch((reason) => onError(reason.message))}>↻</button></div></div>
    <div className="session-controls">
      <label className="session-search"><span aria-hidden="true">⌕</span><input type="search" value={query} onChange={(event) => { setQuery(event.target.value); setSelectedIds(new Set()) }} placeholder="搜索标题或模型" aria-label="搜索会话" /></label>
      <select value={sort} onChange={(event) => setSort(event.target.value as 'newest' | 'oldest')} aria-label="按时间排序"><option value="newest">最新</option><option value="oldest">最早</option></select>
    </div>
    {managing && <div className="session-manage"><button onClick={toggleAll} disabled={!selectableSessions.length}>{allSelected ? '取消全选' : '全选'}</button><span>已选 {selectedCount}</span><button className="manage-delete" onClick={requestRemoveSelected} disabled={!selectedCount || deleting}>{deleting ? '删除中…' : `删除${selectedCount ? ` (${selectedCount})` : ''}`}</button></div>}
    <div className="session-list">
      {data.sessions.length === 0 && <div className="empty-list">还没有对话</div>}
      {data.sessions.length > 0 && visibleSessions.length === 0 && <div className="empty-list"><strong>没有匹配的会话</strong><button onClick={() => setQuery('')}>清空搜索</button></div>}
      {visibleSessions.map((item) => <div key={item.id} className={`session-row ${session?.id === item.id ? 'active' : ''} ${managing ? 'managing' : ''}`}>
        {managing && <label className="session-select" title={isActive(item.status) ? '运行中的会话不能删除' : '选择会话'}><input type="checkbox" checked={selectedIds.has(item.id)} disabled={isActive(item.status)} onChange={() => toggleSelected(item.id)} aria-label={`选择会话 ${item.title}`} /></label>}
        <button className="session-open" onClick={() => onOpen(item.id)} aria-current={session?.id === item.id ? 'page' : undefined} title={item.title}><span className={`status ${item.status}`} /><span className="session-copy"><strong>{item.title}</strong><small>{formatTime(item.updatedAt)} · {statusLabel(item.status)}{item.model ? ` · ${item.model}` : ''}</small></span></button>
        {!managing && !isActive(item.status) && <button className="session-delete" aria-label={`删除会话 ${item.title}`} title="删除会话" onClick={() => requestRemove(item)}><TrashIcon /></button>}
      </div>)}
    </div>
    <div className="sidebar-feedback" aria-live="polite">{feedback}</div>
    <div className="sidebar-foot"><span className="service-dot" />本地服务正常 <small>v0.1</small></div>
    {pendingDelete.length > 0 && <ConfirmDialog
      title={pendingDelete.length === 1 ? '删除这个会话？' : `删除选中的 ${pendingDelete.length} 个会话？`}
      description="会话消息和对应的 Agent Trace 将一起删除，删除后无法恢复。"
      subject={pendingDelete.length === 1 ? pendingDelete[0].title : pendingDelete.map((item) => item.title).join('、')}
      confirmLabel={pendingDelete.length === 1 ? '删除会话' : `删除 ${pendingDelete.length} 个会话`}
      busy={deleting}
      onCancel={() => setPendingDelete([])}
      onConfirm={confirmRemove}
    />}
  </aside>
}

function Chat({ session, data, onSession, onRefresh, onError, onOpenSkills, onOpenCapabilities }: { session: Session | null; data: Bootstrap; onSession: (session: Session) => void; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void; onOpenSkills: () => void; onOpenCapabilities: () => void }) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [attachments, setAttachments] = useState<PendingAttachment[]>([])
  const [attachmentError, setAttachmentError] = useState('')
  const [dragging, setDragging] = useState(false)
  const [capabilityOpen, setCapabilityOpen] = useState(false)
  const [capabilityQuery, setCapabilityQuery] = useState('')
  const [capabilityIndex, setCapabilityIndex] = useState(0)
  const [capabilityRange, setCapabilityRange] = useState<{ start: number; end: number } | null>(null)
  const [workspace, setWorkspace] = useState(session?.workspace || data.runtime.workspace)
  const endRef = useRef<HTMLDivElement>(null)
  const composerRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const capabilitySearchRef = useRef<HTMLInputElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const attachmentRef = useRef<PendingAttachment[]>([])
  const capabilities = useMemo(() => capabilityOptions(data), [data])
  const visibleCapabilities = useMemo(() => {
    const keyword = capabilityQuery.trim().toLocaleLowerCase()
    return capabilities.filter((item) => !keyword || item.name.toLocaleLowerCase().includes(keyword) || item.description.toLocaleLowerCase().includes(keyword) || item.token.slice(1).toLocaleLowerCase().includes(keyword) || capabilityKindLabel(item.kind).toLocaleLowerCase().includes(keyword))
  }, [capabilities, capabilityQuery])
  const selectedCapabilities = useMemo(() => capabilities.filter((item) => hasCapabilityToken(draft, item.token)), [capabilities, draft])
  const enabledCapabilityCount = capabilities.filter((item) => item.enabled).length
  const enabledSkillCount = data.skills.filter((item) => item.enabled).length
  const enabledMCPCount = data.mcps.filter((item) => item.enabled).length
  const workspaceOptions = useMemo(() => Array.from(new Set([
    data.runtime.workspace,
    ...data.sessions.map((item) => item.workspace).filter(Boolean),
  ])), [data.runtime.workspace, data.sessions])
  useEffect(() => { endRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [session?.messages.length, session?.status, session?.partialOutput])
  useEffect(() => {
    if (!textareaRef.current) return
    textareaRef.current.style.height = 'auto'
    textareaRef.current.style.height = `${Math.min(textareaRef.current.scrollHeight, 180)}px`
  }, [draft])
  useEffect(() => { attachmentRef.current = attachments }, [attachments])
  useEffect(() => () => attachmentRef.current.forEach((item) => item.preview && URL.revokeObjectURL(item.preview)), [])
  useEffect(() => { setCapabilityIndex(0) }, [capabilityQuery])
  useEffect(() => { setWorkspace(session?.workspace || data.runtime.workspace) }, [session?.id, session?.workspace, data.runtime.workspace])
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

  const handleCapabilityKey = (event: React.KeyboardEvent) => {
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

  const updateDraft = (event: React.ChangeEvent<HTMLTextAreaElement>) => {
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
      const next = session ? await api.sendMessage(session.id, message, payload) : await api.createSession(message, payload, workspace.trim())
      onSession(next); setDraft(''); closeCapabilityPicker(); attachments.forEach((item) => item.preview && URL.revokeObjectURL(item.preview)); setAttachments([]); await onRefresh()
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
      {!session && <div className="welcome"><div className="agent-orb"><Logo /></div><p className="eyebrow">一个核心 Agent · 能力按需加载</p><h1>想解决什么问题？</h1><p>直接描述目标，也可以添加代码、日志、图片或 PDF；输入 <code>@</code> 可明确指定 Tool、Skill 或 MCP。</p><div className="suggestion-heading"><strong>从一个场景开始</strong><span>点击即可运行；文件场景会先请你选择附件</span></div><div className="suggestions">{starterSuggestions.map((suggestion) => <button key={suggestion.category} onClick={() => startSuggestion(suggestion)} aria-label={`${suggestion.category}：${suggestion.title}`}><span className="suggestion-copy"><em>{suggestion.category}</em><strong>{suggestion.title}</strong></span><span className="suggestion-arrow">{suggestion.attachment ? '+' : '↗'}</span></button>)}</div></div>}
      {session && <ContextBar session={session} />}
      {session?.messages.map((message) => <MessageView key={message.id} message={message} />)}
      {session?.status === 'queued' && <div className="assistant-row"><Avatar /><div className="thinking queued"><i /><i /><i /><span>任务正在排队，等待本地执行槽…</span></div></div>}
      {session?.status === 'running' && (session.partialOutput
        ? <div className="assistant-row"><Avatar /><div className="assistant-message streaming-message"><div className="answer-text"><Markdown>{session.partialOutput}</Markdown></div></div></div>
        : <div className="assistant-row"><Avatar /><div className="thinking"><i /><i /><i /><span>Agent 正在思考和使用工具…</span></div></div>)}
      {session?.status === 'failed' && <RunError error={session.error} ollamaRunning={data.ollama.running} retrying={sending} onRetry={() => {
        const lastUserMessage = session.messages.slice().reverse().find((message) => message.role === 'user')
        if (lastUserMessage) send(lastUserMessage.attachments?.length ? '请重新完成上一条包含附件的请求。' : lastUserMessage.content)
      }} onOpenCapabilities={onOpenCapabilities} />}
      {session?.status === 'canceled' && <div className="run-error canceled"><div className="run-error-mark" aria-hidden="true">■</div><div className="run-error-copy"><strong>任务已停止</strong><span>你可以继续发送新消息。</span></div></div>}
      <div ref={endRef} />
    </div>
    <div className="composer-wrap"><div ref={composerRef} className={`composer ${dragging ? 'dragging' : ''}`} onDragEnter={(event) => { event.preventDefault(); setDragging(true) }} onDragOver={(event) => event.preventDefault()} onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setDragging(false) }} onDrop={(event) => { event.preventDefault(); setDragging(false); addFiles(event.dataTransfer.files) }}>
      {capabilityOpen && <CapabilityPicker items={visibleCapabilities} activeIndex={capabilityIndex} query={capabilityQuery} searchRef={capabilitySearchRef} onQuery={setCapabilityQuery} onKeyDown={handleCapabilityKey} onPick={insertCapability} onOpenSkills={onOpenSkills} onOpenCapabilities={onOpenCapabilities} />}
      <label className={`workspace-control ${session ? 'locked' : ''}`} title={session ? '工作区在创建会话后固定；如需切换请新建会话' : '选择最近使用的工作区，或输入服务器上已经存在的目录'}>
        <span>工作区</span>
        <input list="easyagent-workspaces" value={workspace} readOnly={Boolean(session)} disabled={sending || isActive(session?.status)} onChange={(event) => setWorkspace(event.target.value)} placeholder={data.runtime.workspace} aria-label="会话工作区" />
        <em>{session ? '本会话已固定' : '新会话'}</em>
      </label>
      <datalist id="easyagent-workspaces">{workspaceOptions.map((item) => <option key={item} value={item} />)}</datalist>
      {attachments.length > 0 && <div className="attachment-preview-list" aria-label="待发送附件">{attachments.map((item) => <div className="attachment-preview" key={item.id}>{item.preview ? <img src={item.preview} alt={item.file.name} /> : <span className="attachment-file-icon"><FileIcon /></span>}<span><strong title={item.file.name}>{item.file.name}</strong><small>{attachmentTypeLabel(item.file)} · {formatBytes(item.file.size)}</small></span><button type="button" disabled={sending || isActive(session?.status)} aria-label={`移除附件 ${item.file.name}`} onClick={() => removeAttachment(item.id)}><CloseIcon /></button></div>)}</div>}
      {selectedCapabilities.length > 0 && <div className="selected-capabilities" aria-label="已指定能力">{selectedCapabilities.map((item) => <span key={item.key}><b>{capabilityKindLabel(item.kind)}</b>{item.name}<button type="button" aria-label={`移除 ${item.name}`} onClick={() => removeCapability(item)}>×</button></span>)}</div>}
      <textarea ref={textareaRef} value={draft} onChange={updateDraft} aria-label="消息内容" aria-describedby="composer-help attachment-error" placeholder={attachments.length ? '描述希望 Agent 如何处理这些附件…' : '给 EasyAgent 发消息… 输入 @ 选择能力'} rows={1} onPaste={(event) => { const files = Array.from(event.clipboardData.files); if (files.length) { event.preventDefault(); addFiles(files) } }} onKeyDown={(event) => { if (handleCapabilityKey(event)) return; if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }} />
      <div className="composer-toolbar"><div className="composer-tools"><button type="button" className="attach-button" disabled={sending || isActive(session?.status)} aria-label="添加文件或图片" onClick={() => fileInputRef.current?.click()}><AttachIcon /><span>附件</span></button><button type="button" className={`capability-button ${capabilityOpen ? 'active' : ''}`} disabled={sending || isActive(session?.status)} aria-label={`选择 Agent 能力，共 ${capabilities.length} 项，${enabledCapabilityCount} 项已启用`} aria-expanded={capabilityOpen} aria-haspopup="listbox" onClick={() => capabilityOpen ? closeCapabilityPicker() : openCapabilityPicker()}><span aria-hidden="true">@</span><strong>能力</strong><small>{capabilities.length}</small></button><small>{enabledSkillCount}/{data.skills.length} Skills · {data.builtinTools.length} Tools · {enabledMCPCount}/{data.mcps.length} MCP</small><input ref={fileInputRef} className="visually-hidden" type="file" multiple tabIndex={-1} aria-hidden="true" accept={attachmentAccept} onChange={(event) => { if (event.target.files) addFiles(event.target.files); event.target.value = '' }} /></div><button type="button" className="send-button" aria-label={sending ? '正在发送' : '发送消息'} disabled={(!draft.trim() && attachments.length === 0) || sending || isActive(session?.status)} onClick={() => send()}>{sending ? <span className="send-spinner" /> : <SendIcon />}</button></div>
      {attachmentError && <div id="attachment-error" className="composer-error" role="alert">{attachmentError}</div>}
    </div><small id="composer-help" className="composer-hint">Enter 发送 · Shift + Enter 换行 · 可拖入或粘贴 · 单文件最大 5 MiB · 图片/PDF 需要当前模型支持多模态</small></div>
  </section>
}

function CapabilityPicker({ items, activeIndex, query, searchRef, onQuery, onKeyDown, onPick, onOpenSkills, onOpenCapabilities }: { items: CapabilityOption[]; activeIndex: number; query: string; searchRef: React.RefObject<HTMLInputElement>; onQuery: (value: string) => void; onKeyDown: (event: React.KeyboardEvent) => void; onPick: (item: CapabilityOption) => void; onOpenSkills: () => void; onOpenCapabilities: () => void }) {
  return <div className="capability-picker" role="dialog" aria-label="选择 Agent 能力">
    <div className="capability-picker-head"><div><strong>选择能力</strong><span>点击或输入 @ 指定本轮使用</span></div><label><span aria-hidden="true">⌕</span><input ref={searchRef} type="search" value={query} onChange={(event) => onQuery(event.target.value)} onKeyDown={onKeyDown} placeholder="搜索 Skill、Tool 或 MCP" aria-label="搜索能力" /></label></div>
    <div className="capability-options" role="listbox" aria-label="可用能力">
      {items.length === 0 && <div className="capability-empty">没有匹配的能力</div>}
      {items.map((item, index) => <button key={item.key} type="button" role="option" aria-selected={index === activeIndex} className={`${index === activeIndex ? 'active' : ''} ${item.enabled ? '' : 'disabled'}`} onMouseDown={(event) => event.preventDefault()} onClick={() => onPick(item)} disabled={!item.enabled}><span className={`capability-kind ${item.kind}`}>{capabilityKindShort(item.kind)}</span><span><strong>{item.name}</strong><small>{item.description}</small></span><em>{item.enabled ? item.token : '未启用'}</em></button>)}
    </div>
    <div className="capability-picker-foot"><span>↑↓ 选择 · Enter 插入 · Esc 关闭</span><div><button type="button" onClick={onOpenSkills}>管理 Skills</button><button type="button" onClick={onOpenCapabilities}>管理 Tools / MCP</button></div></div>
  </div>
}

function capabilityOptions(data: Bootstrap): CapabilityOption[] {
  const skills = data.skills.map((item) => ({ key: `skill:${item.name}`, kind: 'skill' as const, name: item.name, description: item.description, enabled: item.enabled, token: `@skill:${item.name}` }))
  const tools = data.builtinTools.map((item) => ({ key: `tool:${item.name}`, kind: 'tool' as const, name: item.name, description: item.description, enabled: true, token: `@tool:${item.name}` }))
  const mcps = data.mcps.map((item) => ({ key: `mcp:${item.id}`, kind: 'mcp' as const, name: item.name || item.id, description: item.description || '外部 MCP Server', enabled: item.enabled, token: `@mcp:${item.id}` }))
  return [...skills, ...tools, ...mcps].sort((left, right) => Number(right.enabled) - Number(left.enabled) || left.kind.localeCompare(right.kind) || left.name.localeCompare(right.name))
}

function capabilityMention(value: string, caret: number) {
  const before = value.slice(0, caret)
  const match = before.match(/(?:^|\s)@([^\s@]*)$/)
  if (!match) return null
  return { start: before.lastIndexOf('@'), query: match[1] }
}

function hasCapabilityToken(value: string, token: string) {
  return value.split(/\s+/).includes(token)
}

function capabilityKindLabel(kind: CapabilityKind) { return kind === 'skill' ? 'Skill' : kind === 'tool' ? 'Tool' : 'MCP' }
function capabilityKindShort(kind: CapabilityKind) { return kind === 'skill' ? 'S' : kind === 'tool' ? 'T' : 'M' }

function MessageView({ message }: { message: Session['messages'][number] }) {
  if (message.role === 'tool') return <details className="tool-result"><summary><span>⌁</span>{message.name || '工具'} 返回结果</summary><Payload value={message.content || ''} /></details>
  if (message.role === 'user') return <div className="user-row"><div className="user-message">{message.attachments?.length > 0 && <MessageAttachments attachments={message.attachments} />}{message.content && <div>{message.content}</div>}</div></div>
  if (message.role !== 'assistant') return null
  return <div className="assistant-row"><Avatar /><div className="assistant-message">{message.toolCalls?.length > 0 && <div className="tool-intent">{message.toolCalls.map((call) => <span key={call.id}>调用 {call.name}</span>)}</div>}{message.content && <div className="answer-text"><Markdown>{message.content}</Markdown></div>}</div></div>
}

function MessageAttachments({ attachments }: { attachments: Session['messages'][number]['attachments'] }) {
  return <div className="message-attachments">{attachments.map((attachment) => attachment.kind === 'image'
    ? <a key={attachment.id} className="message-image" href={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} target="_blank" rel="noreferrer" title={`查看 ${attachment.name}`}><img src={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} alt={attachment.name} loading="lazy" /><span>{attachment.name}</span></a>
    : <a key={attachment.id} className="message-file" href={`/api/v1/attachments/${encodeURIComponent(attachment.id)}`} target="_blank" rel="noreferrer" download={attachment.name}><FileIcon /><span><strong>{attachment.name}</strong><small>{attachment.kind === 'pdf' ? 'PDF' : '文本文件'} · {formatBytes(attachment.size)}</small></span></a>)}</div>
}

function Avatar() { return <div className="avatar"><Logo /></div> }
function Markdown({ children }: { children: string }) {
  if (hasMath(children)) return <Suspense fallback={<ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>}><MathMarkdown>{children}</MathMarkdown></Suspense>
  return <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
}

function ContextBar({ session }: { session: Session }) {
  const context = session.context
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
      <span>{context.cacheReported ? `缓存 ${cacheRate}%` : '缓存未上报'}</span>
      <span className="context-workspace" title={session.workspace}>工作区 {workspaceName(session.workspace)}</span>
      <em>{context.compressionCount > 0 ? `已压缩 ${context.compressionCount} 次` : context.compressionMode === 'auto' ? `自动 ${context.compressionThresholdPercent}%` : '压缩停用'}</em>
    </summary>
    <div className="context-details">
      <ContextDatum label="最近一次模型输入" value={context.lastInputTokens > 0 ? `${context.lastInputTokens.toLocaleString()} Token` : session.status === 'failed' ? '本轮 Token 未上报' : '尚无数据'} hint={context.contextWindowTokens > 0 ? `模型窗口 ${context.contextWindowTokens.toLocaleString()} · 使用 ${utilization}%` : '模型没有提供窗口上限，请在“模型与工具”中填写'} />
      <ContextDatum label="会话历史" value={`${context.userTurns} 个用户轮次 · ${context.historyMessages} 条消息`} hint={`最近请求发送 ${context.requestMessages || '—'} 条消息项 · ${context.toolDefinitions || 0} 个工具定义`} />
      <ContextDatum label="Prompt Cache" value={context.cacheReported ? `命中 ${context.lastCachedTokens.toLocaleString()} · ${cacheRate}%` : 'Provider 未上报'} hint={context.cacheReported ? `本次写入 ${context.lastCacheWriteTokens.toLocaleString()} Token` : '不等于确认没有缓存，只表示响应中没有缓存字段'} />
      <ContextDatum label="上下文压缩" value={context.compressionCount > 0 ? `${context.compressionCount} 次 · 摘要代表 ${context.compressedMessages} 条` : context.compressionMode === 'auto' ? `自动 · ${context.compressionThresholdPercent}% 触发` : '已停用'} hint={context.compressionCount > 0 ? `最近 ${context.retainedMessages} 条仍原样发送；SQLite 永久保留全部 ${context.historyMessages} 条消息` : '达到阈值后生成结构化检查点，并保留最近原始轮次；不会静默删除历史'} />
      <ContextDatum label="会话工作区" value={session.workspace || '默认工作区'} hint={session.workspace ? '文件、Shell 和 stdio MCP 都在这个目录中运行；切换工作区需要新建会话' : '该会话使用 EasyAgent 默认工作区'} />
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

function TracePanel({ session, onClose }: { session: Session; onClose: () => void }) {
  const cacheRate = session.usage.cacheReported && session.usage.cacheInputTokens ? Math.round(session.usage.cachedTokens / session.usage.cacheInputTokens * 100) : 0
  const context = session.context
  return <aside className="trace-panel"><div className="trace-head"><div><p className="eyebrow">AUDITABLE RUNTIME</p><h2>Agent Trace</h2></div><button aria-label="关闭 Agent Trace" onClick={onClose}>×</button></div><div className="metrics"><Metric label="LLM" value={`${session.usage.modelCalls} 次`} sub={formatDuration(session.usage.modelDurationMs)} /><Metric label="工具" value={`${session.usage.toolCalls} 次`} sub={formatDuration(session.usage.toolDurationMs)} /><Metric label="Token" value={session.usage.totalTokens.toLocaleString()} sub={`入 ${session.usage.inputTokens} · 出 ${session.usage.outputTokens}`} /><Metric label="Prompt Cache" value={session.usage.cacheReported ? `${cacheRate}%` : '未上报'} sub={session.usage.cacheReported ? `命中 ${session.usage.cachedTokens} · 写入 ${session.usage.cacheWriteTokens}` : 'Provider 未返回缓存字段'} /></div><div className="context-ledger"><div><span>最近上下文</span><strong>{context.lastInputTokens ? formatTokens(context.lastInputTokens) : session.status === 'failed' ? '未上报' : '—'}{context.contextWindowTokens ? ` / ${formatTokens(context.contextWindowTokens)}` : ''}</strong></div><div><span>会话历史</span><strong>{context.userTurns} 个用户轮次 · {context.historyMessages} 条消息</strong></div><div><span>发送方式</span><strong>{historyModeLabel(context.historyMode)}</strong></div><div><span>压缩</span><strong>{context.compressionCount > 0 ? `${context.compressionCount} 次 · ${context.compressedMessages} 条` : context.compressionMode === 'auto' ? `自动 ${context.compressionThresholdPercent}%` : '已停用'}</strong></div></div><div className="trace-events">{session.events.length === 0 && <div className="trace-empty">还没有 Trace</div>}{session.events.map((event) => <TraceRow key={event.id} event={event} />)}</div></aside>
}

function TraceRow({ event }: { event: TraceEvent }) {
  const isModelResult = event.kind === 'model_end' || event.kind === 'compaction_end'
  const title = event.kind === 'model_start' ? '模型请求开始' : event.kind === 'model_end' ? `模型响应 · ${event.name || '模型'}` : event.kind === 'compaction_start' ? '准备压缩上下文' : event.kind === 'compaction_end' ? `上下文检查点 · ${event.name || '模型'}` : event.kind === 'tool_start' ? `工具开始 · ${event.name}` : event.kind === 'tool_end' ? `工具结果 · ${event.name}` : `MCP · ${event.name}`
  const cacheRate = event.cacheReported && event.inputTokens ? Math.round((event.cachedTokens || 0) / event.inputTokens * 100) : 0
  const tokenMissing = event.status === 'error' && !event.totalTokens && !event.inputTokens && !event.outputTokens
  const location = `${event.turn ? `第 ${event.turn} 轮 · ` : ''}${event.step ? `第 ${event.step} 步` : '独立阶段'}${event.attempt ? ` · 尝试 ${event.attempt}` : ''}`
  return <details className={`trace-row ${event.status}`} open={isModelResult && event.status === 'error'}><summary><span className="trace-node" /><div><strong>{title}</strong><small>{location} {event.statusCode ? `· HTTP ${event.statusCode} ` : ''}· {event.durationMs || 0} ms {event.totalTokens ? `· ${event.totalTokens} tokens` : tokenMissing ? '· Token 未上报' : ''}{isModelResult ? ` · ${event.cacheReported ? `缓存 ${cacheRate}%` : '缓存未上报'}` : ''}</small></div><em>{eventStatusLabel(event.status)}</em></summary>{event.detail && <p className="event-error">{event.detail}</p>}{isModelResult && <div className="event-usage"><span>输入 <b>{tokenMissing ? '未上报' : (event.inputTokens || 0).toLocaleString()}</b></span><span>输出 <b>{tokenMissing ? '未上报' : (event.outputTokens || 0).toLocaleString()}</b></span><span>缓存命中 <b>{event.cacheReported ? (event.cachedTokens || 0).toLocaleString() : '未上报'}</b></span><span>缓存写入 <b>{event.cacheReported ? (event.cacheWriteTokens || 0).toLocaleString() : '未上报'}</b></span><span>历史 <b>{historyModeLabel(event.historyMode || '')} · {event.requestMessages || 0} 项</b></span><span>工具定义 <b>{event.toolDefinitions || 0}</b></span></div>}{event.input && <div><p className="trace-label">{isModelResult ? '模型请求 · 实际发送' : '工具输入'}</p><Payload value={event.input} /></div>}{event.output && (isModelResult ? <ModelTraceResponse value={event.output} /> : <div><p className="trace-label">工具响应 · 原始返回</p><Payload value={event.output} /></div>)}</details>
}

function eventStatusLabel(status: string) { return status === 'started' ? '开始' : status === 'success' ? '成功' : status === 'error' ? '失败' : status }

function Metric({ label, value, sub }: { label: string; value: string; sub: string }) { return <div><span>{label}</span><strong>{value}</strong><small>{sub}</small></div> }
function Payload({ value }: { value: string }) { const formatted = useMemo(() => { try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value } }, [value]); return <pre>{formatted}</pre> }

type StreamTracePayload = { stream?: boolean; final_response?: unknown; raw_chunks?: unknown[] }

function ModelTraceResponse({ value }: { value: string }) {
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

function Skills({ data, onRefresh, onError }: { data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const [selectedName, setSelectedName] = useState(data.skills[0]?.name || '')
  const selected = data.skills.find((item) => item.name === selectedName)
  const [draft, setDraft] = useState<Skill | null>(selected ? { ...selected } : null)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDescription, setNewDescription] = useState('')
  const [createError, setCreateError] = useState('')

  // 自定义 Skill 在首次保存前还不在 data.skills 中，不能用第一项覆盖这份草稿。
  useEffect(() => { if (selected) setDraft({ ...selected }) }, [selectedName, data.skills, selected])

  const save = async () => {
    if (!draft) return
    try {
      await api.saveSkill(draft)
      const next = await onRefresh()
      const saved = next.skills.find((item) => item.name === draft.name)
      if (saved) setDraft({ ...saved })
    } catch (reason) { onError((reason as Error).message) }
  }
  const reset = async () => { if (!draft) return; try { await api.resetSkill(draft.name); const next = await onRefresh(); const current = next.skills.find((item) => item.name === draft.name); if (current) setDraft({ ...current }) } catch (reason) { onError((reason as Error).message) } }

  const openCreate = () => {
    setNewName('')
    setNewDescription('')
    setCreateError('')
    setCreating(true)
  }
  const create = (event: React.FormEvent) => {
    event.preventDefault()
    const name = newName.trim()
    const description = newDescription.trim() || '说明这个 Skill 何时使用'
    if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(name)) {
      setCreateError('名称只能包含小写英文、数字和短横线，例如 incident-analysis')
      return
    }
    if (data.skills.some((item) => item.name === name)) {
      setCreateError('这个 Skill 已经存在，请换一个名称')
      return
    }
    setSelectedName(name)
    setDraft({
      name,
      description,
      content: `---\nname: ${name}\ndescription: ${description}\n---\n\n# Instructions\n\n在这里编写执行说明。`,
      enabled: true,
      builtin: false,
    })
    setCreating(false)
  }

  return <section className="settings-page">
    <div className="page-intro"><p className="eyebrow">按需加载</p><h1>Skills</h1><p>默认只向模型提供名称和简介，由 Agent 按需调用 <code>load_skill</code>；在输入框用 <code>@skill:name</code> 明确选择时，本轮会直接使用完整说明。</p></div>
    <div className="split-settings">
      <div className="settings-list">
        <button className="add-row" onClick={openCreate}>＋ 添加 Skill</button>
        {data.skills.map((item) => <button key={item.name} className={item.name === draft?.name ? 'active' : ''} onClick={() => setSelectedName(item.name)}><span className={`status ${item.enabled ? 'idle' : 'off'}`} /><div><strong>{item.name}</strong><small>{item.description}</small></div><em>{item.builtin ? '内置' : '自定义'}</em></button>)}
      </div>
      {draft && <div className="editor-pane"><div className="editor-title"><div><h2>{draft.name}</h2><span>{draft.builtin ? '内置 Skill · 修改会保存为覆盖' : '自定义 Skill'}</span></div><label className="switch"><input type="checkbox" checked={draft.enabled} onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })} /><span /></label></div><label>用途描述<input value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} /></label><label>SKILL.md<textarea className="code-editor" value={draft.content} onChange={(event) => setDraft({ ...draft, content: event.target.value })} /></label><div className="form-actions">{draft.builtin && <button className="ghost-button" onClick={reset}>恢复内置版本</button>}<button className="primary-button" onClick={save}>保存 Skill</button></div></div>}
    </div>
    {creating && <div className="modal-backdrop" onMouseDown={() => setCreating(false)}><form className="modal create-skill-modal" onSubmit={create} onMouseDown={(event) => event.stopPropagation()}><div className="modal-head"><div><p className="eyebrow">NEW SKILL</p><h2>创建一项按需能力</h2></div><button type="button" onClick={() => setCreating(false)}>×</button></div><p className="modal-copy">先定义清晰的名称和触发场景，创建后再编辑完整 SKILL.md。</p><label>名称<input autoFocus value={newName} onChange={(event) => { setNewName(event.target.value); setCreateError('') }} placeholder="例如 incident-analysis" /></label><label>用途描述<input value={newDescription} onChange={(event) => setNewDescription(event.target.value)} placeholder="什么时候应该使用这个 Skill？" /></label>{createError && <div className="field-error">{createError}</div>}<div className="form-actions"><button type="button" className="ghost-button" onClick={() => setCreating(false)}>取消</button><button className="primary-button" type="submit">创建并编辑</button></div></form></div>}
  </section>
}

function Capabilities({ data, onRefresh, onError }: { data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const [model, setModel] = useState<ModelSettings>({ ...data.model })
  const [mcp, setMCP] = useState<MCPConfig | null>(null)
  const [testingModel, setTestingModel] = useState(false)
  const [modelNotice, setModelNotice] = useState<{ ready: boolean; title: string; message: string } | null>(null)
  const [installingPreset, setInstallingPreset] = useState('')
  const [checkingPreset, setCheckingPreset] = useState('')
  const [savingMCP, setSavingMCP] = useState(false)
  const [togglingMCP, setTogglingMCP] = useState('')
  const [deletingMCP, setDeletingMCP] = useState(false)
  const [confirmingMCPDelete, setConfirmingMCPDelete] = useState(false)
  const [mcpNotice, setMCPNotice] = useState<{ ready: boolean; title: string; message: string; tools: string[] } | null>(null)
  useEffect(() => setModel({ ...data.model }), [data.model])
  const saveModel = async () => { try { await api.saveModel(model); await onRefresh() } catch (reason) { onError((reason as Error).message) } }
  const testModel = async () => {
    if (testingModel) return
    setTestingModel(true); setModelNotice(null); onError('')
    try {
      const result = await api.testModel(model)
      setModelNotice({ ready: true, title: `${result.model} · Agent 能力可用`, message: `原生 Function Calling 与工具结果回传均通过 · ${result.inputTokens + result.outputTokens} Token · ${formatDuration(result.durationMs)}` })
    } catch (reason) {
      setModelNotice({ ready: false, title: '模型不适合当前 Agent 配置', message: (reason as Error).message })
    } finally { setTestingModel(false) }
  }
  const useOllama = async (name: string) => { try { await api.useOllama(name); await onRefresh() } catch (reason) { onError((reason as Error).message) } }
  const presetConfig = (preset: Bootstrap['mcpPresets'][number]): MCPConfig => ({ id: preset.id, name: preset.name, description: preset.description, enabled: false, transport: preset.transport as MCPConfig['transport'], command: preset.command, args: preset.args || [], endpoint: preset.endpoint, authType: preset.authType, headers: preset.headers || {}, environment: {} })
  const installPreset = async (preset: Bootstrap['mcpPresets'][number]) => {
    setMCPNotice(null)
    if (preset.action === 'configure') { setMCP(presetConfig(preset)); return }
    setInstallingPreset(preset.id)
    try {
      const result = await api.installMCPPreset(preset.id)
      setMCPNotice({ ready: result.ready, title: `${preset.name} · ${result.ready ? '已启用' : '尚未就绪'}`, message: result.message, tools: result.tools.map((tool) => tool.name) })
      await onRefresh()
    } catch (reason) { onError((reason as Error).message) } finally { setInstallingPreset('') }
  }
  const checkPreset = async (preset: Bootstrap['mcpPresets'][number]) => {
    setCheckingPreset(preset.id); setMCPNotice(null); onError('')
    try {
      const result = await api.checkMCPPreset(preset.id)
      setMCPNotice({ ready: result.ok, title: `${preset.name} · ${result.installed ? '已安装' : result.ok ? '环境可用' : '缺少依赖'}`, message: result.message, tools: [] })
    } catch (reason) { onError((reason as Error).message) } finally { setCheckingPreset('') }
  }
  const saveMCP = async () => {
    if (!mcp || savingMCP) return
    setSavingMCP(true)
    try {
      const saved = await api.saveMCP(mcp)
      setMCPNotice({ ready: true, title: `${saved.name} · ${saved.enabled ? '已验证并启用' : '配置已保存'}`, message: saved.enabled ? '握手和工具清单读取成功；Agent 会在任务需要时按需连接。' : '当前不会向 Agent 暴露此 MCP。', tools: [] })
      await onRefresh()
      setMCP(null)
    } catch (reason) { onError((reason as Error).message) } finally { setSavingMCP(false) }
  }
  const removeMCP = async () => {
    if (!mcp || deletingMCP) return
    setDeletingMCP(true); onError('')
    try {
      const preset = data.mcpPresets.find((candidate) => candidate.id === mcp.id)
      if (preset?.action === 'install') await api.uninstallMCPPreset(mcp.id)
      else await api.deleteMCP(mcp.id)
      await onRefresh()
      setConfirmingMCPDelete(false)
      setMCP(null)
    } catch (reason) { onError((reason as Error).message) } finally { setDeletingMCP(false) }
  }
  const toggleMCP = async (item: MCPConfig) => {
    if (togglingMCP) return
    setTogglingMCP(item.id); setMCPNotice(null); onError('')
    try {
      const saved = await api.saveMCP({ ...item, enabled: !item.enabled })
      setMCPNotice({
        ready: true,
        title: `${saved.name} · ${saved.enabled ? '已启用' : '已停用'}`,
        message: saved.enabled ? '连接验证成功；Agent 会在任务需要时按需加载工具。' : '配置和私有安装包均保留，可随时重新启用。',
        tools: [],
      })
      await onRefresh()
    } catch (reason) { onError((reason as Error).message) } finally { setTogglingMCP('') }
  }
  const testMCP = async (id: string) => { setMCPNotice(null); try { const result = await api.testMCP(id); setMCPNotice({ ready: true, title: `连接成功 · ${result.tools.length} 个工具`, message: 'MCP 握手和工具清单读取正常。', tools: result.tools.map((item) => item.name) }) } catch (reason) { onError((reason as Error).message) } }
  const persistedMCP = Boolean(mcp && data.mcps.some((item) => item.id === mcp.id))
  const editingPreset = mcp ? data.mcpPresets.find((candidate) => candidate.id === mcp.id) : undefined
  return <section className="settings-page capabilities"><div className="page-intro"><p className="eyebrow">可插拔能力</p><h1>模型与工具</h1><p>模型、内置 Tool、MCP 和基础提示词分开管理；启用后统一注册给同一个核心 Agent。</p></div>
    <div className="section-block"><div className="section-heading"><div><h2>模型</h2><p>支持 OpenAI Chat Completions 和 Responses 兼容接口。</p></div><span className="tag">{model.protocol}</span></div>
      <div className="form-grid"><label>提供方<input value={model.provider} onChange={(e) => setModel({ ...model, provider: e.target.value })} /></label><label>协议<select value={model.protocol} onChange={(e) => setModel({ ...model, protocol: e.target.value as ModelSettings['protocol'] })}><option value="chat_completions">Chat Completions</option><option value="responses">Responses</option></select></label><label className="wide">Base URL<input value={model.baseUrl} onChange={(e) => setModel({ ...model, baseUrl: e.target.value })} /></label><label>模型名称<input value={model.model} onChange={(e) => setModel({ ...model, model: e.target.value })} /></label><label>推理模式<select value={model.thinking || ''} onChange={(e) => setModel({ ...model, thinking: e.target.value })}><option value="">模型默认</option><option value="disabled">尝试关闭推理</option></select><small>兼容服务建议使用模型默认；为保证 Function Calling，Ollama 工具选择轮可能保留推理</small></label><label>最大输出 Token<input type="number" value={model.maxOutputTokens} onChange={(e) => setModel({ ...model, maxOutputTokens: Number(e.target.value) })} /></label><label>模型超时（秒）<input type="number" min={data.modelRules.minRequestTimeoutSeconds} max={data.modelRules.maxRequestTimeoutSeconds} value={model.requestTimeoutSeconds} onChange={(e) => setModel({ ...model, requestTimeoutSeconds: Number(e.target.value) })} /><small>默认 {data.modelRules.defaultRequestTimeoutSeconds} 秒；单次请求最多 {data.modelRules.maxRequestTimeoutSeconds} 秒</small></label><label>上下文窗口 Token<input type="number" min="0" value={model.contextWindowTokens || 0} onChange={(e) => setModel({ ...model, contextWindowTokens: Number(e.target.value) })} /><small>0 表示未知；Ollama 运行后读取当前实际窗口</small></label><label>自动压缩阈值<input type="number" min={data.modelRules.minCompressionThresholdPercent} max={data.modelRules.maxCompressionThresholdPercent} value={model.compressionThresholdPercent} onChange={(e) => setModel({ ...model, compressionThresholdPercent: Number(e.target.value) })} /><small>默认达到上下文窗口的 {data.modelRules.defaultCompressionThresholdPercent}% 后生成检查点</small></label><label>API Key<input type="password" placeholder={model.secretConfigured ? '已配置，留空不修改' : '可留空'} value={model.apiKey || ''} onChange={(e) => setModel({ ...model, apiKey: e.target.value })} /></label><label>API Key 环境变量<input placeholder="例如 OPENAI_API_KEY" value={model.apiKeyEnv || ''} onChange={(e) => setModel({ ...model, apiKeyEnv: e.target.value })} /></label></div>{modelNotice && <div role="status" aria-live="polite" className={`model-notice ${modelNotice.ready ? 'ready' : 'failed'}`}><div><strong>{modelNotice.title}</strong><span>{modelNotice.message}</span></div><button aria-label="关闭模型测试结果" onClick={() => setModelNotice(null)}>×</button></div>}<div className="form-actions"><button className="ghost-button" disabled={testingModel} onClick={testModel}>{testingModel ? '正在验证 Function Calling…' : '测试当前模型'}</button><button className="primary-button" onClick={saveModel}>保存模型</button></div><div className="ollama-strip"><div><strong><span className={`service-dot ${data.ollama.running ? '' : 'off'}`} />Ollama · 无需 API Key</strong><small>{data.ollama.message}</small></div><div>{data.ollama.models.map((item) => <button key={item.name} className="ghost-button" onClick={() => useOllama(item.name)}>使用 {item.name}</button>)}</div></div></div>
    <div className="section-block"><div className="section-heading"><div><h2>内置 Tools</h2><p>首轮只发送精简能力目录；模型需要时再加载完整 Tool Schema 并调用。</p></div><span className="tag">{data.builtinTools.length} 个</span></div><div className="capability-note"><strong>EasyAgent Home</strong><span><code>{data.runtime.home}</code></span><strong>默认工作区</strong><span><code>{data.runtime.workspace}</code></span><strong>私有运行时</strong><span><code>{data.runtime.runtime}</code></span></div><div className="tool-table">{data.builtinTools.map((tool) => <div key={tool.name}><code>{tool.name}</code><span>{tool.description}</span><em>{tool.category || tool.source}</em></div>)}</div></div>
    <div className="section-block">
      <div className="section-heading"><div><h2>MCP Servers</h2><p>远程服务配置连接；本地预设先检测宿主环境，再把 MCP 包安装到 EasyAgent 私有目录。</p></div><button className="ghost-button" onClick={() => setMCP({ id: `mcp-${Date.now()}`, name: 'New MCP', description: '', enabled: false, transport: 'http', args: [], headers: {}, environment: {} })}>＋ 自定义</button></div>
      <div className="capability-note"><strong>能力边界</strong><span>工作区文件工具已经内置；EasyAgent 只管理 MCP 包，不会全局安装或升级 Node、Python、Java 等项目运行时。</span></div>
      {mcpNotice && <div role="status" aria-live="polite" className={`mcp-notice ${mcpNotice.ready ? 'ready' : 'failed'}`}><div><strong>{mcpNotice.title}</strong><span>{mcpNotice.message}</span></div>{mcpNotice.tools.length > 0 && <details><summary>查看 {mcpNotice.tools.length} 个工具</summary><code>{mcpNotice.tools.join('\n')}</code></details>}<button aria-label="关闭 MCP 状态" onClick={() => setMCPNotice(null)}>×</button></div>}
      <div className="mcp-grid">{data.mcps.map((item) => {
        const preset = data.mcpPresets.find((candidate) => candidate.id === item.id)
        const canInstall = !item.enabled && preset?.action === 'install'
        const busy = installingPreset === item.id || checkingPreset === item.id || togglingMCP === item.id
        return <div className="mcp-row" key={item.id}>
          <div className="mcp-row-info"><span className={`status ${item.enabled ? 'idle' : 'off'}`} /><strong>{preset?.name || item.name}</strong><small title={preset?.description || item.description}>{preset?.description || item.description || (item.transport === 'stdio' ? `${item.command} ${item.args.join(' ')}` : item.endpoint)}</small></div>
          <span>{item.enabled ? '已启用' : '已停用'}</span>
          <div className="mcp-row-actions">
            {preset?.action === 'install' && <button disabled={busy} onClick={() => checkPreset(preset)}>{checkingPreset === item.id ? '检测中…' : '检测环境'}</button>}
            <button disabled={busy} onClick={() => testMCP(item.id)}>测试连接</button>
            <button disabled={busy} onClick={() => canInstall && preset ? installPreset(preset) : toggleMCP(item)}>{installingPreset === item.id ? '安装中…' : togglingMCP === item.id ? '处理中…' : canInstall ? '安装并启用' : item.enabled ? '停用' : '启用'}</button>
            <button disabled={busy} onClick={() => setMCP({ ...item, name: preset?.name || item.name, description: preset?.description || item.description })}>编辑</button>
          </div>
        </div>
      })}</div>
      <div className="presets"><span>MCP 预设 · 检测不会修改系统；安装操作只写入 EasyAgent 私有 Runtime</span>{data.mcpPresets.filter((preset) => !data.mcps.some((item) => item.id === preset.id)).map((preset) => <div className="preset-card" key={preset.id}><strong>{preset.name}</strong><small>{preset.description}</small><em>{preset.requirement}</em><div className="preset-actions">{preset.action === 'install' && <button type="button" disabled={!!installingPreset || !!checkingPreset} onClick={() => checkPreset(preset)}>{checkingPreset === preset.id ? '检测中…' : '检测环境'}</button>}<button type="button" disabled={!!installingPreset || !!checkingPreset} onClick={() => installPreset(preset)}>{installingPreset === preset.id ? '安装中…' : preset.action === 'install' ? '安装并启用' : '配置连接'}</button></div></div>)}</div>
    </div>
    <details className="prompt-block"><summary><div><h2>基础 System Prompt</h2><p>独立 Markdown 包，只定义稳定行为；具体任务方法和团队约定写进 Skill。</p></div><span>查看</span></summary><pre>{data.systemPrompt}</pre></details>
    {mcp && <div className="modal-backdrop" onMouseDown={() => setMCP(null)}><div className="modal" onMouseDown={(e) => e.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">MCP SERVER</p><h2>{mcp.name}</h2></div><button aria-label="关闭 MCP 配置" onClick={() => setMCP(null)}>×</button></div>
      <div className="form-grid">
        <label>ID<input value={mcp.id} disabled /></label>
        <label>名称<input value={mcp.name} onChange={(e) => setMCP({ ...mcp, name: e.target.value })} /></label>
        <label className="wide">用途描述<input value={mcp.description || ''} onChange={(e) => setMCP({ ...mcp, description: e.target.value })} placeholder="告诉 Agent 什么时候应该加载这个 MCP" /></label>
        <label>Transport<select value={mcp.transport} onChange={(e) => setMCP({ ...mcp, transport: e.target.value as MCPConfig['transport'] })}><option value="stdio">stdio</option><option value="http">HTTP</option></select></label>
        <label className="check-label"><input type="checkbox" checked={mcp.enabled} onChange={(e) => setMCP({ ...mcp, enabled: e.target.checked })} />启用</label>
        {mcp.transport === 'stdio' ? <>
          <label>命令<input value={mcp.command || ''} onChange={(e) => setMCP({ ...mcp, command: e.target.value })} /></label>
          <label className="wide">参数（每行一个）<textarea value={mcp.args.join('\n')} onChange={(e) => setMCP({ ...mcp, args: e.target.value.split('\n').filter(Boolean) })} /></label>
          <label className="wide">环境变量（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.environment)} onChange={(e) => setMCP({ ...mcp, environment: parseRecord(e.target.value) })} /></label>
        </> : <>
          <label className="wide">Endpoint<input value={mcp.endpoint || ''} onChange={(e) => setMCP({ ...mcp, endpoint: e.target.value })} /></label>
          <label>认证<select value={mcp.authType || ''} onChange={(e) => setMCP({ ...mcp, authType: e.target.value })}><option value="">无</option><option value="bearer">Bearer Token</option><option value="basic">用户名密码</option></select></label>
          {mcp.authType === 'bearer' && <label>Token<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.token || ''} onChange={(e) => setMCP({ ...mcp, token: e.target.value })} /></label>}
          {mcp.authType === 'basic' && <><label>用户名<input value={mcp.username || ''} onChange={(e) => setMCP({ ...mcp, username: e.target.value })} /></label><label>密码<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.password || ''} onChange={(e) => setMCP({ ...mcp, password: e.target.value })} /></label></>}
          <label className="wide">自定义 Header（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.headers)} onChange={(e) => setMCP({ ...mcp, headers: parseRecord(e.target.value) })} /></label>
        </>}
      </div>
      {mcp.enabled && <p className="modal-copy verify-copy">保存时会先校验认证、连接服务并读取工具清单；失败时不会启用。</p>}
      <div className="form-actions"><button className="ghost-button danger" disabled={savingMCP || deletingMCP} onClick={() => persistedMCP ? setConfirmingMCPDelete(true) : setMCP(null)}>{persistedMCP ? '删除' : '放弃新增'}</button><button className="primary-button" disabled={savingMCP || deletingMCP} onClick={saveMCP}>{savingMCP ? '正在验证…' : mcp.enabled ? '验证并启用' : '保存配置'}</button></div>
    </div></div>}
    {mcp && confirmingMCPDelete && <ConfirmDialog
      title={editingPreset?.action === 'install' ? `卸载 ${mcp.name}？` : '删除这个 MCP 配置？'}
      description={editingPreset?.action === 'install' ? 'EasyAgent 私有目录中的 MCP 包及其配置会被删除；不会卸载宿主机 Node/npm，也不会修改项目文件。' : '认证信息和连接配置将被永久删除，Agent 也将无法再调用它提供的工具。'}
      subject={mcp.name}
      confirmLabel={editingPreset?.action === 'install' ? '卸载 MCP' : '删除 MCP'}
      busy={deletingMCP}
      onCancel={() => setConfirmingMCPDelete(false)}
      onConfirm={removeMCP}
    />}
  </section>
}

function ConfirmDialog({ title, description, subject, confirmLabel, busy, onCancel, onConfirm }: { title: string; description: string; subject?: string; confirmLabel: string; busy: boolean; onCancel: () => void; onConfirm: () => void }) {
  const cancelRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    cancelRef.current?.focus()
    const closeWithEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) onCancel()
    }
    document.addEventListener('keydown', closeWithEscape)
    return () => document.removeEventListener('keydown', closeWithEscape)
  }, [busy, onCancel])

  return <div className="confirm-backdrop" onMouseDown={() => !busy && onCancel()}>
    <div className="confirm-dialog" role="alertdialog" aria-modal="true" aria-label={title} onMouseDown={(event) => event.stopPropagation()}>
      <div className="confirm-symbol"><TrashIcon /></div>
      <div className="confirm-copy"><p className="eyebrow">确认删除</p><h2>{title}</h2><p>{description}</p>{subject && <div className="confirm-subject" title={subject}>{subject}</div>}</div>
      <div className="confirm-actions"><button ref={cancelRef} className="ghost-button" disabled={busy} onClick={onCancel}>取消</button><button className="danger-button" disabled={busy} onClick={onConfirm}>{busy ? '删除中…' : confirmLabel}</button></div>
    </div>
  </div>
}

function RunError({ error, ollamaRunning, retrying, onRetry, onOpenCapabilities }: { error?: string; ollamaRunning: boolean; retrying: boolean; onRetry: () => void; onOpenCapabilities: () => void }) {
  const explanation = explainRunError(error, ollamaRunning)
  return <div className="run-error" role="alert">
    <div className="run-error-mark" aria-hidden="true">!</div>
    <div className="run-error-copy"><strong>{explanation.title}</strong><span>{explanation.message}</span>
      <div className="run-error-actions"><button className="primary-button" disabled={retrying} onClick={onRetry}>{retrying ? '正在重试…' : '重新发送'}</button><button className="ghost-button" onClick={onOpenCapabilities}>检查模型配置</button></div>
      {error && <details><summary>查看技术详情</summary><code>{error}</code></details>}
    </div>
  </div>
}

function explainRunError(error?: string, ollamaRunning = false) {
  const value = error || '没有收到具体错误信息'
  if (/(multimodal.*(?:not support|unsupported)|does not support multimodal|vision.*(?:not support|unsupported)|(?:image|file).*(?:not support|unsupported))/i.test(value)) {
    return { title: '当前模型不支持这类附件', message: '附件已经正常上传，但当前模型不能读取图片或 PDF。请到“模型与工具”换用支持视觉/文件输入的模型，或改为发送文本、日志和代码文件。' }
  }
  if (/(?:127\.0\.0\.1|localhost):11434/i.test(value) && /(connection refused|connect: connection refused|ECONNREFUSED)/i.test(value)) {
    return ollamaRunning
      ? { title: '本地模型连接已恢复', message: '该轮执行时无法连接 Ollama；现在服务已经恢复，直接点击“重新发送”即可，不需要新建会话。' }
      : { title: '无法连接本地模型', message: 'EasyAgent 正常运行，但 Ollama 没有启动。启动 Ollama 后点击“重新发送”即可，不需要新建会话。' }
  }
  if (/(context deadline exceeded|Client\.Timeout|timeout|timed out)/i.test(value)) {
    return { title: '模型响应超时', message: '模型在设定时间内没有返回。可直接重试，或到“模型与工具”增加超时时间、换用更小的模型。' }
  }
  return { title: '本轮没有完成', message: 'Agent 执行过程中遇到错误。可以查看技术详情后重试，或检查模型与工具配置。' }
}

function friendlyError(error: string) {
  const explanation = explainRunError(error)
  if (explanation.title !== '本轮没有完成') return `${explanation.title}：${explanation.message}`
  return error
}

function TrashIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m3 0-1 13H7L6 7m4 4v5m4-5v5" /></svg> }
function Logo() { return <img src="/logo.svg" alt="" aria-hidden="true" /> }
function Icon({ name }: { name: string }) { const paths: Record<string, string> = { chat: 'M4 5h16v11H8l-4 4V5Z', skill: 'M6 3h12v18H6zM9 8h6M9 12h6', plug: 'M9 3v6m6-6v6M7 9h10v3a5 5 0 0 1-10 0V9Zm5 8v4' }; return <svg viewBox="0 0 24 24" aria-hidden="true"><path d={paths[name]} /></svg> }
function AttachIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m8.5 12.5 6.8-6.8a3.5 3.5 0 0 1 5 5l-9.2 9.2a5 5 0 0 1-7.1-7.1l9-9m-6.2 11.4 8.5-8.5" /></svg> }
function SendIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5m0 0-6 6m6-6 6 6" /></svg> }
function CloseIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg> }
function FileIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h8l4 4v14H6V3Zm8 0v5h5M9 13h6m-6 4h5" /></svg> }

type PendingAttachment = { id: string; file: File; preview: string }
const attachmentAccept = 'image/png,image/jpeg,image/webp,image/gif,text/*,application/json,application/xml,application/pdf,.md,.log,.csv,.yaml,.yml,.go,.java,.py,.js,.ts,.tsx,.jsx,.css,.html,.sh,.sql,.properties,.toml,.ini,.conf'
const textAttachmentExtensions = new Set(['txt', 'md', 'log', 'csv', 'json', 'xml', 'yaml', 'yml', 'go', 'java', 'py', 'js', 'ts', 'tsx', 'jsx', 'css', 'html', 'sh', 'sql', 'properties', 'toml', 'ini', 'conf'])

function supportedAttachment(file: File) {
  const extension = file.name.split('.').pop()?.toLowerCase() || ''
  return ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'application/pdf', 'application/json', 'application/xml', 'application/javascript', 'application/yaml', 'application/x-yaml'].includes(file.type)
    || file.type.startsWith('text/') || textAttachmentExtensions.has(extension)
}

function encodeAttachment(item: PendingAttachment): Promise<AttachmentInput> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(new Error(`无法读取附件 ${item.file.name}`))
    reader.onload = () => {
      const value = String(reader.result || '')
      const marker = value.indexOf(',')
      if (marker < 0) { reject(new Error(`无法编码附件 ${item.file.name}`)); return }
      resolve({ name: item.file.name, mimeType: item.file.type || 'application/octet-stream', size: item.file.size, data: value.slice(marker + 1) })
    }
    reader.readAsDataURL(item.file)
  })
}

function attachmentTypeLabel(file: File) {
  if (file.type.startsWith('image/')) return '图片'
  if (file.type === 'application/pdf') return 'PDF'
  return '文本文件'
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(value < 10 * 1024 ? 1 : 0)} KiB`
  return `${(value / 1024 / 1024).toFixed(1)} MiB`
}

function formatTime(value: string) { const date = new Date(value); return date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' }) + ' ' + date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }
function statusLabel(value: Session['status']) { return value === 'idle' ? '完成' : value === 'queued' ? '排队中' : value === 'running' ? '运行中' : value === 'failed' ? '失败' : '已停止' }
function formatDuration(ms: number) { return ms > 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms` }
function formatTokens(value: number) { return value >= 1000 ? `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)}k` : value.toLocaleString() }
function historyModeLabel(value: string) { return value === 'full_history' ? '完整历史' : value === 'provider_continuation' ? 'Provider 续接' : value === 'responses_full_input' ? 'Responses 全量' : value === 'summary_history' ? '摘要 + 最近历史' : '等待识别' }
function recordLines(value: Record<string, string>) { return Object.entries(value || {}).map(([key, item]) => `${key}=${item}`).join('\n') }
function parseRecord(value: string) { const result: Record<string, string> = {}; value.split('\n').forEach((line) => { const index = line.indexOf('='); if (index > 0) result[line.slice(0, index).trim()] = line.slice(index + 1) }); return result }

export default App
