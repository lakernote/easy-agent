import type { CodexProviderConfig, CodexRuntimeStatus, ModelProfile, ModelRules, ModelSettings, OllamaStatus } from './model'
import type { MCPConfig, MCPPreset, Skill } from './capability'
import type { Session } from './session'

export type Project = { id: string; name: string; directories: string[]; default: boolean; createdAt: string; updatedAt: string }

export type ResearchProvider = {
  id: string
  name: string
  category: string
  description: string
  endpointLabel?: string
  endpointPlaceholder?: string
  secretLabel?: string
  requiresEndpoint?: boolean
  requiresSecret?: boolean
  availableWithoutAuth?: boolean
  primarySearch?: boolean
  environmentVariables?: string[]
  endpoint?: string
  effectiveEndpoint?: string
  endpointSource?: 'saved' | 'environment'
  secretConfigured?: boolean
  secretAvailable?: boolean
  secretSource?: 'saved' | 'environment'
  ready: boolean
}

export type ResearchProviderInput = { id: string; endpoint?: string; secret?: string; clearSecret?: boolean }
export type ResearchExecutionLayer = { id: string; name: string; components: string; description: string }
export type ResearchSettings = { providers: ResearchProvider[]; executionLayers: ResearchExecutionLayer[] }
export type ResearchSettingsInput = { providers: ResearchProviderInput[] }
export type ResearchProviderTest = { id: string; name: string; resultCount?: number; durationMs: number; detail: string }

export type Bootstrap = {
  sessions: Session[]
  projects: Project[]
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
  researchSettings: ResearchSettings
}
