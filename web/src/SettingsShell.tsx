import { useEffect, useState } from 'react'
import type { Bootstrap } from './types'
import { api } from './api'
import type { Page } from './sessionState'
import { Capabilities, type SettingsSection } from './CapabilitiesPage'
import { Skills } from './Skills'
import { UsagePage } from './UsagePage'
import { WeixinPage } from './WeixinPage'

type SettingsShellProps = {
  page: Page
  data: Bootstrap
  onPage: (page: Page) => void
  onRefresh: () => Promise<Bootstrap>
  onError: (value: string) => void
  onLogout: () => Promise<void>
  onOpenSession: (id: string) => void
}

const sectionGroups: { label: string; sections: { id: SettingsSection; label: string }[] }[] = [
  { label: '运行与模型', sections: [
    { id: 'runtime', label: '运行与模型' },
    { id: 'tasks', label: '任务设置' },
  ] },
  { label: '能力扩展', sections: [
    { id: 'skills', label: 'Skills' },
    { id: 'tools', label: '工具与 MCP' },
  ] },
  { label: '渠道与账户', sections: [
    { id: 'weixin', label: '微信远程' },
    { id: 'usage', label: '用量' },
    { id: 'security', label: '账户安全' },
  ] },
]

function activeSection(page: Page): SettingsSection {
  return page === 'tasks' || page === 'skills' || page === 'tools' || page === 'usage' || page === 'weixin' || page === 'security' ? page : 'runtime'
}

export function SettingsShell({ page, data, onPage, onRefresh, onError, onLogout, onOpenSession }: SettingsShellProps) {
  const selected = activeSection(page)
  const activeProfile = data.modelProfiles.find((profile) => profile.id === data.activeModelProfileId)
  const pageDescription: Record<SettingsSection, string> = {
    runtime: '选择执行引擎并管理模型配置',
    models: '选择执行引擎并管理模型配置',
    tasks: '调整并发、超时与恢复',
    skills: '管理按需加载的 Skills',
    tools: '管理内置工具与 MCP',
    usage: '查看模型与任务用量',
    weixin: '管理微信绑定与远程任务',
    security: '修改登录密码',
  }
  const selectedLabel = sectionGroups.flatMap((group) => group.sections).find((section) => section.id === selected)?.label || '设置'
  const [showPassword, setShowPassword] = useState(false)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [accountMessage, setAccountMessage] = useState('')
  const [accountError, setAccountError] = useState('')
  const [savingPassword, setSavingPassword] = useState(false)
  useEffect(() => {
    document.querySelector<HTMLElement>('.settings-hub-content')?.scrollTo({ top: 0, behavior: 'auto' })
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
        <h1>{selectedLabel}</h1>
        <p>{pageDescription[selected]}</p>
      </div>
      <div className="settings-hub-context">
        <span className="service-dot" />
        <div><small>新会话默认</small><strong>{data.model.runtime === 'codex' ? 'Codex · 一套配置' : `EasyAgent · ${activeProfile?.name || '未命名配置'}`}</strong></div>
        <button className="account-logout" type="button" onClick={() => void onLogout()}>退出</button>
      </div>
    </header>
    <div className="settings-hub-layout">
      <nav className="settings-side-nav" aria-label="设置分区">
        <div className="settings-nav-groups">
          {sectionGroups.map((group) => <section className="settings-nav-group" key={group.label} aria-label={group.label}>
            <p className="settings-nav-group-label">{group.label}</p>
            {group.sections.map((section) => <button key={section.id} className={selected === section.id ? 'active' : ''} type="button" aria-current={selected === section.id ? 'page' : undefined} onClick={() => onPage(section.id)}>
              <span className="settings-nav-marker" aria-hidden="true" />
              <strong>{section.label}</strong>
            </button>)}
          </section>)}
        </div>
      </nav>
      <main className="settings-hub-content">
        {selected === 'skills' && <Skills data={data} onRefresh={onRefresh} onError={onError} />}
        {selected === 'usage' && <UsagePage data={data} />}
        {selected === 'weixin' && <WeixinPage onError={onError} onOpenSession={onOpenSession} />}
        {(selected === 'runtime' || selected === 'tasks' || selected === 'models' || selected === 'tools') && <Capabilities section="settings" initialSection={selected} data={data} onRefresh={onRefresh} onError={onError} onSettingsSectionChange={onPage} />}
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
