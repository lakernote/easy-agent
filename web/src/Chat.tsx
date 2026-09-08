import { useEffect, useRef } from 'react'
import type { Bootstrap, Session, TraceEvent } from './types'
import { starterSuggestions } from './suggestions'
import { Avatar, collectResearchCitations, ContextBar, Markdown, MessageView } from './chat/MessageContent'
import { Logo } from './ui'
import { RunError } from './dialogs'
import { ChatComposer } from './chat/ChatComposer'
import { useChatComposer } from './chat/useChatComposer'
import { CodexActivityGroup, ExecutionProgress, codexConversationActivities } from './chat/CapabilityActivity'

type ConversationItem = { kind: 'message'; createdAt: string; id: number; message: Session['messages'][number] } | { kind: 'activity'; createdAt: string; id: number; event: TraceEvent }
type GroupedConversationItem = ConversationItem | { kind: 'activity-group'; createdAt: string; id: number; events: TraceEvent[] }

export function Chat({ session, data, onSession, onRefresh, onError, onLoadOlder, onOpenSkills, onOpenCapabilities, onOpenModelSettings, onOpenTrace, onStop, onPause, onResume, runActionBusy }: { session: Session | null; data: Bootstrap; onSession: (session: Session) => void; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void; onLoadOlder: (id: string, kind: 'messages' | 'events', before: number) => Promise<void>; onOpenSkills: () => void; onOpenCapabilities: () => void; onOpenModelSettings: () => void; onOpenTrace: () => void; onStop: () => Promise<void>; onPause: () => Promise<void>; onResume: () => Promise<void>; runActionBusy: boolean }) {
  const endRef = useRef<HTMLDivElement>(null)
  const conversationRef = useRef<HTMLDivElement>(null)
  const loadingOlderRef = useRef(false)
  const stickToBottomRef = useRef(true)
  const previousSessionIDRef = useRef<string | undefined>(undefined)
  const composer = useChatComposer({ session, data, onSession, onRefresh, onError, onOpenSkills, onOpenCapabilities, onOpenModelSettings })
  const { isCodexRuntime, sending, send, startSuggestion } = composer
  const callsByID = new Map(session?.messages.flatMap((message) => message.toolCalls.map((call) => [call.id, call] as const)) || [])
  const researchCitations = collectResearchCitations(session?.messages || [])
  const conversationItems: ConversationItem[] = session ? [
    ...session.messages.map((message) => ({ kind: 'message' as const, createdAt: message.createdAt, id: message.id, message })),
    ...(session.runtime === 'codex' ? codexConversationActivities(session.events).map((event) => ({ kind: 'activity' as const, createdAt: event.createdAt, id: event.id, event })) : []),
  ].sort((left, right) => Date.parse(left.createdAt) - Date.parse(right.createdAt) || left.id - right.id) : []
  const groupedConversationItems = groupConversationActivities(conversationItems)

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
    if (!stickToBottomRef.current) return
    // 流式输出可能每几百毫秒更新一次。反复启动 smooth scroll 会让尚未结束的
    // 动画互相打断，表现为整块对话上下闪动；实时更新只需在下一帧贴住底部。
    const frame = window.requestAnimationFrame(() => {
      node.scrollTop = node.scrollHeight
    })
    return () => window.cancelAnimationFrame(frame)
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
  return <section className="chat-page">
    <div className="conversation">
      <div ref={conversationRef} className="conversation-content">
      {!session && <div className="welcome"><div className="agent-orb"><Logo /></div><p className="eyebrow">自托管 Agent · 在服务器持续执行</p><h1>今天要交付什么？</h1><p>选择项目与运行环境，描述要完成的任务；输入 <code>@</code> 可指定 Skill、Tool 或 MCP。</p><div className="suggestion-heading"><div><strong>试试这些任务</strong><span>快速查询，也可以处理真实研发工作</span></div><small>点击填入，可继续编辑</small></div><div className="suggestions">{starterSuggestions.map((suggestion) => <button key={suggestion.category} onClick={() => startSuggestion(suggestion)} aria-label={`填入示例：${suggestion.category}，${suggestion.title}`}><span className="suggestion-copy"><em>{suggestion.category}</em><strong>{suggestion.title}</strong></span><span className="suggestion-arrow" aria-hidden="true">→</span></button>)}</div></div>}
      {session && <ContextBar session={session} />}
      {session?.messagesTruncated && <div className="history-window-note">当前显示最近一段消息；向上滚动加载更早记录。原始历史仍保存在本地数据库，并参与 Agent 上下文处理。</div>}
      {groupedConversationItems.map((item) => item.kind === 'message' ? <MessageView key={`message-${item.id}`} message={item.message} relatedCall={item.message.toolCallId ? callsByID.get(item.message.toolCallId) : undefined} researchCitations={researchCitations.byMessageID.get(item.message.id)} /> : item.kind === 'activity-group' ? <CodexActivityGroup key={`activities-${item.id}`} events={item.events} onOpenTrace={onOpenTrace} /> : null)}
      {session?.status === 'queued' && <div className="assistant-row"><Avatar /><div className="thinking queued" role="status" aria-live="polite"><i /><i /><i /><span>{session.runProgress || `${isCodexRuntime ? 'Codex' : 'EasyAgent'} · 任务排队中`}</span></div></div>}
      {session?.status === 'paused' && <div className="run-error paused"><div className="run-error-mark" aria-hidden="true">Ⅱ</div><div className="run-error-copy"><strong>排队任务已暂停</strong><span>任务尚未开始执行，可以在输入区继续或取消。</span></div></div>}
      {session?.status === 'running' && <ExecutionProgress session={session} />}
      {session?.status === 'running' && session.partialOutput && <div className="assistant-row"><Avatar /><div className="assistant-message streaming-message"><div className="answer-text"><Markdown researchCitations={researchCitations.active}>{session.partialOutput}</Markdown></div></div></div>}
      {session?.status === 'failed' && <RunError error={session.error} ollamaRunning={data.ollama.running} retrying={sending} onRetry={() => {
        const lastUserMessage = session.messages.slice().reverse().find((message) => message.role === 'user')
        if (lastUserMessage) send(lastUserMessage.attachments?.length ? '请重新完成上一条包含附件的请求。' : lastUserMessage.content)
      }} onOpenCapabilities={onOpenCapabilities} />}
      {session?.status === 'canceled' && <div className="run-error canceled"><div className="run-error-mark" aria-hidden="true">■</div><div className="run-error-copy"><strong>任务已停止</strong><span>你可以继续发送新消息。</span></div></div>}
      <div ref={endRef} className="conversation-end-space" aria-hidden="true" />
      </div>
    </div>
    <ChatComposer {...composer} onStop={onStop} onPause={onPause} onResume={onResume} runActionBusy={runActionBusy} />
  </section>
}

function groupConversationActivities(items: ConversationItem[]): GroupedConversationItem[] {
  const grouped: GroupedConversationItem[] = []
  for (const item of items) {
    if (item.kind === 'activity') {
      const previous = grouped.at(-1)
      if (previous?.kind === 'activity-group') {
        previous.events.push(item.event)
      } else {
        grouped.push({ kind: 'activity-group', createdAt: item.createdAt, id: item.id, events: [item.event] })
      }
      continue
    }
    grouped.push(item)
  }
  return grouped
}
