import { FormEvent, useState } from 'react'
import { api } from './api'
import { Logo } from './ui'

type LoginPageProps = { onLogin: () => Promise<void>; initialError?: string }

export function LoginPage({ onLogin, initialError = '' }: LoginPageProps) {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(initialError)
  const [submitting, setSubmitting] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (submitting) return
    setError('')
    setSubmitting(true)
    try {
      await api.login(username, password)
      await onLogin()
    } catch (reason) {
      setError((reason as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return <main className="login-page">
    <section className="login-panel" aria-labelledby="login-title">
      <div className="login-brand"><span className="login-logo"><Logo /></span><span><strong>EasyAgent</strong><small>SELF-HOSTED AGENT WORKBENCH</small></span></div>
      <div className="login-copy"><p className="login-kicker">团队任务工作台</p><h1 id="login-title">登录 EasyAgent</h1><p>在自己的服务器运行研发、测试和运维任务，并保留完整执行记录。</p></div>
      <form onSubmit={submit} className="login-form">
        <label>用户名<input autoFocus name="username" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} /></label>
        <label>密码<input name="password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} /></label>
        {error && <p className="login-error" role="alert">{error}</p>}
        <button className="login-submit" type="submit" disabled={submitting || !username.trim() || !password}>{submitting ? '正在登录…' : '登录'}</button>
      </form>
      <p className="login-hint">首次启动默认账号：<code>admin</code> / <code>admin</code>。登录后可在设置中修改密码。</p>
    </section>
  </main>
}
