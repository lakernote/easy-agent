import { useEffect, useRef } from 'react'
import QRCode from 'qrcode'
import type { WeixinLogin } from '../types'

type Props = {
  login: WeixinLogin
  verifyCode: string
  busy: boolean
  onVerifyCode: (value: string) => void
  onVerify: () => void
  onClose: () => void
}

export function WeixinLoginCard({ login, verifyCode, busy, onVerifyCode, onVerify, onClose }: Props) {
  const canvas = useRef<HTMLCanvasElement>(null)
  useEffect(() => {
    if (canvas.current && login.qrContent) void QRCode.toCanvas(canvas.current, login.qrContent, { width: 216, margin: 1, color: { dark: '#173d2a', light: '#ffffff' }, errorCorrectionLevel: 'M' })
  }, [login.qrContent])
  const finished = ['confirmed', 'expired', 'already_bound', 'failed'].includes(login.status)

  return <div className={`weixin-login ${finished ? 'finished' : ''}`} role="status" aria-live="polite">
    <div className="weixin-qr">{login.qrContent && !finished ? <canvas ref={canvas} aria-label="微信绑定二维码" /> : <span>{login.status === 'confirmed' ? '绑定成功' : '扫码结束'}</span>}</div>
    <div className="weixin-login-copy">
      <p className="settings-kicker">{login.label}</p>
      <h4>{login.message}</h4>
      <p>{finished ? (login.status === 'confirmed' ? '成员已加入绑定列表。开启远程通道后即可发送任务。' : '可以关闭此卡片后重新生成二维码。') : '打开手机微信扫描左侧二维码，并按手机提示确认。二维码只用于本次绑定。'}</p>
      {login.status === 'need_verifycode' && <div className="weixin-verify"><label htmlFor="weixin-code">手机显示的数字</label><div><input id="weixin-code" inputMode="numeric" autoComplete="one-time-code" value={verifyCode} onChange={(event) => onVerifyCode(event.target.value.replace(/\D/g, '').slice(0, 8))} /><button className="primary-button" disabled={busy || !verifyCode} onClick={onVerify}>{busy ? '验证中…' : '验证'}</button></div></div>}
      <button className="ghost-button" type="button" onClick={onClose}>{finished ? '完成' : '取消扫码'}</button>
    </div>
  </div>
}
