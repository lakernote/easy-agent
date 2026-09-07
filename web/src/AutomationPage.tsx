import { useEffect, useState } from 'react'
import { api, type AutomationTaskInput } from './api'
import { ConfirmDialog } from './dialogs'
import { formatTime } from './format'
import { Icon } from './ui'
import type { AutomationTask, Bootstrap } from './types'

type Project = Bootstrap['projects'][number]

const intervalOptions = [
  { value: 60, label: '每小时' },
  { value: 360, label: '每 6 小时' },
  { value: 1440, label: '每天' },
  { value: 10080, label: '每周' },
]

type Repeat = 'interval' | 'daily' | 'weekdays' | 'weekly'

const repeatOptions: { value: Repeat; label: string }[] = [
  { value: 'interval', label: '间隔' },
  { value: 'daily', label: '每天' },
  { value: 'weekdays', label: '工作日' },
  { value: 'weekly', label: '每周' },
]

const weekdayOptions = [
  { value: 1, label: '周一' },
  { value: 2, label: '周二' },
  { value: 3, label: '周三' },
  { value: 4, label: '周四' },
  { value: 5, label: '周五' },
  { value: 6, label: '周六' },
  { value: 7, label: '周日' },
]

function intervalLabel(minutes?: number) {
  return intervalOptions.find((option) => option.value === minutes)?.label || '未设置'
}

function scheduleLabel(task: AutomationTask) {
  const repeat = task.repeat || 'interval'
  if (repeat === 'interval') return `定时 · ${intervalLabel(task.intervalMinutes)}`
  const time = task.scheduleTime || '10:00'
  if (repeat === 'weekly') {
    const weekday = weekdayOptions.find((option) => option.value === task.scheduleWeekday)?.label || '周一'
    return `定时 · 每周${weekday} ${time}`
  }
  return `定时 · ${repeatOptions.find((option) => option.value === repeat)?.label || '每天'} ${time}`
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
    <div className="automation-row-main"><div className="automation-row-title"><h2>{task.name}</h2><span className={`automation-status ${status.tone}`}>{status.label}</span></div><p>{task.prompt}</p><div className="automation-row-meta"><span>{task.triggerType === 'interval' ? scheduleLabel(task) : '手动运行'}</span><span>{project?.name || '未知项目'}</span></div></div>
    <div className="automation-row-times"><div><small>下次执行</small><strong>{task.enabled && task.triggerType === 'interval' ? safeTime(task.nextRunAt) : '手动触发'}</strong></div><div><small>最近执行</small><strong>{safeTime(task.lastRunAt)}</strong></div></div>
    <div className="automation-row-actions"><button className="primary-button" type="button" disabled={!task.enabled || running} onClick={onRun}>{running ? '提交中…' : '立即运行'}</button>{task.lastSessionId && <button className="ghost-button" type="button" onClick={() => onOpenSession(task.lastSessionId || '')}>查看会话</button>}<button className="ghost-button" type="button" onClick={onEdit}>编辑</button><button className="danger-text-button" type="button" onClick={onDelete}>删除</button></div>
  </article>
}

function AutomationEditor({ task, data, busy, onCancel, onSave }: { task: AutomationTask | null; data: Bootstrap; busy: boolean; onCancel: () => void; onSave: (value: AutomationTaskInput) => void }) {
  const defaultProject = data.projects[0]
  const [name, setName] = useState(task?.name || '')
  const [prompt, setPrompt] = useState(task?.prompt || '')
  const [projectID, setProjectID] = useState(task?.projectId || defaultProject?.id || '')
  const [profileID, setProfileID] = useState(task?.profileId || data.activeModelProfileId || data.modelProfiles[0]?.id || '')
  const [triggerType, setTriggerType] = useState<'manual' | 'interval'>(task?.triggerType || 'manual')
  const [intervalMinutes, setIntervalMinutes] = useState(task?.intervalMinutes || 1440)
  const [repeat, setRepeat] = useState<Repeat>(task?.repeat || 'interval')
  const [scheduleTime, setScheduleTime] = useState(task?.scheduleTime || '10:00')
  const [scheduleWeekday, setScheduleWeekday] = useState(task?.scheduleWeekday || 1)
  const [enabled, setEnabled] = useState(task?.enabled ?? true)
  const valid = name.trim().length > 0 && prompt.trim().length > 0 && Boolean(projectID && profileID)

  const changeProject = (nextID: string) => {
    setProjectID(nextID)
  }

  return <div className="modal-backdrop" onMouseDown={() => !busy && onCancel()}>
    <section className="modal automation-editor" role="dialog" aria-modal="true" aria-labelledby="automation-editor-title" onMouseDown={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><p className="eyebrow">自动化任务</p><h2 id="automation-editor-title">{task ? '编辑任务' : '新建任务'}</h2></div><button type="button" aria-label="关闭" disabled={busy} onClick={onCancel}>×</button></div>
      <p className="modal-copy">先定义一次任务上下文，手动运行和定时运行都会使用同一份 Prompt 与项目配置。</p>
      <div className="automation-form">
        <label><span>任务名称</span><input autoFocus value={name} maxLength={80} placeholder="例如 每日检查构建状态" onChange={(event) => setName(event.target.value)} /></label>
        <label className="automation-form-wide"><span>Prompt</span><textarea value={prompt} maxLength={50000} placeholder="例如：检查当前项目最近一次构建结果，整理失败原因并给出修复建议。" onChange={(event) => setPrompt(event.target.value)} /><small>每次触发都会创建一条新的普通会话，保留完整 Trace。</small></label>
        <div className="automation-form-divider"><span>执行上下文</span><small>任务会使用保存时选定的项目和模型配置</small></div>
        <label><span>项目</span><select value={projectID} onChange={(event) => changeProject(event.target.value)}><option value="">选择项目</option>{data.projects.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        <label><span>模型配置</span><select value={profileID} onChange={(event) => setProfileID(event.target.value)}>{data.modelProfiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name} · {profile.settings.runtime === 'codex' ? 'Codex' : 'EasyAgent'}</option>)}</select></label>
        <div className="automation-form-wide"><span className="automation-field-label">触发方式</span><div className="automation-trigger-picker" role="radiogroup" aria-label="触发方式"><button type="button" role="radio" aria-checked={triggerType === 'manual'} className={triggerType === 'manual' ? 'selected' : ''} onClick={() => setTriggerType('manual')}><strong>手动运行</strong><small>需要时点击“立即运行”</small></button><button type="button" role="radio" aria-checked={triggerType === 'interval'} className={triggerType === 'interval' ? 'selected' : ''} onClick={() => setTriggerType('interval')}><strong>定时运行</strong><small>服务器后台按频率触发</small></button></div></div>
        {triggerType === 'interval' && <div className="automation-form-wide automation-schedule"><div className="automation-schedule-head"><span>频率</span><small>设置任务重复方式和执行时间</small></div><div className="automation-schedule-rows"><label><span>重复</span><select value={repeat} onChange={(event) => setRepeat(event.target.value as Repeat)}>{repeatOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>{repeat === 'interval' ? <label><span>间隔</span><select value={intervalMinutes} onChange={(event) => setIntervalMinutes(Number(event.target.value))}>{intervalOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label> : <label><span>时间</span><input type="time" value={scheduleTime} onChange={(event) => setScheduleTime(event.target.value)} /></label>}{repeat === 'weekly' && <label><span>星期</span><select value={scheduleWeekday} onChange={(event) => setScheduleWeekday(Number(event.target.value))}>{weekdayOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>}</div></div>}
        <label className="automation-enabled automation-form-wide"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span><strong>启用任务</strong><small>停用后不会自动触发，但不会删除任务历史。</small></span></label>
      </div>
      <div className="automation-editor-actions"><button className="ghost-button" type="button" disabled={busy} onClick={onCancel}>取消</button><button className="primary-button" type="button" disabled={busy || !valid} onClick={() => onSave({ name: name.trim(), prompt: prompt.trim(), projectId: projectID, profileId: profileID, triggerType, intervalMinutes, repeat, scheduleTime, scheduleWeekday, enabled })}>{busy ? '保存中…' : task ? '保存修改' : '创建任务'}</button></div>
    </section>
  </div>
}
