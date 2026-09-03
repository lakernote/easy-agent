import { useEffect, useState } from 'react'
import { api } from './api'
import type { Bootstrap, MCPConfig } from './types'
import { parseRecord, recordLines } from './format'
import { ConfirmDialog } from './dialogs'
import { Skills } from './Skills'
import { UsagePage } from './UsagePage'
import { CodexSettings, CodexStatus, EasyAgentSettings, MCPSettings } from './settings/SettingsPanels'
import { useModelConfiguration } from './settings/useModelConfiguration'

export type SettingsSection = 'runtime' | 'models' | 'skills' | 'tools' | 'usage'
type CapabilitiesSection = 'runtime' | 'tools' | 'settings'

export function Capabilities({ section, initialSection, data, onRefresh, onError }: { section: CapabilitiesSection; initialSection?: SettingsSection; data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const [mcp, setMCP] = useState<MCPConfig | null>(null)
  const [installingPreset, setInstallingPreset] = useState('')
  const [checkingPreset, setCheckingPreset] = useState('')
  const [savingMCP, setSavingMCP] = useState(false)
  const [togglingMCP, setTogglingMCP] = useState('')
  const [deletingMCP, setDeletingMCP] = useState(false)
  const [confirmingMCPDelete, setConfirmingMCPDelete] = useState(false)
  const [mcpNotice, setMCPNotice] = useState<{ ready: boolean; title: string; message: string; tools: string[] } | null>(null)
  const [settingsSection, setSettingsSection] = useState<SettingsSection>(initialSection || (section === 'tools' ? 'tools' : 'runtime'))

  useEffect(() => setSettingsSection(initialSection || (section === 'tools' ? 'tools' : 'runtime')), [initialSection, section])

  const {
    model, setModel, testingModel, savingModel, modelNotice, deletingProfile,
    modelEditorOpen, setModelEditorOpen, modelEditorMode, codexConfig, setCodexConfig,
    savingCodexConfig, installingCodex, currentProfileSaved, saveModel, testModel,
    selectRuntime, openProfileEditor, closeModelEditor, removeProfile, activateProfile,
    activateOllamaModel, detectCodex, installCodex, saveCodexConfig,
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
  const profileChanged = model.profileId !== data.model.profileId
  const runtimeChanged = codex !== persistedCodex || profileChanged
  const activeRuntimeLabel = runtimeChanged ? '待启用' : '已启用'

  return <section className={`settings-page capabilities ${codex ? 'codex' : 'easyagent'} ${section === 'tools' ? 'extensions-page' : 'runtime-page'}`}>
    <div className="page-intro runtime-intro"><p className="eyebrow">{section === 'tools' ? '扩展管理' : '设置中心'}</p><h1>{section === 'tools' ? '工具与连接' : section === 'settings' ? '设置' : '运行时与模型'}</h1><p>{section === 'tools' ? '管理 EasyAgent 的内置工具和外部 MCP 连接；Codex 的工具能力由 app-server 自己管理。' : section === 'settings' ? '选择运行时，管理模型与能力，并查看已经发生的用量。' : '把执行引擎、模型、Skills、工具和用量分开管理；新会话创建时才会读取默认配置。'}</p></div>
    {(section === 'runtime' || section === 'settings') && <nav className="settings-section-nav" aria-label="设置分区" role="tablist">
      <button className={settingsSection === 'runtime' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'runtime'} onClick={() => setSettingsSection('runtime')}><span>01</span><strong>执行引擎</strong><small>EasyAgent / Codex</small></button>
      <button className={settingsSection === 'models' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'models'} onClick={() => setSettingsSection('models')}><span>02</span><strong>模型配置</strong><small>{data.modelProfiles.length} 套可选配置</small></button>
      <button className={settingsSection === 'skills' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'skills'} onClick={() => setSettingsSection('skills')}><span>03</span><strong>Skills</strong><small>{data.skills.filter((item) => item.enabled).length}/{data.skills.length} 已启用</small></button>
      <button className={settingsSection === 'tools' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'tools'} onClick={() => setSettingsSection('tools')}><span>04</span><strong>工具与 MCP</strong><small>{data.builtinTools.length + data.mcps.length} 项能力</small></button>
      <button className={settingsSection === 'usage' ? 'active' : ''} type="button" role="tab" aria-selected={settingsSection === 'usage'} onClick={() => setSettingsSection('usage')}><span>05</span><strong>用量</strong><small>按时间和模型</small></button>
    </nav>}
    {section === 'settings' && <div className="settings-scope-note"><span>共享目录</span><strong>Skills 内容</strong><small>统一编辑，是否参与当前任务由 Runtime 决定</small><span>Runtime 绑定</span><strong>模型 · Tools / MCP</strong><small>模型配置按 Runtime 隔离；Codex 能力由 app-server 管理</small></div>}
    <div className={`runtime-workbench settings-view-${settingsSection}`}>
      {(section === 'runtime' || section === 'settings') && settingsSection === 'runtime' && <nav className="runtime-rail" aria-label="选择 Agent Runtime">
        <div className="runtime-rail-head"><p className="eyebrow">运行引擎</p><strong>执行引擎</strong><small>新会话创建时固定</small></div>
        <button className={`runtime-nav-item ${!codex ? 'selected' : ''}`} type="button" onClick={() => selectRuntime('easyagent')} aria-pressed={!codex}>
          <span className="runtime-nav-dot easyagent-dot" /><span><strong>EasyAgent</strong><small>Go Agent · Ollama / OpenAI</small></span><em>{!codex ? (persistedCodex ? '待启用' : '已启用') : data.ollama.running ? '就绪' : '配置'}</em>
        </button>
        <button className={`runtime-nav-item ${codex ? 'selected' : ''}`} type="button" onClick={() => selectRuntime('codex')} aria-pressed={codex}>
          <span className={`runtime-nav-dot ${data.codex.installed && data.codex.appServerAvailable ? 'ready' : ''}`} /><span><strong>Codex</strong><small>app-server · thread / sandbox</small></span><em>{codex ? (persistedCodex ? '已启用' : '待启用') : data.codex.installed && data.codex.appServerAvailable ? '就绪' : '检测'}</em>
        </button>
        <div className="runtime-rail-foot">切换只影响下一次新会话</div>
      </nav>}
      <div className="runtime-main">
        {settingsSection === 'runtime' && <>
        <div className="runtime-main-head"><div><p className="eyebrow">{runtimeChanged ? '待切换运行时' : '当前运行时'}</p><h2>{codex ? 'Codex Runtime' : 'EasyAgent Runtime'}</h2><p>{runtimeChanged ? `保存后，${codex ? '新会话' : '下一次新会话'}将使用 ${codex ? 'Codex app-server' : 'EasyAgent Go'}；已有会话不变。` : codex ? 'Codex app-server 负责 Agent 循环、thread、工具、Skill、沙箱、审批和实时事件。' : 'EasyAgent Go 负责 Agent 循环、工具调用、MCP、Skill 和上下文压缩。'}</p></div><div className="runtime-main-head-actions"><span className={`runtime-state ${runtimeChanged ? 'pending' : 'active'}`}>{activeRuntimeLabel}</span>{runtimeChanged && <button className="primary-button runtime-enable-button" disabled={savingModel} onClick={saveModel}>{savingModel ? '启用中…' : `启用 ${codex ? 'Codex' : 'EasyAgent'} Runtime`}</button>}</div></div>
        {codex && <CodexStatus data={data} installing={installingCodex} onInstall={installCodex} onDetect={detectCodex} />}
        <div className="runtime-summary"><div><p className="eyebrow">新会话默认</p><strong>{data.model.profileName || '未命名模型配置'}</strong><span>{data.model.runtime === 'codex' ? 'Codex Runtime' : 'EasyAgent Runtime'} · {data.model.model || (data.model.runtime === 'codex' ? '使用 ~/.codex/config.toml' : '未填写模型')}</span></div><div><p className="eyebrow">配置数量</p><strong>{data.modelProfiles.length} 套</strong><span>可在模型配置中分别保存并切换</span></div><button className="ghost-button" type="button" onClick={() => setSettingsSection('models')}>管理模型配置 <span aria-hidden="true">→</span></button></div>
        </>}
        {settingsSection === 'models' && <>
        <div className="model-catalog"><div className="model-catalog-head"><div><p className="eyebrow">配置列表</p><h2>模型配置</h2><p>不同 Runtime 的配置分开保存。点击配置名称或“编辑”修改，新增配置也会在抽屉中完成。</p></div><div className="catalog-actions"><span className="settings-count">{data.modelProfiles.length} 套</span><button className="primary-button" type="button" onClick={() => openProfileEditor()}>＋ 新建配置</button></div></div><div className="model-profile-directory" aria-label="模型配置列表">{data.modelProfiles.map((profile) => { const profileCodex = profile.settings.runtime === 'codex'; const isDefault = profile.id === data.activeModelProfileId; return <div className={`model-profile-row ${isDefault ? 'selected' : ''}`} key={profile.id}><button className="model-profile-select" type="button" onClick={() => openProfileEditor(profile)} aria-label={`编辑 ${profile.name}`}><span className={`runtime-nav-dot ${profileCodex ? (data.codex.installed && data.codex.appServerAvailable ? 'ready' : '') : 'easyagent-dot'}`} /><span><strong>{profile.name}</strong><small>{profileCodex ? 'Codex Runtime' : 'EasyAgent Runtime'} · {profile.settings.model || (profileCodex ? '使用 config.toml' : '未填写模型')}</small></span></button><div className="model-profile-row-actions"><span className={`profile-runtime-tag ${profileCodex ? 'codex' : 'easyagent'}`}>{profileCodex ? 'Codex' : 'EasyAgent'}</span><button className={`profile-activate ${isDefault ? 'active' : ''}`} type="button" disabled={isDefault || savingModel} onClick={() => activateProfile(profile)}>{isDefault ? '当前默认' : '设为默认'}</button><button className="profile-edit" type="button" onClick={() => openProfileEditor(profile)}>编辑</button></div></div>})}{!currentProfileSaved && model.profileId && <div className="model-profile-row draft"><button className="model-profile-select" type="button" onClick={() => setModelEditorOpen(true)} aria-label="编辑未保存配置"><span className="runtime-nav-dot" /><span><strong>{model.profileName || '新配置'}</strong><small>{model.runtime === 'codex' ? 'Codex Runtime' : 'EasyAgent Runtime'} · 未保存</small></span></button><div className="model-profile-row-actions"><span className="profile-runtime-tag draft">草稿</span><button className="profile-edit" type="button" onClick={() => setModelEditorOpen(true)}>继续编辑</button></div></div>}<div className="directory-foot">默认配置会出现在首页输入框；已有会话不会因修改配置而改变。</div></div></div>
        </>}
        {settingsSection === 'skills' && <Skills data={data} onRefresh={onRefresh} onError={onError} />}
        {settingsSection === 'usage' && <UsagePage data={data} />}
        {settingsSection === 'tools' && (codex ? <div className="section-block codex-tools-note"><div className="runtime-boundary-note"><strong>Codex 能力边界</strong><span>工具、Skill、沙箱和审批由 Codex app-server 管理；EasyAgent 的内置 Tools 和 MCP 不会注入 Codex。</span></div></div> : <>
          <div className="section-block"><div className="section-heading"><div><h2>内置 Tools</h2><p>首轮只发送少量核心 Schema；文件、Shell、网页和 Skill 需要时再加载。</p></div><span className="tag">{data.builtinTools.length} 个</span></div><div className="capability-note"><strong>工作区</strong><span><code>{data.runtime.workspace}</code></span><strong>私有 Runtime</strong><span><code>{data.runtime.runtime}</code></span></div><div className="tool-table">{data.builtinTools.map((tool) => <div key={tool.name}><code>{tool.name}</code><span>{tool.description}</span><em>{tool.category || tool.source}</em></div>)}</div></div>
          <MCPSettings data={data} mcpNotice={mcpNotice} installingPreset={installingPreset} checkingPreset={checkingPreset} togglingMCP={togglingMCP} onInstall={installPreset} onCheck={checkPreset} onTest={testMCP} onToggle={toggleMCP} onCreate={() => setMCP({ id: `mcp-${Date.now()}`, name: 'New MCP', description: '', enabled: false, transport: 'http', args: [], headers: {}, environment: {} })} onEdit={(item, preset) => setMCP({ ...item, name: preset?.name || item.name, description: preset?.description || item.description })} dataPresets={data.mcpPresets} onCloseNotice={() => setMCPNotice(null)} />
          <details className="prompt-block"><summary><div><h2>基础 System Prompt</h2><p>只属于 EasyAgent Runtime；Codex 使用自己的 instructions/config。</p></div><span>查看</span></summary><pre>{data.systemPrompt}</pre></details>
        </>)}
      </div>
    </div>
    {modelEditorOpen && <div className="model-drawer-backdrop" onMouseDown={closeModelEditor}><aside className="model-drawer" aria-label="模型配置编辑" onMouseDown={(event) => event.stopPropagation()}><header className="model-drawer-head"><div><p className="eyebrow">{modelEditorMode === 'new' ? '新建配置' : '编辑配置'}</p><h2>{modelEditorMode === 'new' ? '新建模型配置' : '编辑模型配置'}</h2><p>这套配置只属于当前 Runtime；保存后用于下一次新会话。</p></div><button className="drawer-close" type="button" aria-label="关闭模型配置编辑" onClick={closeModelEditor}>×</button></header><div className="model-drawer-body"><div className="model-drawer-meta"><span className={`profile-runtime-tag ${codex ? 'codex' : 'easyagent'}`}>{codex ? 'Codex' : 'EasyAgent'}</span><strong>{model.profileName || '未命名配置'}</strong><small>{codex ? 'app-server · thread / sandbox' : `${model.provider || 'Provider 未设置'} · ${model.model || '模型未设置'}`}</small></div><label className="model-drawer-name">配置名称<input value={model.profileName || ''} onChange={(event) => setModel({ ...model, profileName: event.target.value })} placeholder="例如：本地 Ollama" /></label>{codex ? <CodexSettings data={data} config={codexConfig} setConfig={setCodexConfig} model={model} setModel={setModel} notice={modelNotice} testing={testingModel} saving={savingModel} savingConfig={savingCodexConfig} onTest={testModel} onSave={saveModel} onSaveConfig={saveCodexConfig} /> : <EasyAgentSettings data={data} model={model} setModel={setModel} notice={modelNotice} testing={testingModel} saving={savingModel} onTest={testModel} onSave={saveModel} onActivateOllama={activateOllamaModel} />}<div className="model-drawer-danger">{currentProfileSaved && <button className="ghost-button danger" type="button" disabled={data.modelProfiles.length <= 1 || deletingProfile} onClick={removeProfile}>{deletingProfile ? '删除中…' : '删除这套配置'}</button>}<span>{currentProfileSaved ? '删除不会影响已创建会话。' : '关闭抽屉会放弃这次未保存的配置。'}</span></div></div></aside></div>}
    {mcp && <div className="modal-backdrop" onMouseDown={() => setMCP(null)}><div className="modal" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">MCP 连接</p><h2>{mcp.name}</h2></div><button aria-label="关闭 MCP 配置" onClick={() => setMCP(null)}>×</button></div>
      <div className="form-grid"><label>ID<input value={mcp.id} disabled /></label><label>名称<input value={mcp.name} onChange={(event) => setMCP({ ...mcp, name: event.target.value })} /></label><label className="wide">用途描述<input value={mcp.description || ''} onChange={(event) => setMCP({ ...mcp, description: event.target.value })} placeholder="告诉 Agent 什么时候应该加载这个 MCP" /></label><label>Transport<select value={mcp.transport} onChange={(event) => setMCP({ ...mcp, transport: event.target.value as MCPConfig['transport'] })}><option value="stdio">stdio</option><option value="http">HTTP</option></select></label><label className="check-label"><input type="checkbox" checked={mcp.enabled} onChange={(event) => setMCP({ ...mcp, enabled: event.target.checked })} />启用</label>{mcp.transport === 'stdio' ? <><label>命令<input value={mcp.command || ''} onChange={(event) => setMCP({ ...mcp, command: event.target.value })} /></label><label className="wide">参数（每行一个）<textarea value={mcp.args.join('\n')} onChange={(event) => setMCP({ ...mcp, args: event.target.value.split('\n').filter(Boolean) })} /></label><label className="wide">环境变量（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.environment)} onChange={(event) => setMCP({ ...mcp, environment: parseRecord(event.target.value) })} /></label></> : <><label className="wide">Endpoint<input value={mcp.endpoint || ''} onChange={(event) => setMCP({ ...mcp, endpoint: event.target.value })} /></label><label>认证<select value={mcp.authType || ''} onChange={(event) => setMCP({ ...mcp, authType: event.target.value })}><option value="">无</option><option value="bearer">Bearer Token</option><option value="basic">用户名密码</option></select></label>{mcp.authType === 'bearer' && <label>Token<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.token || ''} onChange={(event) => setMCP({ ...mcp, token: event.target.value })} /></label>}{mcp.authType === 'basic' && <><label>用户名<input value={mcp.username || ''} onChange={(event) => setMCP({ ...mcp, username: event.target.value })} /></label><label>密码<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.password || ''} onChange={(event) => setMCP({ ...mcp, password: event.target.value })} /></label></>}<label className="wide">自定义 Header（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.headers)} onChange={(event) => setMCP({ ...mcp, headers: parseRecord(event.target.value) })} /></label></>}</div>
      {mcp.enabled && <p className="modal-copy verify-copy">保存时会先校验认证、连接服务并读取工具清单；失败时不会启用。</p>}
      <div className="form-actions"><button className="ghost-button danger" disabled={savingMCP || deletingMCP} onClick={() => persistedMCP ? setConfirmingMCPDelete(true) : setMCP(null)}>{persistedMCP ? '删除' : '放弃新增'}</button><button className="primary-button" disabled={savingMCP || deletingMCP} onClick={saveMCP}>{savingMCP ? '正在验证…' : mcp.enabled ? '验证并启用' : '保存配置'}</button></div>
    </div></div>}
    {mcp && confirmingMCPDelete && <ConfirmDialog title={editingPreset?.action === 'install' ? `卸载 ${mcp.name}？` : '删除这个 MCP 配置？'} description={editingPreset?.action === 'install' ? 'EasyAgent 私有目录中的 MCP 包及其配置会被删除；不会卸载宿主机 Node/npm，也不会修改项目文件。' : '认证信息和连接配置将被永久删除，删除后无法恢复。'} subject={mcp.name} confirmLabel={editingPreset?.action === 'install' ? '卸载 MCP' : '删除 MCP'} busy={deletingMCP} onCancel={() => setConfirmingMCPDelete(false)} onConfirm={removeMCP} />}
  </section>
}
