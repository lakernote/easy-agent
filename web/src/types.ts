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

export type Usage = {
  inputTokens: number
  outputTokens: number
  cachedTokens: number
  cacheWriteTokens: number
  totalTokens: number
  modelDurationMs: number
  toolDurationMs: number
  modelCalls: number
  toolCalls: number
  cacheReported: boolean
  cacheInputTokens: number
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
  usage: Usage
  context: ContextInfo
  partialOutput?: string
}

export type SessionHistoryPage = {
  messages?: Message[]
  events?: TraceEvent[]
  messageCount?: number
  eventCount?: number
  messagesHasMore?: boolean
  eventsHasMore?: boolean
}

export type ModelSettings = {
  runtime: 'easyagent' | 'codex'
  provider: string
  protocol: 'chat_completions' | 'responses'
  baseUrl: string
  model: string
  apiKey?: string
  apiKeyEnv?: string
  thinking?: string
  maxOutputTokens: number
  requestTimeoutSeconds: number
  contextWindowTokens: number
  compressionThresholdPercent: number
  secretConfigured?: boolean
}

export type ModelRules = {
  defaultMaxOutputTokens: number
  defaultRequestTimeoutSeconds: number
  minRequestTimeoutSeconds: number
  maxRequestTimeoutSeconds: number
  defaultCompressionThresholdPercent: number
  minCompressionThresholdPercent: number
  maxCompressionThresholdPercent: number
}

export type Skill = {
  name: string
  description: string
  content: string
  enabled: boolean
  builtin: boolean
}

export type MCPConfig = {
  id: string
  name: string
  description?: string
  enabled: boolean
  transport: 'stdio' | 'http' | 'streamable_http'
  command?: string
  args: string[]
  endpoint?: string
  authType?: string
  token?: string
  username?: string
  password?: string
  headers: Record<string, string>
  environment: Record<string, string>
  secretConfigured?: boolean
}

export type MCPPreset = {
  id: string
  name: string
  description: string
  transport: string
  command?: string
  args?: string[]
  endpoint?: string
  authType?: string
  headers?: Record<string, string>
  action: 'install' | 'configure'
  requirement: string
  requiredCommands?: string[]
  minimumNodeMajor?: number
}

export type MCPInstallResult = {
  ready: boolean
  status: 'ready' | 'missing_dependency' | 'install_failed' | 'connect_failed'
  message: string
  mcp: MCPConfig
  tools: { name: string; description: string }[]
}

export type OllamaStatus = {
  installed: boolean
  running: boolean
  baseUrl: string
  models: { name: string; model?: string; size?: number }[]
  message: string
}

export type CodexRuntimeStatus = {
  installed: boolean
  path?: string
  version?: string
  message: string
  installCommand: string
  installUrl: string
}

export type Bootstrap = {
  sessions: Session[]
  sessionsHasMore?: boolean
  model: ModelSettings
  modelRules: ModelRules
  skills: Skill[]
  builtinTools: { name: string; description: string; source: string; category: string }[]
  mcpPresets: MCPPreset[]
  mcps: MCPConfig[]
  systemPrompt: string
  ollama: OllamaStatus
  codex: CodexRuntimeStatus
  runtime: { home: string; workspace: string; runtime: string }
}
