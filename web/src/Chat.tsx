import { useEffect, useRef } from 'react'
import type { Bootstrap, Session } from './types'
import { starterSuggestions } from './suggestions'
import { Avatar, ContextBar, Markdown, MessageView } from './chat/MessageContent'
import { Logo } from './ui'
import { RunError } from './dialogs'
import { ChatComposer } from './chat/ChatComposer'
import { useChatComposer } from './chat/useChatComposer'

export function Chat({ session, data, onSession, onRefresh, onError, onLoadOlder, onOpenSkills, onOpenCapabilities }: { session: Session | null; data: Bootstrap; onSession: (session: Session) => void; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void; onLoadOlder: (id: string, kind: 'messages' | 'events', before: number) => Promise<void>; onOpenSkills: () => void; onOpenCapabilities: () => void }) {
  const endRef = useRef<HTMLDivElement>(null)
  const conversationRef = useRef<HTMLDivElement>(null)
  const loadingOlderRef = useRef(false)
  const stickToBottomRef = useRef(true)
  const previousSessionIDRef = useRef<string | undefined>(undefined)
  const composer = useChatComposer({ session, data, onSession, onRefresh, onError, onOpenSkills, onOpenCapabilities })
  const { isCodexRuntime, sending, send, startSuggestion } = composer

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
  return <section className="chat-page">
    <div className="conversation">
      <div ref={conversationRef} className="conversation-content">
      {!session && <div className="welcome"><div className="agent-orb"><Logo /></div><p className="eyebrow">一个核心 Agent · 能力按需加载</p><h1>想解决什么问题？</h1><p>直接描述目标，也可以添加代码、日志、图片或 PDF；输入 <code>@</code> 可明确指定 Tool、Skill 或 MCP。</p><div className="suggestion-heading"><strong>从一个场景开始</strong><span>点击即可开始一轮新会话</span></div><div className="suggestions">{starterSuggestions.map((suggestion) => <button key={suggestion.category} onClick={() => startSuggestion(suggestion)} aria-label={`${suggestion.category}：${suggestion.title}`}><span className="suggestion-copy"><em>{suggestion.category}</em><strong>{suggestion.title}</strong></span><span className="suggestion-arrow">{suggestion.attachment ? '+' : '↗'}</span></button>)}</div></div>}
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
    <ChatComposer {...composer} />
  </section>
}
