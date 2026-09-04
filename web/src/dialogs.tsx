import { useEffect, useRef } from 'react'
import { TrashIcon } from './ui'
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
