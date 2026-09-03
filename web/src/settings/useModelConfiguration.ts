import { useEffect, useState } from 'react'
import { api } from '../api'
import type { Bootstrap, CodexProviderConfig, ModelProfile, ModelSettings } from '../types'
import { formatDuration } from '../format'
import type { Notice } from './SettingsPanels'

type ModelConfigurationArgs = {
  data: Bootstrap
  onRefresh: () => Promise<Bootstrap>
  onError: (value: string) => void
}

export function useModelConfiguration({ data, onRefresh, onError }: ModelConfigurationArgs) {
  const [model, setModel] = useState<ModelSettings>({ ...data.model })
  const [testingModel, setTestingModel] = useState(false)
  const [savingModel, setSavingModel] = useState(false)
  const [modelNotice, setModelNotice] = useState<Notice | null>(null)
  const [deletingProfile, setDeletingProfile] = useState(false)
  const [modelEditorOpen, setModelEditorOpen] = useState(false)
  const [modelEditorMode, setModelEditorMode] = useState<'new' | 'edit'>('edit')
  const [modelEditorSnapshot, setModelEditorSnapshot] = useState<ModelSettings | null>(null)
  const [codexConfig, setCodexConfig] = useState<CodexProviderConfig>({ ...data.codexConfig })
  const [savingCodexConfig, setSavingCodexConfig] = useState(false)
  const [installingCodex, setInstallingCodex] = useState(false)

  useEffect(() => setModel({ ...data.model }), [data.model])
  useEffect(() => setCodexConfig({ ...data.codexConfig }), [data.codexConfig])

  const saveModel = async () => {
    if (savingModel) return
    setSavingModel(true); setModelNotice(null); onError('')
    try { await api.saveModel(model); await onRefresh(); setModelEditorSnapshot(null); setModelEditorOpen(false); onError('') }
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
  const selectProfile = (profile: ModelProfile) => {
    setModel({ ...profile.settings, profileId: profile.id, profileName: profile.name })
    setModelNotice(null); onError('')
  }

  const createProfile = () => {
    const nextID = `${model.runtime}-${Date.now()}`
    setModel({ ...model, profileId: nextID, profileName: `${model.runtime === 'codex' ? 'Codex' : 'EasyAgent'} 新配置` })
    setModelNotice(null); onError('')
  }

  const openProfileEditor = (profile?: ModelProfile) => {
    if (profile) {
      setModelEditorSnapshot({ ...model }); selectProfile(profile); setModelEditorMode('edit')
    } else {
      setModelEditorSnapshot({ ...data.model }); createProfile(); setModelEditorMode('new')
    }
    setModelEditorOpen(true); setModelNotice(null); onError('')
  }

  const closeModelEditor = () => {
    if (savingModel) return
    if (modelEditorSnapshot) setModel({ ...modelEditorSnapshot })
    else if (!currentProfileSaved) setModel({ ...data.model })
    setModelEditorSnapshot(null); setModelEditorOpen(false); setModelNotice(null); onError('')
  }

  const removeProfile = async () => {
    if (!model.profileId || deletingProfile) return
    if (!currentProfileSaved) { setModel({ ...data.model }); return }
    if (data.modelProfiles.length <= 1) return
    if (!window.confirm(`删除“${model.profileName || '当前配置'}”？已有会话不会受影响。`)) return
    setDeletingProfile(true); onError('')
    try { await api.deleteModelProfile(model.profileId); await onRefresh(); setModelEditorSnapshot(null); setModelEditorOpen(false) }
    catch (reason) { onError((reason as Error).message) }
    finally { setDeletingProfile(false) }
  }

  const activateProfile = async (profile: ModelProfile) => {
    if (savingModel || profile.id === data.activeModelProfileId) { selectProfile(profile); return }
    setModel({ ...profile.settings, profileId: profile.id, profileName: profile.name })
    setSavingModel(true); setModelNotice(null); onError('')
    try { await api.saveModel({ ...profile.settings, profileId: profile.id, profileName: profile.name }); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingModel(false) }
  }

  const activateOllamaModel = async (name: string) => {
    const existing = data.modelProfiles.find((profile) => profile.settings.runtime === 'easyagent' && profile.settings.model === name)
    if (existing) { await activateProfile(existing); return }
    const next: ModelSettings = { ...data.model, profileId: `easyagent-${Date.now()}`, profileName: `Ollama · ${name}`, runtime: 'easyagent', provider: 'ollama', protocol: 'chat_completions', baseUrl: `${data.ollama.baseUrl}/v1`, model: name }
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

  const installCodex = async () => {
    if (installingCodex) return
    if (!window.confirm('将在运行 EasyAgent 的服务器当前用户目录安装官方 Codex CLI。继续吗？')) return
    setInstallingCodex(true); onError('')
    try { await api.installCodex(); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setInstallingCodex(false) }
  }

  const saveCodexConfig = async (input: CodexProviderConfig & { apiKey?: string; clearApiKey?: boolean }) => {
    if (savingCodexConfig) return
    setSavingCodexConfig(true); setModelNotice(null); onError('')
    try { setCodexConfig(await api.saveCodexConfig({ provider: input.provider, providerName: input.providerName, baseUrl: input.baseUrl, model: input.model, reasoningEffort: input.reasoningEffort, envKey: input.envKey, apiKey: input.apiKey, clearApiKey: input.clearApiKey })) }
    catch (reason) { setModelNotice({ ready: false, title: 'Codex 配置保存失败', message: (reason as Error).message }) }
    finally { setSavingCodexConfig(false) }
  }

  return { model, setModel, testingModel, savingModel, modelNotice, deletingProfile, modelEditorOpen, setModelEditorOpen, modelEditorMode, codexConfig, setCodexConfig, savingCodexConfig, installingCodex, currentProfileSaved, saveModel, testModel, selectRuntime, selectProfile, createProfile, openProfileEditor, closeModelEditor, removeProfile, activateProfile, activateOllamaModel, detectCodex, installCodex, saveCodexConfig }
}
