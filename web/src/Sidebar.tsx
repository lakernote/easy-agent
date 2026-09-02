import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from './api'
import type { Bootstrap, Session } from './types'
import { isActive, type Page } from './sessionState'
import { formatTime, statusLabel } from './format'
import { Logo, Icon, TrashIcon } from './ui'
import { ConfirmDialog } from './dialogs'
export function Sidebar({ page, data, session, onPage, onOpen, onNew, onRefresh, onLoadOlder, onError }: { page: Page; data: Bootstrap; session: Session | null; onPage: (page: Page) => void; onOpen: (id: string) => void; onNew: () => void; onRefresh: () => Promise<Bootstrap>; onLoadOlder: (beforeUpdatedAt: string, beforeID: string) => Promise<void>; onError: (value: string) => void }) {
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<'newest' | 'oldest'>('newest')
  const [managing, setManaging] = useState(false)
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [deleting, setDeleting] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<Session[]>([])
  const [feedback, setFeedback] = useState('')
  const sessionListRef = useRef<HTMLDivElement>(null)
  const loadingOlderSessionsRef = useRef(false)

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

  useEffect(() => {
    const node = sessionListRef.current
    if (!node || !data.sessionsHasMore || query.trim() || sort !== 'newest') return
    const loadOlder = async () => {
      if (loadingOlderSessionsRef.current || node.scrollTop + node.clientHeight < node.scrollHeight - 80) return
      const last = data.sessions.at(-1)
      if (!last) return
      loadingOlderSessionsRef.current = true
      try { await onLoadOlder(last.updatedAt, last.id) } catch (reason) { onError((reason as Error).message) }
      finally { loadingOlderSessionsRef.current = false }
    }
    node.addEventListener('scroll', loadOlder)
    return () => node.removeEventListener('scroll', loadOlder)
  }, [data.sessions, data.sessionsHasMore, query, sort, onLoadOlder, onError])

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
    <div ref={sessionListRef} className="session-list">
      {data.sessions.length === 0 && <div className="empty-list">还没有对话</div>}
      {data.sessions.length > 0 && visibleSessions.length === 0 && <div className="empty-list"><strong>没有匹配的会话</strong><button onClick={() => setQuery('')}>清空搜索</button></div>}
      {visibleSessions.map((item) => <div key={item.id} className={`session-row ${session?.id === item.id ? 'active' : ''} ${managing ? 'managing' : ''}`}>
        {managing && <label className="session-select" title={isActive(item.status) ? '运行中的会话不能删除' : '选择会话'}><input type="checkbox" checked={selectedIds.has(item.id)} disabled={isActive(item.status)} onChange={() => toggleSelected(item.id)} aria-label={`选择会话 ${item.title}`} /></label>}
        <button className="session-open" onClick={() => onOpen(item.id)} aria-current={session?.id === item.id ? 'page' : undefined} title={item.title}><span className={`status ${item.status}`} /><span className="session-copy"><strong>{item.title}</strong><small>{formatTime(item.updatedAt)} · {isActive(item.status) ? item.runProgress || '运行中' : `${statusLabel(item.status)} · ${item.runtime === 'codex' ? 'Codex' : 'EasyAgent'}${item.model ? ` · ${item.model}` : ''}`}</small></span></button>
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
