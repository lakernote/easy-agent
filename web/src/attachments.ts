import type { AttachmentInput } from './types'
export type PendingAttachment = { id: string; file: File; preview: string }
export const attachmentAccept = 'image/png,image/jpeg,image/webp,image/gif,text/*,application/json,application/xml,application/pdf,.md,.log,.csv,.yaml,.yml,.go,.java,.py,.js,.ts,.tsx,.jsx,.css,.html,.sh,.sql,.properties,.toml,.ini,.conf'
const textAttachmentExtensions = new Set(['txt', 'md', 'log', 'csv', 'json', 'xml', 'yaml', 'yml', 'go', 'java', 'py', 'js', 'ts', 'tsx', 'jsx', 'css', 'html', 'sh', 'sql', 'properties', 'toml', 'ini', 'conf'])
let attachmentSequence = 0

// crypto.randomUUID() is not exposed by some HTTP pages, older browsers and
// embedded WebViews. Attachments only need a client-side list key, so keep a
// secure path when available and degrade gracefully everywhere else.
export function createAttachmentID() {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID()
  if (typeof globalThis.crypto?.getRandomValues === 'function') {
    const values = new Uint32Array(3)
    globalThis.crypto.getRandomValues(values)
    return `attachment-${values[0].toString(36)}-${values[1].toString(36)}-${values[2].toString(36)}`
  }
  attachmentSequence += 1
  return `attachment-${Date.now().toString(36)}-${attachmentSequence.toString(36)}-${Math.random().toString(36).slice(2)}`
}

export function supportedAttachment(file: File) {
  const extension = file.name.split('.').pop()?.toLowerCase() || ''
  return ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'application/pdf', 'application/json', 'application/xml', 'application/javascript', 'application/yaml', 'application/x-yaml'].includes(file.type)
    || file.type.startsWith('text/') || textAttachmentExtensions.has(extension)
}

export function encodeAttachment(item: PendingAttachment): Promise<AttachmentInput> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(new Error(`无法读取附件 ${item.file.name}`))
    reader.onload = () => {
      const value = String(reader.result || '')
      const marker = value.indexOf(',')
      if (marker < 0) { reject(new Error(`无法编码附件 ${item.file.name}`)); return }
      resolve({ name: item.file.name, mimeType: item.file.type || 'application/octet-stream', size: item.file.size, data: value.slice(marker + 1) })
    }
    reader.readAsDataURL(item.file)
  })
}

export function attachmentTypeLabel(file: File) {
  if (file.type.startsWith('image/')) return '图片'
  if (file.type === 'application/pdf') return 'PDF'
  return '文本文件'
}

export function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(value < 10 * 1024 ? 1 : 0)} KiB`
  return `${(value / 1024 / 1024).toFixed(1)} MiB`
}
