import { useEffect, useRef, useState, type CSSProperties, type DragEvent } from 'react'
import { MEMO_SIDE_NOTE_DRAG_TYPE } from '../editor/BlockNoteMemoEditor'
import type { MemoSideNoteRecord } from '../types'

const NOTE_COLORS = [
  { label: '便签黄', value: '#fff7d6' },
  { label: '天空蓝', value: '#eaf6ff' },
  { label: '樱花粉', value: '#fff0f5' },
  { label: '薄荷绿', value: '#edfbf2' },
  { label: '淡紫色', value: '#f4efff' },
]

type DropPosition = 'before' | 'after'

type SideNoteRailProps = {
  activeBlockId: string | null
  activeNoteId: string | null
  notes: MemoSideNoteRecord[]
  open: boolean
  onAdd: (anchorBlockId: string | null) => void
  onAttach: (noteId: string, blockId: string | null) => void
  onClose: () => void
  onConvertToCallout: (note: MemoSideNoteRecord) => void
  onDelete: (noteId: string) => void
  onReorder: (sourceId: string, targetId: string, position: DropPosition) => void
  onSelect: (note: MemoSideNoteRecord) => void
  onUpdate: (noteId: string, patch: Partial<Pick<MemoSideNoteRecord, 'body_json' | 'collapsed' | 'color'>>) => void
}

export function SideNoteRail({
  activeBlockId,
  activeNoteId,
  notes,
  open,
  onAdd,
  onAttach,
  onClose,
  onConvertToCallout,
  onDelete,
  onReorder,
  onSelect,
  onUpdate,
}: SideNoteRailProps) {
  const railRef = useRef<HTMLElement | null>(null)
  const [draggingId, setDraggingId] = useState<string | null>(null)
  const [dropTarget, setDropTarget] = useState<{ id: string; position: DropPosition } | null>(null)
  const anchored = notes.filter((note) => note.anchor_block_id && note.status !== 'orphaned' && note.status !== 'archived')
  const documentNotes = notes.filter((note) => !note.anchor_block_id && note.status !== 'archived')
  const orphaned = notes.filter((note) => note.status === 'orphaned')

  useEffect(() => {
    if (!activeNoteId) return
    const frame = window.requestAnimationFrame(() => {
      railRef.current
        ?.querySelector<HTMLElement>(`[data-note-id="${CSS.escape(activeNoteId)}"]`)
        ?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [activeNoteId, open])

  return (
    <aside
      aria-label="侧边便签"
      className={`memo-side-note-rail${open ? ' memo-panel-open' : ''}`}
      data-layout-region="memo-card-rail"
      ref={railRef}
    >
      <header className="memo-panel-header">
        <div>
          <strong>侧边便签</strong>
          <span>{notes.filter((note) => note.status !== 'archived').length}</span>
        </div>
        <button aria-label="关闭便签栏" className="memo-icon-button memo-mobile-only" onClick={onClose} title="关闭" type="button">×</button>
      </header>

      <div className="memo-note-create-row">
        <button className="memo-command-button" onClick={() => onAdd(null)} type="button">+ 文档便签</button>
        <button className="memo-command-button" disabled={!activeBlockId} onClick={() => onAdd(activeBlockId)} type="button">+ 块便签</button>
      </div>

      <div className="memo-side-note-scroll">
        <SideNoteGroup
          activeBlockId={activeBlockId}
          activeNoteId={activeNoteId}
          dropTarget={dropTarget}
          label="块便签"
          notes={anchored}
          onAttach={onAttach}
          onConvertToCallout={onConvertToCallout}
          onDelete={onDelete}
          onDragEnd={() => { setDraggingId(null); setDropTarget(null) }}
          onDragOver={(note, event) => {
            if (!draggingId || draggingId === note.id) return
            event.preventDefault()
            const bounds = event.currentTarget.getBoundingClientRect()
            setDropTarget({ id: note.id, position: event.clientY - bounds.top < bounds.height / 2 ? 'before' : 'after' })
          }}
          onDragStart={(note, event) => {
            setDraggingId(note.id)
            event.dataTransfer.effectAllowed = 'move'
            event.dataTransfer.setData(MEMO_SIDE_NOTE_DRAG_TYPE, note.id)
          }}
          onDrop={(note, event) => {
            event.preventDefault()
            if (draggingId && draggingId !== note.id) onReorder(draggingId, note.id, dropTarget?.position ?? 'after')
            setDraggingId(null)
            setDropTarget(null)
          }}
          onSelect={onSelect}
          onUpdate={onUpdate}
        />
        <SideNoteGroup
          activeBlockId={activeBlockId}
          activeNoteId={activeNoteId}
          dropTarget={dropTarget}
          label="文档便签"
          notes={documentNotes}
          onAttach={onAttach}
          onConvertToCallout={onConvertToCallout}
          onDelete={onDelete}
          onDragEnd={() => { setDraggingId(null); setDropTarget(null) }}
          onDragOver={() => undefined}
          onDragStart={(note, event) => {
            setDraggingId(note.id)
            event.dataTransfer.effectAllowed = 'move'
            event.dataTransfer.setData(MEMO_SIDE_NOTE_DRAG_TYPE, note.id)
          }}
          onDrop={() => undefined}
          onSelect={onSelect}
          onUpdate={onUpdate}
        />
        <SideNoteGroup
          activeBlockId={activeBlockId}
          activeNoteId={activeNoteId}
          dropTarget={dropTarget}
          label="失去关联"
          notes={orphaned}
          onAttach={onAttach}
          onConvertToCallout={onConvertToCallout}
          onDelete={onDelete}
          onDragEnd={() => { setDraggingId(null); setDropTarget(null) }}
          onDragOver={() => undefined}
          onDragStart={(note, event) => {
            setDraggingId(note.id)
            event.dataTransfer.effectAllowed = 'move'
            event.dataTransfer.setData(MEMO_SIDE_NOTE_DRAG_TYPE, note.id)
          }}
          onDrop={() => undefined}
          onSelect={onSelect}
          onUpdate={onUpdate}
        />
      </div>
    </aside>
  )
}

type SideNoteGroupProps = {
  activeBlockId: string | null
  activeNoteId: string | null
  dropTarget: { id: string; position: DropPosition } | null
  label: string
  notes: MemoSideNoteRecord[]
  onAttach: (noteId: string, blockId: string | null) => void
  onConvertToCallout: (note: MemoSideNoteRecord) => void
  onDelete: (noteId: string) => void
  onDragEnd: () => void
  onDragOver: (note: MemoSideNoteRecord, event: DragEvent<HTMLElement>) => void
  onDragStart: (note: MemoSideNoteRecord, event: DragEvent<HTMLElement>) => void
  onDrop: (note: MemoSideNoteRecord, event: DragEvent<HTMLElement>) => void
  onSelect: (note: MemoSideNoteRecord) => void
  onUpdate: SideNoteRailProps['onUpdate']
}

function SideNoteGroup(props: SideNoteGroupProps) {
  if (props.notes.length === 0) return null
  return (
    <section className="memo-note-group">
      <h3>{props.label}<span>{props.notes.length}</span></h3>
      <div className="memo-note-list">
        {props.notes.map((note, index) => {
          const classNames = [
            'memo-side-note',
            note.id === props.activeNoteId ? 'memo-side-note-active' : '',
            props.dropTarget?.id === note.id ? `memo-side-note-drop-${props.dropTarget.position}` : '',
          ].filter(Boolean).join(' ')
          return (
            <article
              className={classNames}
              data-note-id={note.id}
              draggable
              key={note.id}
              onClick={() => props.onSelect(note)}
              onDragEnd={props.onDragEnd}
              onDragOver={(event) => props.onDragOver(note, event)}
              onDragStart={(event) => props.onDragStart(note, event)}
              onDrop={(event) => props.onDrop(note, event)}
              style={{ '--memo-note-color': note.color } as CSSProperties}
            >
              <header className="memo-side-note-toolbar">
                <button
                  aria-label={note.collapsed ? `展开便签 ${index + 1}` : `折叠便签 ${index + 1}`}
                  className="memo-note-title"
                  onClick={(event) => {
                    event.stopPropagation()
                    props.onUpdate(note.id, { collapsed: !note.collapsed })
                  }}
                  type="button"
                >
                  <span>{note.anchor_block_id ? '块便签' : '文档便签'}</span>
                  <small>{formatNoteTime(note.updated_at)}</small>
                </button>
                <div className="memo-note-actions">
                  <button
                    aria-label="转为正文提示块"
                    className="memo-icon-button"
                    disabled={!note.body_json?.text?.trim()}
                    onClick={(event) => {
                      event.stopPropagation()
                      props.onConvertToCallout(note)
                    }}
                    title="转为提示块"
                    type="button"
                  >↳</button>
                  <button
                    aria-label={note.anchor_block_id ? '解除正文关联' : '关联当前正文块'}
                    className="memo-icon-button"
                    disabled={!note.anchor_block_id && !props.activeBlockId}
                    onClick={(event) => {
                      event.stopPropagation()
                      props.onAttach(note.id, note.anchor_block_id ? null : props.activeBlockId)
                    }}
                    title={note.anchor_block_id ? '解除关联' : '关联当前块'}
                    type="button"
                  >{note.anchor_block_id ? '⌁' : '↗'}</button>
                  <button
                    aria-label={`删除便签 ${index + 1}`}
                    className="memo-icon-button memo-icon-button-danger"
                    onClick={(event) => {
                      event.stopPropagation()
                      props.onDelete(note.id)
                    }}
                    title="删除"
                    type="button"
                  >×</button>
                </div>
              </header>
              {!note.collapsed && (
                <>
                  <textarea
                    aria-label={`${props.label} ${index + 1} 内容`}
                    onChange={(event) => props.onUpdate(note.id, { body_json: { text: event.target.value } })}
                    onClick={(event) => event.stopPropagation()}
                    value={note.body_json?.text ?? ''}
                  />
                  <div className="memo-note-colors" onClick={(event) => event.stopPropagation()}>
                    {NOTE_COLORS.map((color) => (
                      <button
                        aria-label={`设为${color.label}`}
                        aria-pressed={note.color === color.value}
                        key={color.value}
                        onClick={() => props.onUpdate(note.id, { color: color.value })}
                        style={{ '--memo-note-swatch': color.value } as CSSProperties}
                        title={color.label}
                        type="button"
                      />
                    ))}
                  </div>
                </>
              )}
            </article>
          )
        })}
      </div>
    </section>
  )
}

function formatNoteTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(date)
}
