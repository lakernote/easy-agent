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
  { id: 'models', label: '模型', description: '连接与配置' },
  { id: 'skills', label: 'Skills', description: '按需加载能力' },
  { id: 'tools', label: '扩展', description: 'Tools 与 MCP' },
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
        <p className="settings-kicker">设置</p>
        <h1>设置</h1>
        <p>管理运行时、模型和能力。每个新会话会固定创建时选择的运行环境。</p>
      </div>
      <span className="settings-hub-status"><span className="service-dot" />本地配置</span>
    </header>
    <div className="settings-hub-layout">
      <nav className="settings-side-nav" aria-label="设置分区">
        <p className="settings-side-label">工作区</p>
        {sections.map((section, index) => <button key={section.id} className={selected === section.id ? 'active' : ''} type="button" aria-current={selected === section.id ? 'page' : undefined} onClick={() => onPage(section.id)}>
          <span className="settings-nav-index">{String(index + 1).padStart(2, '0')}</span>
          <span><strong>{section.label}</strong><small>{section.description}</small></span>
        </button>)}
        <p className="settings-side-note">共享能力会显示在所有运行时；模型和连接配置按运行时分别保存。</p>
      </nav>
      <main className="settings-hub-content">
        {selected === 'skills' && <Skills data={data} onRefresh={onRefresh} onError={onError} />}
        {selected === 'usage' && <UsagePage data={data} />}
        {(selected === 'runtime' || selected === 'models' || selected === 'tools') && <Capabilities section="settings" initialSection={selected} data={data} onRefresh={onRefresh} onError={onError} />}
      </main>
    </div>
  </section>
}
