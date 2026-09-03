import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import type { Bootstrap, Session } from './types'
import { isActive, mergeSessionHistory, mergeSessionSnapshot, updateSessionSummary, type Page } from './sessionState'
import { friendlyError } from './dialogs'
import { Logo } from './ui'
import { Sidebar } from './Sidebar'
import { Chat } from './Chat'
import { TracePanel } from './TracePanel'
import { SettingsShell } from './SettingsShell'
export default function App() {
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
    const timer = window.setInterval(async () => {
      try {
        const current = await api.session(session.id)
        setSession((previous) => previous?.id === current.id ? mergeSessionSnapshot(previous, current) : current)
        setData((previous) => previous ? updateSessionSummary(previous, current) : previous)
        if (!isActive(current.status)) await refresh()
      } catch (reason) { setError((reason as Error).message) }
    }, 800)
    return () => window.clearInterval(timer)
  }, [session?.id, session?.status, refresh, setCurrentSession])

  const newChat = () => { setSession(null); setPage('chat'); setTraceOpen(false); setError('') }
  const stopSession = async () => {
    if (!session || !isActive(session.status)) return
    try {
      const next = await api.cancelSession(session.id)
      setCurrentSession(next)
      await refresh()
    } catch (reason) { setError((reason as Error).message) }
  }

  if (loading) return <div className="boot"><span className="spinner" />正在启动 EasyAgent…</div>
  if (!data) return <div className="boot error-page">无法读取服务：{error || '未知错误'}</div>

  return <div className="app-shell">
    <Sidebar page={page} data={data} session={session} onPage={setPage} onOpen={openSession} onNew={newChat} onRefresh={refresh} onLoadOlder={loadOlderSessions} onError={setError} />
    <main className={`main-canvas ${page === 'chat' ? 'chat-canvas' : 'settings-canvas'}`}>
      <header className="topbar">
        <div className="mobile-brand"><Logo /></div>
        <div className="topbar-title">{page === 'chat' ? (session?.title || '新会话') : '设置'}</div>
        <div className="topbar-actions">{page === 'chat' && session && isActive(session.status) && <button className="stop-button" onClick={stopSession}>停止</button>}{page === 'chat' && session && <button className="ghost-button trace-button" onClick={() => setTraceOpen(!traceOpen)}>Trace · {session.events.length}</button>}</div>
      </header>
      {error && <div className="toast" role="alert"><span>{friendlyError(error)}</span><button aria-label="关闭错误提示" onClick={() => setError('')}>×</button></div>}
      {page === 'chat' && <Chat session={session} data={data} onSession={setCurrentSession} onRefresh={refresh} onError={setError} onLoadOlder={loadSessionHistory} onOpenSkills={() => setPage('skills')} onOpenCapabilities={() => setPage('tools')} />}
      {page !== 'chat' && <SettingsShell page={page} data={data} onPage={setPage} onRefresh={refresh} onError={setError} />}
    </main>
    {traceOpen && session && <TracePanel session={session} onLoadOlder={loadSessionHistory} onError={setError} onClose={() => setTraceOpen(false)} />}
  </div>
}
