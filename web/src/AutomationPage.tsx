import { useEffect, useState } from 'react'
import { api, type AutomationTaskInput } from './api'
import { ConfirmDialog } from './dialogs'
import { formatTime } from './format'
import { Icon } from './ui'
import type { AutomationTask, Bootstrap } from './types'

type Project = Bootstrap['projects'][number]

const cronPresets = [
  { value: '0 10 * * 1-5', label: '工作日 10:00' },
  { value: '0 10 * * *', label: '每天 10:00' },
  { value: '0 10 * * 1', label: '每周一 10:00' },
  { value: '*/15 * * * *', label: '每 15 分钟' },
]

function cronLabel(expression?: string) {
  const value = expression?.trim() || ''
  return cronPresets.find((preset) => preset.value === value)?.label || value || '未设置'
}

function scheduleLabel(task: AutomationTask) {
  return task.scheduleEnabled ? `定时 · ${cronLabel(task.cron)}` : '手动运行'
}

function safeTime(value?: string) {
  return value ? formatTime(value) : '尚未执行'
}

function taskStatus(task: AutomationTask) {
  if (!task.enabled) return { label: '已停用', tone: 'muted' }
  if (task.lastStatus === 'failed') return { label: '上次失败', tone: 'error' }
  if (task.lastStatus === 'queued') return { label: '已排队', tone: 'queued' }
  return { label: '已启用', tone: 'ready' }
}

export function AutomationPage({ data, onError, onOpenSession }: { data: Bootstrap; onError: (value: string) => void; onOpenSession: (id: string) => void }) {
  const [tasks, setTasks] = useState<AutomationTask[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [runningID, setRunningID] = useState('')
  const [editing, setEditing] = useState<AutomationTask | null>(null)
  const [creating, setCreating] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<AutomationTask | null>(null)
  const [notice, setNotice] = useState('')

  const load = async () => {
    setLoading(true)
    try { setTasks(await api.listAutomationTasks()) }
    catch (reason) { onError((reason as Error).message) }
    finally { setLoading(false) }
  }

  useEffect(() => { void load() }, [])

  const openCreate = () => { setEditing(null); setCreating(true) }
  const openEdit = (task: AutomationTask) => { setEditing(task); setCreating(true) }
  const closeEditor = () => { if (!saving) { setCreating(false); setEditing(null) } }

  const save = async (value: AutomationTaskInput) => {
    setSaving(true); onError('')
    try {
      const result = editing ? await api.updateAutomationTask(editing.id, value) : await api.createAutomationTask(value)
      setTasks((current) => editing ? current.map((item) => item.id === result.id ? result : item) : [result, ...current])
      setCreating(false); setEditing(null); setNotice(editing ? '任务已更新' : '任务已创建')
      window.setTimeout(() => setNotice(''), 2600)
    } catch (reason) { onError((reason as Error).message) }
    finally { setSaving(false) }
  }

  const run = async (task: AutomationTask) => {
    if (runningID) return
    setRunningID(task.id); onError('')
    try {
      const result = await api.runAutomationTask(task.id)
      setTasks((current) => current.map((item) => item.id === result.task.id ? result.task : item))
      setNotice('任务已提交到执行队列')
      window.setTimeout(() => setNotice(''), 2600)
    } catch (reason) { onError((reason as Error).message) }
    finally { setRunningID('') }
  }

  const remove = async () => {
    if (!pendingDelete || saving) return
    setSaving(true); onError('')
    try {
      await api.deleteAutomationTask(pendingDelete.id)
      setTasks((current) => current.filter((item) => item.id !== pendingDelete.id))
      setPendingDelete(null); setNotice('任务已删除')
      window.setTimeout(() => setNotice(''), 2600)
    } catch (reason) { onError((reason as Error).message) }
    finally { setSaving(false) }
  }

  return <section className="automation-page" aria-labelledby="automation-title">
    <header className="automation-head">
      <div><p className="settings-kicker">执行编排</p><h1 id="automation-title">自动化任务</h1><p>把固定 Prompt 和项目执行上下文保存下来，按需手动运行或定时触发。每次执行都会生成一条普通会话。</p></div>
      <button className="primary-button automation-create" type="button" onClick={openCreate}>＋ 新建任务</button>
    </header>
    <div className="automation-capabilities" aria-label="触发方式状态"><div className="automation-capability active"><Icon name="automation" /><span><strong>手动 / 定时</strong><small>当前可用</small></span></div></div>
    <div className="automation-toolbar"><span>{loading ? '正在读取任务…' : `${tasks.length} 个任务`}</span><small>定时任务由服务器后台执行，浏览器关闭后仍会继续。</small></div>
    {loading && <div className="automation-empty"><span className="spinner" /><span>正在加载自动化任务…</span></div>}
    {!loading && tasks.length === 0 && <div className="automation-empty"><div className="automation-empty-mark"><Icon name="automation" /></div><strong>还没有自动化任务</strong><span>先保存一个固定 Prompt，之后可以一键重复执行。</span><button className="ghost-button" type="button" onClick={openCreate}>创建第一个任务</button></div>}
    {!loading && tasks.length > 0 && <div className="automation-list">{tasks.map((task) => <AutomationRow key={task.id} task={task} projects={data.projects} running={runningID === task.id} onRun={() => void run(task)} onEdit={() => openEdit(task)} onDelete={() => setPendingDelete(task)} onOpenSession={onOpenSession} />)}</div>}
    {creating && <AutomationEditor key={editing?.id || 'new'} task={editing} data={data} busy={saving} onCancel={closeEditor} onSave={(value) => void save(value)} />}
    {pendingDelete && <ConfirmDialog title="删除这个自动化任务？" description="只会删除任务配置，不会删除已经生成的会话、Trace 或服务器文件。" subject={pendingDelete.name} confirmLabel="删除任务" busy={saving} onCancel={() => setPendingDelete(null)} onConfirm={() => void remove()} />}
    {notice && <div className="automation-notice" role="status">{notice}</div>}
  </section>
}

function AutomationRow({ task, projects, running, onRun, onEdit, onDelete, onOpenSession }: { task: AutomationTask; projects: Project[]; running: boolean; onRun: () => void; onEdit: () => void; onDelete: () => void; onOpenSession: (id: string) => void }) {
  const status = taskStatus(task)
  const project = projects.find((item) => item.id === task.projectId)
  return <article className={`automation-row ${!task.enabled ? 'disabled' : ''}`}>
    <div className={`automation-status-dot ${status.tone}`} aria-label={status.label} title={status.label} />
    <div className="automation-row-main"><div className="automation-row-title"><h2>{task.name}</h2><span className={`automation-status ${status.tone}`}>{status.label}</span></div><p>{task.prompt}</p><div className="automation-row-meta"><span>{scheduleLabel(task)}</span><span>{project?.name || '未知项目'}</span></div></div>
    <div className="automation-row-times"><div><small>下次执行</small><strong>{task.enabled && task.scheduleEnabled ? safeTime(task.nextRunAt) : '手动触发'}</strong></div><div><small>最近执行</small><strong>{safeTime(task.lastRunAt)}</strong></div></div>
    <div className="automation-row-actions"><button className="primary-button" type="button" disabled={!task.enabled || running} onClick={onRun}>{running ? '提交中…' : '立即运行'}</button>{task.lastSessionId && <button className="ghost-button" type="button" onClick={() => onOpenSession(task.lastSessionId || '')}>查看会话</button>}<button className="ghost-button" type="button" onClick={onEdit}>编辑</button><button className="danger-text-button" type="button" onClick={onDelete}>删除</button></div>
  </article>
}

function AutomationEditor({ task, data, busy, onCancel, onSave }: { task: AutomationTask | null; data: Bootstrap; busy: boolean; onCancel: () => void; onSave: (value: AutomationTaskInput) => void }) {
  const defaultProject = data.projects[0]
  const [name, setName] = useState(task?.name || '')
  const [prompt, setPrompt] = useState(task?.prompt || '')
  const [projectID, setProjectID] = useState(task?.projectId || defaultProject?.id || '')
  const [profileID, setProfileID] = useState(task?.profileId || data.activeModelProfileId || data.modelProfiles[0]?.id || '')
  const [scheduleEnabled, setScheduleEnabled] = useState(task?.scheduleEnabled ?? false)
  const [cron, setCron] = useState(task?.cron || '0 10 * * 1-5')
  const [enabled, setEnabled] = useState(task?.enabled ?? true)
  const valid = name.trim().length > 0 && prompt.trim().length > 0 && Boolean(projectID && profileID) && (!scheduleEnabled || cron.trim().length > 0)

  const changeProject = (nextID: string) => {
    setProjectID(nextID)
  }

  return <div className="modal-backdrop" onMouseDown={() => !busy && onCancel()}>
    <section className="modal automation-editor" role="dialog" aria-modal="true" aria-labelledby="automation-editor-title" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">自动化任务</p><h2 id="automation-editor-title">{task ? '编辑任务' : '新建任务'}</h2></div><button type="button" aria-label="关闭" disabled={busy} onClick={onCancel}>×</button></div>
      <p className="modal-copy">先定义一次任务上下文；任务可以手动运行，也可以单独启用 Cron 定时。</p>
      <div className="automation-form">
        <label><span>任务名称</span><input autoFocus value={name} maxLength={80} placeholder="例如 每日检查构建状态" onChange={(event) => setName(event.target.value)} /></label>
        <label className="automation-form-wide"><span>Prompt</span><textarea value={prompt} maxLength={50000} placeholder="例如：检查当前项目最近一次构建结果，整理失败原因并给出修复建议。" onChange={(event) => setPrompt(event.target.value)} /><small>每次触发都会创建一条新的普通会话，保留完整 Trace。</small></label>
        <div className="automation-form-divider"><span>执行上下文</span><small>任务会使用保存时选定的项目和模型配置</small></div>
        <label><span>项目</span><select value={projectID} onChange={(event) => changeProject(event.target.value)}><option value="">选择项目</option>{data.projects.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        <label><span>模型配置</span><select value={profileID} onChange={(event) => setProfileID(event.target.value)}>{data.modelProfiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name} · {profile.settings.runtime === 'codex' ? 'Codex' : 'EasyAgent'}</option>)}</select></label>
        <div className="automation-form-wide automation-schedule"><div className="automation-schedule-head"><span>定时执行</span><small>使用标准 5 段 Cron：分 时 日 月 周</small></div><label className="automation-schedule-toggle"><input type="checkbox" checked={scheduleEnabled} onChange={(event) => setScheduleEnabled(event.target.checked)} /><span><strong>启用定时</strong><small>关闭后仍可以手动运行任务</small></span></label>{scheduleEnabled && <div className="automation-cron-editor"><label><span>Cron 表达式</span><input value={cron} spellCheck={false} placeholder="0 10 * * 1-5" onChange={(event) => setCron(event.target.value)} /><small>例如 <code>0 10 * * 1-5</code> 表示工作日 10:00 执行。</small></label><div className="automation-cron-presets" aria-label="常用 Cron 预设">{cronPresets.map((preset) => <button key={preset.value} type="button" className={cron.trim() === preset.value ? 'selected' : ''} onClick={() => setCron(preset.value)}>{preset.label}</button>)}</div><div className="automation-cron-preview" role="status">当前规则：<strong>{cronLabel(cron)}</strong></div></div>}</div>
        <label className="automation-enabled automation-form-wide"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span><strong>启用任务</strong><small>停用后不会自动触发，但不会删除任务历史。</small></span></label>
      </div>
      <div className="automation-editor-actions"><button className="ghost-button" type="button" disabled={busy} onClick={onCancel}>取消</button><button className="primary-button" type="button" disabled={busy || !valid} onClick={() => onSave({ name: name.trim(), prompt: prompt.trim(), projectId: projectID, profileId: profileID, scheduleEnabled, cron: cron.trim(), enabled })}>{busy ? '保存中…' : task ? '保存修改' : '创建任务'}</button></div>
    </section>
  </div>
}
