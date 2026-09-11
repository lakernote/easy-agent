import { useEffect, useRef, useState } from 'react'
import { api } from './api'
import type { Bootstrap, MCPConfig, ModelSettings } from './types'
import { parseRecord, recordLines } from './format'
import { ConfirmDialog } from './dialogs'
import { Skills } from './Skills'
import { UsagePage } from './UsagePage'
import { CodexSettings, CodexStatus, EasyAgentSettings, MCPSettings, RuntimeOperationsSettings } from './settings/SettingsPanels'
import { ResearchSettings } from './settings/ResearchSettings'
import { useModelConfiguration } from './settings/useModelConfiguration'

export type SettingsSection = 'runtime' | 'tasks' | 'models' | 'skills' | 'tools' | 'usage' | 'weixin' | 'security'
type CapabilitiesSection = 'runtime' | 'tools' | 'settings'

function runtimeLabel(runtime: ModelSettings['runtime']) {
  return runtime === 'codex' ? 'Codex' : 'EasyAgent'
}

function providerLabel(profile: Bootstrap['modelProfiles'][number]) {
  if (profile.settings.runtime === 'codex') return profile.settings.provider && profile.settings.provider !== 'codex' ? `Codex · ${profile.settings.provider}` : 'Codex · 官方登录'
  if (profile.settings.provider === 'ollama') return 'Ollama'
  if (profile.settings.provider) return profile.settings.provider
  return 'Provider 未设置'
}

function modelLabel(profile: Bootstrap['modelProfiles'][number]) {
  if (profile.settings.model) return profile.settings.model
  return profile.settings.runtime === 'codex' ? '跟随 Codex 默认模型' : '模型未设置'
}

type CodexProviderDeleteTarget = { id: string; name: string }

function CodexProviderDirectory({ config, profile, saving, deleting, onSelect, onEdit, onDelete, onCreate }: { config: Bootstrap['codexConfig']; profile?: Bootstrap['modelProfiles'][number]; saving: boolean; deleting: boolean; onSelect: (provider: string) => void; onEdit: (provider: string) => void; onDelete: (provider: CodexProviderDeleteTarget) => void; onCreate: () => void }) {
  const activeProvider = profile?.settings.provider || ''
  const providers = config.providers || []
  return <div className="codex-directory" aria-label="Codex Provider 目录">
    <div className="model-catalog-toolbar codex-directory-toolbar"><div><strong>选择连接</strong><small>Codex 只有一套运行配置；这里的连接来自同一个 config.toml。</small></div><button className="primary-button" type="button" onClick={onCreate}>＋ 新建 Provider</button></div>
    <div className="codex-provider-list">
      <div className={`codex-provider-row ${activeProvider === '' ? 'selected' : ''}`}>
        <div className="codex-provider-main">
          <span className="runtime-nav-dot ready" /><span><strong>官方 ChatGPT 登录</strong><small>Codex 官方账号 · 不需要 env_key</small></span>
        </div>
        <div className="codex-provider-actions">{activeProvider === '' ? <span className="profile-default-state"><span className="service-dot" />新会话默认</span> : <button className="profile-activate" type="button" disabled={saving || deleting} onClick={() => onSelect('')}>设为新会话默认</button>}<span className="codex-provider-managed">由 Codex CLI 管理</span></div>
      </div>
      {providers.map((provider) => <div className={`codex-provider-row ${activeProvider === provider.id ? 'selected' : ''}`} key={provider.id}>
        <div className="codex-provider-main">
          <span className={`runtime-nav-dot ${provider.apiKeyConfigured ? 'ready' : ''}`} /><span><strong>{provider.name || provider.id}</strong><small>{provider.id} · {provider.envKey ? `env_key ${provider.envKey}` : '未设置 env_key'} · {provider.model || '跟随默认模型'}</small></span>
        </div>
        <div className="codex-provider-actions">{activeProvider === provider.id ? <span className="profile-default-state"><span className="service-dot" />新会话默认</span> : <button className="profile-activate" type="button" disabled={saving || deleting} onClick={() => onSelect(provider.id)}>设为新会话默认</button>}<button className="profile-edit" type="button" disabled={saving || deleting} onClick={() => onEdit(provider.id)}>编辑</button><button className="provider-delete" type="button" disabled={saving || deleting} onClick={() => onDelete({ id: provider.id, name: provider.name || provider.id })}>删除</button></div>
      </div>)}
      {!providers.length && <div className="model-empty-state codex-provider-empty"><span className="runtime-nav-dot" /><div><strong>还没有第三方 Provider</strong><small>需要使用 Groq、OpenRouter 等服务时，再新增 Provider。</small></div><button className="ghost-button" type="button" onClick={onCreate}>新增 Provider</button></div>}
    </div>
  </div>
}

export function Capabilities({ section, initialSection, initialModelRuntime, data, onRefresh, onError, onSettingsSectionChange }: { section: CapabilitiesSection; initialSection?: SettingsSection; initialModelRuntime?: ModelSettings['runtime']; data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void; onSettingsSectionChange?: (section: SettingsSection) => void }) {
  const [mcp, setMCP] = useState<MCPConfig | null>(null)
  const [installingPreset, setInstallingPreset] = useState('')
  const [checkingPreset, setCheckingPreset] = useState('')
  const [savingMCP, setSavingMCP] = useState(false)
  const [togglingMCP, setTogglingMCP] = useState('')
  const [deletingMCP, setDeletingMCP] = useState(false)
  const [confirmingMCPDelete, setConfirmingMCPDelete] = useState(false)
  const [confirmingProfileDelete, setConfirmingProfileDelete] = useState(false)
  const [codexProviderDeleteTarget, setCodexProviderDeleteTarget] = useState<CodexProviderDeleteTarget | null>(null)
  const [confirmingModelDiscard, setConfirmingModelDiscard] = useState(false)
  const [mcpNotice, setMCPNotice] = useState<{ ready: boolean; title: string; message: string; tools: string[] } | null>(null)
  const [settingsSection, setSettingsSection] = useState<SettingsSection>(initialSection || (section === 'tools' ? 'tools' : 'runtime'))
  const [modelRuntimeFilter, setModelRuntimeFilter] = useState<ModelSettings['runtime']>(initialModelRuntime || data.model.runtime)
  const modelDrawerRef = useRef<HTMLElement>(null)
  const modelEditorTriggerRef = useRef<HTMLElement | null>(null)
  const modelEditorWasOpenRef = useRef(false)

  useEffect(() => setSettingsSection(initialSection || (section === 'tools' ? 'tools' : 'runtime')), [initialSection, section])

  const {
    model, setModel, testingModel, savingModel, modelNotice, deletingProfile,
    modelEditorOpen, setModelEditorOpen, modelEditorMode, modelEditorDirty, codexConfig, setCodexConfig,
    savingCodexConfig, currentProfileSaved, saveModel, testModel,
    selectRuntime, openProfileEditor, openCodexProviderEditor, activateCodexProvider, closeModelEditor, removeProfile, activateProfile,
    deletingCodexProvider, removeCodexProvider, activateOllamaModel, detectCodex, saveCodexConfig,
  } = useModelConfiguration({ data, onRefresh, onError })

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
  const selectedRuntimeIsDefault = codex === persistedCodex
  const easyAgentProfiles = data.modelProfiles.filter((profile) => profile.settings.runtime === 'easyagent')
  const codexProfiles = data.modelProfiles.filter((profile) => profile.settings.runtime === 'codex')
  const codexProfile = codexProfiles[0]
  const selectedRuntimeName = runtimeLabel(modelRuntimeFilter)
  const runtimeProfiles = modelRuntimeFilter === 'codex' ? codexProfiles : easyAgentProfiles
  const selectRuntimeContext = (runtime: ModelSettings['runtime']) => {
    setModelRuntimeFilter(runtime)
    selectRuntime(runtime)
  }
  const changeSettingsSection = (next: SettingsSection) => {
    setSettingsSection(next)
    onSettingsSectionChange?.(next)
  }
  const requestModelEditorClose = () => {
    if (savingModel || savingCodexConfig || deletingCodexProvider) return
    if (modelEditorDirty) setConfirmingModelDiscard(true)
    else closeModelEditor()
  }

  useEffect(() => {
    if (modelEditorOpen && !modelEditorWasOpenRef.current) {
      modelEditorTriggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
      window.requestAnimationFrame(() => {
        const firstField = modelDrawerRef.current?.querySelector<HTMLElement>('.model-drawer-body input:not([disabled]), .model-drawer-body select:not([disabled]), .model-drawer-body textarea:not([disabled]), .model-drawer-body button:not([disabled])')
        ;(firstField || modelDrawerRef.current)?.focus()
      })
    } else if (!modelEditorOpen && modelEditorWasOpenRef.current) {
      const trigger = modelEditorTriggerRef.current
      window.requestAnimationFrame(() => trigger?.focus())
      modelEditorTriggerRef.current = null
    }
    modelEditorWasOpenRef.current = modelEditorOpen
  }, [modelEditorOpen])

  useEffect(() => {
    if (!modelEditorOpen || confirmingModelDiscard || codexProviderDeleteTarget || confirmingProfileDelete) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        requestModelEditorClose()
        return
      }
      if (event.key !== 'Tab' || !modelDrawerRef.current) return
      const focusable = Array.from(modelDrawerRef.current.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), details > summary, [tabindex]:not([tabindex="-1"])'))
        .filter((element) => element.getClientRects().length > 0)
      if (!focusable.length) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [modelEditorOpen, modelEditorDirty, savingModel, savingCodexConfig, deletingCodexProvider, confirmingModelDiscard, codexProviderDeleteTarget, confirmingProfileDelete])

  return <section className={`settings-page capabilities ${codex ? 'codex' : 'easyagent'} ${section === 'tools' ? 'extensions-page' : 'runtime-page'}`}>
    <div className="page-intro runtime-intro"><p className="eyebrow">{section === 'tools' ? '扩展管理' : '设置中心'}</p><h1>{section === 'tools' ? '工具与连接' : section === 'settings' ? '设置' : '运行时与模型'}</h1><p>{section === 'tools' ? '管理共享 MCP 连接；EasyAgent 还提供内置工具，Codex 保留自己的原生工具。' : section === 'settings' ? '选择运行时，管理模型与能力，并查看已经发生的用量。' : '把执行引擎、模型、Skills、工具和用量分开管理；新会话创建时才会读取默认配置。'}</p></div>
    {(section === 'runtime' || section === 'settings') && <nav className="settings-section-nav" aria-label="设置分区" role="tablist">
      <button className={settingsSection === 'runtime' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'runtime'} onClick={() => changeSettingsSection('runtime')}><span>01</span><strong>执行引擎</strong><small>EasyAgent / Codex</small></button>
      <button className={settingsSection === 'tasks' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'tasks'} onClick={() => changeSettingsSection('tasks')}><span>02</span><strong>任务设置</strong><small>并发 / 超时 / 恢复</small></button>
      <button className={settingsSection === 'models' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'models'} onClick={() => changeSettingsSection('models')}><span>03</span><strong>模型配置</strong><small>{data.modelProfiles.length} 套可选配置</small></button>
      <button className={settingsSection === 'skills' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'skills'} onClick={() => changeSettingsSection('skills')}><span>04</span><strong>Skills</strong><small>{data.skills.filter((item) => item.enabled).length}/{data.skills.length} 已启用</small></button>
      <button className={settingsSection === 'tools' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'tools'} onClick={() => changeSettingsSection('tools')}><span>05</span><strong>工具与 MCP</strong><small>{data.builtinTools.length + data.mcps.length} 项能力</small></button>
      <button className={settingsSection === 'usage' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'usage'} onClick={() => changeSettingsSection('usage')}><span>06</span><strong>用量</strong><small>按时间和模型</small></button>
    </nav>}
    {section === 'settings' && <div className="settings-scope-note"><span>共享能力</span><strong>Skills · MCP</strong><small>统一编辑，并转换成各 Runtime 的标准输入</small><span>Runtime 绑定</span><strong>模型 · 原生 Tools</strong><small>模型配置分开保存；各执行引擎保留自己的内置工具</small></div>}
    <div className={`runtime-workbench settings-view-${settingsSection}`}>
      <div className="runtime-main">
        {settingsSection === 'runtime' && <>
        <div className="runtime-main-head"><div><p className="eyebrow">运行环境</p><h2>{codex ? 'Codex Runtime' : 'EasyAgent Runtime'}</h2><p>{codex ? 'Codex 的会话、工具与沙箱执行环境。' : 'EasyAgent 的 Agent、工具与 MCP 执行环境。'}</p></div><div className="runtime-main-head-actions"><span className={`runtime-state ${selectedRuntimeIsDefault ? 'active' : 'pending'}`}>{selectedRuntimeIsDefault ? '包含新会话默认' : '可选运行环境'}</span></div></div>
        {codex && <CodexStatus data={data} onDetect={detectCodex} />}
        {!codex && <div className="runtime-status ready" role="status"><div><strong><span className="service-dot" />EasyAgent Go Runtime 已就绪</strong><small>{data.ollama.running ? `本地 Ollama 已连接 · ${data.ollama.models.length} 个模型` : '本地 Ollama 当前未连接；OpenAI 兼容 Provider 配置仍可使用。'}</small></div></div>}
        </>}
        {settingsSection === 'tasks' && <RuntimeOperationsSettings data={data} onRefresh={onRefresh} onError={onError} />}
        {(settingsSection === 'runtime' || settingsSection === 'models') && <>
        <div className="model-catalog">
          <div className="model-catalog-head"><div><p className="eyebrow">Runtime 与模型</p><h2>模型配置</h2><p>EasyAgent 可以有多套配置；Codex 只有一套运行配置，Provider 在目录中切换。</p></div><span className="settings-count">{modelRuntimeFilter === 'codex' ? '1 套运行配置' : `${data.modelProfiles.length} 套配置`}</span></div>
          <div className="model-runtime-tabs" role="tablist" aria-label="按 Runtime 查看模型配置">
            <button className={modelRuntimeFilter === 'easyagent' ? 'selected' : ''} type="button" role="tab" aria-selected={modelRuntimeFilter === 'easyagent'} onClick={() => selectRuntimeContext('easyagent')}><span className="runtime-nav-dot easyagent-dot" /><span><strong>EasyAgent</strong><small>Ollama / OpenAI 兼容 Provider</small></span><em>{easyAgentProfiles.length} 套{data.model.runtime === 'easyagent' ? ' · 当前默认' : ''}</em></button>
            <button className={modelRuntimeFilter === 'codex' ? 'selected' : ''} type="button" role="tab" aria-selected={modelRuntimeFilter === 'codex'} onClick={() => selectRuntimeContext('codex')}><span className={`runtime-nav-dot ${data.codex.installed && data.codex.appServerAvailable ? 'ready' : ''}`} /><span><strong>Codex</strong><small>一套配置 · Provider 目录</small></span><em>1 套{data.model.runtime === 'codex' ? ' · 当前默认' : ''}</em></button>
          </div>
          {modelRuntimeFilter === 'codex' && <CodexProviderDirectory config={data.codexConfig} profile={codexProfile} saving={savingModel} deleting={deletingCodexProvider} onSelect={(provider) => void activateCodexProvider(provider)} onEdit={(provider) => openCodexProviderEditor(provider)} onDelete={setCodexProviderDeleteTarget} onCreate={() => openCodexProviderEditor('__new__')} />}
          {modelRuntimeFilter === 'easyagent' && <><div className="model-catalog-toolbar"><div><strong>{selectedRuntimeName} 配置</strong><small>{runtimeProfiles.length ? '配置 = Provider 连接 + 模型参数；保存后可在新会话中直接切换。' : '当前 Runtime 还没有保存的配置。'}</small></div><div className="model-catalog-toolbar-actions"><button className="primary-button" type="button" onClick={() => openProfileEditor(undefined, modelRuntimeFilter)}>＋ 新建配置</button></div></div>
          <div className="model-profile-directory" aria-label={`${selectedRuntimeName} 模型配置列表`}>
            {runtimeProfiles.map((profile) => { const profileCodex = profile.settings.runtime === 'codex'; const isDefault = profile.id === data.activeModelProfileId; return <div className={`model-profile-row ${isDefault ? 'selected' : ''}`} key={profile.id}><button className="model-profile-select" type="button" onClick={() => openProfileEditor(profile)} aria-label={`编辑 ${profile.name}`}><span className={`runtime-nav-dot ${profileCodex ? (data.codex.installed && data.codex.appServerAvailable ? 'ready' : '') : 'easyagent-dot'}`} /><span><strong>{profile.name}</strong><small>{providerLabel(profile)} · {modelLabel(profile)}</small></span></button><div className="model-profile-row-actions">{isDefault ? <span className="profile-default-state"><span className="service-dot" />新会话默认</span> : <button className="profile-activate" type="button" disabled={savingModel} onClick={() => activateProfile(profile)}>设为新会话默认</button>}<button className="profile-edit" type="button" onClick={() => openProfileEditor(profile)}>编辑</button></div></div>})}
            {!runtimeProfiles.length && <div className="model-empty-state"><span className={`runtime-nav-dot ${modelRuntimeFilter === 'easyagent' ? 'easyagent-dot' : ''}`} /><div><strong>{`还没有 ${selectedRuntimeName} 配置`}</strong><small>新建后可以先测试连接，再设为新会话默认。</small></div><button className="ghost-button" type="button" onClick={() => openProfileEditor(undefined, modelRuntimeFilter)}>新建配置</button></div>}
            {!currentProfileSaved && model.profileId && model.runtime === modelRuntimeFilter && <div className="model-profile-row draft"><button className="model-profile-select" type="button" onClick={() => setModelEditorOpen(true)} aria-label="编辑未保存配置"><span className="runtime-nav-dot" /><span><strong>{model.profileName || '新配置'}</strong><small>尚未保存 · 关闭编辑器后会放弃</small></span></button><div className="model-profile-row-actions"><span className="profile-runtime-tag draft">草稿</span><button className="profile-edit" type="button" onClick={() => setModelEditorOpen(true)}>继续编辑</button></div></div>}
            <div className="directory-foot">默认配置只用于未手动选择的新会话；创建前可直接切换 Runtime 和模型，已有会话不会随配置切换。</div>
          </div></>}
        </div>
        </>}
        {settingsSection === 'skills' && <Skills data={data} onRefresh={onRefresh} onError={onError} />}
        {settingsSection === 'usage' && <UsagePage data={data} />}
		{settingsSection === 'tools' && <>
		  <ResearchSettings data={data} onRefresh={onRefresh} onError={onError} />
		  {!codex && <div className="section-block"><div className="section-heading"><div><h2>EasyAgent 内置 Tools</h2><p>当前时间、Shell、只读文件检索和网页研究首轮可用；写入与低频能力按需加载。</p></div><span className="tag">{data.builtinTools.length} 个</span></div><div className="capability-note"><strong>工作区</strong><span><code>{data.runtime.workspace}</code></span><strong>私有 Runtime</strong><span><code>{data.runtime.runtime}</code></span></div><div className="tool-table">{data.builtinTools.map((tool) => <div key={tool.name}><code>{tool.name}</code><span>{tool.description}</span><em>{tool.category || tool.source}</em></div>)}</div></div>}
          {codex && <div className="section-block codex-tools-note"><div className="runtime-boundary-note"><strong>Codex 原生工具</strong><span>Shell、文件修改、搜索和沙箱仍由 app-server 管理；下方 MCP 与 Skills 则由 EasyAgent 统一同步。</span></div></div>}
          <MCPSettings data={data} mcpNotice={mcpNotice} installingPreset={installingPreset} checkingPreset={checkingPreset} togglingMCP={togglingMCP} onInstall={installPreset} onCheck={checkPreset} onTest={testMCP} onToggle={toggleMCP} onCreate={() => setMCP({ id: `mcp-${Date.now()}`, name: 'New MCP', description: '', enabled: false, transport: 'http', args: [], headers: {}, environment: {} })} onEdit={(item, preset) => setMCP({ ...item, name: preset?.name || item.name, description: preset?.description || item.description })} dataPresets={data.mcpPresets} onCloseNotice={() => setMCPNotice(null)} />
          {!codex && <details className="prompt-block"><summary><div><h2>基础 System Prompt</h2><p>只属于 EasyAgent Runtime；Codex 使用自己的 instructions/config。</p></div><span>查看</span></summary><pre>{data.systemPrompt}</pre></details>}
        </>}
      </div>
    </div>
    {modelEditorOpen && <div className="model-drawer-backdrop" onMouseDown={requestModelEditorClose}><aside ref={modelDrawerRef} className="model-drawer" role="dialog" aria-modal="true" aria-labelledby="model-editor-title" tabIndex={-1} onMouseDown={(event) => event.stopPropagation()}><header className="model-drawer-head"><div><p className="eyebrow">{codex ? (modelEditorMode === 'new' ? '新建 Provider' : '编辑 Provider') : `${modelEditorMode === 'new' ? '新建配置' : '编辑配置'} · EasyAgent`}</p><h2 id="model-editor-title">{codex ? (modelEditorMode === 'new' ? '新建 Provider' : '编辑 Provider') : modelEditorMode === 'new' ? '新建模型配置' : '编辑模型配置'}</h2>{!codex && <p>保存后可在新会话输入区直接选择；需要作为默认配置时，再回到列表设为新会话默认。</p>}</div><button className="drawer-close" type="button" aria-label="关闭模型配置编辑" onClick={requestModelEditorClose}>×</button></header><div className="model-drawer-body">{!codex && <label className="model-drawer-name">配置名称<input value={model.profileName || ''} onChange={(event) => setModel({ ...model, profileName: event.target.value })} placeholder="例如：本地 Ollama" /></label>}{codex ? <CodexSettings config={codexConfig} setConfig={setCodexConfig} notice={modelNotice} savingConfig={savingCodexConfig} deleting={deletingCodexProvider} onDelete={() => setCodexProviderDeleteTarget({ id: codexConfig.provider, name: codexConfig.providerName || codexConfig.provider })} onSaveConfig={saveCodexConfig} /> : <EasyAgentSettings data={data} model={model} setModel={setModel} notice={modelNotice} testing={testingModel} saving={savingModel} onTest={testModel} onSave={saveModel} onActivateOllama={activateOllamaModel} />}{!codex && <div className="model-drawer-danger">{currentProfileSaved && <button className="ghost-button danger" type="button" disabled={data.modelProfiles.length <= 1 || deletingProfile} onClick={() => setConfirmingProfileDelete(true)}>{deletingProfile ? '删除中…' : '删除这套配置'}</button>}<span>{currentProfileSaved ? '删除不会影响已创建会话。' : '关闭抽屉会放弃这次未保存的配置。'}</span></div>}</div></aside></div>}
    {mcp && <div className="modal-backdrop" onMouseDown={() => setMCP(null)}><div className="modal" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">MCP 连接</p><h2>{mcp.name}</h2></div><button aria-label="关闭 MCP 配置" onClick={() => setMCP(null)}>×</button></div>
      <div className="form-grid"><label>ID<input value={mcp.id} disabled /></label><label>名称<input value={mcp.name} onChange={(event) => setMCP({ ...mcp, name: event.target.value })} /></label><label className="wide">用途描述<input value={mcp.description || ''} onChange={(event) => setMCP({ ...mcp, description: event.target.value })} placeholder="告诉 Agent 什么时候应该加载这个 MCP" /></label><label>Transport<select value={mcp.transport} onChange={(event) => setMCP({ ...mcp, transport: event.target.value as MCPConfig['transport'] })}><option value="stdio">stdio</option><option value="http">HTTP</option></select></label><label className="check-label"><input type="checkbox" checked={mcp.enabled} onChange={(event) => setMCP({ ...mcp, enabled: event.target.checked })} />启用</label>{mcp.transport === 'stdio' ? <><label>命令<input value={mcp.command || ''} onChange={(event) => setMCP({ ...mcp, command: event.target.value })} /></label><label className="wide">参数（每行一个）<textarea value={mcp.args.join('\n')} onChange={(event) => setMCP({ ...mcp, args: event.target.value.split('\n').filter(Boolean) })} /></label><label className="wide">环境变量（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.environment)} onChange={(event) => setMCP({ ...mcp, environment: parseRecord(event.target.value) })} /></label></> : <><label className="wide">Endpoint<input value={mcp.endpoint || ''} onChange={(event) => setMCP({ ...mcp, endpoint: event.target.value })} /></label><label>认证<select value={mcp.authType || ''} onChange={(event) => setMCP({ ...mcp, authType: event.target.value })}><option value="">无</option><option value="bearer">Bearer Token</option><option value="basic">用户名密码</option></select></label>{mcp.authType === 'bearer' && <label>Token<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.token || ''} onChange={(event) => setMCP({ ...mcp, token: event.target.value })} /></label>}{mcp.authType === 'basic' && <><label>用户名<input value={mcp.username || ''} onChange={(event) => setMCP({ ...mcp, username: event.target.value })} /></label><label>密码<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.password || ''} onChange={(event) => setMCP({ ...mcp, password: event.target.value })} /></label></>}<label className="wide">自定义 Header（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.headers)} onChange={(event) => setMCP({ ...mcp, headers: parseRecord(event.target.value) })} /></label></>}</div>
      {mcp.enabled && <p className="modal-copy verify-copy">保存时会先校验认证、连接服务并读取工具清单；失败时不会启用。</p>}
      <div className="form-actions"><button className="ghost-button danger" disabled={savingMCP || deletingMCP} onClick={() => persistedMCP ? setConfirmingMCPDelete(true) : setMCP(null)}>{persistedMCP ? '删除' : '放弃新增'}</button><button className="primary-button" disabled={savingMCP || deletingMCP} onClick={saveMCP}>{savingMCP ? '正在验证…' : mcp.enabled ? '验证并启用' : '保存配置'}</button></div>
    </div></div>}
    {mcp && confirmingMCPDelete && <ConfirmDialog title={editingPreset?.action === 'install' ? `卸载 ${mcp.name}？` : '删除这个 MCP 配置？'} description={editingPreset?.action === 'install' ? 'EasyAgent 私有目录中的 MCP 包及其配置会被删除；不会卸载宿主机 Node/npm，也不会修改项目文件。' : '认证信息和连接配置将被永久删除，删除后无法恢复。'} subject={mcp.name} confirmLabel={editingPreset?.action === 'install' ? '卸载 MCP' : '删除 MCP'} busy={deletingMCP} onCancel={() => setConfirmingMCPDelete(false)} onConfirm={removeMCP} />}
    {codexProviderDeleteTarget && <ConfirmDialog title="删除这个 Provider？" description="将从 config.toml 中移除；如果它是新会话默认连接，会自动切回官方 ChatGPT 登录。" subject={codexProviderDeleteTarget.name} confirmLabel="删除 Provider" busy={deletingCodexProvider} onCancel={() => setCodexProviderDeleteTarget(null)} onConfirm={() => { void removeCodexProvider(codexProviderDeleteTarget.id).then((deleted) => { if (deleted) setCodexProviderDeleteTarget(null) }) }} />}
    {confirmingProfileDelete && <ConfirmDialog title="删除这套模型配置？" description="已有会话不会受影响，但之后不能再为新会话选择这套配置。" subject={model.profileName || '当前配置'} confirmLabel="删除配置" busy={deletingProfile} onCancel={() => setConfirmingProfileDelete(false)} onConfirm={() => { setConfirmingProfileDelete(false); void removeProfile() }} />}
    {confirmingModelDiscard && <ConfirmDialog kind="discard" title="放弃未保存的修改？" description="关闭后，本次对模型配置的修改将不会保留。" subject={model.profileName || '未命名配置'} confirmLabel="放弃修改" busy={false} onCancel={() => setConfirmingModelDiscard(false)} onConfirm={() => { setConfirmingModelDiscard(false); closeModelEditor() }} />}
  </section>
}
