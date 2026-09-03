export type ToolCall = { id: string; name: string; arguments: string }

export type Attachment = {
  id: string
  name: string
  mimeType: string
  kind: 'text' | 'image' | 'pdf'
  size: number
}

export type AttachmentInput = {
  name: string
  mimeType: string
  size: number
  data: string
}

export type Message = {
  id: number
  role: 'user' | 'assistant' | 'tool' | 'system'
  content?: string
  attachments: Attachment[]
  toolCalls: ToolCall[]
  toolCallId?: string
  name?: string
  createdAt: string
}

export type TraceEvent = {
  id: number
  kind: string
  turn?: number
  step: number
  attempt?: number
  name?: string
  status: string
  detail?: string
  input?: string
  output?: string
  inputTokens?: number
  outputTokens?: number
  cachedTokens?: number
  cacheWriteTokens?: number
  cacheReported: boolean
  totalTokens?: number
  protocol?: string
  statusCode?: number
  historyMode?: string
  requestMessages?: number
  toolDefinitions?: number
  durationMs?: number
  createdAt: string
}

export type ContextInfo = {
  historyMessages: number
  userTurns: number
  lastInputTokens: number
  contextWindowTokens: number
  historyMode: string
  requestMessages: number
  toolDefinitions: number
  compressionMode: string
  compressionThresholdPercent: number
  compressionCount: number
  compressedMessages: number
  retainedMessages: number
  cacheReported: boolean
  lastCachedTokens: number
  lastCacheWriteTokens: number
}

export type Session = {
  id: string
  title: string
  status: 'idle' | 'queued' | 'running' | 'failed' | 'canceled'
  error?: string
  runtime: 'easyagent' | 'codex'
  model?: string
  workspace: string
  createdAt: string
  updatedAt: string
  messages: Message[]
  events: TraceEvent[]
  messageCount?: number
  eventCount?: number
  userTurnCount?: number
  messagesTruncated?: boolean
  eventsTruncated?: boolean
  messagesHasMore?: boolean
  eventsHasMore?: boolean
  usage: import('./usage').Usage
  context: ContextInfo
  partialOutput?: string
  runProgress?: string
}

export type SessionHistoryPage = {
  messages?: Message[]
  events?: TraceEvent[]
  messageCount?: number
  eventCount?: number
  messagesHasMore?: boolean
  eventsHasMore?: boolean
}
