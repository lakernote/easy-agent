import { useEffect } from 'react'
import type { Bootstrap } from './types'
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
}

const sections: { id: SettingsSection; label: string; description: string }[] = [
  { id: 'runtime', label: '运行时', description: '选择执行引擎' },
  { id: 'models', label: '模型配置', description: '按 Runtime 保存' },
  { id: 'skills', label: 'Skills', description: '按需加载能力' },
  { id: 'tools', label: '工具与 MCP', description: '共享工具与连接' },
  { id: 'usage', label: '用量', description: '调用统计' },
]

function activeSection(page: Page): SettingsSection {
  return page === 'models' || page === 'skills' || page === 'tools' || page === 'usage' ? page : 'runtime'
}

export function SettingsShell({ page, data, onPage, onRefresh, onError }: SettingsShellProps) {
  const selected = activeSection(page)
  useEffect(() => {
    document.querySelector<HTMLElement>('.settings-canvas')?.scrollTo({ top: 0, behavior: 'auto' })
  }, [page])

  return <section className="settings-hub">
    <header className="settings-hub-header">
      <div>
        <p className="settings-kicker">配置中心</p>
        <h1>设置</h1>
        <p>选择运行时，并管理它可用的模型、Skills、工具与用量。新会话会固定创建时的运行环境。</p>
      </div>
      <div className="settings-hub-context">
        <span className="service-dot" />
        <div><small>当前默认运行时</small><strong>{data.model.runtime === 'codex' ? 'Codex Runtime' : 'EasyAgent Runtime'}</strong></div>
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
      </main>
    </div>
  </section>
}
