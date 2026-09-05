import { useEffect, useRef, useState } from 'react'
import { FolderIcon, TrashIcon } from './ui'
import type { Bootstrap, Session } from './types'
export function ConfirmDialog({ title, description, subject, confirmLabel, busy, onCancel, onConfirm }: { title: string; description: string; subject?: string; confirmLabel: string; busy: boolean; onCancel: () => void; onConfirm: () => void }) {
  const cancelRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    cancelRef.current?.focus()
    const closeWithEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) onCancel()
    }
    document.addEventListener('keydown', closeWithEscape)
    return () => document.removeEventListener('keydown', closeWithEscape)
  }, [busy, onCancel])

  return <div className="confirm-backdrop" onMouseDown={() => !busy && onCancel()}>
    <div className="confirm-dialog" role="alertdialog" aria-modal="true" aria-label={title} onMouseDown={(event) => event.stopPropagation()}>
      <div className="confirm-symbol"><TrashIcon /></div>
      <div className="confirm-copy"><p className="eyebrow">确认删除</p><h2>{title}</h2><p>{description}</p>{subject && <div className="confirm-subject" title={subject}>{subject}</div>}</div>
      <div className="confirm-actions"><button ref={cancelRef} className="ghost-button" disabled={busy} onClick={onCancel}>取消</button><button className="danger-button" disabled={busy} onClick={onConfirm}>{busy ? '删除中…' : confirmLabel}</button></div>
    </div>
  </div>
}

export function RenameSessionDialog({ session, projects, busy, onCancel, onSave, onDelete }: { session: Session; projects: Bootstrap['projects']; busy: boolean; onCancel: () => void; onSave: (title: string, projectId: string) => void; onDelete: () => void }) {
  const [title, setTitle] = useState(session.title)
  const [projectId, setProjectId] = useState(session.projectId || '')
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    inputRef.current?.focus(); inputRef.current?.select()
    const closeWithEscape = (event: KeyboardEvent) => { if (event.key === 'Escape' && !busy) onCancel() }
    document.addEventListener('keydown', closeWithEscape)
    return () => document.removeEventListener('keydown', closeWithEscape)
  }, [busy, onCancel])
  const valid = title.trim().length > 0 && title.trim().length <= 120
  return <div className="modal-backdrop" onMouseDown={() => !busy && onCancel()}>
    <section className="modal session-dialog" role="dialog" aria-modal="true" aria-labelledby="session-dialog-title" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">CONVERSATION</p><h2 id="session-dialog-title">编辑会话</h2></div><button type="button" aria-label="关闭" disabled={busy} onClick={onCancel}>×</button></div>
      <label className="project-field"><span>会话名称</span><input ref={inputRef} value={title} maxLength={120} onChange={(event) => setTitle(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && valid) onSave(title.trim(), projectId) }} /></label>
      <label className="project-field"><span>所属项目</span><select value={projectId} onChange={(event) => setProjectId(event.target.value)}><option value="">历史会话（不归入项目）</option>{projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}</select></label>
      <div className="session-project-fact"><span>固定执行目录</span><code>{session.sourceWorkspace || session.workspace}</code></div>
      <p className="project-help">移动会话只调整项目归属及后续可用源文件夹，不会切换这个会话已经固定的执行目录。</p>
      <div className="project-dialog-actions"><button className="danger-link" type="button" disabled={busy} onClick={onDelete}><TrashIcon />删除会话</button><div><button className="ghost-button" type="button" disabled={busy} onClick={onCancel}>取消</button><button className="primary-button" type="button" disabled={busy || !valid} onClick={() => onSave(title.trim(), projectId)}>{busy ? '保存中…' : '保存'}</button></div></div>
    </section>
  </div>
}

type Project = Bootstrap['projects'][number]

export function ProjectDialog({ project, projectCount, busy, onCancel, onSave, onDelete }: { project: Project | null; projectCount: number; busy: boolean; onCancel: () => void; onSave: (value: { name: string; directories: string[]; default: boolean }) => void; onDelete: () => void }) {
  const [name, setName] = useState(project?.name || '')
  const [directories, setDirectories] = useState<string[]>(project?.directories.length ? project.directories : [''])
  const [makeDefault, setMakeDefault] = useState(project?.default || false)
  const nameRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    nameRef.current?.focus()
    const closeWithEscape = (event: KeyboardEvent) => { if (event.key === 'Escape' && !busy) onCancel() }
    document.addEventListener('keydown', closeWithEscape)
    return () => document.removeEventListener('keydown', closeWithEscape)
  }, [busy, onCancel])
  const normalized = directories.map((value) => value.trim()).filter(Boolean)
  const duplicateSources = new Set(normalized).size !== normalized.length
  const sourceError = normalized.length === 0 ? '至少添加一个源文件夹后才能保存' : duplicateSources ? '同一个源文件夹不能重复添加' : ''
  const valid = name.trim().length > 0 && name.trim().length <= 60 && !sourceError
  const onlyProject = Boolean(project && projectCount <= 1)
  const updateDirectory = (index: number, value: string) => setDirectories((current) => current.map((item, itemIndex) => itemIndex === index ? value : item))
  const removeDirectory = (index: number) => setDirectories((current) => current.filter((_, itemIndex) => itemIndex !== index))
  return <div className="modal-backdrop" onMouseDown={() => !busy && onCancel()}>
    <section className="modal project-dialog" role="dialog" aria-modal="true" aria-labelledby="project-dialog-title" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">项目设置</p><h2 id="project-dialog-title">{project ? '编辑项目' : '添加项目'}</h2></div><button type="button" aria-label="关闭" disabled={busy} onClick={onCancel}>×</button></div>
      <label className="project-field"><span>项目名称</span><div className="project-name-input"><FolderIcon /><input ref={nameRef} value={name} maxLength={60} placeholder="例如 EasyAgent" onChange={(event) => setName(event.target.value)} /></div></label>
      <fieldset className="project-sources"><legend><span>源文件夹</span><small>项目可访问的服务器目录</small></legend><div className="project-source-list">{directories.map((directory, index) => <div className="project-source" key={`${index}-${project?.id || 'new'}`}><FolderIcon /><input value={directory} aria-label={`源文件夹 ${index + 1}`} placeholder="/srv/projects/repository" onChange={(event) => updateDirectory(index, event.target.value)} /><button type="button" aria-label={`移除源文件夹 ${index + 1}`} disabled={busy} onClick={() => removeDirectory(index)}>×</button></div>)}{directories.length === 0 && <div className="project-source-empty"><strong>尚未添加源文件夹</strong><span>添加一个服务器目录后即可保存项目</span></div>}</div><button className="add-source" type="button" disabled={busy || directories.length >= 12} onClick={() => setDirectories((current) => [...current, ''])}><FolderIcon add />添加文件夹</button>{duplicateSources && <p className="project-field-error" role="status">同一个源文件夹不能重复添加</p>}<p className="project-source-help">列表中的目录都可以更换或移除；这里只修改项目配置，不会删除磁盘文件。</p></fieldset>
      {project?.default ? <div className="project-default-state"><strong>当前默认项目</strong><span>浏览器或微信没有指定项目时使用</span></div> : <label className="project-default"><input type="checkbox" checked={makeDefault} onChange={(event) => setMakeDefault(event.target.checked)} /><span><strong>设为默认项目</strong><small>浏览器或微信没有指定项目时使用</small></span></label>}
      <div className="project-dialog-actions">{project && <div className="project-remove"><button className="danger-link" type="button" disabled={busy || onlyProject} title={onlyProject ? '至少需要保留一个项目' : '移除项目配置'} onClick={onDelete}>移除本地项目</button>{onlyProject && <small>至少保留一个项目</small>}</div>}<div><button className="ghost-button" type="button" disabled={busy} onClick={onCancel}>取消</button><button className="primary-button" type="button" disabled={busy || !valid} onClick={() => onSave({ name: name.trim(), directories: normalized, default: makeDefault })}>{busy ? '保存中…' : '保存'}</button></div></div>
    </section>
  </div>
}

export function RunError({ error, ollamaRunning, retrying, onRetry, onOpenCapabilities }: { error?: string; ollamaRunning: boolean; retrying: boolean; onRetry: () => void; onOpenCapabilities: () => void }) {
  const explanation = explainRunError(error, ollamaRunning)
  return <div className="run-error" role="alert">
    <div className="run-error-mark" aria-hidden="true">!</div>
    <div className="run-error-copy"><strong>{explanation.title}</strong><span>{explanation.message}</span>
      <div className="run-error-actions"><button className="primary-button" disabled={retrying} onClick={onRetry}>{retrying ? '正在重试…' : '重新发送'}</button><button className="ghost-button" onClick={onOpenCapabilities}>检查模型配置</button></div>
      {error && <details><summary>查看技术详情</summary><code>{error}</code></details>}
    </div>
  </div>
}

export function explainRunError(error?: string, ollamaRunning = false) {
  const value = error || '没有收到具体错误信息'
  if (/(multimodal.*(?:not support|unsupported)|does not support multimodal|vision.*(?:not support|unsupported)|(?:image|file).*(?:not support|unsupported))/i.test(value)) {
    return { title: '当前模型不支持这类附件', message: '附件已经正常上传，但当前模型不能读取图片或 PDF。请到“模型与工具”换用支持视觉/文件输入的模型，或改为发送文本、日志和代码文件。' }
  }
  if (/(?:127\.0\.0\.1|localhost):11434/i.test(value) && /(connection refused|connect: connection refused|ECONNREFUSED)/i.test(value)) {
    return ollamaRunning
      ? { title: '本地模型连接已恢复', message: '该轮执行时无法连接 Ollama；现在服务已经恢复，直接点击“重新发送”即可，不需要新建会话。' }
      : { title: '无法连接本地模型', message: 'EasyAgent 正常运行，但 Ollama 没有启动。启动 Ollama 后点击“重新发送”即可，不需要新建会话。' }
  }
  if (/Codex.*整轮任务|整轮任务.*上限/i.test(value)) {
    return { title: 'Codex 整轮任务超时', message: '这轮任务包含思考、命令、文件变更、MCP 和审批等待，累计超过了整轮任务上限。可到“模型与工具”增加上限，或拆分任务后重试。' }
  }
  if (/(context deadline exceeded|Client\.Timeout|timeout|timed out)/i.test(value)) {
    return { title: '模型响应超时', message: '模型在设定时间内没有返回。可直接重试，或到“模型与工具”增加超时时间、换用更小的模型。' }
  }
  return { title: '本轮没有完成', message: 'Agent 执行过程中遇到错误。可以查看技术详情后重试，或检查模型与工具配置。' }
}

export function friendlyError(error: string) {
  const explanation = explainRunError(error)
  if (explanation.title !== '本轮没有完成') return `${explanation.title}：${explanation.message}`
  return error
}

export type ForkWorkspaceMode = 'shared' | 'worktree'

export function ForkDialog({ busy, onCancel, onConfirm }: { busy: boolean; onCancel: () => void; onConfirm: (mode: ForkWorkspaceMode) => void }) {
  const [mode, setMode] = useState<ForkWorkspaceMode>('worktree')
  const cancelRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    cancelRef.current?.focus()
    const closeWithEscape = (event: KeyboardEvent) => { if (event.key === 'Escape' && !busy) onCancel() }
    document.addEventListener('keydown', closeWithEscape)
    return () => document.removeEventListener('keydown', closeWithEscape)
  }, [busy, onCancel])
  return <div className="modal-backdrop" onMouseDown={() => !busy && onCancel()}>
    <section className="modal workspace-dialog" role="dialog" aria-modal="true" aria-labelledby="fork-dialog-title" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">CODEX THREAD</p><h2 id="fork-dialog-title">创建对话分支</h2></div><button ref={cancelRef} type="button" aria-label="关闭" disabled={busy} onClick={onCancel}>×</button></div>
      <p className="modal-copy">分支会复制当前对话上下文。请选择新任务如何使用项目目录。</p>
      <div className="workspace-mode-list" role="radiogroup" aria-label="分支工作区模式">
        <button type="button" role="radio" aria-checked={mode === 'worktree'} className={mode === 'worktree' ? 'selected' : ''} onClick={() => setMode('worktree')}><span><strong>独立 worktree</strong><em>推荐并行任务</em></span><small>从当前已提交的 HEAD 创建独立分支和目录；源项目必须是干净的 Git 工作区。</small></button>
        <button type="button" role="radio" aria-checked={mode === 'shared'} className={mode === 'shared' ? 'selected' : ''} onClick={() => setMode('shared')}><span><strong>复用当前工作区</strong><em>适合连续探索</em></span><small>新旧对话共用文件目录；同一项目的任务会串行，避免同时修改冲突。</small></button>
      </div>
      <div className="workspace-dialog-actions"><button className="ghost-button" type="button" disabled={busy} onClick={onCancel}>取消</button><button className="primary-button" type="button" disabled={busy} onClick={() => onConfirm(mode)}>{busy ? '正在创建…' : '创建分支'}</button></div>
    </section>
  </div>
}

export function WorktreeDialog({ session, busy, onCancel, onCleanup }: { session: Session; busy: boolean; onCancel: () => void; onCleanup: () => void }) {
  const closeRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    closeRef.current?.focus()
    const closeWithEscape = (event: KeyboardEvent) => { if (event.key === 'Escape' && !busy) onCancel() }
    document.addEventListener('keydown', closeWithEscape)
    return () => document.removeEventListener('keydown', closeWithEscape)
  }, [busy, onCancel])
  return <div className="modal-backdrop" onMouseDown={() => !busy && onCancel()}>
    <section className="modal workspace-dialog" role="dialog" aria-modal="true" aria-labelledby="worktree-dialog-title" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">PROJECT ISOLATION</p><h2 id="worktree-dialog-title">工作树</h2></div><button ref={closeRef} type="button" aria-label="关闭" disabled={busy} onClick={onCancel}>×</button></div>
      <div className="worktree-facts"><div><span>分支</span><code>{session.worktreeBranch}</code></div><div><span>源项目</span><code>{session.sourceWorkspace || '—'}</code></div><div><span>执行目录</span><code>{session.workspace}</code></div></div>
      {session.workspaceNotice && <p className="worktree-notice">{session.workspaceNotice}</p>}
      <p className="modal-copy">任务完成后可以继续保留，方便以后恢复。安全清理只会在目录无修改、提交已进入源项目，并且没有其他会话引用时执行。</p>
      <div className="workspace-dialog-actions"><button className="ghost-button" type="button" disabled={busy} onClick={onCancel}>继续保留</button><button className="danger-button" type="button" disabled={busy} onClick={onCleanup}>{busy ? '正在检查…' : '安全清理'}</button></div>
    </section>
  </div>
}
