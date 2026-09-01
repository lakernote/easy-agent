import type { RefObject, KeyboardEvent } from 'react'
import type { CapabilityOption } from './capabilities'
import { capabilityKindShort } from './capabilities'
export function CapabilityPicker({ items, activeIndex, query, searchRef, onQuery, onKeyDown, onPick, onOpenSkills, onOpenCapabilities }: { items: CapabilityOption[]; activeIndex: number; query: string; searchRef: RefObject<HTMLInputElement>; onQuery: (value: string) => void; onKeyDown: (event: KeyboardEvent) => void; onPick: (item: CapabilityOption) => void; onOpenSkills: () => void; onOpenCapabilities: () => void }) {
  return <div className="capability-picker" role="dialog" aria-label="选择 Agent 能力">
    <div className="capability-picker-head"><div><strong>选择能力</strong><span>点击或输入 @ 指定本轮使用</span></div><label><span aria-hidden="true">⌕</span><input ref={searchRef} type="search" value={query} onChange={(event) => onQuery(event.target.value)} onKeyDown={onKeyDown} placeholder="搜索 Skill、Tool 或 MCP" aria-label="搜索能力" /></label></div>
    <div className="capability-options" role="listbox" aria-label="可用能力">
      {items.length === 0 && <div className="capability-empty">没有匹配的能力</div>}
      {items.map((item, index) => <button key={item.key} type="button" role="option" aria-selected={index === activeIndex} className={`${index === activeIndex ? 'active' : ''} ${item.enabled ? '' : 'disabled'}`} onMouseDown={(event) => event.preventDefault()} onClick={() => onPick(item)} disabled={!item.enabled}><span className={`capability-kind ${item.kind}`}>{capabilityKindShort(item.kind)}</span><span><strong>{item.name}</strong><small>{item.description}</small></span><em>{item.enabled ? item.token : '未启用'}</em></button>)}
    </div>
    <div className="capability-picker-foot"><span>↑↓ 选择 · Enter 插入 · Esc 关闭</span><div><button type="button" onClick={onOpenSkills}>管理 Skills</button><button type="button" onClick={onOpenCapabilities}>管理 Tools / MCP</button></div></div>
  </div>
}
