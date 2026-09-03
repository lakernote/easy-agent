import { formatBytes, attachmentAccept, attachmentTypeLabel } from '../attachments'
import { capabilityKindLabel } from '../capabilities'
import { CapabilityPicker } from '../CapabilityPicker'
import { AttachIcon, CloseIcon, FileIcon, SendIcon } from '../ui'
import { isActive } from '../sessionState'
import { useChatComposer } from './useChatComposer'

type ChatComposerModel = ReturnType<typeof useChatComposer>

export function ChatComposer(model: ChatComposerModel) {
  const {
    data, session, draft, sending, attachments, attachmentError, dragging, setDragging,
    capabilityOpen, capabilityQuery, setCapabilityQuery, capabilityIndex, capabilitySearchRef,
    visibleCapabilities, selectedCapabilities, capabilities, enabledCapabilityCount, enabledSkillCount, enabledMCPCount,
    composerRef, textareaRef, fileInputRef, isCodexRuntime, workspace, profileOptions, selectedProfileId,
    displayedModel, onOpenSkills, onOpenCapabilities, addFiles, removeAttachment,
    closeCapabilityPicker, openCapabilityPicker, insertCapability, removeCapability, handleCapabilityKey, updateDraft, send,
    setSelectedProfileId,
  } = model

  return <div className="composer-wrap"><div ref={composerRef} className={`composer ${dragging ? 'dragging' : ''}`} onDragEnter={(event) => { event.preventDefault(); setDragging(true) }} onDragOver={(event) => event.preventDefault()} onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setDragging(false) }} onDrop={(event) => { event.preventDefault(); setDragging(false); addFiles(event.dataTransfer.files) }}>
      {capabilityOpen && <CapabilityPicker items={visibleCapabilities} activeIndex={capabilityIndex} query={capabilityQuery} searchRef={capabilitySearchRef} onQuery={setCapabilityQuery} onKeyDown={handleCapabilityKey} onPick={insertCapability} onOpenSkills={onOpenSkills} onOpenCapabilities={onOpenCapabilities} />}
      <div className="composer-context" aria-label="会话运行环境">
        <div className="composer-context-item composer-workspace" title={session ? '工作区在会话创建后固定；如需切换请新建会话' : '新会话将使用当前服务工作区'}><span>工作区</span><strong>{workspace}</strong></div>
        <div className="composer-context-item"><span>运行时</span><strong>{isCodexRuntime ? 'Codex Runtime' : 'EasyAgent Runtime'}</strong></div>
        <div className="composer-context-item composer-model">
          <span>模型</span>
          {!session && profileOptions.length > 0 ? <select value={selectedProfileId} onChange={(event) => setSelectedProfileId(event.target.value)} disabled={sending} aria-label="新会话模型配置">
            {profileOptions.map((item) => <option key={item.id} value={item.id}>{item.name}{item.settings.model ? ` · ${item.settings.model}` : ''}</option>)}
          </select> : <strong title={displayedModel}>{displayedModel}</strong>}
        </div>
        <em>{session ? '本会话已固定' : '新会话创建时固定'}</em>
      </div>
      {attachments.length > 0 && <div className="attachment-preview-list" aria-label="待发送附件">{attachments.map((item) => <div className="attachment-preview" key={item.id}>{item.preview ? <img src={item.preview} alt={item.file.name} /> : <span className="attachment-file-icon"><FileIcon /></span>}<span><strong title={item.file.name}>{item.file.name}</strong><small>{attachmentTypeLabel(item.file)} · {formatBytes(item.file.size)}</small></span><button type="button" disabled={sending || isActive(session?.status)} aria-label={`移除附件 ${item.file.name}`} onClick={() => removeAttachment(item.id)}><CloseIcon /></button></div>)}</div>}
      {selectedCapabilities.length > 0 && <div className="selected-capabilities" aria-label="已指定能力">{selectedCapabilities.map((item) => <span key={item.key}><b>{capabilityKindLabel(item.kind)}</b>{item.name}<button type="button" aria-label={`移除 ${item.name}`} onClick={() => removeCapability(item)}>×</button></span>)}</div>}
      <textarea ref={textareaRef} value={draft} onChange={updateDraft} aria-label="消息内容" aria-describedby="composer-help attachment-error" placeholder={attachments.length ? '描述希望 Agent 如何处理这些附件…' : `${isCodexRuntime ? '给 Codex' : '给 EasyAgent'} 发消息…${isCodexRuntime ? '' : ' 输入 @ 选择能力'}`} rows={1} onPaste={(event) => { const files = Array.from(event.clipboardData.files); if (files.length) { event.preventDefault(); addFiles(files) } }} onKeyDown={(event) => { if (handleCapabilityKey(event)) return; if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }} />
      <div className="composer-toolbar"><div className="composer-tools"><button type="button" className="attach-button" disabled={sending || isActive(session?.status)} aria-label="添加文件或图片" onClick={() => fileInputRef.current?.click()}><AttachIcon /><span>附件</span></button>{!isCodexRuntime && <button type="button" className={`capability-button ${capabilityOpen ? 'active' : ''}`} disabled={sending || isActive(session?.status)} aria-label={`选择 Agent 能力，共 ${capabilities.length} 项，${enabledCapabilityCount} 项已启用`} aria-expanded={capabilityOpen} aria-haspopup="listbox" onClick={() => capabilityOpen ? closeCapabilityPicker() : openCapabilityPicker()}><span aria-hidden="true">@</span><strong>能力</strong><small>{capabilities.length}</small></button>}<small>{isCodexRuntime ? 'Codex app-server · 工具、Skill、沙箱由 Codex 管理' : `${enabledSkillCount}/${data.skills.length} Skills · ${data.builtinTools.length} Tools · ${enabledMCPCount}/${data.mcps.length} MCP`}</small><input ref={fileInputRef} className="visually-hidden" type="file" multiple tabIndex={-1} aria-hidden="true" accept={attachmentAccept} onChange={(event) => { if (event.target.files) addFiles(event.target.files); event.target.value = '' }} /></div><button type="button" className="send-button" aria-label={sending ? '正在发送' : '发送消息'} disabled={(!draft.trim() && attachments.length === 0) || sending || isActive(session?.status)} onClick={() => send()}>{sending ? <span className="send-spinner" /> : <SendIcon />}</button></div>
      {attachmentError && <div id="attachment-error" className="composer-error" role="alert">{attachmentError}</div>}
    </div><small id="composer-help" className="composer-hint">Enter 发送 · Shift + Enter 换行 · 可拖入或粘贴 · 单文件最大 5 MiB · 图片/PDF 需要当前模型支持多模态</small></div>
}
