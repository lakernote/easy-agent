import { useEffect, useState } from 'react'
import { api } from './api'
import type { Bootstrap, MCPConfig, ModelSettings } from './types'
import { formatDuration, parseRecord, recordLines } from './format'
import { ConfirmDialog } from './dialogs'
export function Capabilities({ data, onRefresh, onError }: { data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const [model, setModel] = useState<ModelSettings>({ ...data.model })
  const [mcp, setMCP] = useState<MCPConfig | null>(null)
  const [testingModel, setTestingModel] = useState(false)
  const [modelNotice, setModelNotice] = useState<{ ready: boolean; title: string; message: string } | null>(null)
  const [installingPreset, setInstallingPreset] = useState('')
  const [checkingPreset, setCheckingPreset] = useState('')
  const [savingMCP, setSavingMCP] = useState(false)
  const [togglingMCP, setTogglingMCP] = useState('')
  const [deletingMCP, setDeletingMCP] = useState(false)
  const [confirmingMCPDelete, setConfirmingMCPDelete] = useState(false)
  const [mcpNotice, setMCPNotice] = useState<{ ready: boolean; title: string; message: string; tools: string[] } | null>(null)
  useEffect(() => setModel({ ...data.model }), [data.model])
  const saveModel = async () => { try { await api.saveModel(model); await onRefresh() } catch (reason) { onError((reason as Error).message) } }
  const testModel = async () => {
    if (testingModel) return
    setTestingModel(true); setModelNotice(null); onError('')
    try {
      const result = await api.testModel(model)
      setModelNotice({ ready: true, title: model.runtime === 'codex' ? 'Codex Runtime · 已检测' : `${result.model} · Agent 能力可用`, message: model.runtime === 'codex' ? `${result.answer}；新会话会通过 thread/start，后续轮次通过 thread/resume` : `原生 Function Calling 与工具结果回传均通过 · ${result.inputTokens + result.outputTokens} Token · ${formatDuration(result.durationMs)}` })
    } catch (reason) {
      setModelNotice({ ready: false, title: '模型不适合当前 Agent 配置', message: (reason as Error).message })
    } finally { setTestingModel(false) }
  }
  const useOllama = async (name: string) => { try { await api.useOllama(name); await onRefresh() } catch (reason) { onError((reason as Error).message) } }
  const detectCodex = async () => { try { await api.codex(); await onRefresh() } catch (reason) { onError((reason as Error).message) } }
  const presetConfig = (preset: Bootstrap['mcpPresets'][number]): MCPConfig => ({ id: preset.id, name: preset.name, description: preset.description, enabled: false, transport: preset.transport as MCPConfig['transport'], command: preset.command, args: preset.args || [], endpoint: preset.endpoint, authType: preset.authType, headers: preset.headers || {}, environment: {} })
  const installPreset = async (preset: Bootstrap['mcpPresets'][number]) => {
    setMCPNotice(null)
    if (preset.action === 'configure') { setMCP(presetConfig(preset)); return }
    setInstallingPreset(preset.id)
    try {
      const result = await api.installMCPPreset(preset.id)
      setMCPNotice({ ready: result.ready, title: `${preset.name} · ${result.ready ? '已启用' : '尚未就绪'}`, message: result.message, tools: result.tools.map((tool) => tool.name) })
      await onRefresh()
    } catch (reason) { onError((reason as Error).message) } finally { setInstallingPreset('') }
  }
  const checkPreset = async (preset: Bootstrap['mcpPresets'][number]) => {
    setCheckingPreset(preset.id); setMCPNotice(null); onError('')
    try {
      const result = await api.checkMCPPreset(preset.id)
      setMCPNotice({ ready: result.ok, title: `${preset.name} · ${result.installed ? '已安装' : result.ok ? '环境可用' : '缺少依赖'}`, message: result.message, tools: [] })
    } catch (reason) { onError((reason as Error).message) } finally { setCheckingPreset('') }
  }
  const saveMCP = async () => {
    if (!mcp || savingMCP) return
    setSavingMCP(true)
    try {
      const saved = await api.saveMCP(mcp)
      setMCPNotice({ ready: true, title: `${saved.name} · ${saved.enabled ? '已验证并启用' : '配置已保存'}`, message: saved.enabled ? '握手和工具清单读取成功；Agent 会在任务需要时按需连接。' : '当前不会向 Agent 暴露此 MCP。', tools: [] })
      await onRefresh()
      setMCP(null)
    } catch (reason) { onError((reason as Error).message) } finally { setSavingMCP(false) }
  }
  const removeMCP = async () => {
    if (!mcp || deletingMCP) return
    setDeletingMCP(true); onError('')
    try {
      const preset = data.mcpPresets.find((candidate) => candidate.id === mcp.id)
      if (preset?.action === 'install') await api.uninstallMCPPreset(mcp.id)
      else await api.deleteMCP(mcp.id)
      await onRefresh()
      setConfirmingMCPDelete(false)
      setMCP(null)
    } catch (reason) { onError((reason as Error).message) } finally { setDeletingMCP(false) }
  }
  const toggleMCP = async (item: MCPConfig) => {
    if (togglingMCP) return
    setTogglingMCP(item.id); setMCPNotice(null); onError('')
    try {
      const saved = await api.saveMCP({ ...item, enabled: !item.enabled })
      setMCPNotice({
        ready: true,
        title: `${saved.name} · ${saved.enabled ? '已启用' : '已停用'}`,
        message: saved.enabled ? '连接验证成功；Agent 会在任务需要时按需加载工具。' : '配置和私有安装包均保留，可随时重新启用。',
        tools: [],
      })
      await onRefresh()
    } catch (reason) { onError((reason as Error).message) } finally { setTogglingMCP('') }
  }
  const testMCP = async (id: string) => { setMCPNotice(null); try { const result = await api.testMCP(id); setMCPNotice({ ready: true, title: `连接成功 · ${result.tools.length} 个工具`, message: 'MCP 握手和工具清单读取正常。', tools: result.tools.map((item) => item.name) }) } catch (reason) { onError((reason as Error).message) } }
  const persistedMCP = Boolean(mcp && data.mcps.some((item) => item.id === mcp.id))
  const editingPreset = mcp ? data.mcpPresets.find((candidate) => candidate.id === mcp.id) : undefined
  return <section className="settings-page capabilities"><div className="page-intro"><p className="eyebrow">可插拔能力</p><h1>模型与工具</h1><p>模型、内置 Tool、MCP 和基础提示词分开管理；启用后统一注册给同一个核心 Agent。</p></div>
    <div className="section-block runtime-section"><div className="section-heading"><div><h2>运行时</h2><p>运行时决定谁负责 Agent 循环、工具、Skill、沙箱和会话上下文；切换只对新会话生效。</p></div><span className="tag">{model.runtime === 'codex' ? 'Codex' : 'EasyAgent'}</span></div>
      <div className="runtime-picker" role="radiogroup" aria-label="选择 Agent Runtime">
        <label className={model.runtime === 'easyagent' ? 'selected' : ''}><input type="radio" name="runtime" value="easyagent" checked={model.runtime === 'easyagent'} onChange={() => setModel({ ...model, runtime: 'easyagent' })} /><span><strong>EasyAgent Runtime</strong><small>使用当前 Go Agent、内置 Tools、Skills 和 MCP 按需加载。</small></span></label>
        <label className={model.runtime === 'codex' ? 'selected' : ''}><input type="radio" name="runtime" value="codex" checked={model.runtime === 'codex'} onChange={() => setModel({ ...model, runtime: 'codex' })} /><span><strong>Codex Runtime</strong><small>由 Codex app-server 管理工具、Skill、沙箱和 thread 历史，读取本机 ~/.codex 配置。</small></span></label>
      </div>
      {model.runtime === 'codex' && <div className={`runtime-status ${data.codex.installed ? 'ready' : 'missing'}`} role="status" aria-live="polite"><div><strong><span className={`service-dot ${data.codex.installed ? '' : 'off'}`} />{data.codex.installed ? `Codex Runtime 已检测${data.codex.version ? ` · ${data.codex.version}` : ''}` : 'Codex Runtime 未安装'}</strong><small>{data.codex.installed ? data.codex.path : data.codex.message}</small></div><div className="runtime-status-actions">{!data.codex.installed && <a className="ghost-button" href={data.codex.installUrl} target="_blank" rel="noreferrer">查看安装说明</a>}<button className="ghost-button" onClick={detectCodex}>重新检测</button></div>{!data.codex.installed && <code className="runtime-install-command">{data.codex.installCommand}</code>}</div>}
    </div>
    <div className="section-block"><div className="section-heading"><div><h2>模型</h2><p>{model.runtime === 'codex' ? 'Codex Runtime 使用 ~/.codex/config.toml 的 provider；这里的模型名可作为 thread 覆盖值。' : '支持 OpenAI Chat Completions 和 Responses 兼容接口。'}</p></div><span className="tag">{model.runtime === 'codex' ? 'app-server' : model.protocol}</span></div>
      <div className="form-grid"><label>提供方<input value={model.provider} onChange={(e) => setModel({ ...model, provider: e.target.value })} /></label><label>协议<select value={model.protocol} onChange={(e) => setModel({ ...model, protocol: e.target.value as ModelSettings['protocol'] })}><option value="chat_completions">Chat Completions</option><option value="responses">Responses</option></select></label><label className="wide">Base URL<input value={model.baseUrl} onChange={(e) => setModel({ ...model, baseUrl: e.target.value })} /></label><label>模型名称<input value={model.model} onChange={(e) => setModel({ ...model, model: e.target.value })} /></label><label>推理模式<select value={model.thinking || ''} onChange={(e) => setModel({ ...model, thinking: e.target.value })}><option value="">模型默认</option><option value="disabled">尝试关闭推理</option></select><small>兼容服务建议使用模型默认；为保证 Function Calling，Ollama 工具选择轮可能保留推理</small></label><label>最大输出 Token<input type="number" value={model.maxOutputTokens} onChange={(e) => setModel({ ...model, maxOutputTokens: Number(e.target.value) })} /></label><label>模型超时（秒）<input type="number" min={data.modelRules.minRequestTimeoutSeconds} max={data.modelRules.maxRequestTimeoutSeconds} value={model.requestTimeoutSeconds} onChange={(e) => setModel({ ...model, requestTimeoutSeconds: Number(e.target.value) })} /><small>默认 {data.modelRules.defaultRequestTimeoutSeconds} 秒；单次请求最多 {data.modelRules.maxRequestTimeoutSeconds} 秒</small></label><label>上下文窗口 Token<input type="number" min="0" value={model.contextWindowTokens || 0} onChange={(e) => setModel({ ...model, contextWindowTokens: Number(e.target.value) })} /><small>0 表示未知；Ollama 运行后读取当前实际窗口</small></label><label>自动压缩阈值<input type="number" min={data.modelRules.minCompressionThresholdPercent} max={data.modelRules.maxCompressionThresholdPercent} value={model.compressionThresholdPercent} onChange={(e) => setModel({ ...model, compressionThresholdPercent: Number(e.target.value) })} /><small>默认达到上下文窗口的 {data.modelRules.defaultCompressionThresholdPercent}% 后生成检查点</small></label><label>API Key<input type="password" placeholder={model.secretConfigured ? '已配置，留空不修改' : '可留空'} value={model.apiKey || ''} onChange={(e) => setModel({ ...model, apiKey: e.target.value })} /></label><label>API Key 环境变量<input placeholder="例如 OPENAI_API_KEY" value={model.apiKeyEnv || ''} onChange={(e) => setModel({ ...model, apiKeyEnv: e.target.value })} /></label></div>{modelNotice && <div role="status" aria-live="polite" className={`model-notice ${modelNotice.ready ? 'ready' : 'failed'}`}><div><strong>{modelNotice.title}</strong><span>{modelNotice.message}</span></div><button aria-label="关闭模型测试结果" onClick={() => setModelNotice(null)}>×</button></div>}<div className="form-actions"><button className="ghost-button" disabled={testingModel} onClick={testModel}>{testingModel ? '正在验证 Function Calling…' : '测试当前模型'}</button><button className="primary-button" onClick={saveModel}>保存模型</button></div><div className="ollama-strip"><div><strong><span className={`service-dot ${data.ollama.running ? '' : 'off'}`} />Ollama · 无需 API Key</strong><small>{data.ollama.message}</small></div><div>{data.ollama.models.map((item) => <button key={item.name} className="ghost-button" onClick={() => useOllama(item.name)}>使用 {item.name}</button>)}</div></div></div>
    <div className="section-block"><div className="section-heading"><div><h2>内置 Tools</h2><p>首轮常驻少量核心工具；文件、Shell、网页和 Skill 需要时再加载完整 Tool Schema。</p></div><span className="tag">{data.builtinTools.length} 个</span></div><div className="capability-note"><strong>EasyAgent Home</strong><span><code>{data.runtime.home}</code></span><strong>默认工作区</strong><span><code>{data.runtime.workspace}</code></span><strong>私有运行时</strong><span><code>{data.runtime.runtime}</code></span></div><div className="tool-table">{data.builtinTools.map((tool) => <div key={tool.name}><code>{tool.name}</code><span>{tool.description}</span><em>{tool.category || tool.source}</em></div>)}</div></div>
    <div className="section-block">
      <div className="section-heading"><div><h2>MCP Servers</h2><p>远程服务配置连接；本地预设先检测宿主环境，再把 MCP 包安装到 EasyAgent 私有目录。</p></div><button className="ghost-button" onClick={() => setMCP({ id: `mcp-${Date.now()}`, name: 'New MCP', description: '', enabled: false, transport: 'http', args: [], headers: {}, environment: {} })}>＋ 自定义</button></div>
      <div className="capability-note"><strong>能力边界</strong><span>工作区文件工具已经内置；EasyAgent 只管理 MCP 包，不会全局安装或升级 Node、Python、Java 等项目运行时。</span></div>
      {mcpNotice && <div role="status" aria-live="polite" className={`mcp-notice ${mcpNotice.ready ? 'ready' : 'failed'}`}><div><strong>{mcpNotice.title}</strong><span>{mcpNotice.message}</span></div>{mcpNotice.tools.length > 0 && <details><summary>查看 {mcpNotice.tools.length} 个工具</summary><code>{mcpNotice.tools.join('\n')}</code></details>}<button aria-label="关闭 MCP 状态" onClick={() => setMCPNotice(null)}>×</button></div>}
      <div className="mcp-grid">{data.mcps.map((item) => {
        const preset = data.mcpPresets.find((candidate) => candidate.id === item.id)
        const canInstall = !item.enabled && preset?.action === 'install'
        const busy = installingPreset === item.id || checkingPreset === item.id || togglingMCP === item.id
        return <div className="mcp-row" key={item.id}>
          <div className="mcp-row-info"><span className={`status ${item.enabled ? 'idle' : 'off'}`} /><strong>{preset?.name || item.name}</strong><small title={preset?.description || item.description}>{preset?.description || item.description || (item.transport === 'stdio' ? `${item.command} ${item.args.join(' ')}` : item.endpoint)}</small></div>
          <span>{item.enabled ? '已启用' : '已停用'}</span>
          <div className="mcp-row-actions">
            {preset?.action === 'install' && <button disabled={busy} onClick={() => checkPreset(preset)}>{checkingPreset === item.id ? '检测中…' : '检测环境'}</button>}
            <button disabled={busy} onClick={() => testMCP(item.id)}>测试连接</button>
            <button disabled={busy} onClick={() => canInstall && preset ? installPreset(preset) : toggleMCP(item)}>{installingPreset === item.id ? '安装中…' : togglingMCP === item.id ? '处理中…' : canInstall ? '安装并启用' : item.enabled ? '停用' : '启用'}</button>
            <button disabled={busy} onClick={() => setMCP({ ...item, name: preset?.name || item.name, description: preset?.description || item.description })}>编辑</button>
          </div>
        </div>
      })}</div>
      <div className="presets"><span>MCP 预设 · 检测不会修改系统；安装操作只写入 EasyAgent 私有 Runtime</span>{data.mcpPresets.filter((preset) => !data.mcps.some((item) => item.id === preset.id)).map((preset) => <div className="preset-card" key={preset.id}><strong>{preset.name}</strong><small>{preset.description}</small><em>{preset.requirement}</em><div className="preset-actions">{preset.action === 'install' && <button type="button" disabled={!!installingPreset || !!checkingPreset} onClick={() => checkPreset(preset)}>{checkingPreset === preset.id ? '检测中…' : '检测环境'}</button>}<button type="button" disabled={!!installingPreset || !!checkingPreset} onClick={() => installPreset(preset)}>{installingPreset === preset.id ? '安装中…' : preset.action === 'install' ? '安装并启用' : '配置连接'}</button></div></div>)}</div>
    </div>
    <details className="prompt-block"><summary><div><h2>基础 System Prompt</h2><p>独立 Markdown 包，只定义稳定行为；具体任务方法和团队约定写进 Skill。</p></div><span>查看</span></summary><pre>{data.systemPrompt}</pre></details>
    {mcp && <div className="modal-backdrop" onMouseDown={() => setMCP(null)}><div className="modal" onMouseDown={(e) => e.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">MCP SERVER</p><h2>{mcp.name}</h2></div><button aria-label="关闭 MCP 配置" onClick={() => setMCP(null)}>×</button></div>
      <div className="form-grid">
        <label>ID<input value={mcp.id} disabled /></label>
        <label>名称<input value={mcp.name} onChange={(e) => setMCP({ ...mcp, name: e.target.value })} /></label>
        <label className="wide">用途描述<input value={mcp.description || ''} onChange={(e) => setMCP({ ...mcp, description: e.target.value })} placeholder="告诉 Agent 什么时候应该加载这个 MCP" /></label>
        <label>Transport<select value={mcp.transport} onChange={(e) => setMCP({ ...mcp, transport: e.target.value as MCPConfig['transport'] })}><option value="stdio">stdio</option><option value="http">HTTP</option></select></label>
        <label className="check-label"><input type="checkbox" checked={mcp.enabled} onChange={(e) => setMCP({ ...mcp, enabled: e.target.checked })} />启用</label>
        {mcp.transport === 'stdio' ? <>
          <label>命令<input value={mcp.command || ''} onChange={(e) => setMCP({ ...mcp, command: e.target.value })} /></label>
          <label className="wide">参数（每行一个）<textarea value={mcp.args.join('\n')} onChange={(e) => setMCP({ ...mcp, args: e.target.value.split('\n').filter(Boolean) })} /></label>
          <label className="wide">环境变量（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.environment)} onChange={(e) => setMCP({ ...mcp, environment: parseRecord(e.target.value) })} /></label>
        </> : <>
          <label className="wide">Endpoint<input value={mcp.endpoint || ''} onChange={(e) => setMCP({ ...mcp, endpoint: e.target.value })} /></label>
          <label>认证<select value={mcp.authType || ''} onChange={(e) => setMCP({ ...mcp, authType: e.target.value })}><option value="">无</option><option value="bearer">Bearer Token</option><option value="basic">用户名密码</option></select></label>
          {mcp.authType === 'bearer' && <label>Token<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.token || ''} onChange={(e) => setMCP({ ...mcp, token: e.target.value })} /></label>}
          {mcp.authType === 'basic' && <><label>用户名<input value={mcp.username || ''} onChange={(e) => setMCP({ ...mcp, username: e.target.value })} /></label><label>密码<input type="password" placeholder={mcp.secretConfigured ? '已配置，留空不修改' : ''} value={mcp.password || ''} onChange={(e) => setMCP({ ...mcp, password: e.target.value })} /></label></>}
          <label className="wide">自定义 Header（KEY=VALUE，每行一个）<textarea value={recordLines(mcp.headers)} onChange={(e) => setMCP({ ...mcp, headers: parseRecord(e.target.value) })} /></label>
        </>}
      </div>
      {mcp.enabled && <p className="modal-copy verify-copy">保存时会先校验认证、连接服务并读取工具清单；失败时不会启用。</p>}
      <div className="form-actions"><button className="ghost-button danger" disabled={savingMCP || deletingMCP} onClick={() => persistedMCP ? setConfirmingMCPDelete(true) : setMCP(null)}>{persistedMCP ? '删除' : '放弃新增'}</button><button className="primary-button" disabled={savingMCP || deletingMCP} onClick={saveMCP}>{savingMCP ? '正在验证…' : mcp.enabled ? '验证并启用' : '保存配置'}</button></div>
    </div></div>}
    {mcp && confirmingMCPDelete && <ConfirmDialog
      title={editingPreset?.action === 'install' ? `卸载 ${mcp.name}？` : '删除这个 MCP 配置？'}
      description={editingPreset?.action === 'install' ? 'EasyAgent 私有目录中的 MCP 包及其配置会被删除；不会卸载宿主机 Node/npm，也不会修改项目文件。' : '认证信息和连接配置将被永久删除，Agent 也将无法再调用它提供的工具。'}
      subject={mcp.name}
      confirmLabel={editingPreset?.action === 'install' ? '卸载 MCP' : '删除 MCP'}
      busy={deletingMCP}
      onCancel={() => setConfirmingMCPDelete(false)}
      onConfirm={removeMCP}
    />}
  </section>
}
