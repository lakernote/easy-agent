import type { Bootstrap } from './types'
export type CapabilityKind = 'skill' | 'tool' | 'mcp'
export type CapabilityOption = { key: string; kind: CapabilityKind; name: string; description: string; enabled: boolean; token: string }
export function capabilityOptions(data: Bootstrap): CapabilityOption[] {
  const skills = data.skills.map((item) => ({ key: `skill:${item.name}`, kind: 'skill' as const, name: item.name, description: item.description, enabled: item.enabled, token: `@skill:${item.name}` }))
  const tools = data.builtinTools.map((item) => ({ key: `tool:${item.name}`, kind: 'tool' as const, name: item.name, description: item.description, enabled: true, token: `@tool:${item.name}` }))
  const mcps = data.mcps.map((item) => ({ key: `mcp:${item.id}`, kind: 'mcp' as const, name: item.name || item.id, description: item.description || '外部 MCP Server', enabled: item.enabled, token: `@mcp:${item.id}` }))
  return [...skills, ...tools, ...mcps].sort((left, right) => Number(right.enabled) - Number(left.enabled) || left.kind.localeCompare(right.kind) || left.name.localeCompare(right.name))
}

export function capabilityMention(value: string, caret: number) {
  const before = value.slice(0, caret)
  const match = before.match(/(?:^|\s)@([^\s@]*)$/)
  if (!match) return null
  return { start: before.lastIndexOf('@'), query: match[1] }
}

export function hasCapabilityToken(value: string, token: string) {
  return value.split(/\s+/).includes(token)
}

export function capabilityKindLabel(kind: CapabilityKind) { return kind === 'skill' ? 'Skill' : kind === 'tool' ? 'Tool' : 'MCP' }
export function capabilityKindShort(kind: CapabilityKind) { return kind === 'skill' ? 'S' : kind === 'tool' ? 'T' : 'M' }
