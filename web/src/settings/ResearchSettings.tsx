import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import { formatDuration } from '../format'
import type { Bootstrap, ResearchProvider, ResearchProviderInput } from '../types'

type ProviderDraft = { endpoint: string; secret: string; clearSecret: boolean }
type Drafts = Record<string, ProviderDraft>
type ProviderNotice = { ready: boolean; message: string }

function draftsFromProviders(providers: ResearchProvider[]): Drafts {
  return Object.fromEntries(providers.map((provider) => [provider.id, {
    endpoint: provider.endpoint || '', secret: '', clearSecret: false,
  }]))
}

function settingsInput(providers: ResearchProvider[], drafts: Drafts): { providers: ResearchProviderInput[] } {
  return { providers: providers.map((provider) => ({ id: provider.id, ...(provider.endpointLabel ? { endpoint: drafts[provider.id]?.endpoint || '' } : {}), ...(drafts[provider.id]?.secret ? { secret: drafts[provider.id].secret } : {}), ...(drafts[provider.id]?.clearSecret ? { clearSecret: true } : {}) })) }
}

function providerStatus(provider: ResearchProvider) {
  if (provider.ready && provider.secretSource === 'saved') return '已保存'
  if (provider.ready && (provider.secretSource === 'environment' || provider.endpointSource === 'environment')) return '环境变量'
  if (provider.ready && provider.availableWithoutAuth) return '匿名可用'
  if (provider.ready) return '已配置'
  return provider.primarySearch ? '未配置' : '可选'
}

function providerHelper(provider: ResearchProvider) {
  const variables = provider.environmentVariables?.join(' / ') || ''
  if (provider.secretSource === 'saved') return '密钥已保存在本机数据库中；输入新值可替换，原值不会回显。'
  if (provider.secretSource === 'environment') return `密钥由服务环境提供${variables ? `（${variables}）` : ''}；输入新值可在页面覆盖。`
  if (provider.endpointSource === 'environment') return `地址由服务环境提供：${provider.effectiveEndpoint}`
  return variables ? `也可通过服务环境配置：${variables}` : '配置只保存在当前 EasyAgent 实例。'
}

export function ResearchSettings({ data, onRefresh, onError }: { data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const providers = data.researchSettings.providers
  const [drafts, setDrafts] = useState<Drafts>(() => draftsFromProviders(providers))
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState('')
  const [saveMessage, setSaveMessage] = useState('')
  const [notices, setNotices] = useState<Record<string, ProviderNotice>>({})

  useEffect(() => {
    setDrafts(draftsFromProviders(providers))
    setNotices({})
  }, [providers])

  const dirty = useMemo(() => providers.some((provider) => {
    const draft = drafts[provider.id]
    return Boolean(draft && (draft.endpoint !== (provider.endpoint || '') || draft.secret || draft.clearSecret))
  }), [drafts, providers])

  const update = (id: string, value: Partial<ProviderDraft>) => {
    setDrafts((current) => {
      const previous = current[id] ?? { endpoint: '', secret: '', clearSecret: false }
      return { ...current, [id]: { ...previous, ...value } }
    })
    setSaveMessage('')
    setNotices((current) => { const next = { ...current }; delete next[id]; return next })
  }

  const save = async () => {
    if (!dirty || saving) return
    setSaving(true); setSaveMessage(''); onError('')
    try {
      await api.saveResearchSettings(settingsInput(providers, drafts))
      await onRefresh()
      setSaveMessage('Research 配置已保存；下一轮 Agent 调用立即生效，无需重启服务。')
    } catch (reason) { onError((reason as Error).message) }
    finally { setSaving(false) }
  }

  const test = async (provider: ResearchProvider) => {
    if (testing) return
    setTesting(provider.id); onError('')
    setNotices((current) => ({ ...current, [provider.id]: { ready: true, message: '正在执行真实连接测试…' } }))
    try {
      const result = await api.testResearchProvider(provider.id, settingsInput(providers, drafts))
      setNotices((current) => ({ ...current, [provider.id]: { ready: true, message: `${result.detail} · ${formatDuration(result.durationMs)}` } }))
    } catch (reason) {
      setNotices((current) => ({ ...current, [provider.id]: { ready: false, message: (reason as Error).message } }))
    } finally { setTesting('') }
  }

  const primaryReady = providers.filter((provider) => provider.primarySearch && provider.ready).length
  return <section className="section-block research-settings" aria-labelledby="research-settings-title">
    <div className="section-heading research-settings-head">
      <div><p className="eyebrow">EasyAgent Runtime</p><h2 id="research-settings-title">Web Research</h2><p>模型只看到一个高层工具；这里配置候选搜索、正文读取和精确数据连接。密钥不会进入 Prompt、Tool 参数或 Trace。</p></div>
      <div className="research-health" aria-label={`${primaryReady} 个生产搜索源已就绪`}><strong>{primaryReady}</strong><span>生产搜索源</span></div>
    </div>
    <div className="research-architecture-note">
      <span><strong>模型决策</strong><small>data_type · subject · 深度 · 来源范围</small></span>
      <b aria-hidden="true">→</b>
      <span><strong>Runtime 执行</strong><small>结构化数据 · 多源搜索 · 安全抓取 · 引用</small></span>
    </div>
    <div className="research-provider-list">
      {providers.map((provider) => {
        const draft = drafts[provider.id] || { endpoint: '', secret: '', clearSecret: false }
        const notice = notices[provider.id]
        const status = providerStatus(provider)
        return <form className={`research-provider ${provider.ready ? 'ready' : ''}`} key={provider.id} onSubmit={(event) => { event.preventDefault(); void test(provider) }}>
          <header><div><span className={`service-dot ${provider.ready ? '' : 'off'}`} /><div><strong>{provider.name}</strong><small>{provider.category}</small></div></div><em>{status}</em></header>
          <p>{provider.description}</p>
          {(provider.endpointLabel || provider.secretLabel) && <div className="research-provider-fields">
            {provider.endpointLabel && <label><span>{provider.endpointLabel}</span><input type="url" value={draft.endpoint} placeholder={provider.endpointSource === 'environment' ? provider.effectiveEndpoint : provider.endpointPlaceholder} onChange={(event) => update(provider.id, { endpoint: event.target.value })} /><small>{provider.endpointSource === 'environment' && !draft.endpoint ? '当前使用环境变量地址；填写后由页面配置覆盖。' : '必须是 http/https URL，保存后下一轮立即生效。'}</small></label>}
            {provider.secretLabel && <label><span>{provider.secretLabel}{provider.requiresSecret ? '（必填）' : '（可选）'}</span><input type="password" autoComplete="off" value={draft.secret} placeholder={provider.secretAvailable ? '已配置，留空不修改' : '输入后只发送到当前服务器'} onChange={(event) => update(provider.id, { secret: event.target.value, clearSecret: false })} /><small>{providerHelper(provider)}</small></label>}
          </div>}
          <footer>
            <div>{provider.secretConfigured && <label className="research-clear-secret"><input type="checkbox" checked={draft.clearSecret} onChange={(event) => update(provider.id, { clearSecret: event.target.checked, secret: event.target.checked ? '' : draft.secret })} />清除已保存密钥</label>}</div>
            <button className="ghost-button" type="submit" disabled={Boolean(testing)}>{testing === provider.id ? '测试中…' : '测试连接'}</button>
          </footer>
          {notice && <div className={`research-provider-notice ${notice.ready ? 'ready' : 'failed'}`} role={notice.ready ? 'status' : 'alert'}>{notice.message}</div>}
        </form>
      })}
    </div>
    <div className="research-builtins">{data.researchSettings.executionLayers.map((layer) => <div key={layer.id}><strong>{layer.name}</strong><span>{layer.components}</span><small>{layer.description}</small></div>)}</div>
    <div className="research-settings-actions"><p>{saveMessage || (dirty ? '有未保存修改；可先测试，再统一保存。' : '页面保存值优先于同名环境变量。')}</p><button className="primary-button" type="button" disabled={!dirty || saving} onClick={() => void save()}>{saving ? '保存中…' : '保存 Research 配置'}</button></div>
  </section>
}
