import { formatBytes, attachmentAccept, attachmentTypeLabel } from '../attachments'
import { capabilityKindLabel } from '../capabilities'
import { CapabilityPicker } from '../CapabilityPicker'
import { AttachIcon, CloseIcon, FileIcon, SendIcon, StopIcon } from '../ui'
import { isActive } from '../sessionState'
import { useChatComposer } from './useChatComposer'

type ChatComposerModel = ReturnType<typeof useChatComposer> & { onStop: () => Promise<void>; onPause: () => Promise<void>; onResume: () => Promise<void>; runActionBusy: boolean }

export function ChatComposer(model: ChatComposerModel) {
  const {
    session, draft, sending, attachments, attachmentError, dragging, setDragging,
    capabilityOpen, capabilityQuery, setCapabilityQuery, capabilityIndex, capabilitySearchRef,
    visibleCapabilities, selectedCapabilities, capabilities, enabledCapabilityCount,
    composerRef, textareaRef, fileInputRef, isCodexRuntime, selectedRuntime, selectRuntime, selectedPermissionMode, selectPermissionMode, workspace, projectOptions, selectedProject, selectedProjectId, selectProject, workspaceOpen, setWorkspaceOpen, profileOptions, selectedProfileId,
    displayedModel, readiness, onOpenSkills, onOpenCapabilities, onOpenModelSettings, addFiles, removeAttachment,
    closeCapabilityPicker, openCapabilityPicker, insertCapability, removeCapability, handleCapabilityKey, updateDraft, send,
    setSelectedProfileId, onStop, onPause, onResume, runActionBusy,
  } = model

  const workspaceLabel = workspace.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || workspace
  const activeSession = isActive(session?.status)
  const pausedSession = session?.status === 'paused'
  const controlsLocked = activeSession || pausedSession

  return <div className="composer-wrap"><div ref={composerRef} className={`composer ${dragging ? 'dragging' : ''}`} onDragEnter={(event) => { event.preventDefault(); setDragging(true) }} onDragOver={(event) => event.preventDefault()} onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setDragging(false) }} onDrop={(event) => { event.preventDefault(); setDragging(false); addFiles(event.dataTransfer.files) }}>
      {capabilityOpen && <CapabilityPicker items={visibleCapabilities} activeIndex={capabilityIndex} query={capabilityQuery} searchRef={capabilitySearchRef} onQuery={setCapabilityQuery} onKeyDown={handleCapabilityKey} onPick={insertCapability} onOpenSkills={onOpenSkills} onOpenCapabilities={onOpenCapabilities} />}
      <div className={`composer-context ${session ? 'session-context' : 'new-session-context'}`} aria-label={session ? '会话运行环境（已固定）' : '新会话执行配置'}>
        {!session ? <>
          <button type="button" className="composer-workspace composer-project-select" title={workspace} aria-expanded={workspaceOpen} aria-controls="workspace-picker" onClick={() => setWorkspaceOpen(!workspaceOpen)}><span className="composer-field-label">项目</span><strong>{selectedProject?.name || workspaceLabel}</strong><span className="composer-field-chevron" aria-hidden="true">⌄</span></button>
          <label className="composer-select-field composer-runtime-select"><span>Runtime</span><select value={selectedRuntime} onChange={(event) => selectRuntime(event.target.value as typeof selectedRuntime)} disabled={sending} aria-label="选择新会话 Runtime"><option value="easyagent">EasyAgent</option><option value="codex">Codex</option></select></label>
          <label className="composer-select-field composer-permission-select"><span>权限</span><select value={selectedPermissionMode} onChange={(event) => selectPermissionMode(event.target.value as typeof selectedPermissionMode)} disabled={sending} aria-label="选择新会话权限"><option value="read_only">只读</option><option value="workspace_write">工作区可写</option><option value="full_access">完全访问</option><option value="custom">自定义目录</option></select></label>
          <label className="composer-select-field composer-model-select"><span>模型</span>{profileOptions.length > 0 ? <select value={selectedProfileId} onChange={(event) => setSelectedProfileId(event.target.value)} disabled={sending} aria-label="选择新会话模型配置">
            {profileOptions.map((item) => <option key={item.id} value={item.id}>{item.name}{item.settings.model ? ` · ${item.settings.model}` : ''}</option>)}
          </select> : <button type="button" className="composer-setup-link" onClick={onOpenModelSettings}>去设置创建</button>}</label>
        </> : <>
          <div className="composer-fixed-field composer-fixed-project" title={workspace}><span>项目</span><strong>{workspaceLabel}</strong></div>
          <div className="composer-fixed-field composer-fixed-runtime"><span>Runtime</span><strong className={`composer-runtime ${isCodexRuntime ? 'codex' : ''}`}>{isCodexRuntime ? 'Codex' : 'EasyAgent'}</strong></div>
          <div className="composer-fixed-field composer-fixed-permission"><span>权限</span><strong>{session?.permissions?.mode === 'read_only' ? '只读' : session?.permissions?.mode === 'workspace_write' ? '工作区可写' : session?.permissions?.mode === 'custom' ? '自定义目录' : '完全访问'}</strong></div>
          <div className="composer-model composer-fixed-field"><span>模型</span><strong title={displayedModel}>{displayedModel}</strong></div>
        </>}
      </div>
      {!session && <div className={`composer-readiness ${readiness.tone}`} role="status" title={readiness.detail}><span aria-hidden="true" /><div><strong>{readiness.label}</strong><small>{readiness.detail}</small></div>{readiness.tone !== 'ready' && <button type="button" onClick={onOpenModelSettings}>检查配置</button>}</div>}
      {!session && workspaceOpen && <div id="workspace-picker" className="workspace-picker project-picker" role="listbox" aria-label="选择项目">
        <div className="project-picker-list">{projectOptions.map((project) => <button key={project.id} type="button" role="option" aria-selected={project.id === selectedProjectId} className={project.id === selectedProjectId ? 'selected' : ''} onClick={() => selectProject(project.id)}><strong>{project.name}</strong><span>{project.directories[0] || '未配置目录'}</span></button>)}</div>
      </div>}
      {attachments.length > 0 && <div className="attachment-preview-list" aria-label="待发送附件">{attachments.map((item) => <div className="attachment-preview" key={item.id}>{item.preview ? <img src={item.preview} alt={item.file.name} /> : <span className="attachment-file-icon"><FileIcon /></span>}<span><strong title={item.file.name}>{item.file.name}</strong><small>{attachmentTypeLabel(item.file)} · {formatBytes(item.file.size)}</small></span><button type="button" disabled={sending || controlsLocked} aria-label={`移除附件 ${item.file.name}`} onClick={() => removeAttachment(item.id)}><CloseIcon /></button></div>)}</div>}
      {selectedCapabilities.length > 0 && <div className="selected-capabilities" aria-label="已指定能力">{selectedCapabilities.map((item) => <span key={item.key}><b>{capabilityKindLabel(item.kind)}</b>{item.name}<button type="button" aria-label={`移除 ${item.name}`} onClick={() => removeCapability(item)}>×</button></span>)}</div>}
      <textarea ref={textareaRef} value={draft} onChange={updateDraft} aria-label="消息内容" aria-describedby="composer-help attachment-error" placeholder={!session && !readiness.canSend ? '请先完成运行配置…' : attachments.length ? '描述如何处理这些附件…' : `给 ${isCodexRuntime ? 'Codex' : 'EasyAgent'} 发消息…`} rows={1} onPaste={(event) => { const files = Array.from(event.clipboardData.files); if (files.length) { event.preventDefault(); addFiles(files) } }} onKeyDown={(event) => { if (handleCapabilityKey(event)) return; if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }} />
      <div className="composer-toolbar"><div className="composer-tools"><button type="button" className="attach-button" disabled={sending || controlsLocked} aria-label="添加文件或图片" onClick={() => fileInputRef.current?.click()}><AttachIcon /><span>附件</span></button><button type="button" className={`capability-button ${capabilityOpen ? 'active' : ''}`} disabled={sending || controlsLocked} aria-label={`选择 Agent 能力，共 ${capabilities.length} 项，${enabledCapabilityCount} 项已启用`} aria-expanded={capabilityOpen} aria-haspopup="listbox" onClick={() => capabilityOpen ? closeCapabilityPicker() : openCapabilityPicker()}><span aria-hidden="true">@</span><strong>能力</strong><small>{enabledCapabilityCount}</small></button><input ref={fileInputRef} className="visually-hidden" type="file" multiple tabIndex={-1} aria-hidden="true" accept={attachmentAccept} onChange={(event) => { if (event.target.files) addFiles(event.target.files); event.target.value = '' }} /></div>{session?.status === 'queued' ? <div className="composer-run-actions"><button type="button" className="composer-run-secondary" disabled={runActionBusy} onClick={() => void onPause()}>{runActionBusy ? '处理中…' : '暂停排队'}</button><button type="button" className="send-button composer-stop-button" disabled={runActionBusy} aria-label="取消排队" title="取消排队" onClick={() => void onStop()}><StopIcon /></button></div> : pausedSession ? <div className="composer-run-actions"><button type="button" className="composer-run-secondary danger" disabled={runActionBusy} onClick={() => void onStop()}>取消任务</button><button type="button" className="composer-run-primary" disabled={runActionBusy} onClick={() => void onResume()}>{runActionBusy ? '处理中…' : '继续运行'}</button></div> : session?.status === 'running' ? <button type="button" className="send-button composer-stop-button" disabled={runActionBusy} aria-label="停止任务" title="停止任务" onClick={() => void onStop()}><StopIcon /></button> : <button type="button" className="send-button" aria-label={sending ? '正在发送' : '发送消息'} disabled={(!draft.trim() && attachments.length === 0) || sending || (!session && !readiness.canSend)} onClick={() => send()}>{sending ? <span className="send-spinner" /> : <SendIcon />}</button>}</div>
      {attachmentError && <div id="attachment-error" className="composer-error" role="alert">{attachmentError}</div>}
    </div><small id="composer-help" className="composer-hint">Enter 发送 · Shift + Enter 换行 · 可拖入或粘贴 · 单文件最大 5 MiB · 图片/PDF 需要当前模型支持多模态</small></div>
}
