import { useEffect, useRef } from 'react'
import type { StewardConversation } from '../types'
import { formatDate } from './model'

type Props = {
  open: boolean
  conversations: StewardConversation[]
  loading: boolean
  busyId: string
  error: string
  onClose: () => void
  onRestore: (conversation: StewardConversation) => void
}

export function ArchivedConversationsDialog({
  open,
  conversations,
  loading,
  busyId,
  error,
  onClose,
  onRestore,
}: Props) {
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const dialogRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!open) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    closeRef.current?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
      if (event.key !== 'Tab') return
      const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', onKeyDown)
    }
  }, [onClose, open])

  if (!open) return null

  return (
    <div
      className="steward-archive-modal"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <section aria-labelledby="steward-archive-title" aria-modal="true" className="steward-archive-dialog" ref={dialogRef} role="dialog">
        <header className="steward-archive-dialog-header">
          <div>
            <h2 id="steward-archive-title">归档对话</h2>
            <small>归档只会隐藏对话，不会删除消息、任务或执行证据。</small>
          </div>
          <button aria-label="关闭归档对话" className="steward-archive-close" onClick={onClose} ref={closeRef} type="button">关闭</button>
        </header>

        <div aria-busy={loading} className="steward-archive-list">
          {loading ? <div className="steward-archive-state" role="status">正在加载归档对话…</div> : null}
          {!loading && error ? <div className="steward-archive-state is-error" role="alert">{error}</div> : null}
          {!loading && !error && conversations.length === 0 ? (
            <div className="steward-archive-state">
              <strong>暂无归档对话</strong>
              <span>你归档的对话会显示在这里，并可随时恢复。</span>
            </div>
          ) : null}
          {!loading && conversations.map((conversation) => (
            <article className="steward-archive-item" key={conversation.id}>
              <div>
                <strong title={conversation.title}>{conversation.title}</strong>
                <small>
                  {conversation.message_count} 条消息 · 归档于 {formatDate(conversation.archived_at || conversation.updated_at)}
                </small>
              </div>
              <button
                className="steward-button steward-button-secondary"
                disabled={Boolean(busyId)}
                onClick={() => onRestore(conversation)}
                type="button"
              >
                {busyId === conversation.id ? '恢复中' : '恢复'}
              </button>
            </article>
          ))}
        </div>
      </section>
    </div>
  )
}
