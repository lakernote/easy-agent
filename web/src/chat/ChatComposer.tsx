import { formatBytes, attachmentAccept, attachmentTypeLabel } from '../attachments'
import { capabilityKindLabel } from '../capabilities'
import { CapabilityPicker } from '../CapabilityPicker'
import { AttachIcon, CloseIcon, FileIcon, SendIcon } from '../ui'
import { isActive } from '../sessionState'
import { useChatComposer } from './useChatComposer'

type ChatComposerModel = ReturnType<typeof useChatComposer>

export function ChatComposer(model: ChatComposerModel) {
  const {
    session, draft, sending, attachments, attachmentError, dragging, setDragging,
    capabilityOpen, capabilityQuery, setCapabilityQuery, capabilityIndex, capabilitySearchRef,
    visibleCapabilities, selectedCapabilities, capabilities, enabledCapabilityCount,
    composerRef, textareaRef, fileInputRef, isCodexRuntime, workspace, projectOptions, selectedProject, selectedProjectId, selectProject, workspaceOpen, setWorkspaceOpen, profileOptions, selectedProfileId,
    displayedModel, onOpenSkills, onOpenCapabilities, addFiles, removeAttachment,
    closeCapabilityPicker, openCapabilityPicker, insertCapability, removeCapability, handleCapabilityKey, updateDraft, send,
    setSelectedProfileId,
  } = model

  const workspaceLabel = workspace.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || workspace

  return <div className="composer-wrap"><div ref={composerRef} className={`composer ${dragging ? 'dragging' : ''}`} onDragEnter={(event) => { event.preventDefault(); setDragging(true) }} onDragOver={(event) => event.preventDefault()} onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setDragging(false) }} onDrop={(event) => { event.preventDefault(); setDragging(false); addFiles(event.dataTransfer.files) }}>
      {capabilityOpen && <CapabilityPicker items={visibleCapabilities} activeIndex={capabilityIndex} query={capabilityQuery} searchRef={capabilitySearchRef} onQuery={setCapabilityQuery} onKeyDown={handleCapabilityKey} onPick={insertCapability} onOpenSkills={onOpenSkills} onOpenCapabilities={onOpenCapabilities} />}
      <div className="composer-context" aria-label="会话运行环境">
        <strong className="composer-runtime">{isCodexRuntime ? 'Codex' : 'EasyAgent'}</strong>
        {!session && <button type="button" className="composer-workspace" title={workspace} aria-expanded={workspaceOpen} aria-controls="workspace-picker" onClick={() => setWorkspaceOpen(!workspaceOpen)}>项目 · {selectedProject?.name || workspaceLabel}<span aria-hidden="true">⌄</span></button>}
        <div className="composer-model">
          {!session && profileOptions.length > 0 ? <select value={selectedProfileId} onChange={(event) => setSelectedProfileId(event.target.value)} disabled={sending} aria-label="新会话模型配置">
            {profileOptions.map((item) => <option key={item.id} value={item.id}>{item.name}{item.settings.model ? ` · ${item.settings.model}` : ''}</option>)}
          </select> : <strong title={displayedModel}>{displayedModel}</strong>}
        </div>
        <em>{session ? '已固定' : '创建时固定'}</em>
      </div>
      {!session && workspaceOpen && <div id="workspace-picker" className="workspace-picker project-picker" role="listbox" aria-label="选择项目">
        <label>选择项目</label>
        <div className="project-picker-list">{projectOptions.map((project) => <button key={project.id} type="button" role="option" aria-selected={project.id === selectedProjectId} className={project.id === selectedProjectId ? 'selected' : ''} onClick={() => selectProject(project.id)}><strong>{project.name}</strong><span>{project.directories.length} 个源文件夹 · {project.directories[0]}</span></button>)}</div>
        <small>会话创建后固定到项目的第一个源文件夹；项目内其他源文件夹也可按绝对路径访问。</small>
      </div>}
      {attachments.length > 0 && <div className="attachment-preview-list" aria-label="待发送附件">{attachments.map((item) => <div className="attachment-preview" key={item.id}>{item.preview ? <img src={item.preview} alt={item.file.name} /> : <span className="attachment-file-icon"><FileIcon /></span>}<span><strong title={item.file.name}>{item.file.name}</strong><small>{attachmentTypeLabel(item.file)} · {formatBytes(item.file.size)}</small></span><button type="button" disabled={sending || isActive(session?.status)} aria-label={`移除附件 ${item.file.name}`} onClick={() => removeAttachment(item.id)}><CloseIcon /></button></div>)}</div>}
      {selectedCapabilities.length > 0 && <div className="selected-capabilities" aria-label="已指定能力">{selectedCapabilities.map((item) => <span key={item.key}><b>{capabilityKindLabel(item.kind)}</b>{item.name}<button type="button" aria-label={`移除 ${item.name}`} onClick={() => removeCapability(item)}>×</button></span>)}</div>}
      <textarea ref={textareaRef} value={draft} onChange={updateDraft} aria-label="消息内容" aria-describedby="composer-help attachment-error" placeholder={attachments.length ? '描述如何处理这些附件…' : `给 ${isCodexRuntime ? 'Codex' : 'EasyAgent'} 发消息…`} rows={1} onPaste={(event) => { const files = Array.from(event.clipboardData.files); if (files.length) { event.preventDefault(); addFiles(files) } }} onKeyDown={(event) => { if (handleCapabilityKey(event)) return; if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }} />
      <div className="composer-toolbar"><div className="composer-tools"><button type="button" className="attach-button" disabled={sending || isActive(session?.status)} aria-label="添加文件或图片" onClick={() => fileInputRef.current?.click()}><AttachIcon /><span>附件</span></button><button type="button" className={`capability-button ${capabilityOpen ? 'active' : ''}`} disabled={sending || isActive(session?.status)} aria-label={`选择 Agent 能力，共 ${capabilities.length} 项，${enabledCapabilityCount} 项已启用`} aria-expanded={capabilityOpen} aria-haspopup="listbox" onClick={() => capabilityOpen ? closeCapabilityPicker() : openCapabilityPicker()}><span aria-hidden="true">@</span><strong>能力</strong><small>{enabledCapabilityCount}</small></button><input ref={fileInputRef} className="visually-hidden" type="file" multiple tabIndex={-1} aria-hidden="true" accept={attachmentAccept} onChange={(event) => { if (event.target.files) addFiles(event.target.files); event.target.value = '' }} /></div><button type="button" className="send-button" aria-label={sending ? '正在发送' : '发送消息'} disabled={(!draft.trim() && attachments.length === 0) || sending || isActive(session?.status)} onClick={() => send()}>{sending ? <span className="send-spinner" /> : <SendIcon />}</button></div>
      {attachmentError && <div id="attachment-error" className="composer-error" role="alert">{attachmentError}</div>}
    </div><small id="composer-help" className="composer-hint">Enter 发送 · Shift + Enter 换行 · 可拖入或粘贴 · 单文件最大 5 MiB · 图片/PDF 需要当前模型支持多模态</small></div>
}
