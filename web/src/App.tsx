import { useCallback, useEffect, useState } from 'react'
import { APIError, api } from './api'
import type { Bootstrap, Session } from './types'
import { isActive, mergeSessionHistory, mergeSessionSnapshot, updateSessionSummary, type Page } from './sessionState'
import { ForkDialog, WorktreeDialog, friendlyError, type ForkWorkspaceMode } from './dialogs'
import { Logo } from './ui'
import { Sidebar } from './Sidebar'
import { Chat } from './Chat'
import { TracePanel } from './TracePanel'
import { SettingsShell } from './SettingsShell'
import { LoginPage } from './LoginPage'
export default function App() {
  const [data, setData] = useState<Bootstrap | null>(null)
  const [session, setSession] = useState<Session | null>(null)
  const [page, setPage] = useState<Page>('chat')
  const [traceOpen, setTraceOpen] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [authenticated, setAuthenticated] = useState(false)
  const [authError, setAuthError] = useState('')
  const [forkOpen, setForkOpen] = useState(false)
  const [forking, setForking] = useState(false)
  const [worktreeOpen, setWorktreeOpen] = useState(false)
  const [cleaningWorktree, setCleaningWorktree] = useState(false)

  const refresh = useCallback(async () => {
    const next = await api.bootstrap()
    setData(next)
    return next
  }, [])

  useEffect(() => {
    api.me().then(async (status) => {
      if (!status.authenticated) return
      await refresh()
      setAuthenticated(true)
    }).catch((reason) => {
      if (!(reason instanceof APIError) || reason.status !== 401) setAuthError((reason as Error).message)
    }).finally(() => setLoading(false))
  }, [refresh])

  const afterLogin = useCallback(async () => {
    await refresh()
    setAuthenticated(true)
    setAuthError('')
    setError('')
  }, [refresh])

  const logout = useCallback(async () => {
    try { await api.logout() } finally {
      setAuthenticated(false)
      setData(null)
      setSession(null)
    }
  }, [])

  const openSession = useCallback(async (id: string) => {
    setPage('chat')
    setError('')
    setForkOpen(false)
    setWorktreeOpen(false)
    try { setSession(await api.session(id)) } catch (reason) { setError((reason as Error).message) }
  }, [])

  const setCurrentSession = useCallback((value: Bootstrap['sessions'][number]) => {
    setSession(value)
    setData((current) => current ? updateSessionSummary(current, value) : current)
  }, [])

  const loadSessionHistory = useCallback(async (id: string, kind: 'messages' | 'events', before: number) => {
    const page = await api.sessionHistory(id, kind, before)
    setSession((current) => current?.id === id ? mergeSessionHistory(current, page, kind) : current)
  }, [])

  const loadOlderSessions = useCallback(async (beforeUpdatedAt: string, beforeID: string) => {
    const page = await api.olderSessions(beforeUpdatedAt, beforeID)
    setData((current) => {
      if (!current) return current
      const sessions = Array.from(new Map([...current.sessions, ...page.sessions].map((item) => [item.id, item])).values()).sort((left, right) => new Date(right.updatedAt).getTime() - new Date(left.updatedAt).getTime())
      return { ...current, sessions, sessionsHasMore: page.hasMore }
    })
  }, [])

  useEffect(() => {
    if (!session || !isActive(session.status)) return
    const stream = new EventSource(`/api/v1/sessions/${encodeURIComponent(session.id)}/stream`)
    const update = (event: MessageEvent<string>) => {
      try {
        const current = JSON.parse(event.data) as Session
        setSession((previous) => previous?.id === current.id ? mergeSessionSnapshot(previous, current) : current)
        setData((previous) => previous ? updateSessionSummary(previous, current) : previous)
        if (!isActive(current.status)) { stream.close(); void refresh() }
      } catch { setError('实时任务事件格式无效') }
    }
    stream.addEventListener('session', update as EventListener)
    stream.onerror = () => {
      // EventSource 会携带 Last-Event-ID 自动重连；短暂断网不打断服务器任务。
      if (stream.readyState === EventSource.CLOSED) setError('实时连接已关闭，请重新打开会话')
    }
    return () => stream.close()
  }, [session?.id, session?.status, refresh, setCurrentSession])

  const newChat = () => { setSession(null); setPage('chat'); setTraceOpen(false); setForkOpen(false); setWorktreeOpen(false); setError('') }
  const stopSession = async () => {
    if (!session || (!isActive(session.status) && session.status !== 'paused')) return
    try {
      const next = await api.cancelSession(session.id)
      setCurrentSession(next)
      await refresh()
    } catch (reason) { setError((reason as Error).message) }
  }

  const pauseSession = async () => {
    if (!session || session.status !== 'queued') return
    try {
      setCurrentSession(await api.pauseSession(session.id))
      await refresh()
    } catch (reason) { setError((reason as Error).message) }
  }

  const resumeSession = async () => {
    if (!session || session.status !== 'paused') return
    try {
      setCurrentSession(await api.resumeSession(session.id))
      await refresh()
    } catch (reason) { setError((reason as Error).message) }
  }

  const forkSession = async (workspaceMode: ForkWorkspaceMode) => {
    if (!session || session.runtime !== 'codex' || isActive(session.status) || session.status === 'paused') return
    setForking(true)
    try { setCurrentSession(await api.forkSession(session.id, workspaceMode)); await refresh(); setTraceOpen(false); setForkOpen(false) }
    catch (reason) { setError((reason as Error).message) }
    finally { setForking(false) }
  }

  const cleanupWorktree = async () => {
    if (!session?.worktreeBranch) return
    setCleaningWorktree(true)
    try { setCurrentSession(await api.cleanupWorktree(session.id)); await refresh(); setWorktreeOpen(false) }
    catch (reason) { setError((reason as Error).message) }
    finally { setCleaningWorktree(false) }
  }

  const resolveCodexRequest = async (value: unknown) => {
    if (!session?.codexRequest) return
    try {
      await api.resolveCodexRequest(session.id, session.codexRequest.id, value)
      setSession((current) => current ? { ...current, codexRequest: undefined } : current)
    } catch (reason) { setError((reason as Error).message) }
  }

  if (loading) return <div className="boot"><span className="spinner" />正在启动 EasyAgent…</div>
  if (!authenticated) return <LoginPage onLogin={afterLogin} initialError={authError} />
  if (!data) return <div className="boot error-page">无法读取服务：{error || '未知错误'}</div>

  return <div className="app-shell">
    <Sidebar page={page} data={data} session={session} onPage={setPage} onOpen={openSession} onNew={newChat} onRefresh={refresh} onLoadOlder={loadOlderSessions} onError={setError} />
    <main className={`main-canvas ${page === 'chat' ? 'chat-canvas' : 'settings-canvas'}`}>
      <header className="topbar">
        <button type="button" className="mobile-brand" aria-label="新会话" title="新会话" onClick={newChat}><Logo /></button>
        <div className="topbar-title">{page === 'chat' ? (session?.title || '新会话') : '设置'}</div>
        <div className="topbar-actions">
          {page === 'chat' && session?.worktreeBranch && <button className="ghost-button" onClick={() => setWorktreeOpen(true)}>工作树</button>}
          {page === 'chat' && session?.runtime === 'codex' && !isActive(session.status) && session.status !== 'paused' && <button className="ghost-button" onClick={() => setForkOpen(true)}>对话分支</button>}
          {page === 'chat' && session?.status === 'queued' && <button className="ghost-button" onClick={() => void pauseSession()}>暂停排队</button>}
          {page === 'chat' && session?.status === 'running' && <button className="stop-button" onClick={stopSession}>中断</button>}
          {page === 'chat' && session?.status === 'paused' && <><button className="ghost-button" onClick={() => void stopSession()}>取消任务</button><button className="primary-button" onClick={() => void resumeSession()}>继续</button></>}
          {page === 'chat' && session && <button className="ghost-button trace-button" onClick={() => setTraceOpen(!traceOpen)}>Trace · {session.events.length}</button>}
        </div>
      </header>
      {error && <div className="toast" role="alert"><span>{friendlyError(error)}</span><button aria-label="关闭错误提示" onClick={() => setError('')}>×</button></div>}
      {page === 'chat' && <Chat session={session} data={data} onSession={setCurrentSession} onRefresh={refresh} onError={setError} onLoadOlder={loadSessionHistory} onOpenSkills={() => setPage('skills')} onOpenCapabilities={() => setPage('tools')} />}
      {page !== 'chat' && <SettingsShell page={page} data={data} onPage={setPage} onRefresh={refresh} onError={setError} onLogout={logout} />}
    </main>
    {traceOpen && session && <TracePanel session={session} onLoadOlder={loadSessionHistory} onError={setError} onClose={() => setTraceOpen(false)} />}
    {forkOpen && <ForkDialog busy={forking} onCancel={() => setForkOpen(false)} onConfirm={(mode) => void forkSession(mode)} />}
    {worktreeOpen && session?.worktreeBranch && <WorktreeDialog session={session} busy={cleaningWorktree} onCancel={() => setWorktreeOpen(false)} onCleanup={() => void cleanupWorktree()} />}
    {session?.codexRequest && <CodexRequestPrompt request={session.codexRequest} onResolve={resolveCodexRequest} onCancel={stopSession} />}
  </div>
}

function CodexRequestPrompt({ request, onResolve, onCancel }: { request: NonNullable<Session['codexRequest']>; onResolve: (value: unknown) => Promise<void>; onCancel: () => Promise<void> }) {
  const approval = request.method === 'item/commandExecution/requestApproval' || request.method === 'item/fileChange/requestApproval'
  const elicitation = request.method === 'mcpServer/elicitation/request'
  const responseFor = (accepted: boolean) => approval ? { decision: accepted ? 'accept' : 'decline' } : { action: accepted ? 'accept' : 'decline' }
  return <div className="approval-backdrop" role="presentation"><section className="approval-dialog" role="dialog" aria-modal="true" aria-labelledby="approval-title"><p className="login-kicker">CODEX APP-SERVER</p><h2 id="approval-title">{approval || elicitation ? '需要你的确认' : 'Codex 请求输入'}</h2><p>{approval ? 'Codex 请求执行一个需要 UI 授权的操作。请确认下面的请求内容。' : elicitation ? 'MCP 请求用户确认或补充信息；当前页面支持接受或拒绝。' : '这个请求类型暂未提供专用表单，可查看原始参数后停止当前任务。'}</p><code className="approval-method">{request.method}</code><pre>{JSON.stringify(request.params, null, 2)}</pre><div className="approval-actions"><button className="ghost-button" type="button" onClick={() => void onCancel()}>停止任务</button>{(approval || elicitation) && <button className="ghost-button danger" type="button" onClick={() => void onResolve(responseFor(false))}>拒绝</button>}{(approval || elicitation) && <button className="primary-button" type="button" onClick={() => void onResolve(responseFor(true))}>允许</button>}</div></section></div>
}
