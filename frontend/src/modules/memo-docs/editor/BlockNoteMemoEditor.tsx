import type { PartialBlock } from '@blocknote/core'
import { filterSuggestionItems, insertOrUpdateBlockForSlashMenu } from '@blocknote/core/extensions'
import '@blocknote/core/fonts/inter.css'
import { BlockNoteView } from '@blocknote/mantine'
import '@blocknote/mantine/style.css'
import {
  SuggestionMenuController,
  getDefaultReactSlashMenuItems,
  useCreateBlockNote,
} from '@blocknote/react'
import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from 'react'
import { memoBlockNoteSchema, type MemoBlockNoteEditor } from './schema'
import type { MemoEditorHandle, MemoEditorProps } from './types'
import type { MemoEditorSnapshot } from '../types'

export const MEMO_SIDE_NOTE_DRAG_TYPE = 'application/x-mongojson-memo-side-note'

function getActiveBlockId(editor: MemoBlockNoteEditor) {
  try {
    return editor.getTextCursorPosition().block.id
  } catch {
    return null
  }
}

type StructuralBlock = {
  id: string
  type: string
  content?: unknown
  children?: unknown[]
}

function isEmptyParagraph(block: StructuralBlock | undefined) {
  if (!block || block.type !== 'paragraph' || (block.children?.length ?? 0) > 0) return false
  if (!Array.isArray(block.content) || block.content.length === 0) return true
  return block.content.every((item) => {
    if (!item || typeof item !== 'object') return false
    const inline = item as { type?: string; text?: string }
    return inline.type === 'text' && !inline.text
  })
}

function getBlockText(block: StructuralBlock) {
  if (!Array.isArray(block.content)) return ''
  return block.content.map((item) => {
    if (!item || typeof item !== 'object') return ''
    return (item as { text?: string }).text ?? ''
  }).join('').replace(/\r\n?/g, '\n')
}

async function writeClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    return
  } catch {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    textarea.remove()
  }
}

function getCanonicalBlocks(editor: MemoBlockNoteEditor) {
  const blocks = editor.document
  return isEmptyParagraph(blocks.at(-1) as StructuralBlock | undefined) ? blocks.slice(0, -1) : blocks
}

function getCanonicalActiveBlockId(editor: MemoBlockNoteEditor) {
  const activeBlockId = getActiveBlockId(editor)
  const lastBlock = editor.document.at(-1) as StructuralBlock | undefined
  return activeBlockId && activeBlockId === lastBlock?.id && isEmptyParagraph(lastBlock) ? null : activeBlockId
}

function ensureEditorTail(editor: MemoBlockNoteEditor) {
  const lastBlock = editor.document.at(-1) as StructuralBlock | undefined
  if (!lastBlock || isEmptyParagraph(lastBlock)) return false
  editor.insertBlocks([{ type: 'paragraph' }], lastBlock.id, 'after')
  return true
}

function getSnapshot(editor: MemoBlockNoteEditor): MemoEditorSnapshot {
  const blocks = getCanonicalBlocks(editor)
  return {
    blocks: JSON.parse(JSON.stringify(blocks)) as unknown[],
    markdown: editor.blocksToMarkdownLossy(blocks),
    html: editor.blocksToHTMLLossy(blocks),
    activeBlockId: getCanonicalActiveBlockId(editor),
  }
}

function createCustomSlashItems(editor: MemoBlockNoteEditor) {
  return [
    ...getDefaultReactSlashMenuItems(editor),
    {
      title: '提示块',
      subtext: '插入可强调重要信息的 Callout',
      aliases: ['callout', '提示', '提醒'],
      group: '文档能力',
      onItemClick: () => insertOrUpdateBlockForSlashMenu(editor, { type: 'callout', props: { tone: 'info' } }),
    },
    {
      title: '文件卡片',
      subtext: '插入一个附件引用卡片',
      aliases: ['file', '附件', '文件'],
      group: '文档能力',
      onItemClick: () => insertOrUpdateBlockForSlashMenu(editor, { type: 'fileCard' }),
    },
    {
      title: '嵌入链接',
      subtext: '插入外部资源引用',
      aliases: ['embed', '嵌入', '链接'],
      group: '文档能力',
      onItemClick: () => insertOrUpdateBlockForSlashMenu(editor, { type: 'embed' }),
    },
    {
      title: '无界画布引用',
      subtext: '关联到无界画布模块',
      aliases: ['canvas', '画布', '白板'],
      group: '文档能力',
      onItemClick: () => insertOrUpdateBlockForSlashMenu(editor, { type: 'canvasReference' }),
    },
  ]
}

export const BlockNoteMemoEditor = forwardRef<MemoEditorHandle, MemoEditorProps>(
  function BlockNoteMemoEditor({
    activeNoteBlockId,
    initialBlocks,
    legacyHTML,
    legacyMarkdown,
    noteCounts,
    notePreviews,
    onActiveBlockChange,
    onChange,
    onDropSideNote,
    onOpenBlockNotes,
    onReady,
    onUpload,
  }, ref) {
    const hostRef = useRef<HTMLDivElement | null>(null)
    const callbacksRef = useRef({ onActiveBlockChange, onChange, onDropSideNote, onOpenBlockNotes, onReady, onUpload })
    const copyTimerRef = useRef<number | null>(null)
    const snapshotTimerRef = useRef<number | null>(null)
    const isReadyRef = useRef(false)
    const [markerRevision, setMarkerRevision] = useState(0)
    const [markers, setMarkers] = useState<Array<{ id: string; count: number; previews: string[]; top: number; active: boolean }>>([])
    const [codeControls, setCodeControls] = useState<Array<{ id: string; code: string; top: number; right: number }>>([])
    const [copiedCodeBlockId, setCopiedCodeBlockId] = useState<string | null>(null)
    callbacksRef.current = { onActiveBlockChange, onChange, onDropSideNote, onOpenBlockNotes, onReady, onUpload }

    const editor = useCreateBlockNote({
      schema: memoBlockNoteSchema,
      initialContent: (initialBlocks.length > 0 ? initialBlocks : [{ type: 'paragraph' }]) as PartialBlock[],
      uploadFile: (file) => callbacksRef.current.onUpload(file),
    })

    useEffect(() => {
      if (isReadyRef.current) return
      if (initialBlocks.length === 0) {
        const migratedBlocks = legacyHTML.trim()
          ? editor.tryParseHTMLToBlocks(legacyHTML)
          : legacyMarkdown.trim()
            ? editor.tryParseMarkdownToBlocks(legacyMarkdown)
            : []
        if (migratedBlocks.length > 0) {
          editor.replaceBlocks(editor.document, migratedBlocks)
        }
      }
      ensureEditorTail(editor)
      isReadyRef.current = true
      snapshotTimerRef.current = window.setTimeout(() => {
        snapshotTimerRef.current = null
        callbacksRef.current.onReady(getSnapshot(editor))
      }, 0)
    }, [editor, initialBlocks.length, legacyHTML, legacyMarkdown])

    useEffect(() => () => {
      if (snapshotTimerRef.current) window.clearTimeout(snapshotTimerRef.current)
      if (copyTimerRef.current) window.clearTimeout(copyTimerRef.current)
    }, [])

    useEffect(() => {
      const host = hostRef.current
      if (!host) return
      let frame = 0
      const measureMarkers = () => {
        const hostBounds = host.getBoundingClientRect()
        const nextMarkers = Array.from(host.querySelectorAll<HTMLElement>('[data-node-type="blockOuter"][data-id]'))
          .flatMap((element) => {
            const id = element.dataset.id
            const count = id ? (noteCounts[id] ?? 0) : 0
            if (!id || count === 0) return []
            return [{
              id,
              count,
              previews: notePreviews[id] ?? [],
              top: element.getBoundingClientRect().top - hostBounds.top + 4,
              active: id === activeNoteBlockId,
            }]
          })
        setMarkers(nextMarkers)
        const nextCodeControls = (editor.document as StructuralBlock[]).flatMap((block) => {
          if (block.type !== 'codeBlock') return []
          const element = host.querySelector<HTMLElement>(
            `[data-node-type="blockOuter"][data-id="${CSS.escape(block.id)}"] .bn-block-content[data-content-type="codeBlock"]`,
          )
          if (!element) return []
          const bounds = element.getBoundingClientRect()
          return [{
            id: block.id,
            code: getBlockText(block),
            top: bounds.top - hostBounds.top + 8,
            right: hostBounds.right - bounds.right + 8,
          }]
        })
        setCodeControls(nextCodeControls)
      }
      const scheduleMeasure = () => {
        window.cancelAnimationFrame(frame)
        frame = window.requestAnimationFrame(measureMarkers)
      }
      const scrollElement = host.querySelector<HTMLElement>('.bn-editor')
      scheduleMeasure()
      scrollElement?.addEventListener('scroll', scheduleMeasure, { passive: true })
      window.addEventListener('resize', scheduleMeasure)
      return () => {
        window.cancelAnimationFrame(frame)
        scrollElement?.removeEventListener('scroll', scheduleMeasure)
        window.removeEventListener('resize', scheduleMeasure)
      }
    }, [activeNoteBlockId, editor, markerRevision, noteCounts, notePreviews])

    useImperativeHandle(ref, () => ({
      focus() {
        editor.focus()
      },
      focusBlock(blockId: string) {
        try {
          editor.setTextCursorPosition(blockId, 'start')
          editor.focus()
          hostRef.current?.querySelector<HTMLElement>(`[data-node-type="blockOuter"][data-id="${CSS.escape(blockId)}"]`)?.scrollIntoView({
            behavior: 'smooth', block: 'center',
          })
        } catch {
          // The note can remain orphaned if its source block no longer exists.
        }
      },
      getSnapshot() {
        return getSnapshot(editor)
      },
      insertCallout(text: string) {
        const referenceBlock = getActiveBlockId(editor) ?? editor.document.at(-1)?.id
        if (!referenceBlock) return
        editor.insertBlocks(
          [{ type: 'callout', props: { tone: 'info' }, content: text }],
          referenceBlock,
          'after',
        )
      },
      replaceBlocks(blocks: unknown[]) {
        const nextBlocks = blocks.length > 0 ? blocks : [{ type: 'paragraph' }]
        editor.replaceBlocks(editor.document, nextBlocks as never)
      },
      replaceFromHTML(html: string) {
        const nextBlocks = editor.tryParseHTMLToBlocks(html)
        editor.replaceBlocks(editor.document, nextBlocks.length > 0 ? nextBlocks : [{ type: 'paragraph' }])
      },
      replaceFromMarkdown(markdown: string) {
        const nextBlocks = editor.tryParseMarkdownToBlocks(markdown)
        editor.replaceBlocks(editor.document, nextBlocks.length > 0 ? nextBlocks : [{ type: 'paragraph' }])
      },
    }), [editor])

    const handleDrop = (event: React.DragEvent<HTMLDivElement>) => {
      const noteId = event.dataTransfer.getData(MEMO_SIDE_NOTE_DRAG_TYPE)
      if (!noteId) return
      const block = (event.target as HTMLElement).closest<HTMLElement>('[data-node-type="blockOuter"][data-id]')
      const blockId = block?.dataset.id
      if (!blockId) return
      event.preventDefault()
      event.stopPropagation()
      callbacksRef.current.onDropSideNote(noteId, blockId)
    }

    return (
      <div
        className="memo-block-editor"
        data-layout-region="memo-block-editor"
        onDragOverCapture={(event) => {
          if (event.dataTransfer.types.includes(MEMO_SIDE_NOTE_DRAG_TYPE)) event.preventDefault()
        }}
        onDropCapture={handleDrop}
        ref={hostRef}
      >
        <BlockNoteView
          editor={editor}
          onChange={() => {
            if (isReadyRef.current) {
              setMarkerRevision((value) => value + 1)
              if (snapshotTimerRef.current) window.clearTimeout(snapshotTimerRef.current)
              snapshotTimerRef.current = window.setTimeout(() => {
                snapshotTimerRef.current = null
                ensureEditorTail(editor)
                callbacksRef.current.onChange(getSnapshot(editor))
              }, 0)
            }
          }}
          onSelectionChange={() => callbacksRef.current.onActiveBlockChange(getCanonicalActiveBlockId(editor))}
          slashMenu={false}
          theme="light"
        >
          <SuggestionMenuController
            getItems={async (query) => filterSuggestionItems(createCustomSlashItems(editor), query)}
            triggerCharacter="/"
          />
        </BlockNoteView>
        <div className="memo-block-note-markers">
          {markers.map((marker) => (
            <button
              aria-label={`此正文块有 ${marker.count} 条便签，点击查看`}
              className={marker.active ? 'memo-block-note-marker memo-block-note-marker-active' : 'memo-block-note-marker'}
              key={marker.id}
              onClick={() => callbacksRef.current.onOpenBlockNotes(marker.id)}
              style={{ top: `${marker.top}px` }}
              title={`查看 ${marker.count} 条块便签`}
              type="button"
            >
              <span aria-hidden="true" className="memo-block-note-preview">
                {marker.previews.slice(0, 2).map((preview, index) => <span key={`${marker.id}-${index}`}>{preview}</span>)}
                {marker.count > 2 && <small>还有 {marker.count - 2} 条</small>}
              </span>
              <span aria-hidden="true" className="memo-block-note-count">{marker.count}</span>
            </button>
          ))}
        </div>
        <div className="memo-code-copy-controls">
          {codeControls.map((control) => (
            <button
              aria-label="复制代码块内容"
              className="memo-code-copy-button"
              key={control.id}
              onClick={() => {
                void writeClipboard(control.code).then(() => {
                  setCopiedCodeBlockId(control.id)
                  if (copyTimerRef.current) window.clearTimeout(copyTimerRef.current)
                  copyTimerRef.current = window.setTimeout(() => setCopiedCodeBlockId(null), 1500)
                })
              }}
              style={{ top: `${control.top}px`, right: `${control.right}px` }}
              title="复制代码"
              type="button"
            >{copiedCodeBlockId === control.id ? '已复制' : '复制'}</button>
          ))}
        </div>
      </div>
    )
  },
)
