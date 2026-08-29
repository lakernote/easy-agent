import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { api } from './api'
import type { AttachmentInput, Bootstrap, MCPConfig, ModelSettings, Session, Skill, TraceEvent } from './types'

type Page = 'chat' | 'skills' | 'capabilities'
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
        <div className="mobile-brand">EA</div>
        <div className="topbar-title">{page === 'chat' ? (session?.title || '新会话') : page === 'skills' ? 'Skills' : '模型与工具'}</div>
        <div className="topbar-actions"><span className={`model-dot ${modelReady ? 'ready' : ''}`} /><span className="model-name">{modelLabel}</span>{page === 'chat' && session && isActive(session.status) && <button className="stop-button" onClick={stopSession}>停止</button>}{page === 'chat' && session && <button className="ghost-button" onClick={() => setTraceOpen(!traceOpen)}>Trace · {session.events.length}</button>}</div>
      </header>
      {error && <div className="toast" role="alert"><span>{friendlyError(error)}</span><button aria-label="关闭错误提示" onClick={() => setError('')}>×</button></div>}
      {page === 'chat' && <Chat session={session} data={data} onSession={setSession} onRefresh={refresh} onError={setError} onOpenCapabilities={() => setPage('capabilities')} />}
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

  const remove = async (item: Session) => {
    if (!window.confirm(`删除会话“${item.title}”及其全部 Trace？\n\n此操作不能撤销。`)) return
    try {
      await api.deleteSession(item.id)
      if (session?.id === item.id) onNew()
      await onRefresh()
      showFeedback('会话已删除')
    } catch (reason) { onError((reason as Error).message) }
  }

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

  const removeSelected = async () => {
    const targets = selectableSessions.filter((item) => selectedIds.has(item.id))
    if (!targets.length || !window.confirm(`确认删除选中的 ${targets.length} 条会话及全部 Trace？\n\n此操作不能撤销。`)) return
    setDeleting(true); onError('')
    let removed = 0
    try {
      for (const item of targets) { await api.deleteSession(item.id); removed += 1 }
      if (session && selectedIds.has(session.id)) onNew()
      setSelectedIds(new Set()); setManaging(false)
      await onRefresh()
      showFeedback(`已删除 ${removed} 条会话`)
    } catch (reason) {
      await onRefresh().catch(() => undefined)
      onError(`${removed ? `已删除 ${removed} 条；` : ''}${(reason as Error).message}`)
    } finally { setDeleting(false) }
  }

  const leaveManaging = () => { setManaging(false); setSelectedIds(new Set()) }
  return <aside className="sidebar">
    <div className="brand"><div className="brand-mark">E</div><div><strong>EasyAgent</strong><small>一个核心智能体</small></div></div>
    <button className="new-chat" onClick={onNew}><span>＋</span> 新会话 <kbd>⌘ K</kbd></button>
    <nav className="primary-nav">
      <button className={page === 'chat' ? 'active' : ''} onClick={() => onPage('chat')}><Icon name="chat" />对话</button>
      <button className={page === 'skills' ? 'active' : ''} onClick={() => onPage('skills')}><Icon name="skill" />Skills <em>{data.skills.filter((item) => item.enabled).length}</em></button>
      <button className={page === 'capabilities' ? 'active' : ''} onClick={() => onPage('capabilities')}><Icon name="plug" />模型与工具</button>
    </nav>
    <div className="session-label"><span>会话 <small>{data.sessions.length}</small></span><div><button onClick={managing ? leaveManaging : () => setManaging(true)}>{managing ? '完成' : '管理'}</button><button aria-label="刷新会话" title="刷新会话" onClick={() => onRefresh().catch((reason) => onError(reason.message))}>↻</button></div></div>
    <div className="session-controls">
      <label className="session-search"><span aria-hidden="true">⌕</span><input type="search" value={query} onChange={(event) => { setQuery(event.target.value); setSelectedIds(new Set()) }} placeholder="搜索标题或模型" aria-label="搜索会话" /></label>
      <select value={sort} onChange={(event) => setSort(event.target.value as 'newest' | 'oldest')} aria-label="按时间排序"><option value="newest">最新</option><option value="oldest">最早</option></select>
    </div>
    {managing && <div className="session-manage"><button onClick={toggleAll} disabled={!selectableSessions.length}>{allSelected ? '取消全选' : '全选'}</button><span>已选 {selectedCount}</span><button className="manage-delete" onClick={removeSelected} disabled={!selectedCount || deleting}>{deleting ? '删除中…' : `删除${selectedCount ? ` (${selectedCount})` : ''}`}</button></div>}
    <div className="session-list">
      {data.sessions.length === 0 && <div className="empty-list">还没有对话</div>}
      {data.sessions.length > 0 && visibleSessions.length === 0 && <div className="empty-list"><strong>没有匹配的会话</strong><button onClick={() => setQuery('')}>清空搜索</button></div>}
      {visibleSessions.map((item) => <div key={item.id} className={`session-row ${session?.id === item.id ? 'active' : ''} ${managing ? 'managing' : ''}`}>
        {managing && <label className="session-select" title={isActive(item.status) ? '运行中的会话不能删除' : '选择会话'}><input type="checkbox" checked={selectedIds.has(item.id)} disabled={isActive(item.status)} onChange={() => toggleSelected(item.id)} aria-label={`选择会话 ${item.title}`} /></label>}
        <button className="session-open" onClick={() => onOpen(item.id)} aria-current={session?.id === item.id ? 'page' : undefined} title={item.title}><span className={`status ${item.status}`} /><span className="session-copy"><strong>{item.title}</strong><small>{formatTime(item.updatedAt)} · {statusLabel(item.status)}{item.model ? ` · ${item.model}` : ''}</small></span></button>
        {!managing && !isActive(item.status) && <button className="session-delete" aria-label={`删除会话 ${item.title}`} title="删除会话" onClick={() => remove(item)}><TrashIcon /></button>}
      </div>)}
    </div>
    <div className="sidebar-feedback" aria-live="polite">{feedback}</div>
    <div className="sidebar-foot"><span className="service-dot" />本地服务正常 <small>v0.1</small></div>
  </aside>
}

function Chat({ session, data, onSession, onRefresh, onError, onOpenCapabilities }: { session: Session | null; data: Bootstrap; onSession: (session: Session) => void; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void; onOpenCapabilities: () => void }) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [attachments, setAttachments] = useState<PendingAttachment[]>([])
  const [attachmentError, setAttachmentError] = useState('')
  const [dragging, setDragging] = useState(false)
  const endRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const attachmentRef = useRef<PendingAttachment[]>([])
  useEffect(() => { endRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [session?.messages.length, session?.status, session?.partialOutput])
  useEffect(() => {
    if (!textareaRef.current) return
    textareaRef.current.style.height = 'auto'
    textareaRef.current.style.height = `${Math.min(textareaRef.current.scrollHeight, 180)}px`
  }, [draft])
  useEffect(() => { attachmentRef.current = attachments }, [attachments])
  useEffect(() => () => attachmentRef.current.forEach((item) => item.preview && URL.revokeObjectURL(item.preview)), [])

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

  const send = async (preset?: string) => {
    const message = (preset ?? draft).trim()
    if ((!message && attachments.length === 0) || sending || isActive(session?.status)) return
    setSending(true); onError(''); setAttachmentError('')
    try {
      const payload = await Promise.all(attachments.map(encodeAttachment))
      const next = session ? await api.sendMessage(session.id, message, payload) : await api.createSession(message, payload)
      onSession(next); setDraft(''); attachments.forEach((item) => item.preview && URL.revokeObjectURL(item.preview)); setAttachments([]); await onRefresh()
    } catch (reason) {
      const message = (reason as Error).message
      if (/附件|Base64|MiB|格式/.test(message)) setAttachmentError(message)
      else onError(message)
    } finally { setSending(false) }
  }

  const suggestions = ['今天星期几？', '帮我分析这段错误日志', '设计一个简单的 REST API', '解释 Go 的 interface 和 Java interface 的区别']
  return <section className="chat-page">
    <div className="conversation">
      {!session && <div className="welcome"><div className="agent-orb"><span>EA</span></div><p className="eyebrow">一个智能体 · 处理各种任务</p><h1>想解决什么问题？</h1><p>无需先建项目。直接提问；需要代码、日志或外部系统时，再添加上下文、Skill 或 MCP。</p><div className="suggestions">{suggestions.map((value) => <button key={value} onClick={() => send(value)}>{value}<span>↗</span></button>)}</div></div>}
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
    <div className="composer-wrap"><div className={`composer ${dragging ? 'dragging' : ''}`} onDragEnter={(event) => { event.preventDefault(); setDragging(true) }} onDragOver={(event) => event.preventDefault()} onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setDragging(false) }} onDrop={(event) => { event.preventDefault(); setDragging(false); addFiles(event.dataTransfer.files) }}>
      {attachments.length > 0 && <div className="attachment-preview-list" aria-label="待发送附件">{attachments.map((item) => <div className="attachment-preview" key={item.id}>{item.preview ? <img src={item.preview} alt={item.file.name} /> : <span className="attachment-file-icon"><FileIcon /></span>}<span><strong title={item.file.name}>{item.file.name}</strong><small>{attachmentTypeLabel(item.file)} · {formatBytes(item.file.size)}</small></span><button type="button" disabled={sending || isActive(session?.status)} aria-label={`移除附件 ${item.file.name}`} onClick={() => removeAttachment(item.id)}><CloseIcon /></button></div>)}</div>}
      <textarea ref={textareaRef} value={draft} onChange={(event) => setDraft(event.target.value)} aria-label="消息内容" aria-describedby="composer-help attachment-error" placeholder={attachments.length ? '描述希望 Agent 如何处理这些附件…' : '给 EasyAgent 发消息…'} rows={1} onPaste={(event) => { const files = Array.from(event.clipboardData.files); if (files.length) { event.preventDefault(); addFiles(files) } }} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }} />
      <div className="composer-toolbar"><div className="composer-tools"><button type="button" className="attach-button" disabled={sending || isActive(session?.status)} aria-label="添加文件或图片" onClick={() => fileInputRef.current?.click()}><AttachIcon /><span>添加附件</span></button><small>图片、文本、代码或 PDF</small><input ref={fileInputRef} className="visually-hidden" type="file" multiple tabIndex={-1} aria-hidden="true" accept={attachmentAccept} onChange={(event) => { if (event.target.files) addFiles(event.target.files); event.target.value = '' }} /></div><button type="button" className="send-button" aria-label={sending ? '正在发送' : '发送消息'} disabled={(!draft.trim() && attachments.length === 0) || sending || isActive(session?.status)} onClick={() => send()}>{sending ? <span className="send-spinner" /> : <SendIcon />}</button></div>
      {attachmentError && <div id="attachment-error" className="composer-error" role="alert">{attachmentError}</div>}
    </div><small id="composer-help" className="composer-hint">Enter 发送 · Shift + Enter 换行 · 可拖入或粘贴 · 单文件最大 5 MiB · 图片/PDF 需要当前模型支持多模态</small></div>
  </section>
}

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

function Avatar() { return <div className="avatar">E</div> }
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
      <span>{context.userTurns} 轮 · {context.historyMessages} 条历史</span>
      <span>{historyModeLabel(context.historyMode)}</span>
      <span>{context.cacheReported ? `缓存 ${cacheRate}%` : '缓存未上报'}</span>
      <em>{context.compressionCount > 0 ? `已压缩 ${context.compressionCount} 次` : context.compressionMode === 'auto' ? `自动 ${context.compressionThresholdPercent}%` : '压缩停用'}</em>
    </summary>
    <div className="context-details">
      <ContextDatum label="最近一次模型输入" value={context.lastInputTokens > 0 ? `${context.lastInputTokens.toLocaleString()} Token` : session.status === 'failed' ? '本轮 Token 未上报' : '尚无数据'} hint={context.contextWindowTokens > 0 ? `模型窗口 ${context.contextWindowTokens.toLocaleString()} · 使用 ${utilization}%` : '模型没有提供窗口上限，请在“模型与工具”中填写'} />
      <ContextDatum label="Chat History" value={`${context.userTurns} 轮 · ${context.historyMessages} 条消息`} hint={`最近请求发送 ${context.requestMessages || '—'} 条消息项 · ${context.toolDefinitions || 0} 个工具定义`} />
      <ContextDatum label="Prompt Cache" value={context.cacheReported ? `命中 ${context.lastCachedTokens.toLocaleString()} · ${cacheRate}%` : 'Provider 未上报'} hint={context.cacheReported ? `本次写入 ${context.lastCacheWriteTokens.toLocaleString()} Token` : '不等于确认没有缓存，只表示响应中没有缓存字段'} />
      <ContextDatum label="上下文压缩" value={context.compressionCount > 0 ? `${context.compressionCount} 次 · 摘要代表 ${context.compressedMessages} 条` : context.compressionMode === 'auto' ? `自动 · ${context.compressionThresholdPercent}% 触发` : '已停用'} hint={context.compressionCount > 0 ? `最近 ${context.retainedMessages} 条仍原样发送；SQLite 永久保留全部 ${context.historyMessages} 条消息` : '达到阈值后生成结构化检查点，并保留最近原始轮次；不会静默删除历史'} />
    </div>
  </details>
}

function ContextDatum({ label, value, hint }: { label: string; value: string; hint: string }) {
  return <div><span>{label}</span><strong>{value}</strong><small>{hint}</small></div>
}

function TracePanel({ session, onClose }: { session: Session; onClose: () => void }) {
  const cacheRate = session.usage.cacheReported && session.usage.cacheInputTokens ? Math.round(session.usage.cachedTokens / session.usage.cacheInputTokens * 100) : 0
  const context = session.context
  return <aside className="trace-panel"><div className="trace-head"><div><p className="eyebrow">AUDITABLE RUNTIME</p><h2>Agent Trace</h2></div><button onClick={onClose}>×</button></div><div className="metrics"><Metric label="LLM" value={`${session.usage.modelCalls} 次`} sub={formatDuration(session.usage.modelDurationMs)} /><Metric label="工具" value={`${session.usage.toolCalls} 次`} sub={formatDuration(session.usage.toolDurationMs)} /><Metric label="Token" value={session.usage.totalTokens.toLocaleString()} sub={`入 ${session.usage.inputTokens} · 出 ${session.usage.outputTokens}`} /><Metric label="Prompt Cache" value={session.usage.cacheReported ? `${cacheRate}%` : '未上报'} sub={session.usage.cacheReported ? `命中 ${session.usage.cachedTokens} · 写入 ${session.usage.cacheWriteTokens}` : 'Provider 未返回缓存字段'} /></div><div className="context-ledger"><div><span>最近上下文</span><strong>{context.lastInputTokens ? formatTokens(context.lastInputTokens) : session.status === 'failed' ? '未上报' : '—'}{context.contextWindowTokens ? ` / ${formatTokens(context.contextWindowTokens)}` : ''}</strong></div><div><span>Chat History</span><strong>{context.userTurns} 轮 · {context.historyMessages} 条</strong></div><div><span>发送方式</span><strong>{historyModeLabel(context.historyMode)}</strong></div><div><span>压缩</span><strong>{context.compressionCount > 0 ? `${context.compressionCount} 次 · ${context.compressedMessages} 条` : context.compressionMode === 'auto' ? `自动 ${context.compressionThresholdPercent}%` : '已停用'}</strong></div></div><div className="trace-events">{session.events.length === 0 && <div className="trace-empty">还没有 Trace</div>}{session.events.map((event) => <TraceRow key={event.id} event={event} />)}</div></aside>
}

function TraceRow({ event }: { event: TraceEvent }) {
  const isModelResult = event.kind === 'model_end' || event.kind === 'compaction_end'
  const title = event.kind === 'model_start' ? '准备调用模型' : event.kind === 'model_end' ? `LLM · ${event.name || '模型'}` : event.kind === 'compaction_start' ? '准备压缩上下文' : event.kind === 'compaction_end' ? `上下文检查点 · ${event.name || '模型'}` : event.kind === 'tool_start' ? `准备工具 · ${event.name}` : event.kind === 'tool_end' ? `工具 · ${event.name}` : `MCP · ${event.name}`
  const cacheRate = event.cacheReported && event.inputTokens ? Math.round((event.cachedTokens || 0) / event.inputTokens * 100) : 0
  const tokenMissing = event.status === 'error' && !event.totalTokens && !event.inputTokens && !event.outputTokens
  return <details className={`trace-row ${event.status}`} open={isModelResult && event.status === 'error'}><summary><span className="trace-node" /><div><strong>{title}</strong><small>Step {event.step || '—'} · {event.durationMs || 0} ms {event.totalTokens ? `· ${event.totalTokens} tokens` : tokenMissing ? '· Token 未上报' : ''}{isModelResult ? ` · ${event.cacheReported ? `缓存 ${cacheRate}%` : '缓存未上报'}` : ''}</small></div><em>{event.status}</em></summary>{event.detail && <p className="event-error">{event.detail}</p>}{isModelResult && <div className="event-usage"><span>输入 <b>{tokenMissing ? '未上报' : (event.inputTokens || 0).toLocaleString()}</b></span><span>输出 <b>{tokenMissing ? '未上报' : (event.outputTokens || 0).toLocaleString()}</b></span><span>缓存命中 <b>{event.cacheReported ? (event.cachedTokens || 0).toLocaleString() : '未上报'}</b></span><span>缓存写入 <b>{event.cacheReported ? (event.cacheWriteTokens || 0).toLocaleString() : '未上报'}</b></span><span>历史 <b>{historyModeLabel(event.historyMode || '')} · {event.requestMessages || 0} 项</b></span><span>工具定义 <b>{event.toolDefinitions || 0}</b></span></div>}{event.input && <div><label>输入</label><Payload value={event.input} /></div>}{event.output && <div><label>输出</label><Payload value={event.output} /></div>}</details>
}

function Metric({ label, value, sub }: { label: string; value: string; sub: string }) { return <div><span>{label}</span><strong>{value}</strong><small>{sub}</small></div> }
function Payload({ value }: { value: string }) { const formatted = useMemo(() => { try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value } }, [value]); return <pre>{formatted}</pre> }

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
    <div className="page-intro"><p className="eyebrow">按需加载</p><h1>Skills</h1><p>基础提示词保持精简；只有任务相关时，Agent 才通过 <code>load_skill</code> 读取完整说明，减少无效 Token。</p></div>
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
  const [savingMCP, setSavingMCP] = useState(false)
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
  const useModelPreset = (preset: Bootstrap['modelPresets'][number]) => setModel({
    ...model,
    provider: preset.provider,
    protocol: preset.protocol,
    baseUrl: preset.baseUrl,
    model: preset.model,
    apiKey: '',
    apiKeyEnv: preset.ready ? preset.apiKeyEnv : '',
    thinking: preset.thinking,
    maxOutputTokens: model.maxOutputTokens || data.modelRules.defaultMaxOutputTokens,
    requestTimeoutSeconds: model.requestTimeoutSeconds || data.modelRules.defaultRequestTimeoutSeconds,
    compressionThresholdPercent: model.compressionThresholdPercent || data.modelRules.defaultCompressionThresholdPercent,
    secretConfigured: preset.ready,
  })
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
  const removeMCP = async () => { if (!mcp || !window.confirm(`删除 MCP「${mcp.name}」？`)) return; try { await api.deleteMCP(mcp.id); await onRefresh(); setMCP(null) } catch (reason) { onError((reason as Error).message) } }
  const testMCP = async (id: string) => { setMCPNotice(null); try { const result = await api.testMCP(id); setMCPNotice({ ready: true, title: `连接成功 · ${result.tools.length} 个工具`, message: 'MCP 握手和工具清单读取正常。', tools: result.tools.map((item) => item.name) }) } catch (reason) { onError((reason as Error).message) } }
  return <section className="settings-page capabilities"><div className="page-intro"><p className="eyebrow">可插拔能力</p><h1>模型与工具</h1><p>模型、内置 Tool、MCP 和基础提示词分开管理；启用后统一注册给同一个核心 Agent。</p></div>
    <div className="section-block"><div className="section-heading"><div><h2>模型</h2><p>支持 OpenAI Chat Completions 和 Responses 兼容接口。</p></div><span className="tag">{model.protocol}</span></div>
      <div className="model-presets"><span>免费额度模板 · 仍需注册自己的 API Key，额度和地区以厂商为准</span>{data.modelPresets.map((preset) => { const selected = model.provider === preset.provider && model.baseUrl === preset.baseUrl && model.model === preset.model; return <button key={preset.id} type="button" className={selected ? 'active' : ''} aria-pressed={selected} onClick={() => useModelPreset(preset)}><strong>{preset.name}</strong><small>{preset.description}</small><em>{preset.ready ? `已检测到 ${preset.apiKeyEnv}` : `需要 ${preset.apiKeyEnv}`}</em></button> })}</div>
      <div className="form-grid"><label>提供方<input value={model.provider} onChange={(e) => setModel({ ...model, provider: e.target.value })} /></label><label>协议<select value={model.protocol} onChange={(e) => setModel({ ...model, protocol: e.target.value as ModelSettings['protocol'] })}><option value="chat_completions">Chat Completions</option><option value="responses">Responses</option></select></label><label className="wide">Base URL<input value={model.baseUrl} onChange={(e) => setModel({ ...model, baseUrl: e.target.value })} /></label><label>模型名称<input value={model.model} onChange={(e) => setModel({ ...model, model: e.target.value })} /></label><label>推理模式<select value={model.thinking || ''} onChange={(e) => setModel({ ...model, thinking: e.target.value })}><option value="">模型默认</option><option value="disabled">尝试关闭推理</option></select><small>兼容服务建议使用模型默认；部分模型不支持关闭推理</small></label><label>最大输出 Token<input type="number" value={model.maxOutputTokens} onChange={(e) => setModel({ ...model, maxOutputTokens: Number(e.target.value) })} /></label><label>模型超时（秒）<input type="number" min={data.modelRules.minRequestTimeoutSeconds} max={data.modelRules.maxRequestTimeoutSeconds} value={model.requestTimeoutSeconds} onChange={(e) => setModel({ ...model, requestTimeoutSeconds: Number(e.target.value) })} /><small>默认 {data.modelRules.defaultRequestTimeoutSeconds} 秒；单次请求最多 {data.modelRules.maxRequestTimeoutSeconds} 秒</small></label><label>上下文窗口 Token<input type="number" min="0" value={model.contextWindowTokens || 0} onChange={(e) => setModel({ ...model, contextWindowTokens: Number(e.target.value) })} /><small>0 表示未知；Ollama 运行后读取当前实际窗口</small></label><label>自动压缩阈值<input type="number" min={data.modelRules.minCompressionThresholdPercent} max={data.modelRules.maxCompressionThresholdPercent} value={model.compressionThresholdPercent} onChange={(e) => setModel({ ...model, compressionThresholdPercent: Number(e.target.value) })} /><small>默认达到上下文窗口的 {data.modelRules.defaultCompressionThresholdPercent}% 后生成检查点</small></label><label>API Key<input type="password" placeholder={model.secretConfigured ? '已配置，留空不修改' : '可留空'} value={model.apiKey || ''} onChange={(e) => setModel({ ...model, apiKey: e.target.value })} /></label><label>API Key 环境变量<input placeholder="例如 OPENAI_API_KEY" value={model.apiKeyEnv || ''} onChange={(e) => setModel({ ...model, apiKeyEnv: e.target.value })} /></label></div>{modelNotice && <div role="status" aria-live="polite" className={`model-notice ${modelNotice.ready ? 'ready' : 'failed'}`}><div><strong>{modelNotice.title}</strong><span>{modelNotice.message}</span></div><button aria-label="关闭模型测试结果" onClick={() => setModelNotice(null)}>×</button></div>}<div className="form-actions"><button className="ghost-button" disabled={testingModel} onClick={testModel}>{testingModel ? '正在验证 Function Calling…' : '测试当前模型'}</button><button className="primary-button" onClick={saveModel}>保存模型</button></div><div className="ollama-strip"><div><strong><span className={`service-dot ${data.ollama.running ? '' : 'off'}`} />Ollama · 无需 API Key</strong><small>{data.ollama.message}</small></div><div>{data.ollama.models.map((item) => <button key={item.name} className="ghost-button" onClick={() => useOllama(item.name)}>使用 {item.name}</button>)}</div></div></div>
    <div className="section-block"><div className="section-heading"><div><h2>内置 Tools</h2><p>随 Go 二进制发布，无需安装；由模型通过 Function Calling 自主选择。</p></div><span className="tag">{data.builtinTools.length} 个</span></div><div className="tool-table">{data.builtinTools.map((tool) => <div key={tool.name}><code>{tool.name}</code><span>{tool.description}</span><em>{tool.category || tool.source}</em></div>)}</div></div>
    <div className="section-block"><div className="section-heading"><div><h2>MCP Servers</h2><p>用于浏览器、代码托管和其他外部系统；远端工具按需转换成 <code>mcp__服务__工具</code>。</p></div><button className="ghost-button" onClick={() => setMCP({ id: `mcp-${Date.now()}`, name: 'New MCP', description: '', enabled: false, transport: 'http', args: [], headers: {}, environment: {} })}>＋ 自定义</button></div><div className="capability-note"><strong>工作区文件无需 MCP</strong><span>read、grep、find、ls、edit、write 已内置；Filesystem 只用于额外挂载目录。</span></div>{mcpNotice && <div role="status" aria-live="polite" className={`mcp-notice ${mcpNotice.ready ? 'ready' : 'failed'}`}><div><strong>{mcpNotice.title}</strong><span>{mcpNotice.message}</span></div>{mcpNotice.tools.length > 0 && <details><summary>查看 {mcpNotice.tools.length} 个工具</summary><code>{mcpNotice.tools.join('\n')}</code></details>}<button aria-label="关闭 MCP 状态" onClick={() => setMCPNotice(null)}>×</button></div>}<div className="mcp-grid">{data.mcps.map((item) => { const preset = data.mcpPresets.find((candidate) => candidate.id === item.id); const canInstall = !item.enabled && preset?.action === 'install'; return <div className="mcp-row" key={item.id}><div><span className={`status ${item.enabled ? 'idle' : 'off'}`} /><strong>{preset?.name || item.name}</strong><small title={preset?.description || item.description}>{preset?.description || item.description || (item.transport === 'stdio' ? `${item.command} ${item.args.join(' ')}` : item.endpoint)}</small></div><span>{item.enabled ? '已启用' : '已停用'}</span><button disabled={installingPreset === item.id} onClick={() => canInstall && preset ? installPreset(preset) : testMCP(item.id)}>{installingPreset === item.id ? '检测中…' : canInstall ? '检测并启用' : '测试'}</button><button onClick={() => setMCP({ ...item, name: preset?.name || item.name, description: preset?.description || item.description })}>编辑</button></div> })}</div><div className="presets"><span>MCP 预设 · 仅在任务需要时启用，不会预加载全部工具</span>{data.mcpPresets.filter((preset) => !data.mcps.some((item) => item.id === preset.id)).map((preset) => <button key={preset.id} type="button" disabled={!!installingPreset} onClick={() => installPreset(preset)}><strong>{preset.name}</strong><small>{preset.description}</small><em>{preset.requirement}</em><b>{installingPreset === preset.id ? '正在检测…' : preset.action === 'install' ? '检测并启用' : '配置'}</b></button>)}</div></div>
    <details className="prompt-block"><summary><div><h2>基础 System Prompt</h2><p>独立 Markdown 包，只定义稳定行为；项目做法写进 Skill。</p></div><span>查看</span></summary><pre>{data.systemPrompt}</pre></details>
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
      <div className="form-actions"><button className="ghost-button danger" disabled={savingMCP} onClick={removeMCP}>删除</button><button className="primary-button" disabled={savingMCP} onClick={saveMCP}>{savingMCP ? '正在验证…' : mcp.enabled ? '验证并启用' : '保存配置'}</button></div>
    </div></div>}
  </section>
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
