import type { Session, SessionHistoryPage } from './types'
export type Page = 'chat' | 'skills' | 'runtime' | 'tools' | 'settings'
export const isActive = (status?: Session['status']) => status === 'queued' || status === 'running'
export function mergeSessionHistory(current: Session, page: SessionHistoryPage, kind: 'messages' | 'events'): Session {
  if (kind === 'messages') {
    const messages = Array.from(new Map([...page.messages || [], ...current.messages].map((item) => [item.id, item])).values()).sort((left, right) => left.id - right.id)
    const messageCount = page.messageCount ?? current.messageCount
    return { ...current, messages, messageCount, messagesTruncated: messageCount !== undefined && messages.length < messageCount, messagesHasMore: page.messagesHasMore }
  }
  const events = Array.from(new Map([...page.events || [], ...current.events].map((item) => [item.id, item])).values()).sort((left, right) => left.id - right.id)
  const eventCount = page.eventCount ?? current.eventCount
  return { ...current, events, eventCount, eventsTruncated: eventCount !== undefined && events.length < eventCount, eventsHasMore: page.eventsHasMore }
}

export function mergeSessionSnapshot(current: Session, next: Session): Session {
  const messages = Array.from(new Map([...current.messages, ...next.messages].map((item) => [item.id, item])).values()).sort((left, right) => left.id - right.id)
  const events = Array.from(new Map([...current.events, ...next.events].map((item) => [item.id, item])).values()).sort((left, right) => left.id - right.id)
  return {
    ...next,
    messages,
    events,
    messagesTruncated: next.messageCount !== undefined && messages.length < next.messageCount,
    eventsTruncated: next.eventCount !== undefined && events.length < next.eventCount,
    messagesHasMore: next.messageCount !== undefined && messages.length < next.messageCount,
    eventsHasMore: next.eventCount !== undefined && events.length < next.eventCount,
  }
}
