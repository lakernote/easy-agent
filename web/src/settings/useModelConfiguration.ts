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
  const [modelEditorBaseline, setModelEditorBaseline] = useState<ModelSettings | null>(null)
  const [codexConfig, setCodexConfig] = useState<CodexProviderConfig>({ ...data.codexConfig })
  const [codexConfigBaseline, setCodexConfigBaseline] = useState<CodexProviderConfig | null>(null)
  const [savingCodexConfig, setSavingCodexConfig] = useState(false)
  const [deletingCodexProvider, setDeletingCodexProvider] = useState(false)

  useEffect(() => setModel({ ...data.model }), [data.model])
  useEffect(() => setCodexConfig({ ...data.codexConfig }), [data.codexConfig])

  const saveModel = async (clearAPIKey = false) => {
    if (savingModel) return
    setSavingModel(true); setModelNotice(null); onError('')
    try {
      await api.saveModel({ ...model, clearApiKey: clearAPIKey })
      await onRefresh()
      setCodexConfig(codexConfigBaseline ? { ...codexConfigBaseline } : { ...data.codexConfig })
      setModelEditorSnapshot(null); setModelEditorBaseline(null); setCodexConfigBaseline(null); setModelEditorOpen(false); onError('')
    }
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

  const draftProfile = (runtime: ModelSettings['runtime']): ModelSettings => {
    const saved = data.modelProfiles.find((profile) => profile.settings.runtime === runtime)?.settings
    const seed = saved || data.model
    const common = {
      ...seed,
      profileId: `${runtime}-${Date.now()}`,
      profileName: `${runtime === 'codex' ? 'Codex' : 'EasyAgent'} 新配置`,
      runtime,
      apiKey: '',
      secretConfigured: false,
    }
    if (runtime === 'codex') {
      return {
      ...common,
      provider: '',
        protocol: 'app_server',
        baseUrl: '',
        model: '',
        thinking: '',
        contextWindowTokens: 0,
        compressionThresholdPercent: 0,
        turnTimeoutSeconds: data.modelRules.defaultCodexTurnTimeoutSeconds,
      }
    }
    const ollamaBase = data.ollama.baseUrl.replace(/\/+$/, '')
    return {
      ...common,
      provider: saved?.provider || (data.ollama.running ? 'ollama' : 'openai'),
      protocol: saved?.protocol && saved.protocol !== 'app_server' ? saved.protocol : 'chat_completions',
      baseUrl: saved?.baseUrl || (data.ollama.running ? `${ollamaBase}/v1` : ''),
      model: saved?.model || '',
      thinking: saved?.thinking || '',
    }
  }

  const selectRuntime = (runtime: ModelSettings['runtime']) => {
    const existing = data.modelProfiles.find((profile) => profile.settings.runtime === runtime)
    setModel(existing ? { ...existing.settings, profileId: existing.id, profileName: existing.name } : draftProfile(runtime))
    setModelNotice(null); onError('')
  }

  const currentProfileSaved = data.modelProfiles.some((profile) => profile.id === model.profileId)
  const selectProfile = (profile: ModelProfile) => {
    setModel({ ...profile.settings, profileId: profile.id, profileName: profile.name })
    setModelNotice(null); onError('')
  }

  const createProfile = (runtime: ModelSettings['runtime'] = model.runtime) => {
    setModel(draftProfile(runtime))
    setModelNotice(null); onError('')
  }

  const openProfileEditor = (profile?: ModelProfile, runtime: ModelSettings['runtime'] = model.runtime) => {
    setModelEditorSnapshot({ ...model })
    const next = profile
      ? { ...profile.settings, profileId: profile.id, profileName: profile.name }
      : draftProfile(runtime)
    setModel(next)
    setModelEditorBaseline(next)
    setCodexConfigBaseline({ ...codexConfig })
    setModelEditorMode(profile ? 'edit' : 'new')
    setModelEditorOpen(true); setModelNotice(null); onError('')
  }

  const openCodexProviderEditor = (providerID = '') => {
    const profile = data.modelProfiles.find((candidate) => candidate.settings.runtime === 'codex')
    const next = profile
      ? { ...profile.settings, profileId: profile.id, profileName: profile.name, provider: providerID === '__new__' ? '' : providerID }
      : { ...draftProfile('codex'), provider: providerID === '__new__' ? '' : providerID }
    const provider = data.codexConfig.providers?.find((candidate) => candidate.id === providerID)
    const nextConfig = providerID === '__new__'
      ? { ...data.codexConfig, provider: '', providerName: '', baseUrl: '', model: '', envKey: '', apiKeyConfigured: false, configured: false }
      : provider
      ? { ...data.codexConfig, provider: provider.id, providerName: provider.name || provider.id, baseUrl: provider.baseUrl, model: provider.model || data.codexConfig.model, envKey: provider.envKey, apiKeyConfigured: provider.apiKeyConfigured, configured: Boolean(provider.baseUrl && (provider.model || data.codexConfig.model) && provider.apiKeyConfigured) }
      : { ...data.codexConfig }
    setModelEditorSnapshot({ ...model })
    setModel(next)
    setModelEditorBaseline(next)
    setCodexConfig(nextConfig)
    setCodexConfigBaseline(nextConfig)
    setModelEditorMode(providerID === '__new__' ? 'new' : 'edit')
    setModelEditorOpen(true); setModelNotice(null); onError('')
  }

  const activateCodexProvider = async (providerID: string) => {
    const profile = data.modelProfiles.find((candidate) => candidate.settings.runtime === 'codex')
    if (!profile || savingModel) return
    const next = { ...profile.settings, profileId: profile.id, profileName: profile.name, provider: providerID }
    setModel(next); setSavingModel(true); setModelNotice(null); onError('')
    try { await api.saveModel(next); await api.activateModelProfile(profile.id); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingModel(false) }
  }

  const closeModelEditor = () => {
    if (savingModel || savingCodexConfig) return
    const savedSnapshot = modelEditorSnapshot && data.modelProfiles.some((profile) => profile.id === modelEditorSnapshot.profileId)
      ? modelEditorSnapshot
      : null
    const savedRuntimeProfile = data.modelProfiles.find((profile) => profile.settings.runtime === model.runtime)
    if (savedSnapshot) setModel({ ...savedSnapshot })
    else if (!currentProfileSaved) {
      setModel(savedRuntimeProfile
        ? { ...savedRuntimeProfile.settings, profileId: savedRuntimeProfile.id, profileName: savedRuntimeProfile.name }
        : { ...data.model })
    }
    setCodexConfig(codexConfigBaseline ? { ...codexConfigBaseline } : { ...data.codexConfig })
    setModelEditorSnapshot(null); setModelEditorBaseline(null); setCodexConfigBaseline(null); setModelEditorOpen(false); setModelNotice(null); onError('')
  }

  const modelEditorDirty = Boolean(modelEditorOpen && (
    (modelEditorBaseline && JSON.stringify(model) !== JSON.stringify(modelEditorBaseline))
    || (codexConfigBaseline && JSON.stringify(codexConfig) !== JSON.stringify(codexConfigBaseline))
  ))

  const removeProfile = async () => {
    if (!model.profileId || deletingProfile) return
    if (!currentProfileSaved) { setModel({ ...data.model }); return }
    if (data.modelProfiles.length <= 1) return
    setDeletingProfile(true); onError('')
    try { await api.deleteModelProfile(model.profileId); await onRefresh(); setModelEditorSnapshot(null); setModelEditorBaseline(null); setCodexConfigBaseline(null); setModelEditorOpen(false) }
    catch (reason) { onError((reason as Error).message) }
    finally { setDeletingProfile(false) }
  }

  const activateProfile = async (profile: ModelProfile) => {
    if (savingModel || profile.id === data.activeModelProfileId) { selectProfile(profile); return }
    setModel({ ...profile.settings, profileId: profile.id, profileName: profile.name })
    setSavingModel(true); setModelNotice(null); onError('')
    try { await api.activateModelProfile(profile.id); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingModel(false) }
  }

  const activateOllamaModel = async (name: string) => {
    const existing = data.modelProfiles.find((profile) => profile.settings.runtime === 'easyagent' && profile.settings.model === name)
    if (existing) { await activateProfile(existing); return }
    const next: ModelSettings = { ...data.model, profileId: `easyagent-${Date.now()}`, profileName: `Ollama · ${name}`, runtime: 'easyagent', provider: 'ollama', protocol: 'chat_completions', baseUrl: `${data.ollama.baseUrl}/v1`, model: name }
    setModel(next); setSavingModel(true); setModelNotice(null); onError('')
    try { await api.saveModel(next); await api.activateModelProfile(next.profileId!); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingModel(false) }
  }

  const detectCodex = async () => {
    onError('')
    try { await api.codex(); await onRefresh() }
    catch (reason) { onError((reason as Error).message) }
  }

  const saveCodexConfig = async (input: CodexProviderConfig) => {
    if (savingCodexConfig) return
    setSavingCodexConfig(true); setModelNotice(null); onError('')
    try {
      const saved = await api.saveCodexConfig({ provider: input.provider, providerName: input.providerName, baseUrl: input.baseUrl, model: input.model, reasoningEffort: input.reasoningEffort, envKey: input.envKey })
      setCodexConfig(saved); setCodexConfigBaseline(saved)
    }
    catch (reason) { setModelNotice({ ready: false, title: 'Codex 配置保存失败', message: (reason as Error).message }) }
    finally { setSavingCodexConfig(false) }
  }

  const removeCodexProvider = async () => {
    const providerID = codexConfig.provider.trim()
    if (!providerID || deletingCodexProvider) return false
    setDeletingCodexProvider(true); setModelNotice(null); onError('')
    try {
      const saved = await api.deleteCodexProvider(providerID)
      setCodexConfig(saved); setCodexConfigBaseline(null)
      setModelEditorSnapshot(null); setModelEditorBaseline(null); setModelEditorOpen(false)
      await onRefresh()
      return true
    }
    catch (reason) { onError((reason as Error).message); return false }
    finally { setDeletingCodexProvider(false) }
  }

  return { model, setModel, testingModel, savingModel, modelNotice, deletingProfile, deletingCodexProvider, modelEditorOpen, setModelEditorOpen, modelEditorMode, modelEditorDirty, codexConfig, setCodexConfig, savingCodexConfig, currentProfileSaved, saveModel, testModel, selectRuntime, selectProfile, createProfile, openProfileEditor, openCodexProviderEditor, activateCodexProvider, closeModelEditor, removeProfile, removeCodexProvider, activateProfile, activateOllamaModel, detectCodex, saveCodexConfig }
}
