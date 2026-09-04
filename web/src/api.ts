import type { AttachmentInput, Bootstrap, CodexProviderConfig, CodexProviderConfigInput, MCPConfig, MCPInstallResult, ModelSettings, Session, SessionHistoryPage, Skill, UsageReport, WeixinLogin, WeixinState } from './types'

export class APIError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'APIError'
    this.status = status
  }
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...init,
    headers: init?.body ? { 'Content-Type': 'application/json', ...(init.headers || {}) } : init?.headers,
  })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({ error: `HTTP ${response.status}` }))
    throw new APIError(payload.error || `HTTP ${response.status}`, response.status)
  }
  if (response.status === 204) return undefined as T
  return response.json()
}

export const api = {
  me: () => request<{ authenticated: boolean; username: string }>('/api/v1/auth/me'),
  login: (username: string, password: string) => request<{ authenticated: boolean; username: string }>('/api/v1/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => request<void>('/api/v1/auth/logout', { method: 'POST' }),
  changePassword: (currentPassword: string, newPassword: string) => request<{ authenticated: boolean; message: string }>('/api/v1/auth/password', { method: 'PUT', body: JSON.stringify({ currentPassword, newPassword }) }),
  bootstrap: () => request<Bootstrap>('/api/v1/bootstrap'),
  saveRuntimeSettings: (settings: Bootstrap['runtimeSettings']) => request<Bootstrap['runtimeSettings']>('/api/v1/runtime/settings', { method: 'PUT', body: JSON.stringify(settings) }),
  weixin: () => request<WeixinState>('/api/v1/channels/weixin'),
  saveWeixinSettings: (enabled: boolean) => request<WeixinState>('/api/v1/channels/weixin', { method: 'PUT', body: JSON.stringify({ enabled }) }),
  startWeixinLogin: (label: string) => request<WeixinLogin>('/api/v1/channels/weixin/login', { method: 'POST', body: JSON.stringify({ label }) }),
  weixinLogin: (id: string) => request<WeixinLogin>(`/api/v1/channels/weixin/login/${encodeURIComponent(id)}`),
  cancelWeixinLogin: (id: string) => request<void>(`/api/v1/channels/weixin/login/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  verifyWeixinLogin: (id: string, code: string) => request<WeixinLogin>(`/api/v1/channels/weixin/login/${encodeURIComponent(id)}/verify`, { method: 'POST', body: JSON.stringify({ code }) }),
  updateWeixinAccount: (id: string, label: string, enabled: boolean) => request<WeixinState>(`/api/v1/channels/weixin/accounts/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ label, enabled }) }),
  deleteWeixinAccount: (id: string) => request<void>(`/api/v1/channels/weixin/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  usage: (period: UsageReport['period'], days?: number) => request<UsageReport>(`/api/v1/usage?period=${period}${days ? `&days=${days}` : ''}`),
  olderSessions: (beforeUpdatedAt: string, beforeID: string) => request<{ sessions: Session[]; hasMore: boolean }>(`/api/v1/sessions/history?beforeUpdatedAt=${encodeURIComponent(beforeUpdatedAt)}&beforeID=${encodeURIComponent(beforeID)}`),
  session: (id: string) => request<Session>(`/api/v1/sessions/${id}`),
  sessionHistory: (id: string, kind: 'messages' | 'events', before: number) => request<SessionHistoryPage>(`/api/v1/sessions/${id}/history?kind=${kind}&before=${before}`),
  createSession: (message: string, attachments: AttachmentInput[] = [], workspace = '', profileId = '') => request<Session>('/api/v1/sessions', { method: 'POST', body: JSON.stringify({ message, attachments, workspace, profileId }) }),
  sendMessage: (id: string, message: string, attachments: AttachmentInput[] = []) => request<Session>(`/api/v1/sessions/${id}/messages`, { method: 'POST', body: JSON.stringify({ message, attachments }) }),
  pauseSession: (id: string) => request<Session>(`/api/v1/sessions/${id}/pause`, { method: 'POST' }),
  resumeSession: (id: string) => request<Session>(`/api/v1/sessions/${id}/resume`, { method: 'POST' }),
  cancelSession: (id: string) => request<Session>(`/api/v1/sessions/${id}/cancel`, { method: 'POST' }),
  forkSession: (id: string) => request<Session>(`/api/v1/sessions/${id}/fork`, { method: 'POST' }),
  resolveCodexRequest: (id: string, requestId: string, response: unknown) => request<void>(`/api/v1/sessions/${id}/codex-request`, { method: 'POST', body: JSON.stringify({ requestId, response }) }),
  deleteSession: (id: string) => request<void>(`/api/v1/sessions/${id}`, { method: 'DELETE' }),
  saveModel: (model: ModelSettings & { clearApiKey?: boolean }) => request<ModelSettings>('/api/v1/model', { method: 'PUT', body: JSON.stringify(model) }),
  activateModelProfile: (id: string) => request<ModelSettings>(`/api/v1/model/${encodeURIComponent(id)}/active`, { method: 'PUT', body: JSON.stringify({}) }),
  deleteModelProfile: (id: string) => request<void>(`/api/v1/model/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  testModel: (model: ModelSettings) => request<{ ok: boolean; model: string; toolCall: string; answer: string; inputTokens: number; outputTokens: number; durationMs: number }>('/api/v1/model/test', { method: 'POST', body: JSON.stringify(model) }),
  useOllama: (model: string) => request<ModelSettings>('/api/v1/ollama/use', { method: 'POST', body: JSON.stringify({ model }) }),
  codex: () => request<Bootstrap['codex']>('/api/v1/codex'),
  codexModels: () => request<unknown>('/api/v1/codex/models'),
  codexAccount: () => request<unknown>('/api/v1/codex/account'),
  codexThreads: (cursor = '', search = '') => request<unknown>(`/api/v1/codex/threads?limit=50&cursor=${encodeURIComponent(cursor)}&search=${encodeURIComponent(search)}`),
  codexThread: (id: string) => request<unknown>(`/api/v1/codex/threads/${encodeURIComponent(id)}?includeTurns=true`),
  codexConfig: () => request<CodexProviderConfig>('/api/v1/codex/config'),
  saveCodexConfig: (config: CodexProviderConfigInput) => request<CodexProviderConfig>('/api/v1/codex/config', { method: 'PUT', body: JSON.stringify(config) }),
  installCodex: () => request<{ ok: boolean; status: Bootstrap['codex']; message: string }>('/api/v1/codex/install', { method: 'POST', body: JSON.stringify({}) }),
  saveSkill: (skill: Skill) => request<Skill>(`/api/v1/skills/${encodeURIComponent(skill.name)}`, { method: 'PUT', body: JSON.stringify(skill) }),
  resetSkill: (name: string) => request<void>(`/api/v1/skills/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  saveMCP: (mcp: MCPConfig) => request<MCPConfig>(`/api/v1/mcp/${encodeURIComponent(mcp.id)}`, { method: 'PUT', body: JSON.stringify(mcp) }),
  deleteMCP: (id: string) => request<void>(`/api/v1/mcp/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  testMCP: (id: string) => request<{ ok: boolean; tools: { name: string; description: string }[] }>(`/api/v1/mcp/${encodeURIComponent(id)}/test`, { method: 'POST' }),
  checkMCPPreset: (id: string) => request<{ ok: boolean; installed: boolean; status: string; message: string }>(`/api/v1/mcp/presets/${encodeURIComponent(id)}/check`, { method: 'POST' }),
  installMCPPreset: (id: string) => request<MCPInstallResult>(`/api/v1/mcp/presets/${encodeURIComponent(id)}/install`, { method: 'POST' }),
  uninstallMCPPreset: (id: string) => request<void>(`/api/v1/mcp/presets/${encodeURIComponent(id)}/install`, { method: 'DELETE' }),
}
