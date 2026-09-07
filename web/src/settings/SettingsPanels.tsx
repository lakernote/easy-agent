import { useEffect, useState } from 'react'
import { api } from '../api'
import { writeClipboardText } from '../clipboard'
import type { Bootstrap, CodexProviderConfig, MCPConfig, ModelSettings } from '../types'

export type Notice = { ready: boolean; title: string; message: string }

export function RuntimeOperationsSettings({ data, onRefresh, onError }: { data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const [settings, setSettings] = useState({ ...data.runtimeSettings })
  const [saving, setSaving] = useState(false)
  useEffect(() => setSettings({ ...data.runtimeSettings }), [data.runtimeSettings])
  const save = async () => {
    setSaving(true); onError('')
    try { await api.saveRuntimeSettings(settings); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setSaving(false) }
  }
  const dirty = JSON.stringify(settings) !== JSON.stringify(data.runtimeSettings)
  const turnHours = Number((settings.turnTimeoutSeconds / 3600).toFixed(2))
  return <div className="section-block runtime-operations task-settings-panel">
    <div className="section-heading"><div><p className="eyebrow">共享调度层</p><h2>任务与恢复</h2><p>以下设置同时作用于 EasyAgent Runtime 和 Codex Runtime，不跟随模型配置重复保存。</p></div><span className="tag">全局</span></div>
    <div className="task-settings-grid">
      <section>
        <div className="task-setting-heading"><span>01</span><div><strong>执行容量</strong><small>修改后立即影响排队任务，不中断已运行任务。</small></div></div>
        <label>最大并发任务
          <input type="number" min="1" max="16" value={settings.maxConcurrentTasks} onChange={(event) => setSettings({ ...settings, maxConcurrentTasks: Number(event.target.value) })} />
          <small>默认 4，允许范围 1–16。</small>
        </label>
      </section>
      <section>
        <div className="task-setting-heading"><span>02</span><div><strong>整轮任务上限</strong><small>覆盖模型思考、工具调用、命令、MCP 与审批等待。</small></div></div>
        <label>最长运行时间（小时）
          <input type="number" min={5 / 60} max="24" step="0.5" value={turnHours} onChange={(event) => setSettings({ ...settings, turnTimeoutSeconds: Math.round(Number(event.target.value) * 3600) })} />
          <small>默认 12 小时；到期后中断当前 turn，会话历史仍保留。</small>
        </label>
      </section>
      <section>
        <div className="task-setting-heading"><span>03</span><div><strong>实时连接</strong><small>浏览器断线后会携带事件序号自动续传 Trace。</small></div></div>
        <label>SSE 心跳间隔（秒）
          <input type="number" min="5" max="60" value={settings.sseHeartbeatSeconds} onChange={(event) => setSettings({ ...settings, sseHeartbeatSeconds: Number(event.target.value) })} />
          <small>默认 20 秒；通常无需调小。</small>
        </label>
      </section>
      <section>
        <div className="task-setting-heading"><span>04</span><div><strong>项目冲突控制</strong><small>让并发任务写入相互隔离的 Git 分支。</small></div></div>
        <label className="task-toggle"><span><strong>自动创建 Git worktree</strong><small>Git 项目使用 <code>easyagent/…</code> 分支；非 Git 目录仍按原目录串行。</small></span><input type="checkbox" checked={settings.gitWorktrees} onChange={(event) => setSettings({ ...settings, gitWorktrees: event.target.checked })} /></label>
      </section>
    </div>
    <div className="recovery-policy">
      <div><span className="service-dot" /><p><strong>服务重启恢复</strong><small>未开始的排队任务自动恢复；手动暂停的任务保持暂停。</small></p></div>
      <div><span className="service-dot warning" /><p><strong>运行中任务保护</strong><small>重启后标记为中断，不自动重放可能已经执行的命令或写文件操作。</small></p></div>
    </div>
    <div className="form-actions"><button className="primary-button" type="button" disabled={!dirty || saving} onClick={() => void save()}>{saving ? '保存中…' : '保存任务设置'}</button></div>
  </div>
}

type CodexStatusProps = {
  data: Bootstrap
  onDetect: () => void
}

export function CodexStatus({ data, onDetect }: CodexStatusProps) {
  const [inspection, setInspection] = useState<{ account?: unknown; models?: unknown; threads?: unknown } | null>(null)
  const [thread, setThread] = useState<unknown>(null)
  const [readingThread, setReadingThread] = useState('')
  const [inspecting, setInspecting] = useState(false)
  const [inspectionError, setInspectionError] = useState('')
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const ready = data.codex.installed && data.codex.appServerAvailable
  const title = ready
    ? `Codex CLI + app-server 已就绪${data.codex.version ? ` · ${data.codex.version}` : ''}`
    : data.codex.installed ? '已找到 CLI，但 app-server 不可用' : '未检测到 Codex CLI'
  const detail = data.codex.installed
    ? data.codex.path
    : data.codex.message
  const inspect = async () => {
    setInspecting(true)
    setInspectionError('')
    try {
      const [account, models, threads] = await Promise.all([api.codexAccount(), api.codexModels(), api.codexThreads('', '', 3)])
      setInspection({ account, models, threads })
    } catch (reason) {
      setInspectionError((reason as Error).message)
    } finally { setInspecting(false) }
  }
  const readThread = async (id: string) => {
    setReadingThread(id)
    setInspectionError('')
    try { setThread(await api.codexThread(id)) }
    catch (reason) { setInspectionError((reason as Error).message) }
    finally { setReadingThread('') }
  }
  const copyInstallCommand = async () => {
    try {
      await writeClipboardText(data.codex.installCommand)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
    window.setTimeout(() => setCopyState('idle'), 1600)
  }
  const threads = codexThreadSummaries(inspection?.threads).slice(0, 3)
  const account = codexAccountSummary(inspection?.account)
  const models = codexModelSummary(inspection?.models)
  const threadDetail = codexThreadDetailSummary(thread)

  return (
    <div className={`runtime-status ${ready ? 'ready' : 'missing'}`} role="status" aria-live="polite">
      <div>
        <strong><span className={`service-dot ${ready ? '' : 'off'}`} />{title}</strong>
        <small>{detail}</small>
      </div>
      <div className="runtime-status-actions">
        {!ready && <a className="ghost-button" href={data.codex.installUrl} target="_blank" rel="noreferrer">OpenAI 官方安装说明</a>}
        <button className="ghost-button" type="button" onClick={onDetect}>重新检测</button>
        {ready && <button className="ghost-button" type="button" disabled={inspecting} onClick={() => void inspect()}>{inspecting ? '读取中…' : '查看运行信息'}</button>}
      </div>
      {!ready && <div className="runtime-manual-install"><div><strong>{data.codex.installed ? '请在服务器终端手动更新 Codex CLI' : '请在服务器终端手动安装 Codex CLI'}</strong><small>EasyAgent 只展示官方命令，不会下载或执行安装脚本。请先核对官方说明，再使用运行 EasyAgent 的同一用户执行；完成后点击“重新检测”，系统会校验 CLI 版本和 app-server 子命令。</small></div><div className="runtime-install-command"><code>{data.codex.installCommand}</code><button className="ghost-button" type="button" onClick={() => void copyInstallCommand()}>{copyState === 'copied' ? '已复制' : copyState === 'failed' ? '复制失败' : '复制命令'}</button></div></div>}
      {inspectionError && <div className="codex-inspection-error" role="alert">运行信息读取失败：{inspectionError}</div>}
      {inspection && <div className="codex-inspection">
        <div className="codex-inspection-summary"><section><span>账号</span><strong>{account.title}</strong><small>{account.detail}</small></section><section><span>模型</span><strong>{models.title}</strong><small>{models.detail}</small></section></div>
        <div className="codex-thread-inspection"><div className="codex-inspection-heading"><span>最近 Threads</span><small>按更新时间显示最近 3 条</small></div>{threads.length === 0 ? <small>没有可读取的 Codex Thread</small> : <div>{threads.map((item) => <button type="button" key={item.id} disabled={!!readingThread} onClick={() => void readThread(item.id)}><span><strong>{item.name || item.preview || '未命名 Thread'}</strong><small>{formatThreadMeta(item)}</small></span>{readingThread === item.id ? <em>读取中…</em> : <b aria-hidden="true">→</b>}</button>)}</div>}</div>
        {threadDetail && <div className="codex-thread-detail"><div className="codex-inspection-heading"><span>Thread 摘要</span><small>只读信息，不加载会话内容</small></div><div className="codex-thread-detail-grid"><section><span>名称</span><strong>{threadDetail.name}</strong></section><section><span>状态</span><strong>{threadDetail.status}</strong></section><section><span>模型</span><strong>{threadDetail.model}</strong></section><section><span>来源</span><strong>{threadDetail.source}</strong></section><section className="wide"><span>工作目录</span><strong>{threadDetail.cwd}</strong></section><section><span>更新时间</span><strong>{threadDetail.updatedAt}</strong></section></div></div>}
      </div>}
    </div>
  )
}

type CodexThreadSummary = { id: string; name?: string; preview?: string; updatedAt?: number | string; status?: { type?: string } }

function codexThreadSummaries(value: unknown): CodexThreadSummary[] {
  if (!value || typeof value !== 'object') return []
  const data = (value as { data?: unknown }).data
  if (!Array.isArray(data)) return []
  return data.filter((item): item is CodexThreadSummary => Boolean(item && typeof item === 'object' && typeof (item as { id?: unknown }).id === 'string'))
}

function record(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function codexAccountSummary(value: unknown) {
  const root = record(value)
  const account = record(root?.account)
  const type = typeof account?.type === 'string' ? account.type : ''
  const plan = typeof account?.planType === 'string' ? account.planType : ''
  const connected = Boolean(account)
  return {
    title: connected ? (type === 'chatgpt' ? 'ChatGPT 已连接' : 'Codex 账号已连接') : '未读取到账号',
    detail: connected ? [plan ? plan[0].toUpperCase() + plan.slice(1) : '', '账号标识已隐藏'].filter(Boolean).join(' · ') : '请确认 Codex 登录状态',
  }
}

function codexModelSummary(value: unknown) {
  const root = record(value)
  const data = Array.isArray(root?.data) ? root.data : Array.isArray(root?.models) ? root.models : []
  const names = data.map((item) => {
    const model = record(item)
    for (const key of ['displayName', 'model', 'slug', 'id']) {
      if (typeof model?.[key] === 'string' && model[key]) return model[key] as string
    }
    return ''
  }).filter(Boolean)
  return {
    title: data.length > 0 ? `${data.length} 个可用模型` : '未读取到模型',
    detail: names.slice(0, 3).join(' · ') || 'Codex 未返回模型目录',
  }
}

function formatThreadMeta(item: CodexThreadSummary) {
  const status = threadStatusLabel(item.status?.type)
  if (item.updatedAt === undefined) return status
  const timestamp = typeof item.updatedAt === 'number' && item.updatedAt < 1e12 ? item.updatedAt * 1000 : item.updatedAt
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return status
  return `${status} · ${new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(date)}`
}

function threadStatusLabel(value?: string) {
  const labels: Record<string, string> = {
    notLoaded: '已保存',
    loaded: '已打开',
    active: '活跃',
    running: '运行中',
    idle: '空闲',
    stored: '已保存',
  }
  return value ? labels[value] || value : '已保存'
}

function codexThreadDetailSummary(value: unknown) {
  const root = record(value)
  const detail = record(root?.thread) || root
  if (!detail) return null
  const status = record(detail.status)
  return {
    name: textValue(detail.name) || textValue(detail.preview) || '未命名 Thread',
    status: threadStatusLabel(textValue(status?.type)),
    model: textValue(detail.model) || '跟随 Codex 配置',
    source: threadSourceLabel(textValue(detail.source)),
    cwd: textValue(detail.cwd) || '未提供',
    updatedAt: formatTimestamp(detail.updatedAt),
  }
}

function textValue(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function threadSourceLabel(value: string) {
  const labels: Record<string, string> = { appServer: 'EasyAgent', cli: 'Codex CLI', vscode: 'Codex', exec: 'Codex Exec' }
  return labels[value] || value || 'Codex'
}

function formatTimestamp(value: unknown) {
  if (typeof value !== 'number' && typeof value !== 'string') return '未提供'
  const timestamp = typeof value === 'number' && value < 1e12 ? value * 1000 : value
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return '未提供'
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(date)
}

export function ModelNotice({ notice }: { notice: Notice | null }) {
  if (!notice) return null
  return (
    <div role="status" aria-live="polite" className={`model-notice ${notice.ready ? 'ready' : 'failed'}`}>
      <div><strong>{notice.title}</strong><span>{notice.message}</span></div>
    </div>
  )
}

type CodexSettingsProps = {
  data: Bootstrap
  config: CodexProviderConfig
  setConfig: (value: CodexProviderConfig) => void
  model: ModelSettings
  setModel: (value: ModelSettings) => void
  notice: Notice | null
  testing: boolean
  saving: boolean
  savingConfig: boolean
  onTest: () => void
  onSave: () => void
  onSaveConfig: (value: CodexProviderConfig & { apiKey?: string; clearApiKey?: boolean }) => void
}

export function CodexSettings({ data, config, setConfig, model, setModel, notice, testing, saving, savingConfig, onTest, onSave, onSaveConfig }: CodexSettingsProps) {
  const [apiKey, setAPIKey] = useState('')
  const [clearAPIKey, setClearAPIKey] = useState(false)
  const update = (value: Partial<CodexProviderConfig>) => setConfig({ ...config, ...value })

  return (
    <div className="section-block codex-config">
      <div className="section-heading codex-provider-head">
        <div>
          <p className="eyebrow">全局连接</p>
          <h2>Codex Provider</h2>
          <p>这组连接由所有 Codex 模型配置共享，并写入服务器上的 <code>~/.codex/config.toml</code>。</p>
        </div>
        <span className="tag">共享</span>
      </div>
      <div className="runtime-boundary-note">
        <strong>Provider 与配置分层</strong>
        <span>先保存 Provider 连接，再为当前配置指定模型 override；留空时跟随 config.toml 的默认模型。</span>
      </div>
      <div className="codex-config-summary">
        <span className={`service-dot ${config.configured ? '' : 'off'}`} />
        <strong>{config.configured ? `${config.providerName || config.provider} 已配置` : '还没有完整配置'}</strong>
        <small>{config.configPath || '~/.codex/config.toml'}{config.apiKeyConfigured ? ' · API Key 已配置' : ' · API Key 未配置'}</small>
      </div>
      {config.warning && <div className="codex-config-warning" role="alert">{config.warning}</div>}
      <div className="form-grid codex-provider-fields">
        <label>Provider ID
          <input value={config.provider} onChange={(event) => update({ provider: event.target.value })} placeholder="groq" />
          <small>例如 groq；不要填写 API Key。</small>
        </label>
        <label>显示名称
          <input value={config.providerName} onChange={(event) => update({ providerName: event.target.value })} placeholder="Groq" />
        </label>
        <label className="wide">Base URL
          <input value={config.baseUrl} onChange={(event) => update({ baseUrl: event.target.value })} placeholder="https://api.groq.com/openai/v1" />
        </label>
        <label>默认模型
          <input value={config.model} onChange={(event) => update({ model: event.target.value })} placeholder="openai/gpt-oss-20b" />
        </label>
        <label>推理强度
          <select value={config.reasoningEffort} onChange={(event) => update({ reasoningEffort: event.target.value })}>
            <option value="">Provider 默认</option><option value="low">low</option><option value="medium">medium</option><option value="high">high</option><option value="xhigh">xhigh</option>
          </select>
        </label>
        <label>API Key 环境变量
          <input value={config.envKey} onChange={(event) => update({ envKey: event.target.value })} placeholder="GROQ_API_KEY" />
          <small>这里填变量名，不是 gsk_... 密钥。</small>
        </label>
        <label className="wide">API Key
          <input type="password" autoComplete="new-password" value={apiKey} onChange={(event) => { setAPIKey(event.target.value); setClearAPIKey(false) }} placeholder={config.apiKeyConfigured ? '已配置，留空保持不变' : '粘贴 Groq API Key'} />
          <small>只发送到当前 EasyAgent 服务器；不会写入 config.toml。</small>
        </label>
        {config.apiKeyConfigured && <label className="check-label wide"><input type="checkbox" checked={clearAPIKey} onChange={(event) => { setClearAPIKey(event.target.checked); if (event.target.checked) setAPIKey('') }} />清除已保存的 API Key</label>}
      </div>
      <section className="codex-profile-override">
        <div><p className="eyebrow">当前配置</p><h3>模型覆盖</h3><small>只影响“{model.profileName || '当前配置'}”，不会修改其他 Codex 配置。</small></div>
        <label>模型 override（可选）
          <input value={model.model} onChange={(event) => setModel({ ...model, model: event.target.value })} placeholder={`留空：跟随 ${config.model || 'config.toml 默认模型'}`} />
          <small>需要同一 Provider 使用不同模型时再填写。</small>
        </label>
        <div className="codex-profile-policy"><strong>任务时限</strong><span>统一使用“任务设置”的整轮上限，当前为 {Number((data.runtimeSettings.turnTimeoutSeconds / 3600).toFixed(2))} 小时。</span></div>
      </section>
      <ModelNotice notice={notice} />
      <div className="form-actions codex-form-actions">
        <div><button className="ghost-button" type="button" disabled={savingConfig} onClick={() => onSaveConfig({ ...config, apiKey, clearApiKey: clearAPIKey })}>{savingConfig ? '保存 Provider 中…' : '保存 Provider'}</button><button className="ghost-button" type="button" disabled={testing || !config.configured} onClick={onTest}>{testing ? '正在测试连接…' : '测试连接'}</button></div>
        <button className="primary-button" type="button" disabled={saving} onClick={onSave}>{saving ? '保存中…' : '保存当前配置'}</button>
      </div>
    </div>
  )
}

type EasyAgentSettingsProps = {
  data: Bootstrap
  model: ModelSettings
  setModel: (value: ModelSettings) => void
  notice: Notice | null
  testing: boolean
  saving: boolean
  onTest: () => void
  onSave: (clearAPIKey?: boolean) => void
  onActivateOllama: (name: string) => Promise<void>
}

export function EasyAgentSettings({ data, model, setModel, notice, testing, saving, onTest, onSave, onActivateOllama }: EasyAgentSettingsProps) {
  const [clearAPIKey, setClearAPIKey] = useState(false)
  useEffect(() => setClearAPIKey(false), [model.profileId])

  return (
    <div className="section-block easyagent-config">
      <div className="section-heading">
        <div><h2>模型连接</h2><p>EasyAgent 支持 OpenAI Chat Completions 和 Responses 兼容接口。</p></div>
        <span className="tag">{model.protocol}</span>
      </div>
      <OllamaModelCatalog data={data} saving={saving} onActivate={onActivateOllama} />
      <div className="form-grid">
        <label>提供方<input value={model.provider} onChange={(event) => setModel({ ...model, provider: event.target.value })} /></label>
        <label>协议
          <select value={model.protocol} onChange={(event) => setModel({ ...model, protocol: event.target.value as ModelSettings['protocol'] })}>
            <option value="chat_completions">Chat Completions</option><option value="responses">Responses</option>
          </select>
        </label>
        <label className="wide">Base URL<input value={model.baseUrl} onChange={(event) => setModel({ ...model, baseUrl: event.target.value })} /></label>
        <label>模型名称<input value={model.model} onChange={(event) => setModel({ ...model, model: event.target.value })} /></label>
        <label>推理模式
          <select value={model.thinking || ''} onChange={(event) => setModel({ ...model, thinking: event.target.value })}>
            <option value="">模型默认</option><option value="disabled">尝试关闭推理</option>
          </select>
          <small>工具选择失败时，优先检查服务端是否支持原生 tool_calls。</small>
        </label>
        <label>最大输出 Token<input type="number" value={model.maxOutputTokens} onChange={(event) => setModel({ ...model, maxOutputTokens: Number(event.target.value) })} /></label>
        <label>模型超时（秒）<input type="number" min={data.modelRules.minRequestTimeoutSeconds} max={data.modelRules.maxRequestTimeoutSeconds} value={model.requestTimeoutSeconds} onChange={(event) => setModel({ ...model, requestTimeoutSeconds: Number(event.target.value) })} /></label>
        <label>上下文窗口 Token
          <input type="number" min="0" value={model.contextWindowTokens || 0} onChange={(event) => setModel({ ...model, contextWindowTokens: Number(event.target.value) })} />
          <small>0 表示未知；Ollama 运行后会读取实际窗口。</small>
        </label>
        <label>自动压缩阈值<input type="number" min={data.modelRules.minCompressionThresholdPercent} max={data.modelRules.maxCompressionThresholdPercent} value={model.compressionThresholdPercent} onChange={(event) => setModel({ ...model, compressionThresholdPercent: Number(event.target.value) })} /></label>
        <label>API Key<input type="password" autoComplete="new-password" disabled={clearAPIKey} placeholder={model.secretConfigured ? '已配置，留空保持不变' : '可留空'} value={model.apiKey || ''} onChange={(event) => { setClearAPIKey(false); setModel({ ...model, apiKey: event.target.value }) }} /></label>
        <label>API Key 环境变量<input placeholder="例如 OPENAI_API_KEY" value={model.apiKeyEnv || ''} onChange={(event) => setModel({ ...model, apiKeyEnv: event.target.value })} /></label>
        {model.secretConfigured && <label className="check-label wide"><input type="checkbox" checked={clearAPIKey} onChange={(event) => { setClearAPIKey(event.target.checked); if (event.target.checked) setModel({ ...model, apiKey: '' }) }} />清除这套配置已保存的 API Key</label>}
      </div>
      <ModelNotice notice={notice} />
      <div className="form-actions">
        <button className="ghost-button" type="button" disabled={testing} onClick={onTest}>{testing ? '正在验证 Function Calling…' : '测试 EasyAgent 模型'}</button>
        <button className="primary-button" type="button" disabled={saving} onClick={() => onSave(clearAPIKey)}>{saving ? '保存中…' : '保存 EasyAgent 配置'}</button>
      </div>
    </div>
  )
}

type OllamaModelCatalogProps = {
  data: Bootstrap
  saving: boolean
  onActivate: (name: string) => Promise<void>
}

export function OllamaModelCatalog({ data, saving, onActivate }: OllamaModelCatalogProps) {
  if (!data.ollama.running) return <div className="ollama-model-catalog offline"><strong><span className="service-dot off" />Ollama 未运行</strong><small>{data.ollama.message}</small></div>

  return (
    <div className="ollama-model-catalog">
      <div className="ollama-model-head"><div><strong>已下载模型</strong><small>点击“设为默认”会保存为独立配置，并用于下一次新会话。</small></div><span>{data.ollama.models.length} 个</span></div>
      {data.ollama.models.length === 0
        ? <p className="ollama-empty">Ollama 已连接，但还没有下载模型。</p>
        : <div className="ollama-model-list">{data.ollama.models.map((item) => {
          const profile = data.modelProfiles.find((candidate) => candidate.settings.runtime === 'easyagent' && candidate.settings.model === item.name)
          const active = profile?.id === data.activeModelProfileId
          return <div className="ollama-model-row" key={item.name}><div><code>{item.name}</code><small>{profile ? `已保存为“${profile.name}”` : '尚未建立模型配置'}</small></div><span className={active ? 'active' : profile ? 'configured' : ''}>{active ? '当前默认' : profile ? '已配置' : '可用'}</span><button className="ghost-button" type="button" disabled={saving} onClick={() => void onActivate(item.name)}>{active ? '当前默认' : profile ? '设为默认' : '创建并启用'}</button></div>
        })}</div>}
    </div>
  )
}

type MCPSettingsProps = {
  data: Bootstrap
  mcpNotice: { ready: boolean; title: string; message: string; tools: string[] } | null
  installingPreset: string
  checkingPreset: string
  togglingMCP: string
  onInstall: (preset: Bootstrap['mcpPresets'][number]) => void
  onCheck: (preset: Bootstrap['mcpPresets'][number]) => void
  onTest: (id: string) => void
  onToggle: (item: MCPConfig) => void
  onCreate: () => void
  onEdit: (item: MCPConfig, preset?: Bootstrap['mcpPresets'][number]) => void
  dataPresets: Bootstrap['mcpPresets']
  onCloseNotice: () => void
}

export function MCPSettings({ data, mcpNotice, installingPreset, checkingPreset, togglingMCP, onInstall, onCheck, onTest, onToggle, onCreate, onEdit, dataPresets, onCloseNotice }: MCPSettingsProps) {
  const enabledMCPCount = data.mcps.filter((item) => item.enabled).length

  return (
    <div className="section-block">
      <div className="section-heading"><div><h2>MCP 连接</h2><p>一处配置，同时同步给 EasyAgent Runtime 和 Codex app-server。</p></div><button className="ghost-button" type="button" onClick={onCreate}>＋ 自定义</button></div>
      <div className="mcp-overview"><div><p className="eyebrow">连接数</p><strong>{data.mcps.length} 个 MCP</strong><span>配置保存在 EasyAgent 私有目录</span></div><div><p className="eyebrow">已就绪</p><strong>{enabledMCPCount} 个</strong><span>已验证并暴露给 Agent</span></div><div><p className="eyebrow">加载方式</p><strong>按需</strong><span>任务需要时才读取工具清单</span></div></div>
      <div className="capability-note"><strong>共享方式</strong><span>EasyAgent 直接连接 MCP；Codex 使用同一配置生成标准 <code>mcp_servers</code>，密钥只通过环境变量传递。</span></div>
      {mcpNotice && <MCPNotice notice={mcpNotice} onClose={onCloseNotice} />}
      <MCPList data={data} dataPresets={dataPresets} installingPreset={installingPreset} checkingPreset={checkingPreset} togglingMCP={togglingMCP} onInstall={onInstall} onCheck={onCheck} onTest={onTest} onToggle={onToggle} onCreate={onCreate} onEdit={onEdit} />
      <MCPPresets data={data} dataPresets={dataPresets} installingPreset={installingPreset} checkingPreset={checkingPreset} onInstall={onInstall} onCheck={onCheck} />
    </div>
  )
}

function MCPNotice({ notice, onClose }: { notice: NonNullable<MCPSettingsProps['mcpNotice']>; onClose: () => void }) {
  return <div role="status" aria-live="polite" className={`mcp-notice ${notice.ready ? 'ready' : 'failed'}`}><div><strong>{notice.title}</strong><span>{notice.message}</span></div>{notice.tools.length > 0 && <details><summary>查看 {notice.tools.length} 个工具</summary><code>{notice.tools.join('\n')}</code></details>}<button type="button" aria-label="关闭 MCP 状态" onClick={onClose}>×</button></div>
}

type MCPListProps = Omit<MCPSettingsProps, 'mcpNotice' | 'onCloseNotice'>

function MCPList({ data, dataPresets, installingPreset, checkingPreset, togglingMCP, onInstall, onCheck, onTest, onToggle, onCreate, onEdit }: MCPListProps) {
  if (data.mcps.length === 0) return <div className="mcp-grid"><div className="mcp-empty"><strong>还没有 MCP 连接</strong><span>从下方预设开始，或创建一个自定义 HTTP / stdio 服务。</span><button type="button" className="ghost-button" onClick={onCreate}>创建自定义 MCP</button></div></div>

  return <div className="mcp-grid">{data.mcps.map((item) => {
    const preset = dataPresets.find((candidate) => candidate.id === item.id)
    const canInstall = !item.enabled && preset?.action === 'install'
    const busy = installingPreset === item.id || checkingPreset === item.id || togglingMCP === item.id
    return <div className="mcp-row" key={item.id}><div className="mcp-row-info"><span className={`status ${item.enabled ? 'idle' : 'off'}`} /><strong>{preset?.name || item.name}</strong><small title={preset?.description || item.description}>{preset?.description || item.description || (item.transport === 'stdio' ? `${item.command} ${item.args.join(' ')}` : item.endpoint)}</small></div><span>{item.enabled ? '已启用' : '已停用'}</span><div className="mcp-row-actions">{preset?.action === 'install' && <button type="button" disabled={busy} onClick={() => onCheck(preset)}>{checkingPreset === item.id ? '检测中…' : '检测环境'}</button>}<button type="button" disabled={busy} onClick={() => onTest(item.id)}>测试连接</button><button type="button" disabled={busy} onClick={() => canInstall && preset ? onInstall(preset) : onToggle(item)}>{installingPreset === item.id ? '安装中…' : togglingMCP === item.id ? '处理中…' : canInstall ? '安装并启用' : item.enabled ? '停用' : '启用'}</button><button type="button" disabled={busy} onClick={() => onEdit(item, preset)}>编辑</button></div></div>
  })}</div>
}

type MCPPresetsProps = Pick<MCPSettingsProps, 'data' | 'dataPresets' | 'installingPreset' | 'checkingPreset' | 'onInstall' | 'onCheck'>

function MCPPresets({ data, dataPresets, installingPreset, checkingPreset, onInstall, onCheck }: MCPPresetsProps) {
  const available = dataPresets.filter((preset) => !data.mcps.some((item) => item.id === preset.id))
  return <div className="presets"><span>MCP 预设 · 检测不会修改系统；安装操作只写入 EasyAgent 私有 Runtime</span>{available.map((preset) => <div className="preset-card" key={preset.id}><strong>{preset.name}</strong><small>{preset.description}</small><em>{preset.requirement}</em><div className="preset-actions">{preset.action === 'install' && <button type="button" disabled={!!installingPreset || !!checkingPreset} onClick={() => onCheck(preset)}>{checkingPreset === preset.id ? '检测中…' : '检测环境'}</button>}<button type="button" disabled={!!installingPreset || !!checkingPreset} onClick={() => onInstall(preset)}>{installingPreset === preset.id ? '安装中…' : preset.action === 'install' ? '安装并启用' : '配置连接'}</button></div></div>)}</div>
}
