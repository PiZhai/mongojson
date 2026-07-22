import { useMemo, useState, type RefObject } from 'react'
import type { MemoDocumentSummary } from '../types'
import { MemoIcon } from './MemoIcon'

type MemoDocumentDrawerProps = {
  activeDocumentId?: string
  closeButtonRef: RefObject<HTMLButtonElement | null>
  creating: boolean
  documents: MemoDocumentSummary[]
  error: string
  loading: boolean
  open: boolean
  pinned: boolean
  switchingDocumentId?: string
  onClose: () => void
  onCreate: () => void
  onSelect: (document: MemoDocumentSummary) => void
}

function formatArchiveDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

export function MemoDocumentDrawer({
  activeDocumentId,
  closeButtonRef,
  creating,
  documents,
  error,
  loading,
  open,
  pinned,
  switchingDocumentId,
  onClose,
  onCreate,
  onSelect,
}: MemoDocumentDrawerProps) {
  const [query, setQuery] = useState('')
  const visibleDocuments = useMemo(() => {
    const normalizedQuery = query.trim().toLocaleLowerCase('zh-CN')
    if (!normalizedQuery) return documents
    return documents.filter((document) => (
      document.title.toLocaleLowerCase('zh-CN').includes(normalizedQuery)
      || document.slug.toLocaleLowerCase('zh-CN').includes(normalizedQuery)
    ))
  }, [documents, query])

  return (
    <aside
      aria-hidden={!open}
      aria-label="文档库"
      className={`memo-document-drawer${open ? ' memo-document-drawer-open' : ''}${pinned ? ' memo-document-drawer-pinned' : ''}`}
      id="memo-document-drawer"
    >
      <header className="memo-document-drawer-header">
        <div>
          <span className="memo-document-drawer-icon"><MemoIcon name="archive" /></span>
          <span><strong>文档库</strong><small>{documents.length} 个文档</small></span>
        </div>
        {!pinned ? (
          <button aria-label="关闭文档库" className="memo-icon-button" onClick={onClose} ref={closeButtonRef} title="关闭" type="button">
            <MemoIcon name="close" />
          </button>
        ) : null}
      </header>

      <div className="memo-document-create-area">
        <button
          aria-busy={creating}
          className="memo-document-create"
          disabled={creating || Boolean(switchingDocumentId)}
          onClick={onCreate}
          type="button"
        >
          <MemoIcon name="plus" />
          <span>{creating ? '正在新建…' : '新建文档'}</span>
        </button>
        <label className="memo-document-search">
          <span className="sr-only">搜索文档</span>
          <MemoIcon name="search" />
          <input aria-label="搜索文档" onChange={(event) => setQuery(event.target.value)} placeholder="搜索标题或标识" type="search" value={query} />
        </label>
      </div>

      <nav aria-label="文档列表" className="memo-document-list">
        {loading ? <p className="memo-document-list-state" role="status">正在载入文档…</p> : null}
        {!loading && error ? <p className="memo-document-list-state memo-document-list-error" role="alert">{error}</p> : null}
        {!loading && !error && documents.length === 0 ? <p className="memo-document-list-state">还没有文档</p> : null}
        {!loading && !error && documents.length > 0 && visibleDocuments.length === 0 ? <p className="memo-document-list-state">没有匹配的文档</p> : null}
        {!loading && !error ? visibleDocuments.map((item) => {
          const active = item.id === activeDocumentId
          const switching = item.id === switchingDocumentId
          return (
            <button
              aria-current={active ? 'page' : undefined}
              className={`memo-document-item${active ? ' memo-document-item-active' : ''}`}
              disabled={creating || Boolean(switchingDocumentId)}
              key={item.id}
              onClick={() => onSelect(item)}
              type="button"
            >
              <span className="memo-document-item-icon"><MemoIcon name="document" /></span>
              <span className="memo-document-item-copy">
                <strong>{item.title.trim() || '未命名文档'}</strong>
                <small>{switching ? '正在切换…' : `${formatArchiveDate(item.updated_at)} · ${item.note_count} 便签`}</small>
              </span>
              {active ? <span className="memo-document-current">当前</span> : null}
            </button>
          )
        }) : null}
      </nav>
    </aside>
  )
}
