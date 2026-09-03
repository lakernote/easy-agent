import type { AttachmentInput, Bootstrap, MCPConfig, MCPInstallResult, ModelSettings, Session, SessionHistoryPage, Skill, UsageReport } from './types'

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...init,
    headers: init?.body ? { 'Content-Type': 'application/json', ...(init.headers || {}) } : init?.headers,
  })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({ error: `HTTP ${response.status}` }))
    throw new Error(payload.error || `HTTP ${response.status}`)
  }
  if (response.status === 204) return undefined as T
  return response.json()
}

export const api = {
  bootstrap: () => request<Bootstrap>('/api/v1/bootstrap'),
  usage: (period: UsageReport['period'], days?: number) => request<UsageReport>(`/api/v1/usage?period=${period}${days ? `&days=${days}` : ''}`),
  olderSessions: (beforeUpdatedAt: string, beforeID: string) => request<{ sessions: Session[]; hasMore: boolean }>(`/api/v1/sessions/history?beforeUpdatedAt=${encodeURIComponent(beforeUpdatedAt)}&beforeID=${encodeURIComponent(beforeID)}`),
  session: (id: string) => request<Session>(`/api/v1/sessions/${id}`),
  sessionHistory: (id: string, kind: 'messages' | 'events', before: number) => request<SessionHistoryPage>(`/api/v1/sessions/${id}/history?kind=${kind}&before=${before}`),
  createSession: (message: string, attachments: AttachmentInput[] = [], workspace = '', profileId = '') => request<Session>('/api/v1/sessions', { method: 'POST', body: JSON.stringify({ message, attachments, workspace, profileId }) }),
  sendMessage: (id: string, message: string, attachments: AttachmentInput[] = []) => request<Session>(`/api/v1/sessions/${id}/messages`, { method: 'POST', body: JSON.stringify({ message, attachments }) }),
  cancelSession: (id: string) => request<Session>(`/api/v1/sessions/${id}/cancel`, { method: 'POST' }),
  deleteSession: (id: string) => request<void>(`/api/v1/sessions/${id}`, { method: 'DELETE' }),
  saveModel: (model: ModelSettings) => request<ModelSettings>('/api/v1/model', { method: 'PUT', body: JSON.stringify(model) }),
  deleteModelProfile: (id: string) => request<void>(`/api/v1/model/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  testModel: (model: ModelSettings) => request<{ ok: boolean; model: string; toolCall: string; answer: string; inputTokens: number; outputTokens: number; durationMs: number }>('/api/v1/model/test', { method: 'POST', body: JSON.stringify(model) }),
  useOllama: (model: string) => request<ModelSettings>('/api/v1/ollama/use', { method: 'POST', body: JSON.stringify({ model }) }),
  codex: () => request<Bootstrap['codex']>('/api/v1/codex'),
  saveSkill: (skill: Skill) => request<Skill>(`/api/v1/skills/${encodeURIComponent(skill.name)}`, { method: 'PUT', body: JSON.stringify(skill) }),
  resetSkill: (name: string) => request<void>(`/api/v1/skills/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  saveMCP: (mcp: MCPConfig) => request<MCPConfig>(`/api/v1/mcp/${encodeURIComponent(mcp.id)}`, { method: 'PUT', body: JSON.stringify(mcp) }),
  deleteMCP: (id: string) => request<void>(`/api/v1/mcp/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  testMCP: (id: string) => request<{ ok: boolean; tools: { name: string; description: string }[] }>(`/api/v1/mcp/${encodeURIComponent(id)}/test`, { method: 'POST' }),
  checkMCPPreset: (id: string) => request<{ ok: boolean; installed: boolean; status: string; message: string }>(`/api/v1/mcp/presets/${encodeURIComponent(id)}/check`, { method: 'POST' }),
  installMCPPreset: (id: string) => request<MCPInstallResult>(`/api/v1/mcp/presets/${encodeURIComponent(id)}/install`, { method: 'POST' }),
  uninstallMCPPreset: (id: string) => request<void>(`/api/v1/mcp/presets/${encodeURIComponent(id)}/install`, { method: 'DELETE' }),
}
