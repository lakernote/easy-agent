export type ModelSettings = {
  profileId?: string
  profileName?: string
  runtime: 'easyagent' | 'codex'
  provider: string
  protocol: 'chat_completions' | 'responses' | 'app_server'
  baseUrl: string
  model: string
  apiKey?: string
  apiKeyEnv?: string
  thinking?: string
  maxOutputTokens: number
  requestTimeoutSeconds: number
  turnTimeoutSeconds: number
  contextWindowTokens: number
  compressionThresholdPercent: number
  secretConfigured?: boolean
}

export type ModelProfile = {
  id: string
  name: string
  settings: ModelSettings
}

export type ModelRules = {
  defaultMaxOutputTokens: number
  defaultRequestTimeoutSeconds: number
  minRequestTimeoutSeconds: number
  maxRequestTimeoutSeconds: number
  defaultCodexTurnTimeoutSeconds: number
  minCodexTurnTimeoutSeconds: number
  maxCodexTurnTimeoutSeconds: number
  defaultCompressionThresholdPercent: number
  minCompressionThresholdPercent: number
  maxCompressionThresholdPercent: number
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
  appServerAvailable: boolean
  path?: string
  version?: string
  message: string
  installCommand: string
  installUrl: string
}

export type CodexProviderConfig = {
  configPath: string
  provider: string
  providerName: string
  baseUrl: string
  model: string
  reasoningEffort: string
  envKey: string
  apiKeyConfigured: boolean
  configured: boolean
  warning?: string
}

export type CodexProviderConfigInput = Pick<CodexProviderConfig, 'provider' | 'providerName' | 'baseUrl' | 'model' | 'reasoningEffort' | 'envKey'> & {
  apiKey?: string
  clearApiKey?: boolean
}
