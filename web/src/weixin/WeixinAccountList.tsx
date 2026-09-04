import type { WeixinAccount } from '../types'

type Props = {
  accounts: WeixinAccount[]
  channelEnabled: boolean
  busy: string
  draftLabels: Record<string, string>
  onDraftLabel: (id: string, value: string) => void
  onUpdate: (account: WeixinAccount, enabled?: boolean) => void
  onRemove: (account: WeixinAccount) => void
}

export function WeixinAccountList({ accounts, channelEnabled, busy, draftLabels, onDraftLabel, onUpdate, onRemove }: Props) {
  return <section className="weixin-list" aria-labelledby="weixin-list-title">
    <div className="weixin-section-title"><div><h3 id="weixin-list-title">已绑定成员</h3><p>单人停用不会删除绑定；再次启用时不会执行停用期间积压的消息。</p></div></div>
    {!accounts.length && <div className="weixin-empty"><strong>还没有绑定成员</strong><span>生成二维码并完成扫码后，绑定会显示在这里。</span></div>}
    {accounts.map((account) => {
      const connected = account.connected && channelEnabled && account.enabled
      const draft = draftLabels[account.id] ?? account.label
      return <article className="weixin-account" key={account.id}>
        <span className={`weixin-account-dot ${connected ? 'online' : ''}`} aria-hidden="true" />
        <div className="weixin-account-main"><div className="weixin-account-name"><input aria-label={`${account.label} 的备注`} value={draft} maxLength={40} onChange={(event) => onDraftLabel(account.id, event.target.value)} /><button type="button" disabled={busy === account.id || draft.trim() === account.label} onClick={() => onUpdate(account)}>保存</button></div><small>{account.userId} · {connected ? '连接中' : account.enabled ? '等待启用通道' : '已停用'}{account.lastMessageAt ? ` · 最近消息 ${formatRelative(account.lastMessageAt)}` : ''}</small></div>
        <label className="switch" title={account.enabled ? '停用此成员' : '启用此成员'}><input type="checkbox" aria-label={account.enabled ? `停用 ${account.label}` : `启用 ${account.label}`} checked={account.enabled} disabled={busy === account.id} onChange={(event) => onUpdate(account, event.target.checked)} /><span /></label>
        <button className="weixin-remove" type="button" onClick={() => onRemove(account)}>移除</button>
      </article>
    })}
  </section>
}

function formatRelative(value: string) {
  const elapsed = Math.max(0, Date.now() - new Date(value).getTime())
  if (elapsed < 60_000) return '刚刚'
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`
  return new Date(value).toLocaleDateString()
}
