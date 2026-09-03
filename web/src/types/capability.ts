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
