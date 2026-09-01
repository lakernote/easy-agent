import { useEffect, useState } from 'react'
import { api } from './api'
import type { Bootstrap, Skill } from './types'
export function Skills({ data, onRefresh, onError }: { data: Bootstrap; onRefresh: () => Promise<Bootstrap>; onError: (value: string) => void }) {
  const [selectedName, setSelectedName] = useState(data.skills[0]?.name || '')
  const selected = data.skills.find((item) => item.name === selectedName)
  const [draft, setDraft] = useState<Skill | null>(selected ? { ...selected } : null)
  const [saving, setSaving] = useState(false)
  const [toggling, setToggling] = useState(false)
  const [toggleState, setToggleState] = useState<'idle' | 'saved' | 'error'>('idle')
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDescription, setNewDescription] = useState('')
  const [createError, setCreateError] = useState('')

  // 自定义 Skill 在首次保存前还不在 data.skills 中，不能用第一项覆盖这份草稿。
  useEffect(() => {
    const current = data.skills.find((item) => item.name === selectedName)
    if (current) setDraft({ ...current })
    setToggleState('idle')
  }, [selectedName])

  const contentDirty = Boolean(draft && (!selected || draft.description !== selected.description || draft.content !== selected.content))

  const save = async () => {
    if (!draft || saving) return
    setSaving(true)
    try {
      await api.saveSkill(draft)
      const next = await onRefresh()
      const saved = next.skills.find((item) => item.name === draft.name)
      if (saved) setDraft({ ...saved })
    } catch (reason) { onError((reason as Error).message) }
    finally { setSaving(false) }
  }
  const reset = async () => { if (!draft) return; try { await api.resetSkill(draft.name); const next = await onRefresh(); const current = next.skills.find((item) => item.name === draft.name); if (current) setDraft({ ...current }) } catch (reason) { onError((reason as Error).message) } }

  const toggleEnabled = async (enabled: boolean) => {
    if (!draft || !selected || toggling || saving) return
    const previous = draft.enabled
    setDraft((current) => current?.name === draft.name ? { ...current, enabled } : current)
    setToggling(true)
    setToggleState('idle')
    let saved: Skill
    try {
      saved = await api.saveSkill({ ...selected, enabled })
    } catch (reason) {
      setDraft((current) => current?.name === draft.name ? { ...current, enabled: previous } : current)
      setToggleState('error')
      onError((reason as Error).message)
      setToggling(false)
      return
    }
    // 开关只保存启用状态，不覆盖编辑器里尚未保存的内容草稿。
    setDraft((current) => current?.name === saved.name ? { ...current, enabled: saved.enabled } : current)
    setToggleState('saved')
    try { await onRefresh() } catch (reason) { onError(`启用状态已保存，但列表刷新失败：${(reason as Error).message}`) }
    finally { setToggling(false) }
  }

  const openCreate = () => {
    setNewName('')
    setNewDescription('')
    setCreateError('')
    setCreating(true)
  }
  const create = (event: React.FormEvent) => {
    event.preventDefault()
    const name = newName.trim()
    const description = newDescription.trim() || '说明这个 Skill 何时使用'
    if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(name)) {
      setCreateError('名称只能包含小写英文、数字和短横线，例如 incident-analysis')
      return
    }
    if (data.skills.some((item) => item.name === name)) {
      setCreateError('这个 Skill 已经存在，请换一个名称')
      return
    }
    setSelectedName(name)
    setDraft({
      name,
      description,
      content: `---\nname: ${name}\ndescription: ${description}\n---\n\n# Instructions\n\n在这里编写执行说明。`,
      enabled: true,
      builtin: false,
    })
    setCreating(false)
  }

  return <section className="settings-page">
    <div className="page-intro"><p className="eyebrow">按需加载</p><h1>Skills</h1><p>默认只向模型提供名称和简介，由 Agent 按需调用 <code>load_skill</code>；在输入框用 <code>@skill:name</code> 明确选择时，本轮会直接使用完整说明。</p></div>
    <div className="split-settings">
      <div className="settings-list">
        <button className="add-row" onClick={openCreate}>＋ 添加 Skill</button>
        {data.skills.map((item) => <button key={item.name} className={item.name === draft?.name ? 'active' : ''} onClick={() => setSelectedName(item.name)}><span className={`status ${item.enabled ? 'idle' : 'off'}`} /><div><strong>{item.name}</strong><small>{item.description}</small></div><em>{item.builtin ? '内置' : '自定义'} · {item.enabled ? '启用' : '停用'}</em></button>)}
      </div>
      {draft && <div className="editor-pane">
        <div className="editor-title">
          <div><h2>{draft.name}</h2><span>{draft.builtin ? '内置 Skill · 内容修改会保存为本地覆盖' : selected ? '自定义 Skill' : '尚未保存的自定义 Skill'}</span></div>
          <div className={`skill-toggle-control ${toggleState}`}>
            <div><strong>{draft.enabled ? '已启用' : '已停用'}</strong><small id="skill-toggle-status" aria-live="polite">{!selected ? '保存内容后即可切换' : toggling ? '正在自动保存…' : toggleState === 'saved' ? '已自动保存' : toggleState === 'error' ? '保存失败，已恢复' : '切换后立即生效'}</small></div>
            <label className="switch" title={selected ? (draft.enabled ? '停用 Skill' : '启用 Skill') : '请先保存 Skill'}><input type="checkbox" aria-label={draft.enabled ? `停用 ${draft.name}` : `启用 ${draft.name}`} aria-describedby="skill-toggle-status" checked={draft.enabled} disabled={!selected || toggling || saving} onChange={(event) => toggleEnabled(event.target.checked)} /><span /></label>
          </div>
        </div>
        <label>用途描述<input value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} /><small className="field-help">帮助 Agent 判断什么时候应该加载这个 Skill。</small></label>
        <label>SKILL.md<textarea className="code-editor" value={draft.content} onChange={(event) => setDraft({ ...draft, content: event.target.value })} /><small className="field-help">正文不会自动保存，确认无误后再保存内容。</small></label>
        <div className="form-actions skill-form-actions"><span className={`edit-state ${contentDirty ? 'dirty' : ''}`}>{contentDirty ? '有未保存的内容修改' : '内容已保存'}</span><div>{draft.builtin && <button className="ghost-button" disabled={saving || toggling} onClick={reset}>恢复内置版本</button>}<button className="primary-button" disabled={!contentDirty || saving || toggling} onClick={save}>{saving ? '保存中…' : '保存内容'}</button></div></div>
      </div>}
    </div>
    {creating && <div className="modal-backdrop" onMouseDown={() => setCreating(false)}><form className="modal create-skill-modal" onSubmit={create} onMouseDown={(event) => event.stopPropagation()}><div className="modal-head"><div><p className="eyebrow">NEW SKILL</p><h2>创建一项按需能力</h2></div><button type="button" onClick={() => setCreating(false)}>×</button></div><p className="modal-copy">先定义清晰的名称和触发场景，创建后再编辑完整 SKILL.md。</p><label>名称<input autoFocus value={newName} onChange={(event) => { setNewName(event.target.value); setCreateError('') }} placeholder="例如 incident-analysis" /></label><label>用途描述<input value={newDescription} onChange={(event) => setNewDescription(event.target.value)} placeholder="什么时候应该使用这个 Skill？" /></label>{createError && <div className="field-error">{createError}</div>}<div className="form-actions"><button type="button" className="ghost-button" onClick={() => setCreating(false)}>取消</button><button className="primary-button" type="submit">创建并编辑</button></div></form></div>}
  </section>
}
