import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import { ConfirmDialog } from './dialogs'
import type { WeixinAccount, WeixinLogin, WeixinState } from './types'
import { WeixinAccountList } from './weixin/WeixinAccountList'
import { WeixinLoginCard } from './weixin/WeixinLoginCard'

export function WeixinPage({ onError }: { onError: (value: string) => void }) {
  const [state, setState] = useState<WeixinState | null>(null)
  const [label, setLabel] = useState('')
  const [login, setLogin] = useState<WeixinLogin | null>(null)
  const [verifyCode, setVerifyCode] = useState('')
  const [busy, setBusy] = useState('')
  const [draftLabels, setDraftLabels] = useState<Record<string, string>>({})
  const [deleting, setDeleting] = useState<WeixinAccount | null>(null)

  const load = useCallback(async () => {
    const next = await api.weixin()
    setState(next)
    setDraftLabels((current) => Object.fromEntries(next.accounts.map((account) => [account.id, current[account.id] ?? account.label])))
    return next
  }, [])

  useEffect(() => { load().catch((reason) => onError((reason as Error).message)) }, [load, onError])
  useEffect(() => {
    if (!login || ['confirmed', 'expired', 'already_bound', 'failed'].includes(login.status)) return
    const timer = window.setInterval(() => {
      api.weixinLogin(login.id).then((next) => {
        setLogin(next)
        if (next.status === 'confirmed') void load()
      }).catch((reason) => onError((reason as Error).message))
    }, 1500)
    return () => window.clearInterval(timer)
  }, [login?.id, login?.status, load, onError])

  const toggleGlobal = async (enabled: boolean) => {
    setBusy('global'); onError('')
    try { setState(await api.saveWeixinSettings(enabled)) }
    catch (reason) { onError((reason as Error).message) }
    finally { setBusy('') }
  }
  const startLogin = async () => {
    if (!label.trim()) return
    setBusy('login'); onError('')
    try { setLogin(await api.startWeixinLogin(label.trim())); setVerifyCode('') }
    catch (reason) { onError((reason as Error).message) }
    finally { setBusy('') }
  }
  const verify = async () => {
    if (!login || !verifyCode.trim()) return
    setBusy('verify'); onError('')
    try { setLogin(await api.verifyWeixinLogin(login.id, verifyCode.trim())) }
    catch (reason) { onError((reason as Error).message) }
    finally { setBusy('') }
  }
  const updateAccount = async (account: WeixinAccount, enabled = account.enabled) => {
    const nextLabel = (draftLabels[account.id] || account.label).trim()
    if (!nextLabel) return
    setBusy(account.id); onError('')
    try { setState(await api.updateWeixinAccount(account.id, nextLabel, enabled)) }
    catch (reason) { onError((reason as Error).message) }
    finally { setBusy('') }
  }
  const removeAccount = async () => {
    if (!deleting) return
    setBusy(`delete-${deleting.id}`); onError('')
    try { await api.deleteWeixinAccount(deleting.id); setDeleting(null); await load() }
    catch (reason) { onError((reason as Error).message) }
    finally { setBusy('') }
  }
  const closeLogin = async () => {
    const current = login
    setLogin(null)
    if (current && !['confirmed', 'expired', 'already_bound', 'failed'].includes(current.status)) {
      try { await api.cancelWeixinLogin(current.id) } catch { /* The login may have completed between polls. */ }
    }
  }

  if (!state) return <div className="weixin-loading"><span className="spinner" />正在读取微信远程配置…</div>
  return <section className="weixin-page" aria-labelledby="weixin-title">
    <header className="weixin-head">
      <div><p className="settings-kicker">远程通道</p><h2 id="weixin-title">微信 ClawBot</h2><p>团队成员可从微信提交任务并接收最终结果。Trace、模型请求和工具过程只保留在 Web 工作台。</p></div>
      <div className="weixin-global"><div><strong>{state.enabled ? '远程已启用' : '远程已停用'}</strong><small>{state.enabled ? '正在接收已绑定成员的消息' : '不会接收或回传微信消息'}</small></div><label className="switch"><input type="checkbox" aria-label={state.enabled ? '停用微信远程' : '启用微信远程'} checked={state.enabled} disabled={busy === 'global'} onChange={(event) => void toggleGlobal(event.target.checked)} /><span /></label></div>
    </header>

    <section className="weixin-bind" aria-labelledby="weixin-bind-title">
      <div className="weixin-section-title"><div><h3 id="weixin-bind-title">绑定成员</h3><p>先填写容易辨认的备注，再由对应成员使用微信扫码。每个人需要单独绑定一次。</p></div><span>{state.accounts.length} 人</span></div>
      <div className="weixin-bind-form"><label htmlFor="weixin-label">成员备注</label><div><input id="weixin-label" value={label} maxLength={40} placeholder="例如：小王 / 运维值班" onChange={(event) => setLabel(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') void startLogin() }} /><button className="primary-button" type="button" disabled={!label.trim() || busy === 'login'} onClick={() => void startLogin()}>{busy === 'login' ? '生成中…' : '生成二维码'}</button></div></div>
      {login && <WeixinLoginCard login={login} verifyCode={verifyCode} busy={busy === 'verify'} onVerifyCode={setVerifyCode} onVerify={verify} onClose={() => void closeLogin()} />}
    </section>

    <WeixinAccountList accounts={state.accounts} channelEnabled={state.enabled} busy={busy} draftLabels={draftLabels} onDraftLabel={(id, value) => setDraftLabels((current) => ({ ...current, [id]: value }))} onUpdate={(account, enabled) => void updateAccount(account, enabled)} onRemove={setDeleting} />
    <aside className="weixin-note"><strong>微信可用命令</strong><code>/new</code><span>新会话</span><code>/status</code><span>查看状态</span><code>/stop</code><span>停止任务</span></aside>
    {deleting && <ConfirmDialog title="移除微信绑定？" description="移除后该成员不能再通过微信控制 EasyAgent，需要重新扫码才能恢复。已有 Web 会话不会删除。" subject={deleting.label} confirmLabel="移除绑定" busy={busy === `delete-${deleting.id}`} onCancel={() => setDeleting(null)} onConfirm={() => void removeAccount()} />}
  </section>
}
