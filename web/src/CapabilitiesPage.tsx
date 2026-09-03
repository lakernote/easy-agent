import { useEffect, useState } from 'react'
import { api } from './api'
import type { Bootstrap, MCPConfig, ModelProfile, ModelSettings } from './types'
import { formatDuration, parseRecord, recordLines } from './format'
import { ConfirmDialog } from './dialogs'

type Notice = { ready: boolean; title: string; message: string }
type SettingsSection = 'runtime' | 'models' | 'tools'

export function Capabilities({ data, onRefresh, onError }: { data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const [model, setModel] = useState<ModelSettings>({ ...data.model })
  const [mcp, setMCP] = useState<MCPConfig | null>(null)
  const [testingModel, setTestingModel] = useState(false)
  const [savingModel, setSavingModel] = useState(false)
  const [modelNotice, setModelNotice] = useState<Notice | null>(null)
  const [installingPreset, setInstallingPreset] = useState('')
  const [checkingPreset, setCheckingPreset] = useState('')
  const [savingMCP, setSavingMCP] = useState(false)
  const [togglingMCP, setTogglingMCP] = useState('')
  const [deletingMCP, setDeletingMCP] = useState(false)
  const [confirmingMCPDelete, setConfirmingMCPDelete] = useState(false)
  const [mcpNotice, setMCPNotice] = useState<{ ready: boolean; title: string; message: string; tools: string[] } | null>(null)
  const [deletingProfile, setDeletingProfile] = useState(false)
  const [settingsSection, setSettingsSection] = useState<SettingsSection>('runtime')

  useEffect(() => setModel({ ...data.model }), [data.model])

  const saveModel = async () => {
    if (savingModel) return
    setSavingModel(true); setModelNotice(null); onError('')
    try { await api.saveModel(model); await onRefresh(); onError('') }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingModel(false) }
  }

  const testModel = async () => {
    if (testingModel) return
    setTestingModel(true); setModelNotice(null); onError('')
    try {
      const result = await api.testModel(model)
      setModelNotice({
        ready: true,
        title: model.runtime === 'codex' ? 'Codex app-server · 实际会话通过' : `${result.model} · Agent 能力可用`,
        message: model.runtime === 'codex' ? `已完成真实 thread/start + turn/start；${formatDuration(result.durationMs)}。测试会创建一个临时 thread。` : `原生 Function Calling 与工具结果回传均通过 · ${result.inputTokens + result.outputTokens} Token · ${formatDuration(result.durationMs)}`,
      })
    } catch (reason) {
      setModelNotice({ ready: false, title: '当前 Runtime 测试失败', message: (reason as Error).message })
    } finally { setTestingModel(false) }
  }

  const selectRuntime = (runtime: ModelSettings['runtime']) => {
    const existing = data.modelProfiles.find((profile) => profile.settings.runtime === runtime)
    setModel(existing ? { ...existing.settings, profileId: existing.id, profileName: existing.name } : (current) => runtime === 'codex'
      ? { ...current, profileId: `codex-${Date.now()}`, profileName: 'Codex 新配置', runtime, provider: 'codex', protocol: 'app_server', baseUrl: '', apiKey: '', apiKeyEnv: '', thinking: '', contextWindowTokens: 0, compressionThresholdPercent: 0, model: '' }
      : { ...current, profileId: `easyagent-${Date.now()}`, profileName: 'EasyAgent 新配置', runtime, provider: data.ollama.running ? 'ollama' : current.provider === 'codex' ? 'ollama' : current.provider, protocol: current.protocol === 'app_server' ? 'chat_completions' : current.protocol, baseUrl: current.baseUrl || data.ollama.baseUrl })
    setModelNotice(null); onError('')
  }

  const currentProfileSaved = data.modelProfiles.some((profile) => profile.id === model.profileId)
  const savedProfileOptions = data.modelProfiles.filter((profile) => profile.settings.runtime === model.runtime)
  const profileOptions = currentProfileSaved || !model.profileId
    ? savedProfileOptions
    : [...savedProfileOptions, { id: model.profileId, name: model.profileName || '新配置', settings: model }]
  const selectProfile = (profile: ModelProfile) => {
    setModel({ ...profile.settings, profileId: profile.id, profileName: profile.name })
    setModelNotice(null); onError('')
  }
  const createProfile = () => {
    const nextID = `${model.runtime}-${Date.now()}`
    setModel({ ...model, profileId: nextID, profileName: `${model.runtime === 'codex' ? 'Codex' : 'EasyAgent'} 新配置` })
    setModelNotice(null); onError('')
  }
  const removeProfile = async () => {
    if (!model.profileId || deletingProfile) return
    if (!currentProfileSaved) { setModel({ ...data.model }); return }
    if (data.modelProfiles.length <= 1) return
    if (!window.confirm(`删除“${model.profileName || '当前配置'}”？已有会话不会受影响。`)) return
    setDeletingProfile(true); onError('')
    try { await api.deleteModelProfile(model.profileId); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setDeletingProfile(false) }
  }

  const activateProfile = async (profile: ModelProfile) => {
    if (savingModel || profile.id === data.activeModelProfileId) {
      selectProfile(profile)
      return
    }
    setModel({ ...profile.settings, profileId: profile.id, profileName: profile.name })
    setSavingModel(true); setModelNotice(null); onError('')
    try { await api.saveModel({ ...profile.settings, profileId: profile.id, profileName: profile.name }); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingModel(false) }
  }

  const activateOllamaModel = async (name: string) => {
    const existing = data.modelProfiles.find((profile) => profile.settings.runtime === 'easyagent' && profile.settings.model === name)
    if (existing) {
      await activateProfile(existing)
      return
    }
    const next: ModelSettings = {
      ...data.model,
      profileId: `easyagent-${Date.now()}`,
      profileName: `Ollama · ${name}`,
      runtime: 'easyagent',
      provider: 'ollama',
      protocol: 'chat_completions',
      baseUrl: `${data.ollama.baseUrl}/v1`,
      model: name,
    }
    setModel(next); setSavingModel(true); setModelNotice(null); onError('')
    try { await api.saveModel(next); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingModel(false) }
  }

  const detectCodex = async () => {
    onError('')
    try { await api.codex(); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
  }

  const presetConfig = (preset: Bootstrap['mcpPresets'][number]): MCPConfig => ({ id: preset.id, name: preset.name, description: preset.description, enabled: false, transport: preset.transport as MCPConfig['transport'], command: preset.command, args: preset.args || [], endpoint: preset.endpoint, authType: preset.authType, headers: preset.headers || {}, environment: {} })
  const installPreset = async (preset: Bootstrap['mcpPresets'][number]) => {
    setMCPNotice(null)
    if (preset.action === 'configure') { setMCP(presetConfig(preset)); return }
    setInstallingPreset(preset.id)
    try {
      const result = await api.installMCPPreset(preset.id)
      setMCPNotice({ ready: result.ready, title: `${preset.name} · ${result.ready ? '已启用' : '尚未就绪'}`, message: result.message, tools: result.tools.map((tool) => tool.name) })
      await onRefresh()
    } catch (reason) { onError((reason as Error).message) }
    finally { setInstallingPreset('') }
  }
  const checkPreset = async (preset: Bootstrap['mcpPresets'][number]) => {
    setCheckingPreset(preset.id); setMCPNotice(null); onError('')
    try {
      const result = await api.checkMCPPreset(preset.id)
      setMCPNotice({ ready: result.ok, title: `${preset.name} · ${result.installed ? '已安装' : result.ok ? '环境可用' : '缺少依赖'}`, message: result.message, tools: [] })
    } catch (reason) { onError((reason as Error).message) }
    finally { setCheckingPreset('') }
  }
  const saveMCP = async () => {
    if (!mcp || savingMCP) return
    setSavingMCP(true); onError('')
    try {
      const saved = await api.saveMCP(mcp)
      setMCPNotice({ ready: true, title: `${saved.name} · ${saved.enabled ? '已验证并启用' : '配置已保存'}`, message: saved.enabled ? '握手和工具清单读取成功；Agent 会在任务需要时按需连接。' : '当前不会向 Agent 暴露此 MCP。', tools: [] })
      await onRefresh(); setMCP(null)
    } catch (reason) { onError((reason as Error).message) }
    finally { setSavingMCP(false) }
  }
  const removeMCP = async () => {
    if (!mcp || deletingMCP) return
    setDeletingMCP(true); onError('')
    try {
      const preset = data.mcpPresets.find((candidate) => candidate.id === mcp.id)
      if (preset?.action === 'install') await api.uninstallMCPPreset(mcp.id)
      else await api.deleteMCP(mcp.id)
      await onRefresh(); setConfirmingMCPDelete(false); setMCP(null)
    } catch (reason) { onError((reason as Error).message) }
    finally { setDeletingMCP(false) }
  }
  const toggleMCP = async (item: MCPConfig) => {
    if (togglingMCP) return
    setTogglingMCP(item.id); setMCPNotice(null); onError('')
    try {
      const saved = await api.saveMCP({ ...item, enabled: !item.enabled })
      setMCPNotice({ ready: true, title: `${saved.name} · ${saved.enabled ? '已启用' : '已停用'}`, message: saved.enabled ? '连接验证成功；Agent 会在任务需要时按需加载工具。' : '配置和私有安装包均保留，可随时重新启用。', tools: [] })
      await onRefresh()
    } catch (reason) { onError((reason as Error).message) }
    finally { setTogglingMCP('') }
  }
  const testMCP = async (id: string) => {
    setMCPNotice(null); onError('')
    try {
      const result = await api.testMCP(id)
      setMCPNotice({ ready: true, title: `连接成功 · ${result.tools.length} 个工具`, message: 'MCP 握手和工具清单读取正常。', tools: result.tools.map((item) => item.name) })
    } catch (reason) { onError((reason as Error).message) }
  }

  const persistedMCP = Boolean(mcp && data.mcps.some((item) => item.id === mcp.id))
  const editingPreset = mcp ? data.mcpPresets.find((candidate) => candidate.id === mcp.id) : undefined
  const codex = model.runtime === 'codex'
  const persistedCodex = data.model.runtime === 'codex'
  const profileChanged = model.profileId !== data.model.profileId
  const runtimeChanged = codex !== persistedCodex || profileChanged
  const activeRuntimeLabel = runtimeChanged ? '待启用' : '已启用'

  return <section className={`settings-page capabilities ${codex ? 'codex' : 'easyagent'}`}>
    <div className="page-intro runtime-intro"><p className="eyebrow">运行时配置</p><h1>运行时与模型</h1><p>把执行引擎、模型连接和工具能力分开管理；新会话创建时选择，已创建会话保持不变。</p></div>
    <nav className="settings-section-nav" aria-label="设置分区" role="tablist">
      <button className={settingsSection === 'runtime' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'runtime'} onClick={() => setSettingsSection('runtime')}><span>01</span><strong>运行时</strong><small>EasyAgent / Codex</small></button>
      <button className={settingsSection === 'models' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'models'} onClick={() => setSettingsSection('models')}><span>02</span><strong>模型配置</strong><small>{data.modelProfiles.length} 套可选配置</small></button>
      <button className={settingsSection === 'tools' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'tools'} onClick={() => setSettingsSection('tools')}><span>03</span><strong>工具与 MCP</strong><small>{codex ? '由 Codex 管理' : `${data.builtinTools.length + data.mcps.length} 项能力`}</small></button>
    </nav>
    <div className={`runtime-workbench settings-view-${settingsSection}`}>
      <nav className="runtime-rail" aria-label="选择 Agent Runtime">
        <div className="runtime-rail-head"><p className="eyebrow">RUNTIME</p><strong>执行引擎</strong><small>新会话创建时固定</small></div>
        <button className={`runtime-nav-item ${!codex ? 'selected' : ''}`} type="button" onClick={() => selectRuntime('easyagent')} aria-pressed={!codex}>
          <span className="runtime-nav-dot easyagent-dot" /><span><strong>EasyAgent</strong><small>Go Agent · Ollama / OpenAI</small></span><em>{!codex ? (persistedCodex ? '待启用' : '已启用') : data.ollama.running ? '就绪' : '配置'}</em>
        </button>
        <button className={`runtime-nav-item ${codex ? 'selected' : ''}`} type="button" onClick={() => selectRuntime('codex')} aria-pressed={codex}>
          <span className={`runtime-nav-dot ${data.codex.installed && data.codex.appServerAvailable ? 'ready' : ''}`} /><span><strong>Codex</strong><small>app-server · thread / sandbox</small></span><em>{codex ? (persistedCodex ? '已启用' : '待启用') : data.codex.installed && data.codex.appServerAvailable ? '就绪' : '检测'}</em>
        </button>
        <div className="runtime-rail-foot">切换只影响下一次新会话</div>
      </nav>
      <div className="runtime-main">
        {settingsSection === 'runtime' && <>
        <div className="runtime-main-head"><div><p className="eyebrow">{runtimeChanged ? 'NEXT RUNTIME' : 'ACTIVE RUNTIME'}</p><h2>{codex ? 'Codex Runtime' : 'EasyAgent Runtime'}</h2><p>{runtimeChanged ? `保存后，${codex ? '新会话' : '下一次新会话'}将使用 ${codex ? 'Codex app-server' : 'EasyAgent Go'}；已有会话不变。` : codex ? 'Codex app-server 负责 Agent 循环、thread、工具、Skill、沙箱、审批和实时事件。' : 'EasyAgent Go 负责 Agent 循环、工具调用、MCP、Skill 和上下文压缩。'}</p></div><div className="runtime-main-head-actions"><span className={`runtime-state ${runtimeChanged ? 'pending' : 'active'}`}>{activeRuntimeLabel}</span>{runtimeChanged && <button className="primary-button runtime-enable-button" disabled={savingModel} onClick={saveModel}>{savingModel ? '启用中…' : `启用 ${codex ? 'Codex' : 'EasyAgent'} Runtime`}</button>}</div></div>
        {codex && <CodexStatus data={data} onDetect={detectCodex} />}
        <div className="runtime-summary"><div><p className="eyebrow">NEW SESSION DEFAULT</p><strong>{data.model.profileName || '未命名模型配置'}</strong><span>{data.model.runtime === 'codex' ? 'Codex Runtime' : 'EasyAgent Runtime'} · {data.model.model || (data.model.runtime === 'codex' ? '使用 ~/.codex/config.toml' : '未填写模型')}</span></div><div><p className="eyebrow">AVAILABLE PROFILES</p><strong>{data.modelProfiles.length} 套</strong><span>可在模型配置中分别保存并切换</span></div><button className="ghost-button" type="button" onClick={() => setSettingsSection('models')}>管理模型配置 <span aria-hidden="true">→</span></button></div>
        </>}
        {settingsSection === 'models' && <>
        <div className="model-catalog"><div className="model-catalog-head"><div><p className="eyebrow">MODEL CATALOG</p><h2>模型配置</h2><p>每套配置独立保存 Provider、模型、上下文和超时；保存后可用于下一次新会话。</p></div><span className="settings-count">{data.modelProfiles.length} 套</span></div><div className="model-profile-directory" aria-label="模型配置列表">{data.modelProfiles.map((profile) => <div className={`model-profile-row ${profile.id === model.profileId ? 'selected' : ''}`} key={profile.id}><button className="model-profile-select" type="button" onClick={() => selectProfile(profile)}><span className={`runtime-nav-dot ${profile.settings.runtime === 'codex' ? (data.codex.installed && data.codex.appServerAvailable ? 'ready' : '') : 'easyagent-dot'}`} /><span><strong>{profile.name}</strong><small>{profile.settings.runtime === 'codex' ? 'Codex' : 'EasyAgent'} · {profile.settings.model || (profile.settings.runtime === 'codex' ? 'config.toml' : '未填写模型')}</small></span></button><button className={`profile-activate ${profile.id === data.activeModelProfileId ? 'active' : ''}`} type="button" disabled={profile.id === data.activeModelProfileId || savingModel} onClick={() => activateProfile(profile)}>{profile.id === data.activeModelProfileId ? '当前新会话默认' : '设为默认'}</button></div>)}{!currentProfileSaved && model.profileId && <div className="model-profile-row draft selected"><div className="model-profile-select"><span className="runtime-nav-dot" /><span><strong>{model.profileName || '新配置'}</strong><small>{model.runtime === 'codex' ? 'Codex' : 'EasyAgent'} · 未保存</small></span></div><em>编辑中</em></div>}<div className="directory-foot">默认配置会出现在首页输入框；删除前会检查是否仍被历史会话使用。</div></div></div>
        <div className="model-profile-switcher"><div><p className="eyebrow">CURRENT PROFILE</p><input className="model-profile-name" aria-label="配置名称" value={model.profileName || ''} onChange={(event) => setModel({ ...model, profileName: event.target.value })} placeholder="配置名称" /><small>当前编辑的配置；切换后请保存。</small></div><select value={model.profileId || ''} onChange={(event) => { const profile = profileOptions.find((item) => item.id === event.target.value); if (profile) selectProfile(profile) }} aria-label="选择当前模型配置"><option value="" disabled>选择配置</option>{profileOptions.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}{profile.settings.model ? ` · ${profile.settings.model}` : ''}</option>)}</select><div className="model-profile-actions"><button className="ghost-button" type="button" onClick={createProfile}>＋ 新建配置</button><button className="ghost-button danger" type="button" disabled={(currentProfileSaved && data.modelProfiles.length <= 1) || deletingProfile} onClick={removeProfile}>{deletingProfile ? '删除中…' : currentProfileSaved ? '删除' : '取消新建'}</button></div></div>
        {codex
          ? <CodexSettings data={data} model={model} setModel={setModel} notice={modelNotice} testing={testingModel} saving={savingModel} onTest={testModel} onSave={saveModel} />
          : <EasyAgentSettings data={data} model={model} setModel={setModel} notice={modelNotice} testing={testingModel} saving={savingModel} onTest={testModel} onSave={saveModel} onActivateOllama={activateOllamaModel} />}
        </>}
        {settingsSection === 'tools' && (codex ? <div className="section-block codex-tools-note"><div className="runtime-boundary-note"><strong>Codex 能力边界</strong><span>工具、Skill、沙箱和审批由 Codex app-server 管理；EasyAgent 的内置 Tools 和 MCP 不会注入 Codex。</span></div></div> : <>
          <div className="section-block"><div className="section-heading"><div><h2>内置 Tools</h2><p>首轮只发送少量核心 Schema；文件、Shell、网页和 Skill 需要时再加载。</p></div><span className="tag">{data.builtinTools.length} 个</span></div><div className="capability-note"><strong>工作区</strong><span><code>{data.runtime.workspace}</code></span><strong>私有 Runtime</strong><span><code>{data.runtime.runtime}</code></span></div><div className="tool-table">{data.builtinTools.map((tool) => <div key={tool.name}><code>{tool.name}</code><span>{tool.description}</span><em>{tool.category || tool.source}</em></div>)}</div></div>
          <MCPSettings data={data} mcp={mcp} mcpNotice={mcpNotice} installingPreset={installingPreset} checkingPreset={checkingPreset} togglingMCP={togglingMCP} onInstall={installPreset} onCheck={checkPreset} onTest={testMCP} onToggle={toggleMCP} onCreate={() => setMCP({ id: `mcp-${Date.now()}`, name: 'New MCP', description: '', enabled: false, transport: 'http', args: [], headers: {}, environment: {} })} onEdit={(item, preset) => setMCP({ ...item, name: preset?.name || item.name, description: preset?.description || item.description })} dataPresets={data.mcpPresets} onCloseNotice={() => setMCPNotice(null)} />
          <details className="prompt-block"><summary><div><h2>基础 System Prompt</h2><p>只属于 EasyAgent Runtime；Codex 使用自己的 instructions/config。</p></div><span>查看</span></summary><pre>{data.systemPrompt}</pre></details>
        </>)}
      </div>
    </div>
    {mcp && <div className="modal-backdrop" onMouseDown={() => setMCP(null)}><div className="modal" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">MCP SERVER</p><h2>{mcp.name}</h2></div><button aria-label="关闭 MCP 配置" onClick={() => setMCP(null)}>×</button></div>
      <div className="form-grid"><label>ID<input value={mcp.id} disabled /></label><label>名称<input value={mcp.name} onChange={(event) => setMCP({ ...mcp, name: event.target.value })} /></label><label className="wide">用途描述<input value={mcp.description || ''} onChange={(event) => setMCP({ ...mcp, description: event.target.value })} placeholder="告诉 Agent 什么时候应该加载这个 MCP" /></label><label>Transport<select value={mcp.transport} onChange={(event) => setMCP({ ...mcp, transport: event.target.value as MCPConfig['transport'] })}><option value="stdio">stdio</option><option value="http">HTTP</option></select></label><label className="check-label"><input type="checkbox" checked={mcp.enabled} onChange={(event) => setMCP({ ...mcp, enabled: event.target.checked })} />启用</label>{mcp.transport === 'stdio' ? <><label>命令<input value={mcp.command || ''} onChange={(event) => setMCP({ ...mcp, command: event.target.value })} /></label><label className="wide">参数（每行一个）<textarea value={mcp.args.join('\n')} onChange={(event) => setMCP({ ...mcp, args: event.target.value.split('\n').filter(Boolean) })} /></label><label className="wide">环境变量（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.environment)} onChange={(event) => setMCP({ ...mcp, environment: parseRecord(event.target.value) })} /></label></> : <><label className="wide">Endpoint<input value={mcp.endpoint || ''} onChange={(event) => setMCP({ ...mcp, endpoint: event.target.value })} /></label><label>认证<select value={mcp.authType || ''} onChange={(event) => setMCP({ ...mcp, authType: event.target.value })}><option value="">无</option><option value="bearer">Bearer Token</option><option value="basic">用户名密码</option></select></label>{mcp.authType === 'bearer' && <label>Token<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.token || ''} onChange={(event) => setMCP({ ...mcp, token: event.target.value })} /></label>}{mcp.authType === 'basic' && <><label>用户名<input value={mcp.username || ''} onChange={(event) => setMCP({ ...mcp, username: event.target.value })} /></label><label>密码<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.password || ''} onChange={(event) => setMCP({ ...mcp, password: event.target.value })} /></label></>}<label className="wide">自定义 Header（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.headers)} onChange={(event) => setMCP({ ...mcp, headers: parseRecord(event.target.value) })} /></label></>}</div>
      {mcp.enabled && <p className="modal-copy verify-copy">保存时会先校验认证、连接服务并读取工具清单；失败时不会启用。</p>}
      <div className="form-actions"><button className="ghost-button danger" disabled={savingMCP || deletingMCP} onClick={() => persistedMCP ? setConfirmingMCPDelete(true) : setMCP(null)}>{persistedMCP ? '删除' : '放弃新增'}</button><button className="primary-button" disabled={savingMCP || deletingMCP} onClick={saveMCP}>{savingMCP ? '正在验证…' : mcp.enabled ? '验证并启用' : '保存配置'}</button></div>
    </div></div>}
    {mcp && confirmingMCPDelete && <ConfirmDialog title={editingPreset?.action === 'install' ? `卸载 ${mcp.name}？` : '删除这个 MCP 配置？'} description={editingPreset?.action === 'install' ? 'EasyAgent 私有目录中的 MCP 包及其配置会被删除；不会卸载宿主机 Node/npm，也不会修改项目文件。' : '认证信息和连接配置将被永久删除，删除后无法恢复。'} subject={mcp.name} confirmLabel={editingPreset?.action === 'install' ? '卸载 MCP' : '删除 MCP'} busy={deletingMCP} onCancel={() => setConfirmingMCPDelete(false)} onConfirm={removeMCP} />}
  </section>
}

function CodexStatus({ data, onDetect }: { data: Bootstrap; onDetect: () => void }) {
  const ready = data.codex.installed && data.codex.appServerAvailable
  return <div className={`runtime-status ${ready ? 'ready' : 'missing'}`} role="status" aria-live="polite"><div><strong><span className={`service-dot ${ready ? '' : 'off'}`} />{ready ? `Codex CLI + app-server 已就绪${data.codex.version ? ` · ${data.codex.version}` : ''}` : data.codex.installed ? '已找到 CLI，但 app-server 不可用' : '未检测到 Codex CLI'}</strong><small>{data.codex.installed ? `${data.codex.path} · app-server 是 CLI 的子命令，不需要另装一个 app` : data.codex.message}</small></div><div className="runtime-status-actions">{!data.codex.installed && <a className="ghost-button" href={data.codex.installUrl} target="_blank" rel="noreferrer">查看 Codex CLI 安装说明</a>}<button className="ghost-button" onClick={onDetect}>重新检测</button></div>{!data.codex.installed && <code className="runtime-install-command">{data.codex.installCommand}</code>}</div>
}

function ModelNotice({ notice }: { notice: Notice | null }) {
  if (!notice) return null
  return <div role="status" aria-live="polite" className={`model-notice ${notice.ready ? 'ready' : 'failed'}`}><div><strong>{notice.title}</strong><span>{notice.message}</span></div></div>
}

function CodexSettings({ data, model, setModel, notice, testing, saving, onTest, onSave }: { data: Bootstrap; model: ModelSettings; setModel: (value: ModelSettings) => void; notice: Notice | null; testing: boolean; saving: boolean; onTest: () => void; onSave: () => void }) {
  return <div className="section-block codex-config"><div className="section-heading"><div><h2>Codex 配置</h2><p>认证和 Provider 从本机 Codex 配置读取；model override 可留空，留空时使用 config.toml。</p></div><span className="tag">app-server</span></div><div className="runtime-boundary-note"><strong>服务器部署</strong><span>只需在运行 EasyAgent 的服务器安装 Codex CLI；<code>codex app-server</code> 随 CLI 提供，不需要安装 ChatGPT Desktop。认证和 ~/.codex 配置也必须位于服务器运行用户的环境中。</span></div><div className="form-grid"><label className="wide">Model override（可选）<input value={model.model} onChange={(event) => setModel({ ...model, model: event.target.value })} placeholder="留空：使用 ~/.codex/config.toml" /><small>只填写当前 Codex 账号和 Provider 支持的模型；不会把 Ollama 模型传给 Codex。</small></label><label>请求超时（秒）<input type="number" min={data.modelRules.minRequestTimeoutSeconds} max={data.modelRules.maxRequestTimeoutSeconds} value={model.requestTimeoutSeconds} onChange={(event) => setModel({ ...model, requestTimeoutSeconds: Number(event.target.value) })} /><small>单次 turn 最多 {data.modelRules.maxRequestTimeoutSeconds} 秒</small></label></div><ModelNotice notice={notice} /><div className="form-actions"><button className="ghost-button" disabled={testing} onClick={onTest}>{testing ? '正在执行实际 app-server 测试…' : '测试 Codex app-server'}</button><button className="primary-button" disabled={saving} onClick={onSave}>{saving ? '保存中…' : '保存 Codex 配置'}</button></div></div>
}

function EasyAgentSettings({ data, model, setModel, notice, testing, saving, onTest, onSave, onActivateOllama }: { data: Bootstrap; model: ModelSettings; setModel: (value: ModelSettings) => void; notice: Notice | null; testing: boolean; saving: boolean; onTest: () => void; onSave: () => void; onActivateOllama: (name: string) => Promise<void> }) {
  return <div className="section-block easyagent-config"><div className="section-heading"><div><h2>模型连接</h2><p>EasyAgent 支持 OpenAI Chat Completions 和 Responses 兼容接口。</p></div><span className="tag">{model.protocol}</span></div><OllamaModelCatalog data={data} saving={saving} onActivate={onActivateOllama} /><div className="form-grid"><label>提供方<input value={model.provider} onChange={(event) => setModel({ ...model, provider: event.target.value })} /></label><label>协议<select value={model.protocol} onChange={(event) => setModel({ ...model, protocol: event.target.value as ModelSettings['protocol'] })}><option value="chat_completions">Chat Completions</option><option value="responses">Responses</option></select></label><label className="wide">Base URL<input value={model.baseUrl} onChange={(event) => setModel({ ...model, baseUrl: event.target.value })} /></label><label>模型名称<input value={model.model} onChange={(event) => setModel({ ...model, model: event.target.value })} /></label><label>推理模式<select value={model.thinking || ''} onChange={(event) => setModel({ ...model, thinking: event.target.value })}><option value="">模型默认</option><option value="disabled">尝试关闭推理</option></select><small>工具选择失败时，优先检查服务端是否支持原生 tool_calls。</small></label><label>最大输出 Token<input type="number" value={model.maxOutputTokens} onChange={(event) => setModel({ ...model, maxOutputTokens: Number(event.target.value) })} /></label><label>模型超时（秒）<input type="number" min={data.modelRules.minRequestTimeoutSeconds} max={data.modelRules.maxRequestTimeoutSeconds} value={model.requestTimeoutSeconds} onChange={(event) => setModel({ ...model, requestTimeoutSeconds: Number(event.target.value) })} /></label><label>上下文窗口 Token<input type="number" min="0" value={model.contextWindowTokens || 0} onChange={(event) => setModel({ ...model, contextWindowTokens: Number(event.target.value) })} /><small>0 表示未知；Ollama 运行后会读取实际窗口。</small></label><label>自动压缩阈值<input type="number" min={data.modelRules.minCompressionThresholdPercent} max={data.modelRules.maxCompressionThresholdPercent} value={model.compressionThresholdPercent} onChange={(event) => setModel({ ...model, compressionThresholdPercent: Number(event.target.value) })} /></label><label>API Key<input type="password" placeholder={model.secretConfigured ? '已配置，留空不修改' : '可留空'} value={model.apiKey || ''} onChange={(event) => setModel({ ...model, apiKey: event.target.value })} /></label><label>API Key 环境变量<input placeholder="例如 OPENAI_API_KEY" value={model.apiKeyEnv || ''} onChange={(event) => setModel({ ...model, apiKeyEnv: event.target.value })} /></label></div><ModelNotice notice={notice} /><div className="form-actions"><button className="ghost-button" disabled={testing} onClick={onTest}>{testing ? '正在验证 Function Calling…' : '测试 EasyAgent 模型'}</button><button className="primary-button" disabled={saving} onClick={onSave}>{saving ? '保存中…' : '保存 EasyAgent 配置'}</button></div></div>
}

function OllamaModelCatalog({ data, saving, onActivate }: { data: Bootstrap; saving: boolean; onActivate: (name: string) => Promise<void> }) {
  if (!data.ollama.running) return <div className="ollama-model-catalog offline"><strong><span className="service-dot off" />Ollama 未运行</strong><small>{data.ollama.message}</small></div>
  return <div className="ollama-model-catalog"><div className="ollama-model-head"><div><strong>已下载模型</strong><small>点击“设为默认”会保存为独立配置，并用于下一次新会话。</small></div><span>{data.ollama.models.length} 个</span></div>{data.ollama.models.length === 0 ? <p className="ollama-empty">Ollama 已连接，但还没有下载模型。</p> : <div className="ollama-model-list">{data.ollama.models.map((item) => { const profile = data.modelProfiles.find((candidate) => candidate.settings.runtime === 'easyagent' && candidate.settings.model === item.name); const active = profile?.id === data.activeModelProfileId; return <div className="ollama-model-row" key={item.name}><div><code>{item.name}</code><small>{profile ? `已保存为“${profile.name}”` : '尚未建立模型配置'}</small></div><span className={active ? 'active' : profile ? 'configured' : ''}>{active ? '当前默认' : profile ? '已配置' : '可用'}</span><button className="ghost-button" type="button" disabled={saving} onClick={() => void onActivate(item.name)}>{active ? '当前默认' : profile ? '设为默认' : '创建并启用'}</button></div>})}</div>}</div>
}

function MCPSettings({ data, mcpNotice, installingPreset, checkingPreset, togglingMCP, onInstall, onCheck, onTest, onToggle, onCreate, onEdit, dataPresets, onCloseNotice }: { data: Bootstrap; mcp: MCPConfig | null; mcpNotice: { ready: boolean; title: string; message: string; tools: string[] } | null; installingPreset: string; checkingPreset: string; togglingMCP: string; onInstall: (preset: Bootstrap['mcpPresets'][number]) => void; onCheck: (preset: Bootstrap['mcpPresets'][number]) => void; onTest: (id: string) => void; onToggle: (item: MCPConfig) => void; onCreate: () => void; onEdit: (item: MCPConfig, preset?: Bootstrap['mcpPresets'][number]) => void; dataPresets: Bootstrap['mcpPresets']; onCloseNotice: () => void }) {
  const enabledMCPCount = data.mcps.filter((item) => item.enabled).length
  return <div className="section-block"><div className="section-heading"><div><h2>MCP Servers</h2><p>只属于 EasyAgent Runtime；Codex 的 MCP/Apps 由 Codex 配置和 app-server 管理。</p></div><button className="ghost-button" onClick={onCreate}>＋ 自定义</button></div><div className="mcp-overview"><div><p className="eyebrow">CONNECTIONS</p><strong>{data.mcps.length} 个 MCP</strong><span>连接配置保存在 EasyAgent 私有目录</span></div><div><p className="eyebrow">READY</p><strong>{enabledMCPCount} 个</strong><span>已验证并暴露给 Agent</span></div><div><p className="eyebrow">LAZY LOAD</p><strong>按需</strong><span>任务需要时才加载工具清单</span></div></div><div className="capability-note"><strong>能力边界</strong><span>EasyAgent 只管理自己的 MCP 包和连接，不会全局安装或升级 Node、Python、Java 等宿主运行时。</span></div>{mcpNotice && <div role="status" aria-live="polite" className={`mcp-notice ${mcpNotice.ready ? 'ready' : 'failed'}`}><div><strong>{mcpNotice.title}</strong><span>{mcpNotice.message}</span></div>{mcpNotice.tools.length > 0 && <details><summary>查看 {mcpNotice.tools.length} 个工具</summary><code>{mcpNotice.tools.join('\n')}</code></details>}<button aria-label="关闭 MCP 状态" onClick={onCloseNotice}>×</button></div>}<div className="mcp-grid">{data.mcps.length === 0 ? <div className="mcp-empty"><strong>还没有 MCP 连接</strong><span>从下方预设开始，或创建一个自定义 HTTP / stdio 服务。</span><button type="button" className="ghost-button" onClick={onCreate}>创建自定义 MCP</button></div> : data.mcps.map((item) => { const preset = dataPresets.find((candidate) => candidate.id === item.id); const canInstall = !item.enabled && preset?.action === 'install'; const busy = installingPreset === item.id || checkingPreset === item.id || togglingMCP === item.id; return <div className="mcp-row" key={item.id}><div className="mcp-row-info"><span className={`status ${item.enabled ? 'idle' : 'off'}`} /><strong>{preset?.name || item.name}</strong><small title={preset?.description || item.description}>{preset?.description || item.description || (item.transport === 'stdio' ? `${item.command} ${item.args.join(' ')}` : item.endpoint)}</small></div><span>{item.enabled ? '已启用' : '已停用'}</span><div className="mcp-row-actions">{preset?.action === 'install' && <button disabled={busy} onClick={() => onCheck(preset)}>{checkingPreset === item.id ? '检测中…' : '检测环境'}</button>}<button disabled={busy} onClick={() => onTest(item.id)}>测试连接</button><button disabled={busy} onClick={() => canInstall && preset ? onInstall(preset) : onToggle(item)}>{installingPreset === item.id ? '安装中…' : togglingMCP === item.id ? '处理中…' : canInstall ? '安装并启用' : item.enabled ? '停用' : '启用'}</button><button disabled={busy} onClick={() => onEdit(item, preset)}>编辑</button></div></div> })}</div><div className="presets"><span>MCP 预设 · 检测不会修改系统；安装操作只写入 EasyAgent 私有 Runtime</span>{dataPresets.filter((preset) => !data.mcps.some((item) => item.id === preset.id)).map((preset) => <div className="preset-card" key={preset.id}><strong>{preset.name}</strong><small>{preset.description}</small><em>{preset.requirement}</em><div className="preset-actions">{preset.action === 'install' && <button type="button" disabled={!!installingPreset || !!checkingPreset} onClick={() => onCheck(preset)}>{checkingPreset === preset.id ? '检测中…' : '检测环境'}</button>}<button type="button" disabled={!!installingPreset || !!checkingPreset} onClick={() => onInstall(preset)}>{installingPreset === preset.id ? '安装中…' : preset.action === 'install' ? '安装并启用' : '配置连接'}</button></div></div>)}</div></div>
}
