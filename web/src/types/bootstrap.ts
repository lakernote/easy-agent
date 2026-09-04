import type { CodexProviderConfig, CodexRuntimeStatus, ModelProfile, ModelRules, ModelSettings, OllamaStatus } from './model'
import type { MCPConfig, MCPPreset, Skill } from './capability'
import type { Session } from './session'

export type Bootstrap = {
  sessions: Session[]
  sessionsHasMore?: boolean
  model: ModelSettings
  modelProfiles: ModelProfile[]
  activeModelProfileId: string
  modelRules: ModelRules
  skills: Skill[]
  builtinTools: { name: string; description: string; source: string; category: string }[]
  mcpPresets: MCPPreset[]
  mcps: MCPConfig[]
  systemPrompt: string
  ollama: OllamaStatus
  codex: CodexRuntimeStatus
  codexConfig: CodexProviderConfig
  runtime: { home: string; workspace: string; runtime: string }
  runtimeSettings: { maxConcurrentTasks: number; turnTimeoutSeconds: number; sseHeartbeatSeconds: number; gitWorktrees: boolean }
}
