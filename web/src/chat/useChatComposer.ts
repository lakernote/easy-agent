import { useEffect, useMemo, useRef, useState } from 'react'
import type { ChangeEvent, KeyboardEvent } from 'react'
import { api } from '../api'
import type { Bootstrap, Session } from '../types'
import { isActive, mergeSessionSnapshot } from '../sessionState'
import { encodeAttachment, supportedAttachment, type PendingAttachment } from '../attachments'
import { capabilityKindLabel, capabilityMention, capabilityOptions, hasCapabilityToken, type CapabilityOption } from '../capabilities'
import type { StarterSuggestion } from '../suggestions'

type ChatComposerOptions = {
  session: Session | null
  data: Bootstrap
  onSession: (session: Session) => void
  onRefresh: () => Promise<Bootstrap>
  onError: (value: string) => void
  onOpenSkills: () => void
  onOpenCapabilities: () => void
}

export function useChatComposer({ session, data, onSession, onRefresh, onError, onOpenSkills, onOpenCapabilities }: ChatComposerOptions) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [attachments, setAttachments] = useState<PendingAttachment[]>([])
  const [attachmentError, setAttachmentError] = useState('')
  const [dragging, setDragging] = useState(false)
  const [capabilityOpen, setCapabilityOpen] = useState(false)
  const [capabilityQuery, setCapabilityQuery] = useState('')
  const [capabilityIndex, setCapabilityIndex] = useState(0)
  const [capabilityRange, setCapabilityRange] = useState<{ start: number; end: number } | null>(null)
  const [selectedProfileId, setSelectedProfileId] = useState(data.activeModelProfileId)
  const [workspace, setWorkspaceState] = useState(() => window.localStorage.getItem('easyagent.workspace') || data.runtime.workspace)
  const [workspaceDraft, setWorkspaceDraft] = useState(workspace)
  const [workspaceOpen, setWorkspaceOpen] = useState(false)
  const composerRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const capabilitySearchRef = useRef<HTMLInputElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const attachmentRef = useRef<PendingAttachment[]>([])
  const runtime = session?.runtime || data.model.runtime
  const isCodexRuntime = runtime === 'codex'
  const capabilities = useMemo(() => capabilityOptions(data).filter((item) => !isCodexRuntime || item.kind !== 'tool'), [data, isCodexRuntime])
  const visibleCapabilities = useMemo(() => {
    const keyword = capabilityQuery.trim().toLocaleLowerCase()
    return capabilities.filter((item) => !keyword || item.name.toLocaleLowerCase().includes(keyword) || item.description.toLocaleLowerCase().includes(keyword) || item.token.slice(1).toLocaleLowerCase().includes(keyword) || capabilityKindLabel(item.kind).toLocaleLowerCase().includes(keyword))
  }, [capabilities, capabilityQuery])
  const selectedCapabilities = useMemo(() => capabilities.filter((item) => hasCapabilityToken(draft, item.token)), [capabilities, draft])
  const enabledCapabilityCount = capabilities.filter((item) => item.enabled).length
  const profileOptions = useMemo(() => data.modelProfiles.filter((item) => item.settings.runtime === runtime), [data.modelProfiles, runtime])
  const selectedProfile = profileOptions.find((item) => item.id === selectedProfileId)
  const displayedModel = session?.model || selectedProfile?.settings.model || (isCodexRuntime ? '使用 config.toml' : data.model.model || '未配置模型')
  useEffect(() => {
    if (!textareaRef.current) return
    textareaRef.current.style.height = 'auto'
    textareaRef.current.style.height = `${Math.min(textareaRef.current.scrollHeight, 180)}px`
  }, [draft])
  useEffect(() => { attachmentRef.current = attachments }, [attachments])
  useEffect(() => () => attachmentRef.current.forEach((item) => item.preview && URL.revokeObjectURL(item.preview)), [])
  useEffect(() => { setCapabilityIndex(0) }, [capabilityQuery])
  useEffect(() => { if (!session) setSelectedProfileId(data.activeModelProfileId) }, [data.activeModelProfileId, session?.id])
  useEffect(() => {
    if (session || workspace.trim()) return
    setWorkspaceState(data.runtime.workspace)
    setWorkspaceDraft(data.runtime.workspace)
  }, [data.runtime.workspace, session, workspace])
  useEffect(() => {
    if (!capabilityOpen) return
    const close = (event: PointerEvent) => {
      if (!composerRef.current?.contains(event.target as Node)) setCapabilityOpen(false)
    }
    document.addEventListener('pointerdown', close)
    return () => document.removeEventListener('pointerdown', close)
  }, [capabilityOpen])

  const addFiles = (files: FileList | File[]) => {
    if (sending || isActive(session?.status)) return
    const incoming = Array.from(files)
    if (!incoming.length) return
    setAttachmentError('')
    setAttachments((current) => {
      const next = [...current]
      let total = current.reduce((sum, item) => sum + item.file.size, 0)
      for (const file of incoming) {
        if (next.length >= 5) { setAttachmentError('每条消息最多添加 5 个附件'); break }
        if (file.size === 0) { setAttachmentError(`${file.name} 是空文件`); continue }
        if (file.size > 5 * 1024 * 1024) { setAttachmentError(`${file.name} 超过 5 MiB`); continue }
        if (!supportedAttachment(file)) { setAttachmentError(`${file.name} 暂不支持；请选择图片、UTF-8 文本/代码或 PDF`); continue }
        if (total + file.size > 10 * 1024 * 1024) { setAttachmentError('本条消息的附件总大小不能超过 10 MiB'); break }
        if (next.some((item) => item.file.name === file.name && item.file.size === file.size && item.file.lastModified === file.lastModified)) continue
        total += file.size
        next.push({ id: `${file.name}-${file.lastModified}-${crypto.randomUUID()}`, file, preview: file.type.startsWith('image/') ? URL.createObjectURL(file) : '' })
      }
      return next
    })
  }

  const removeAttachment = (id: string) => setAttachments((current) => current.filter((item) => {
    if (item.id !== id) return true
    if (item.preview) URL.revokeObjectURL(item.preview)
    return false
  }))

  const closeCapabilityPicker = () => {
    setCapabilityOpen(false)
    setCapabilityQuery('')
    setCapabilityRange(null)
  }

  const openCapabilityPicker = () => {
    if (sending || isActive(session?.status)) return
    const textarea = textareaRef.current
    setCapabilityRange({ start: textarea?.selectionStart ?? draft.length, end: textarea?.selectionEnd ?? draft.length })
    setCapabilityQuery('')
    setCapabilityOpen(true)
    window.setTimeout(() => capabilitySearchRef.current?.focus(), 0)
  }

  const insertCapability = (item: CapabilityOption) => {
    if (!item.enabled) return
    if (hasCapabilityToken(draft, item.token)) {
      closeCapabilityPicker()
      textareaRef.current?.focus()
      return
    }
    const range = capabilityRange || { start: draft.length, end: draft.length }
    const before = draft.slice(0, range.start)
    const after = draft.slice(range.end)
    const prefix = before && !/\s$/.test(before) ? ' ' : ''
    const suffix = after && /^\s/.test(after) ? '' : ' '
    const inserted = `${prefix}${item.token}${suffix}`
    const next = before + inserted + after
    const caret = before.length + inserted.length
    setDraft(next)
    closeCapabilityPicker()
    window.requestAnimationFrame(() => {
      textareaRef.current?.focus()
      textareaRef.current?.setSelectionRange(caret, caret)
    })
  }

  const removeCapability = (item: CapabilityOption) => {
    setDraft((current) => current.replace(item.token, '').replace(/ {2,}/g, ' ').trimStart())
  }

  const handleCapabilityKey = (event: KeyboardEvent) => {
    if (!capabilityOpen) return false
    if (event.key === 'Escape') {
      event.preventDefault()
      closeCapabilityPicker()
      textareaRef.current?.focus()
      return true
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const direction = event.key === 'ArrowDown' ? 1 : -1
      setCapabilityIndex((current) => visibleCapabilities.length ? (current + direction + visibleCapabilities.length) % visibleCapabilities.length : 0)
      return true
    }
    if (event.key === 'Enter' && visibleCapabilities[capabilityIndex]?.enabled) {
      event.preventDefault()
      insertCapability(visibleCapabilities[capabilityIndex])
      return true
    }
    return false
  }

  const updateDraft = (event: ChangeEvent<HTMLTextAreaElement>) => {
    const value = event.target.value
    const caret = event.target.selectionStart
    setDraft(value)
    const mention = capabilityMention(value, caret)
    if (mention) {
      setCapabilityRange({ start: mention.start, end: caret })
      setCapabilityQuery(mention.query)
      setCapabilityOpen(true)
    } else if (capabilityRange && document.activeElement === event.target) {
      closeCapabilityPicker()
    }
  }

  const send = async (preset?: string) => {
    const message = (preset ?? draft).trim()
    if ((!message && attachments.length === 0) || sending || isActive(session?.status)) return
    setSending(true); onError(''); setAttachmentError('')
    try {
      const payload = await Promise.all(attachments.map(encodeAttachment))
      const next = session ? await api.sendMessage(session.id, message, payload) : await api.createSession(message, payload, workspace.trim(), selectedProfileId)
      onSession(session ? mergeSessionSnapshot(session, next) : next); setDraft(''); setWorkspaceOpen(false); closeCapabilityPicker(); attachments.forEach((item) => item.preview && URL.revokeObjectURL(item.preview)); setAttachments([]); await onRefresh()
    } catch (reason) {
      const message = (reason as Error).message
      if (/附件|Base64|MiB|格式/.test(message)) setAttachmentError(message)
      else onError(message)
    } finally { setSending(false) }
  }

  const startSuggestion = (suggestion: StarterSuggestion) => {
    if (suggestion.attachment) {
      setDraft(suggestion.prompt)
      textareaRef.current?.focus()
      window.setTimeout(() => fileInputRef.current?.click(), 0)
      return
    }
    send(suggestion.prompt)
  }

  const applyWorkspace = () => {
    const value = workspaceDraft.trim() || data.runtime.workspace
    setWorkspaceState(value)
    setWorkspaceDraft(value)
    setWorkspaceOpen(false)
    window.localStorage.setItem('easyagent.workspace', value)
    textareaRef.current?.focus()
  }

  return {
    session, data, onOpenSkills, onOpenCapabilities,
    draft, setDraft, sending, attachments, attachmentError, dragging, setDragging,
    capabilityOpen, capabilityQuery, setCapabilityQuery, capabilityIndex, capabilitySearchRef,
    visibleCapabilities, selectedCapabilities, capabilities, enabledCapabilityCount,
    composerRef, textareaRef, fileInputRef, runtime, isCodexRuntime,
    workspace: session?.workspace || workspace, workspaceDraft, setWorkspaceDraft, workspaceOpen, setWorkspaceOpen, applyWorkspace,
    profileOptions, selectedProfileId, setSelectedProfileId, displayedModel,
    addFiles, removeAttachment, closeCapabilityPicker, openCapabilityPicker,
    insertCapability, removeCapability, handleCapabilityKey, updateDraft, send, startSuggestion,
  }
}
