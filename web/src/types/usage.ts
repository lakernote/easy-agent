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
  contextWindowTokens?: number
}

export type UsageAggregate = {
  periodStart: string
  runtime: 'easyagent' | 'codex'
  model: string
  profileId?: string
  sessions: number
  inputTokens: number
  outputTokens: number
  cachedTokens: number
  cacheWriteTokens: number
  totalTokens: number
  modelCalls: number
  toolCalls: number
  modelDurationMs: number
  toolDurationMs: number
  cacheReported: boolean
}

export type UsageReport = {
  period: 'day' | 'week' | 'month'
  from: string
  to: string
  generatedAt: string
  items: UsageAggregate[]
}
