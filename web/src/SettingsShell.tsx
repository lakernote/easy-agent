import { useEffect, useState } from 'react'
import type { Bootstrap } from './types'
import { api } from './api'
import type { Page } from './sessionState'
import { Capabilities, type SettingsSection } from './CapabilitiesPage'
import { Skills } from './Skills'
import { UsagePage } from './UsagePage'

type SettingsShellProps = {
  page: Page
  data: Bootstrap
  onPage: (page: Page) => void
  onRefresh: () => Promise<Bootstrap>
  onError: (value: string) => void
  onLogout: () => Promise<void>
}

const sections: { id: SettingsSection; label: string; description: string }[] = [
  { id: 'runtime', label: '运行时', description: '选择执行引擎' },
  { id: 'models', label: '模型配置', description: '按 Runtime 保存' },
  { id: 'skills', label: 'Skills', description: '按需加载能力' },
  { id: 'tools', label: '工具与 MCP', description: '共享工具与连接' },
  { id: 'usage', label: '用量', description: '调用统计' },
  { id: 'security', label: '账户安全', description: '修改登录密码' },
]

function activeSection(page: Page): SettingsSection {
  return page === 'models' || page === 'skills' || page === 'tools' || page === 'usage' || page === 'security' ? page : 'runtime'
}

export function SettingsShell({ page, data, onPage, onRefresh, onError, onLogout }: SettingsShellProps) {
  const selected = activeSection(page)
  const [showPassword, setShowPassword] = useState(false)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [accountMessage, setAccountMessage] = useState('')
  const [accountError, setAccountError] = useState('')
  const [savingPassword, setSavingPassword] = useState(false)
  useEffect(() => {
    document.querySelector<HTMLElement>('.settings-canvas')?.scrollTo({ top: 0, behavior: 'auto' })
  }, [page])
  useEffect(() => {
    if (selected !== 'security') {
      setShowPassword(false)
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setAccountError('')
      setAccountMessage('')
    }
  }, [selected])

  const changePassword = async () => {
    setAccountMessage('')
    setAccountError('')
    if (newPassword.length < 8) { setAccountError('新密码至少需要 8 个字符'); return }
    if (newPassword !== confirmPassword) { setAccountError('两次输入的新密码不一致'); return }
    setSavingPassword(true)
    try {
      await api.changePassword(currentPassword, newPassword)
      setCurrentPassword(''); setNewPassword(''); setConfirmPassword(''); setShowPassword(false)
      setAccountMessage('密码已修改，请重新登录')
      await onLogout()
    } catch (reason) { setAccountError((reason as Error).message) } finally { setSavingPassword(false) }
  }

  return <section className="settings-hub">
    <header className="settings-hub-header">
      <div>
        <p className="settings-kicker">配置中心</p>
        <h1>设置</h1>
        <p>{selected === 'security' ? '管理 EasyAgent 工作台的登录凭据；密码修改后当前会话会立即退出。' : '选择运行时，并管理它可用的模型、Skills、工具与用量。新会话会固定创建时的运行环境。'}</p>
      </div>
      <div className="settings-hub-context">
        <span className="service-dot" />
        <div><small>当前默认运行时</small><strong>{data.model.runtime === 'codex' ? 'Codex Runtime' : 'EasyAgent Runtime'}</strong></div>
        <button className="account-logout" type="button" onClick={() => void onLogout()}>退出</button>
      </div>
    </header>
    <div className="settings-hub-layout">
      <nav className="settings-side-nav" aria-label="设置分区">
        <p className="settings-side-label">配置中心</p>
        {sections.map((section, index) => <button key={section.id} className={selected === section.id ? 'active' : ''} type="button" aria-current={selected === section.id ? 'page' : undefined} onClick={() => onPage(section.id)}>
          <span className="settings-nav-index">{String(index + 1).padStart(2, '0')}</span>
          <span><strong>{section.label}</strong><small>{section.description}</small></span>
        </button>)}
        <p className="settings-side-note">Skills 是共享内容；模型配置和 MCP 连接按 Runtime 隔离。</p>
      </nav>
      <main className="settings-hub-content">
        {selected === 'skills' && <Skills data={data} onRefresh={onRefresh} onError={onError} />}
        {selected === 'usage' && <UsagePage data={data} />}
        {(selected === 'runtime' || selected === 'models' || selected === 'tools') && <Capabilities section="settings" initialSection={selected} data={data} onRefresh={onRefresh} onError={onError} />}
        {selected === 'security' && <section className="account-panel account-security-page" aria-labelledby="account-title">
          <div><p className="settings-kicker">账户安全</p><h2 id="account-title">管理员账号</h2><p>当前登录用户：<code>admin</code>。服务重启、12 小时后或修改密码后需要重新登录。</p></div>
          <button className="ghost-button" type="button" onClick={() => { setShowPassword(!showPassword); setAccountError(''); setAccountMessage('') }}>{showPassword ? '收起改密' : '修改密码'}</button>
          {showPassword && <div className="password-form"><label>当前密码<input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} /></label><label>新密码<input type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /><small>至少 8 个字符</small></label><label>确认新密码<input type="password" autoComplete="new-password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} /></label><button className="primary-button" type="button" disabled={savingPassword || !currentPassword || newPassword.length < 8 || newPassword !== confirmPassword} onClick={() => void changePassword()}>{savingPassword ? '保存中…' : '保存新密码'}</button></div>}
          {accountMessage && <p className="account-success" role="status">{accountMessage}</p>}
          {accountError && <p className="account-error" role="alert">{accountError}</p>}
        </section>}
      </main>
    </div>
  </section>
}
