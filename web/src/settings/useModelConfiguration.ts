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

  useEffect(() => setModel({ ...data.model }), [data.model])
  useEffect(() => setCodexConfig({ ...data.codexConfig }), [data.codexConfig])

  const saveModel = async (clearAPIKey = false) => {
    if (savingModel) return
    setSavingModel(true); setModelNotice(null); onError('')
    try { await api.saveModel({ ...model, clearApiKey: clearAPIKey }); await onRefresh(); setModelEditorSnapshot(null); setModelEditorOpen(false); onError('') }
    catch (reason) { onError((reason as Error).message) }
    finally { setSavingModel(false) }
  }

  const enableRuntime = async () => {
    if (savingModel || !model.profileId) return
    setSavingModel(true); setModelNotice(null); onError('')
    try {
      if (!currentProfileSaved) await api.saveModel(model)
      await api.activateModelProfile(model.profileId)
      await onRefresh()
    } catch (reason) { onError((reason as Error).message) }
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
        provider: 'codex',
        protocol: 'app_server',
        baseUrl: '',
        model: '',
        apiKeyEnv: '',
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
      apiKeyEnv: saved?.apiKeyEnv || '',
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
    if (profile) {
      selectProfile(profile); setModelEditorMode('edit')
    } else {
      createProfile(runtime); setModelEditorMode('new')
    }
    setModelEditorOpen(true); setModelNotice(null); onError('')
  }

  const closeModelEditor = () => {
    if (savingModel) return
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

  const saveCodexConfig = async (input: CodexProviderConfig & { apiKey?: string; clearApiKey?: boolean }) => {
    if (savingCodexConfig) return
    setSavingCodexConfig(true); setModelNotice(null); onError('')
    try { setCodexConfig(await api.saveCodexConfig({ provider: input.provider, providerName: input.providerName, baseUrl: input.baseUrl, model: input.model, reasoningEffort: input.reasoningEffort, envKey: input.envKey, apiKey: input.apiKey, clearApiKey: input.clearApiKey })) }
    catch (reason) { setModelNotice({ ready: false, title: 'Codex 配置保存失败', message: (reason as Error).message }) }
    finally { setSavingCodexConfig(false) }
  }

  return { model, setModel, testingModel, savingModel, modelNotice, deletingProfile, modelEditorOpen, setModelEditorOpen, modelEditorMode, codexConfig, setCodexConfig, savingCodexConfig, currentProfileSaved, saveModel, enableRuntime, testModel, selectRuntime, selectProfile, createProfile, openProfileEditor, closeModelEditor, removeProfile, activateProfile, activateOllamaModel, detectCodex, saveCodexConfig }
}
