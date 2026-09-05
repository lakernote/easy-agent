import type { WeixinAccount } from '../types'

type Props = {
  accounts: WeixinAccount[]
  channelEnabled: boolean
  busy: string
  draftLabels: Record<string, string>
  onDraftLabel: (id: string, value: string) => void
  onUpdate: (account: WeixinAccount, enabled?: boolean) => void
  onRemove: (account: WeixinAccount) => void
  onRetry: (account: WeixinAccount) => void
  onOpenSession: (id: string) => void
}

export function WeixinAccountList({ accounts, channelEnabled, busy, draftLabels, onDraftLabel, onUpdate, onRemove, onRetry, onOpenSession }: Props) {
  return <section className="weixin-list" aria-labelledby="weixin-list-title">
    <div className="weixin-section-title"><div><h3 id="weixin-list-title">成员与任务</h3><p>查看每个人的连接、当前会话和结果回传状态。</p></div><span>{accounts.length} 人</span></div>
    {!accounts.length && <div className="weixin-empty"><strong>还没有绑定成员</strong><span>生成二维码并完成扫码后，绑定会显示在这里。</span></div>}
    {accounts.map((account) => {
      const connected = account.connected && channelEnabled && account.enabled
      const draft = draftLabels[account.id] ?? account.label
      const connectionLabel = !channelEnabled ? '通道停用' : !account.enabled ? '成员停用' : connected ? '在线' : '未连接'
      const session = account.currentSession
      return <article className="weixin-account" key={account.id}>
        <header className="weixin-account-head"><span className="weixin-avatar" aria-hidden="true">{account.label.trim().slice(0, 1).toUpperCase() || 'W'}</span><div className="weixin-account-main"><div className="weixin-account-name"><input aria-label={`${account.label} 的备注`} value={draft} maxLength={40} onChange={(event) => onDraftLabel(account.id, event.target.value)} /><button type="button" disabled={busy === account.id || !draft.trim() || draft.trim() === account.label} onClick={() => onUpdate(account)}>保存备注</button></div><small>{account.userId}{account.lastSeenAt ? ` · 同步于 ${formatRelative(account.lastSeenAt)}` : ''}</small></div><span className={`weixin-state ${connected ? 'online' : ''}`}><i aria-hidden="true" />{connectionLabel}</span></header>
        <div className={`weixin-task ${session ? session.status : 'empty'}`}>
          {session ? <><div className="weixin-task-copy"><span>当前会话</span><strong>{session.title}</strong><small>{session.progress || statusLabel(session.status)} · {session.runtime === 'codex' ? 'Codex' : 'EasyAgent'}{account.lastMessageAt ? ` · 收到于 ${formatRelative(account.lastMessageAt)}` : ''}</small></div><div className="weixin-task-state"><strong>{statusLabel(session.status)}</strong><span className={`delivery ${account.deliveryStatus}`}>{deliveryLabel(account.deliveryStatus)}</span></div></> : <div className="weixin-task-empty"><strong>还没有远程任务</strong><span>成员从微信发送文字后，会话状态会显示在这里。</span></div>}
        </div>
        <footer className="weixin-account-actions"><div>{session && <button className="ghost-button" type="button" onClick={() => onOpenSession(session.id)}>打开会话</button>}{account.deliveryStatus === 'pending' && <button className="ghost-button retry" type="button" disabled={busy === `retry-${account.id}`} onClick={() => onRetry(account)}>{busy === `retry-${account.id}` ? '重试中…' : '重试回传'}</button>}</div><div><label className="weixin-member-toggle"><span>{account.enabled ? '允许远程' : '已停用'}</span><span className="switch"><input type="checkbox" aria-label={account.enabled ? `停用 ${account.label}` : `启用 ${account.label}`} checked={account.enabled} disabled={busy === account.id} onChange={(event) => onUpdate(account, event.target.checked)} /><span /></span></label><button className="weixin-remove" type="button" onClick={() => onRemove(account)}>移除绑定</button></div></footer>
      </article>
    })}
  </section>
}

function statusLabel(value: NonNullable<WeixinAccount['currentSession']>['status']) {
  return value === 'idle' ? '已完成' : value === 'queued' ? '排队中' : value === 'paused' ? '已暂停' : value === 'running' ? '运行中' : value === 'failed' ? '失败' : '已停止'
}

function deliveryLabel(value: WeixinAccount['deliveryStatus']) {
  return value === 'processing' ? '等待任务完成' : value === 'sending' ? '正在回传' : value === 'delivered' ? '结果已送达' : value === 'pending' ? '结果待重试' : '暂无回传'
}

function formatRelative(value: string) {
  const elapsed = Math.max(0, Date.now() - new Date(value).getTime())
  if (elapsed < 60_000) return '刚刚'
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`
  return new Date(value).toLocaleDateString()
}
