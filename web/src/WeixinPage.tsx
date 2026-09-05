import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import { ConfirmDialog } from './dialogs'
import type { WeixinAccount, WeixinLogin, WeixinState } from './types'
import { WeixinAccountList } from './weixin/WeixinAccountList'
import { WeixinLoginCard } from './weixin/WeixinLoginCard'

type Props = {
  onError: (value: string) => void
  onOpenSession: (id: string) => void
}

export function WeixinPage({ onError, onOpenSession }: Props) {
  const [state, setState] = useState<WeixinState | null>(null)
  const [label, setLabel] = useState('')
  const [login, setLogin] = useState<WeixinLogin | null>(null)
  const [verifyCode, setVerifyCode] = useState('')
  const [busy, setBusy] = useState('')
  const [showBind, setShowBind] = useState(false)
  const [notice, setNotice] = useState('')
  const [refreshedAt, setRefreshedAt] = useState<Date | null>(null)
  const [draftLabels, setDraftLabels] = useState<Record<string, string>>({})
  const [deleting, setDeleting] = useState<WeixinAccount | null>(null)

  const load = useCallback(async () => {
    const next = await api.weixin()
    setState(next)
    setRefreshedAt(new Date())
    setDraftLabels((current) => Object.fromEntries(next.accounts.map((account) => [account.id, current[account.id] ?? account.label])))
    return next
  }, [])

  useEffect(() => { load().catch((reason) => onError((reason as Error).message)) }, [load, onError])
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!document.hidden) void load().catch(() => undefined)
    }, 5000)
    return () => window.clearInterval(timer)
  }, [load])
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
    setBusy('global'); onError(''); setNotice('')
    try { setState(await api.saveWeixinSettings(enabled)); setNotice(enabled ? '微信远程已启用，正在恢复成员连接。' : '微信远程已停用，不会处理停用期间的消息。') }
    catch (reason) { onError((reason as Error).message) }
    finally { setBusy('') }
  }
  const startLogin = async () => {
    if (!label.trim()) return
    setBusy('login'); onError(''); setNotice('')
    try { setLogin(await api.startWeixinLogin(label.trim())); setVerifyCode(''); setShowBind(true) }
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
    setBusy(account.id); onError(''); setNotice('')
    try { setState(await api.updateWeixinAccount(account.id, nextLabel, enabled)); setNotice(`${nextLabel} 已${enabled ? '启用' : '停用'}。`) }
    catch (reason) { onError((reason as Error).message) }
    finally { setBusy('') }
  }
  const removeAccount = async () => {
    if (!deleting) return
    setBusy(`delete-${deleting.id}`); onError(''); setNotice('')
    try { const removed = deleting.label; await api.deleteWeixinAccount(deleting.id); setDeleting(null); await load(); setNotice(`${removed} 的微信绑定已移除。`) }
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

  const retryDelivery = async (account: WeixinAccount) => {
    setBusy(`retry-${account.id}`); onError(''); setNotice('')
    try { setState(await api.retryWeixinDelivery(account.id)); setNotice(`正在重新向 ${account.label} 回传结果。`) }
    catch (reason) { onError((reason as Error).message) }
    finally { setBusy('') }
  }

  if (!state) return <div className="weixin-loading"><span className="spinner" />正在读取微信远程配置…</div>
  const onlineCount = state.accounts.filter((account) => state.enabled && account.enabled && account.connected).length
  const activeCount = state.accounts.filter((account) => account.deliveryStatus === 'processing' || account.deliveryStatus === 'sending').length
  return <section className="weixin-page" aria-labelledby="weixin-title">
    <header className="weixin-head">
      <div><p className="settings-kicker">远程工作台</p><h2 id="weixin-title">微信 ClawBot</h2><p>在微信提交任务、查询进度并接收最终结果；完整过程与 Trace 仍留在 Web 工作台。</p></div>
      <div className={`weixin-global ${state.enabled ? 'enabled' : ''}`}><span className="weixin-channel-mark" aria-hidden="true" /><div><strong>{state.enabled ? '通道运行中' : '通道已停用'}</strong><small>{state.enabled ? `${onlineCount}/${state.accounts.length} 个成员在线` : '不会接收或回传消息'}</small></div><label className="switch"><input type="checkbox" aria-label={state.enabled ? '停用微信远程' : '启用微信远程'} checked={state.enabled} disabled={busy === 'global'} onChange={(event) => void toggleGlobal(event.target.checked)} /><span /></label></div>
    </header>

    <div className="weixin-overview" aria-label="微信远程概况"><div><span>已绑定</span><strong>{state.accounts.length}</strong><small>团队成员</small></div><div><span>在线</span><strong>{onlineCount}</strong><small>{state.enabled ? '正在监听消息' : '通道已停用'}</small></div><div><span>处理中</span><strong>{activeCount}</strong><small>运行或回传中</small></div><button className="weixin-refresh" type="button" disabled={busy === 'refresh'} onClick={() => { setBusy('refresh'); void load().finally(() => setBusy('')) }}><span aria-hidden="true">↻</span>{busy === 'refresh' ? '刷新中' : '刷新状态'}<small>{refreshedAt ? `更新于 ${refreshedAt.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}` : ''}</small></button></div>

    {notice && <p className="weixin-notice" role="status">{notice}</p>}

    <WeixinAccountList accounts={state.accounts} channelEnabled={state.enabled} busy={busy} draftLabels={draftLabels} onDraftLabel={(id, value) => setDraftLabels((current) => ({ ...current, [id]: value }))} onUpdate={(account, enabled) => void updateAccount(account, enabled)} onRemove={setDeleting} onRetry={retryDelivery} onOpenSession={onOpenSession} />

    <section className={`weixin-bind ${showBind || login || !state.accounts.length ? 'open' : ''}`} aria-labelledby="weixin-bind-title">
      <div className="weixin-section-title"><div><h3 id="weixin-bind-title">绑定新成员</h3><p>每个人单独扫码绑定，备注只在管理页面显示。</p></div>{state.accounts.length > 0 && <button className="ghost-button" type="button" aria-expanded={showBind || Boolean(login)} onClick={() => setShowBind((value) => !value)}>{showBind || login ? '收起' : '开始绑定'}</button>}</div>
      {(showBind || login || !state.accounts.length) && <><div className="weixin-bind-form"><label htmlFor="weixin-label">成员备注</label><div><input id="weixin-label" value={label} maxLength={40} placeholder="例如：小王 / 运维值班" onChange={(event) => setLabel(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') void startLogin() }} /><button className="primary-button" type="button" disabled={!label.trim() || busy === 'login'} onClick={() => void startLogin()}>{busy === 'login' ? '生成中…' : '生成二维码'}</button></div></div>{login && <WeixinLoginCard login={login} verifyCode={verifyCode} busy={busy === 'verify'} onVerifyCode={setVerifyCode} onVerify={verify} onClose={() => void closeLogin()} />}</>}
    </section>

    <aside className="weixin-commands"><div><strong>微信快捷指令</strong><span>直接发送中文即可，也兼容斜杠命令。</span></div><div><code>新会话</code><span>开始独立任务</span><small>/new</small></div><div><code>状态</code><span>查看当前进度</span><small>/status</small></div><div><code>停止</code><span>中断当前任务</span><small>/stop</small></div></aside>
    {deleting && <ConfirmDialog title="移除微信绑定？" description="移除后该成员不能再通过微信控制 EasyAgent，需要重新扫码才能恢复。已有 Web 会话不会删除。" subject={deleting.label} confirmLabel="移除绑定" busy={busy === `delete-${deleting.id}`} onCancel={() => setDeleting(null)} onConfirm={() => void removeAccount()} />}
  </section>
}
