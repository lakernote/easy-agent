import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from './api'
import type { Bootstrap, Session } from './types'
import { isActive, type Page } from './sessionState'
import { formatTime, statusLabel } from './format'
import { ChevronIcon, FolderIcon, Icon, Logo, MoreIcon } from './ui'
import { ConfirmDialog, ProjectDialog, RenameSessionDialog } from './dialogs'

type Project = Bootstrap['projects'][number]
const collapsedProjectsKey = 'easyagent.sidebar.collapsed-projects'

function initialCollapsedProjects() {
  try {
    const value = JSON.parse(window.localStorage.getItem(collapsedProjectsKey) || '[]')
    return new Set<string>(Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [])
  } catch { return new Set<string>() }
}

export function Sidebar({ page, data, session, onPage, onOpen, onNew, onSession, onRefresh, onLoadOlder, onError }: { page: Page; data: Bootstrap; session: Session | null; onPage: (page: Page) => void; onOpen: (id: string) => void; onNew: () => void; onSession: (value: Session) => void; onRefresh: () => Promise<Bootstrap>; onLoadOlder: (beforeUpdatedAt: string, beforeID: string) => Promise<void>; onError: (value: string) => void }) {
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<'newest' | 'oldest'>('newest')
  const [managing, setManaging] = useState(false)
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [deleting, setDeleting] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<Session[]>([])
  const [feedback, setFeedback] = useState('')
  const [editingSession, setEditingSession] = useState<Session | null>(null)
  const [projectDialogOpen, setProjectDialogOpen] = useState(false)
  const [editingProject, setEditingProject] = useState<Project | null>(null)
  const [savingMetadata, setSavingMetadata] = useState(false)
  const [pendingProjectDelete, setPendingProjectDelete] = useState<Project | null>(null)
  const [collapsedProjects, setCollapsedProjects] = useState<Set<string>>(initialCollapsedProjects)
  const sessionListRef = useRef<HTMLDivElement>(null)
  const loadingOlderSessionsRef = useRef(false)
  const displayVersion = import.meta.env.VITE_APP_VERSION ? `v${import.meta.env.VITE_APP_VERSION}` : 'dev'

  const visibleSessions = useMemo(() => {
    const keyword = query.trim().toLocaleLowerCase()
    const projects = new Map(data.projects.map((item) => [item.id, item]))
    return data.sessions.filter((item) => {
      if (!keyword) return true
      const project = projects.get(item.projectId || '')
      return item.title.toLocaleLowerCase().includes(keyword) || (item.model || '').toLocaleLowerCase().includes(keyword) || (project?.name || '').toLocaleLowerCase().includes(keyword) || (project?.directories.join(' ') || '').toLocaleLowerCase().includes(keyword)
    }).slice().sort((left, right) => {
      const difference = new Date(left.updatedAt).getTime() - new Date(right.updatedAt).getTime()
      return sort === 'newest' ? -difference : difference
    })
  }, [data.projects, data.sessions, query, sort])

  const projectGroups = useMemo(() => {
    const groups: { project: Project | null; sessions: Session[] }[] = data.projects.map((project) => ({ project, sessions: visibleSessions.filter((item) => item.projectId === project.id) }))
    const known = new Set(data.projects.map((project) => project.id))
    const unassigned = visibleSessions.filter((item) => !item.projectId || !known.has(item.projectId))
    if (unassigned.length) groups.push({ project: null, sessions: unassigned })
    return groups.filter((group) => group.sessions.length > 0 || (!query.trim() && group.project))
  }, [data.projects, query, visibleSessions])

  const selectableSessions = visibleSessions.filter((item) => !isActive(item.status))
  const selectedCount = selectableSessions.filter((item) => selectedIds.has(item.id)).length
  const allSelected = selectableSessions.length > 0 && selectableSessions.every((item) => selectedIds.has(item.id))

  useEffect(() => {
    window.localStorage.setItem(collapsedProjectsKey, JSON.stringify([...collapsedProjects]))
  }, [collapsedProjects])

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

  const showFeedback = (value: string) => { setFeedback(value); window.setTimeout(() => setFeedback(''), 2400) }
  const requestRemove = (item: Session) => setPendingDelete([item])
  const toggleSelected = (id: string) => setSelectedIds((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })
  const toggleAll = () => setSelectedIds((current) => { const next = new Set(current); if (allSelected) selectableSessions.forEach((item) => next.delete(item.id)); else selectableSessions.forEach((item) => next.add(item.id)); return next })
  const requestRemoveSelected = () => { const targets = selectableSessions.filter((item) => selectedIds.has(item.id)); if (targets.length) setPendingDelete(targets) }
  const toggleProject = (id: string) => setCollapsedProjects((current) => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })

  const confirmRemove = async () => {
    if (!pendingDelete.length || deleting) return
    setDeleting(true); onError('')
    let removed = 0
    try {
      for (const item of pendingDelete) { await api.deleteSession(item.id); removed += 1 }
      if (session && pendingDelete.some((item) => item.id === session.id)) onNew()
      setSelectedIds(new Set()); setManaging(false); await onRefresh()
      showFeedback(removed === 1 ? '会话已删除' : `已删除 ${removed} 条会话`)
    } catch (reason) { await onRefresh().catch(() => undefined); onError(`${removed ? `已删除 ${removed} 条；` : ''}${(reason as Error).message}`) }
    finally { setDeleting(false); setPendingDelete([]) }
  }

  const saveSessionTitle = async (title: string, projectId: string) => {
    if (!editingSession || savingMetadata) return
    setSavingMetadata(true); onError('')
    try { const updated = await api.updateSession(editingSession.id, title, projectId); if (session?.id === updated.id) onSession(updated); await onRefresh(); setEditingSession(null); showFeedback('会话信息已更新') }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingMetadata(false) }
  }

  const openProject = (project: Project | null) => { setEditingProject(project); setProjectDialogOpen(true) }
  const saveProject = async (value: { name: string; directories: string[]; default: boolean }) => {
    if (savingMetadata) return
    setSavingMetadata(true); onError('')
    try { if (editingProject) await api.updateProject(editingProject.id, value); else await api.createProject(value); await onRefresh(); setProjectDialogOpen(false); setEditingProject(null); showFeedback(editingProject ? '项目已更新' : '项目已添加') }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingMetadata(false) }
  }

  const confirmProjectDelete = async () => {
    if (!pendingProjectDelete || savingMetadata) return
    setSavingMetadata(true); onError('')
    try { const removedProjectId = pendingProjectDelete.id; await api.deleteProject(removedProjectId); if (session?.projectId === removedProjectId) onSession(await api.session(session.id)); await onRefresh(); setPendingProjectDelete(null); setProjectDialogOpen(false); setEditingProject(null); showFeedback('项目配置已移除，服务器文件未删除') }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingMetadata(false) }
  }

  const renderSession = (item: Session) => <div key={item.id} className={`session-row ${session?.id === item.id ? 'active' : ''} ${managing ? 'managing' : ''}`}>
    {managing && <label className="session-select" title={isActive(item.status) ? '运行中的会话不能删除' : '选择会话'}><input type="checkbox" checked={selectedIds.has(item.id)} disabled={isActive(item.status)} onChange={() => toggleSelected(item.id)} aria-label={`选择会话 ${item.title}`} /></label>}
    <button className="session-open" onClick={() => onOpen(item.id)} aria-current={session?.id === item.id ? 'page' : undefined} title={item.title}><span className={`status ${item.status}`} /><span className="session-copy"><strong>{item.title}</strong><small>{formatTime(item.updatedAt)} · {isActive(item.status) ? item.runProgress || '运行中' : `${statusLabel(item.status)} · ${item.runtime === 'codex' ? 'Codex' : 'EasyAgent'}${item.model ? ` · ${item.model}` : ''}`}</small></span></button>
    {!managing && <button className="session-delete session-more" aria-label={`编辑会话 ${item.title}`} title="编辑会话" onClick={() => setEditingSession(item)}><MoreIcon /></button>}
  </div>

  const leaveManaging = () => { setManaging(false); setSelectedIds(new Set()) }
  return <aside className="sidebar">
    <div className="brand"><div className="brand-mark"><Logo /></div><div><strong>EasyAgent</strong><small>研发 · 测试 · 运维</small></div></div>
    <button className="new-chat" onClick={onNew}><span>＋</span> 新会话 <kbd>⌘ K</kbd></button>
    <nav className="primary-nav" aria-label="主导航"><button className={page === 'chat' ? 'active' : ''} aria-current={page === 'chat' ? 'page' : undefined} onClick={() => onPage('chat')}><Icon name="chat" />对话</button></nav>
    <div className="session-label"><span>项目与会话 <small>{data.sessions.length}</small></span><div><button aria-label="添加项目" title="添加项目" onClick={() => openProject(null)}>＋</button><button onClick={managing ? leaveManaging : () => setManaging(true)}>{managing ? '完成' : '管理'}</button><button aria-label="刷新会话" title="刷新会话" onClick={() => onRefresh().catch((reason) => onError(reason.message))}>↻</button></div></div>
    <div className="session-controls"><label className="session-search"><span aria-hidden="true">⌕</span><input type="search" value={query} onChange={(event) => { setQuery(event.target.value); setSelectedIds(new Set()) }} placeholder="搜索会话或项目" aria-label="搜索会话或项目" /></label><select value={sort} onChange={(event) => setSort(event.target.value as 'newest' | 'oldest')} aria-label="按时间排序"><option value="newest">最新</option><option value="oldest">最早</option></select></div>
    {managing && <div className="session-manage"><button onClick={toggleAll} disabled={!selectableSessions.length}>{allSelected ? '取消全选' : '全选'}</button><span>已选 {selectedCount}</span><button className="manage-delete" onClick={requestRemoveSelected} disabled={!selectedCount || deleting}>{deleting ? '删除中…' : `删除${selectedCount ? ` (${selectedCount})` : ''}`}</button></div>}
    <div ref={sessionListRef} className="session-list">
      {data.sessions.length === 0 && data.projects.length === 0 && <div className="empty-list">还没有项目和对话</div>}
      {data.sessions.length > 0 && visibleSessions.length === 0 && <div className="empty-list"><strong>没有匹配的会话</strong><button onClick={() => setQuery('')}>清空搜索</button></div>}
      {projectGroups.map((group) => {
        const groupID = group.project?.id || 'unassigned'
        const collapsed = collapsedProjects.has(groupID)
        const label = group.project?.name || '历史会话'
        const contentID = `project-sessions-${groupID}`
        return <section className={`session-project ${collapsed ? 'collapsed' : ''}`} key={groupID}><div className="session-project-head" title={group.project?.directories.join('\n') || '未归入项目'}><button className="session-project-toggle" type="button" aria-expanded={!collapsed} aria-controls={contentID} aria-label={`${collapsed ? '展开' : '收起'}项目 ${label}`} onClick={() => toggleProject(groupID)}><span className="session-project-chevron"><ChevronIcon /></span><FolderIcon /><strong>{label}</strong><small>{group.sessions.length}</small></button>{group.project && <button className="session-project-edit" type="button" aria-label={`编辑项目 ${group.project.name}`} title="编辑项目" onClick={() => openProject(group.project)}><MoreIcon /></button>}</div>{!collapsed && <div id={contentID} className="session-project-sessions" role="group" aria-label={`${label} 中的会话`}>{group.sessions.map(renderSession)}</div>}</section>
      })}
    </div>
    <button className={`sidebar-settings ${page !== 'chat' ? 'active' : ''}`} type="button" aria-label="设置" aria-current={page !== 'chat' ? 'page' : undefined} onClick={() => onPage('settings')}><Icon name="settings" /><span>设置</span></button>
    <div className="sidebar-feedback" aria-live="polite">{feedback}</div>
    <div className="sidebar-foot"><a href="https://github.com/lakernote/easy-agent" target="_blank" rel="noopener noreferrer" aria-label="在 GitHub 打开 EasyAgent 仓库"><strong>EasyAgent</strong><span aria-hidden="true">↗</span></a><small>{displayVersion}</small></div>
    {editingSession && <RenameSessionDialog session={editingSession} projects={data.projects} busy={savingMetadata} onCancel={() => setEditingSession(null)} onSave={(title, projectId) => void saveSessionTitle(title, projectId)} onDelete={() => { const value = editingSession; setEditingSession(null); requestRemove(value) }} />}
    {projectDialogOpen && <ProjectDialog project={editingProject} projectCount={data.projects.length} busy={savingMetadata} onCancel={() => { setProjectDialogOpen(false); setEditingProject(null) }} onSave={(value) => void saveProject(value)} onDelete={() => editingProject && setPendingProjectDelete(editingProject)} />}
    {pendingDelete.length > 0 && <ConfirmDialog title={pendingDelete.length === 1 ? '删除这个会话？' : `删除选中的 ${pendingDelete.length} 个会话？`} description="会话消息和对应的 Agent Trace 将一起删除，删除后无法恢复。" subject={pendingDelete.length === 1 ? pendingDelete[0].title : pendingDelete.map((item) => item.title).join('、')} confirmLabel={pendingDelete.length === 1 ? '删除会话' : `删除 ${pendingDelete.length} 个会话`} busy={deleting} onCancel={() => setPendingDelete([])} onConfirm={confirmRemove} />}
    {pendingProjectDelete && <ConfirmDialog title="移除这个本地项目？" description="只移除 EasyAgent 中的项目配置，不会删除服务器上的源文件夹；已有会话仍会保留。" subject={pendingProjectDelete.name} confirmLabel="移除项目" busy={savingMetadata} onCancel={() => setPendingProjectDelete(null)} onConfirm={() => void confirmProjectDelete()} />}
  </aside>
}
