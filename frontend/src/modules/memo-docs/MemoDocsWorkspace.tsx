import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from 'react'
import './styles.css'
import { ApiRequestError } from '../../platform/http/client'
import { StatusBanner } from '../../components/common/StatusBanner'
import type { ToolStatus } from '../../shared/ui/toolStatus'
import {
  createMemoDocument,
  createMemoSideNote,
  deleteMemoSideNote,
  getFileDownloadUrl,
  getMemoDocument,
  listMemoSideNotes,
  saveMemoDocument,
  saveMemoSideNote,
  uploadFile,
} from './api'
import { BlockNoteMemoEditor } from './editor/BlockNoteMemoEditor'
import type { MemoEditorHandle } from './editor/types'
import { getBlockOrder, getMemoOutline, getMemoStats, normalizeBlockDocument } from './lib/document'
import { clearMemoRecovery, loadMemoRecovery, saveMemoRecovery, type MemoRecoverySnapshot } from './lib/recovery'
import { SideNoteRail } from './side-notes/SideNoteRail'
import type { MemoDocumentRecord, MemoEditorSnapshot, MemoSideNoteRecord, MemoWorkspaceMode } from './types'

const MEMO_SLUG = 'inbox'
const DOCUMENT_SCHEMA_VERSION = 1
const WORKSPACE_MODE_KEY = 'mongojson.memoDocs.workspaceMode'
const OUTLINE_WIDTH_KEY = 'mongojson.memoDocs.outlineWidth'
const NOTE_RAIL_WIDTH_KEY = 'mongojson.memoDocs.noteRailWidth'
const SAVE_DELAY_MS = 1000
const NOTE_SAVE_DELAY_MS = 650

function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

function readStoredMode(): MemoWorkspaceMode {
  const value = localStorage.getItem(WORKSPACE_MODE_KEY)
  return value === 'focus' || value === 'wide' ? value : 'standard'
}

function readStoredWidth(key: string, fallback: number, min: number, max: number) {
  const value = Number.parseInt(localStorage.getItem(key) ?? '', 10)
  return Number.isFinite(value) ? Math.min(max, Math.max(min, value)) : fallback
}

function downloadText(name: string, content: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = name
  anchor.click()
  URL.revokeObjectURL(url)
}

function updateNoteList(
  current: MemoSideNoteRecord[],
  noteId: string,
  updater: (note: MemoSideNoteRecord) => MemoSideNoteRecord,
) {
  return current.map((note) => (note.id === noteId ? updater(note) : note))
}

export function MemoDocsWorkspace() {
  const memoSlug = new URLSearchParams(window.location.search).get('slug')?.trim() || MEMO_SLUG
  const editorRef = useRef<MemoEditorHandle | null>(null)
  const importInputRef = useRef<HTMLInputElement | null>(null)
  const documentRef = useRef<MemoDocumentRecord | null>(null)
  const snapshotRef = useRef<MemoEditorSnapshot>({ blocks: [], markdown: '', html: '', activeBlockId: null })
  const notesRef = useRef<MemoSideNoteRecord[]>([])
  const saveTimerRef = useRef<number | null>(null)
  const saveInFlightRef = useRef(false)
  const saveQueuedRef = useRef(false)
  const noteTimersRef = useRef(new Map<string, number>())
  const noteSaveInFlightRef = useRef(new Set<string>())
  const conflictRef = useRef(false)
  const [document, setDocument] = useState<MemoDocumentRecord | null>(null)
  const [editorSeed, setEditorSeed] = useState<unknown[]>([])
  const [editorKey, setEditorKey] = useState(0)
  const [snapshot, setSnapshot] = useState<MemoEditorSnapshot>({ blocks: [], markdown: '', html: '', activeBlockId: null })
  const [notes, setNotes] = useState<MemoSideNoteRecord[]>([])
  const [activeBlockId, setActiveBlockId] = useState<string | null>(null)
  const [activeNoteId, setActiveNoteId] = useState<string | null>(null)
  const [workspaceMode, setWorkspaceMode] = useState<MemoWorkspaceMode>(readStoredMode)
  const [outlineOpen, setOutlineOpen] = useState(false)
  const [notesOpen, setNotesOpen] = useState(false)
  const [outlineWidth, setOutlineWidth] = useState(() => readStoredWidth(OUTLINE_WIDTH_KEY, 200, 160, 280))
  const [noteRailWidth, setNoteRailWidth] = useState(() => readStoredWidth(NOTE_RAIL_WIDTH_KEY, 380, 320, 460))
  const [recovery, setRecovery] = useState<MemoRecoverySnapshot | null>(null)
  const [conflict, setConflict] = useState(false)
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '正在载入随手记。' })
  const [isSaving, setIsSaving] = useState(false)

  const commitNotes = useCallback((updater: (current: MemoSideNoteRecord[]) => MemoSideNoteRecord[]) => {
    const next = updater(notesRef.current)
    notesRef.current = next
    setNotes(next)
    return next
  }, [])

  const reloadDocument = useCallback(async () => {
    setStatus({ kind: 'idle', message: '正在载入随手记。' })
    try {
      const response = await getMemoDocument(memoSlug)
      const loadedDocument = response.document
      const noteResponse = await listMemoSideNotes(loadedDocument.id)
      const structuredBlocks = loadedDocument.editor_type === 'blocknote'
        ? normalizeBlockDocument(loadedDocument.content_json)
        : []
      documentRef.current = loadedDocument
      snapshotRef.current = { blocks: structuredBlocks, markdown: loadedDocument.content_text, html: loadedDocument.content_html, activeBlockId: null }
      notesRef.current = noteResponse.notes
      setDocument(loadedDocument)
      setEditorSeed(structuredBlocks)
      setSnapshot(snapshotRef.current)
      setNotes(noteResponse.notes)
      setEditorKey((value) => value + 1)
      conflictRef.current = false
      setConflict(false)
      setRecovery(await loadMemoRecovery(loadedDocument.id).catch(() => null))
      setStatus({ kind: 'success', message: loadedDocument.editor_type === 'blocknote' ? '结构化随手记已加载。' : '旧版随手记已加载，正在准备无损迁移。' })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? error.message : '加载随手记失败。' })
    }
  }, [memoSlug])

  useEffect(() => {
    const timer = window.setTimeout(() => void reloadDocument(), 0)
    return () => window.clearTimeout(timer)
  }, [reloadDocument])

  useEffect(() => () => {
    if (saveTimerRef.current) window.clearTimeout(saveTimerRef.current)
    noteTimersRef.current.forEach((timer) => window.clearTimeout(timer))
  }, [])

  async function saveDocumentNow(nextSnapshot: MemoEditorSnapshot) {
    const currentDocument = documentRef.current
    if (!currentDocument || conflictRef.current) return
    if (saveInFlightRef.current) {
      saveQueuedRef.current = true
      return
    }
    saveInFlightRef.current = true
    setIsSaving(true)
    setStatus({ kind: 'idle', message: '正在自动保存结构化文档。' })
    try {
      const response = await saveMemoDocument(currentDocument.id, {
        title: currentDocument.title || '随手记',
        content_json: nextSnapshot.blocks,
        content_markdown: nextSnapshot.markdown,
        content_html: nextSnapshot.html,
        schema_version: DOCUMENT_SCHEMA_VERSION,
        revision: currentDocument.revision,
        editor_type: 'blocknote',
      })
      documentRef.current = response.document
      setDocument(response.document)
      const synchronizedNotes = await listMemoSideNotes(response.document.id)
      commitNotes(() => synchronizedNotes.notes)
      await clearMemoRecovery(response.document.id).catch(() => undefined)
      setRecovery(null)
      setStatus({ kind: 'success', message: '文档已自动保存。' })
    } catch (error) {
      if (error instanceof ApiRequestError && error.status === 409) {
        conflictRef.current = true
        setConflict(true)
        setStatus({ kind: 'warning', message: '检测到远端版本冲突，当前内容仍保留在本地恢复区。' })
      } else {
        setStatus({ kind: 'error', message: error instanceof Error ? `保存失败：${error.message}` : '保存失败。' })
      }
    } finally {
      saveInFlightRef.current = false
      setIsSaving(false)
      if (saveQueuedRef.current && !conflictRef.current) {
        saveQueuedRef.current = false
        window.setTimeout(() => void saveDocumentNow(snapshotRef.current), 0)
      }
    }
  }

  function scheduleDocumentSave(nextSnapshot = snapshotRef.current) {
    if (saveTimerRef.current) window.clearTimeout(saveTimerRef.current)
    saveTimerRef.current = window.setTimeout(() => void saveDocumentNow(nextSnapshot), SAVE_DELAY_MS)
  }

  function handleEditorSnapshot(nextSnapshot: MemoEditorSnapshot) {
    snapshotRef.current = nextSnapshot
    setSnapshot(nextSnapshot)
    setActiveBlockId(nextSnapshot.activeBlockId)
    const currentDocument = documentRef.current
    if (currentDocument) {
      void saveMemoRecovery({
        ...nextSnapshot,
        documentId: currentDocument.id,
        revision: currentDocument.revision,
        title: currentDocument.title,
        savedAt: new Date().toISOString(),
      }).catch(() => undefined)
    }
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleDocumentSave(nextSnapshot)
  }

  function handleEditorReady(nextSnapshot: MemoEditorSnapshot) {
    snapshotRef.current = nextSnapshot
    setSnapshot(nextSnapshot)
    if (documentRef.current?.editor_type !== 'blocknote') {
      setStatus({ kind: 'warning', message: '旧版内容已转换为结构化块，正在创建迁移备份。' })
      void saveDocumentNow(nextSnapshot)
    }
  }

  const handleTitleChange = (title: string) => {
    if (!documentRef.current) return
    const next = { ...documentRef.current, title }
    documentRef.current = next
    setDocument(next)
    scheduleDocumentSave()
  }

  async function handleUpload(file: File) {
    setStatus({ kind: 'idle', message: '文件正在上传。' })
    const response = await uploadFile(file)
    setStatus({ kind: 'success', message: '文件已上传。' })
    return getFileDownloadUrl(response.file.id)
  }

  async function persistSideNote(noteId: string) {
    if (noteSaveInFlightRef.current.has(noteId)) return
    const requestNote = notesRef.current.find((note) => note.id === noteId)
    if (!requestNote) return
    noteSaveInFlightRef.current.add(noteId)
    try {
      const response = await saveMemoSideNote(noteId, {
        anchor_block_id: requestNote.anchor_block_id ?? null,
        body_json: requestNote.body_json,
        color: requestNote.color,
        sort_order: requestNote.sort_order,
        collapsed: requestNote.collapsed,
        status: requestNote.status,
        revision: requestNote.revision,
      })
      const latest = notesRef.current.find((note) => note.id === noteId)
      const changedDuringSave = latest && latest.updated_at !== requestNote.updated_at
      commitNotes((items) => updateNoteList(items, noteId, (note) => changedDuringSave
        ? { ...note, revision: response.note.revision }
        : response.note))
      if (changedDuringSave) {
        window.setTimeout(() => void persistSideNote(noteId), 0)
      }
    } catch (error) {
      setStatus({
        kind: error instanceof ApiRequestError && error.status === 409 ? 'warning' : 'error',
        message: error instanceof Error ? `便签保存失败：${error.message}` : '便签保存失败。',
      })
    } finally {
      noteSaveInFlightRef.current.delete(noteId)
    }
  }

  function scheduleSideNoteSave(noteId: string) {
    const previous = noteTimersRef.current.get(noteId)
    if (previous) window.clearTimeout(previous)
    noteTimersRef.current.set(noteId, window.setTimeout(() => {
      noteTimersRef.current.delete(noteId)
      void persistSideNote(noteId)
    }, NOTE_SAVE_DELAY_MS))
  }

  const addSideNote = async (anchorBlockId: string | null) => {
    if (!documentRef.current) return
    try {
      const response = await createMemoSideNote(documentRef.current.id, {
        anchor_block_id: anchorBlockId,
        body_json: { text: '' },
        color: '#fff7d6',
        sort_order: notesRef.current.length,
        collapsed: false,
        status: 'active',
      })
      commitNotes((items) => [...items, response.note])
      setActiveNoteId(response.note.id)
      setNotesOpen(true)
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? `创建便签失败：${error.message}` : '创建便签失败。' })
    }
  }

  const updateSideNote = (noteId: string, patch: Partial<Pick<MemoSideNoteRecord, 'body_json' | 'collapsed' | 'color'>>) => {
    const updatedAt = new Date().toISOString()
    commitNotes((items) => updateNoteList(items, noteId, (note) => ({ ...note, ...patch, updated_at: updatedAt })))
    scheduleSideNoteSave(noteId)
  }

  const attachSideNote = (noteId: string, blockId: string | null) => {
    const updatedAt = new Date().toISOString()
    commitNotes((items) => updateNoteList(items, noteId, (note) => ({
      ...note,
      anchor_block_id: blockId,
      status: 'active',
      updated_at: updatedAt,
    })))
    scheduleSideNoteSave(noteId)
  }

  const removeSideNote = async (noteId: string) => {
    try {
      await deleteMemoSideNote(noteId)
      commitNotes((items) => items.filter((note) => note.id !== noteId))
      if (activeNoteId === noteId) setActiveNoteId(null)
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? `删除便签失败：${error.message}` : '删除便签失败。' })
    }
  }

  const convertSideNoteToCallout = async (note: MemoSideNoteRecord) => {
    const text = note.body_json?.text?.trim()
    if (!text) return
    if (note.anchor_block_id) editorRef.current?.focusBlock(note.anchor_block_id)
    editorRef.current?.insertCallout(text)
    await removeSideNote(note.id)
    setStatus({ kind: 'success', message: '便签已转为正文提示块。' })
  }

  const reorderSideNotes = (sourceId: string, targetId: string, position: 'before' | 'after') => {
    const items = [...notesRef.current]
    const sourceIndex = items.findIndex((note) => note.id === sourceId)
    const targetIndex = items.findIndex((note) => note.id === targetId)
    if (sourceIndex < 0 || targetIndex < 0) return
    const [source] = items.splice(sourceIndex, 1)
    const nextTarget = items.findIndex((note) => note.id === targetId)
    items.splice(position === 'after' ? nextTarget + 1 : nextTarget, 0, source)
    const updatedAt = new Date().toISOString()
    const next = items.map((note, index) => ({ ...note, sort_order: index, updated_at: updatedAt }))
    notesRef.current = next
    setNotes(next)
    next.forEach((note) => scheduleSideNoteSave(note.id))
  }

  const selectSideNote = (note: MemoSideNoteRecord) => {
    setActiveNoteId(note.id)
    if (note.anchor_block_id) {
      editorRef.current?.focusBlock(note.anchor_block_id)
      setActiveBlockId(note.anchor_block_id)
    }
  }

  const openBlockNotes = (blockId: string) => {
    const note = notesRef.current.find((item) => item.anchor_block_id === blockId && item.status === 'active')
    if (!note) return
    setActiveBlockId(blockId)
    setActiveNoteId(note.id)
    setNotesOpen(true)
  }

  const orderedNotes = useMemo(() => {
    const order = new Map(getBlockOrder(snapshot.blocks).map((id, index) => [id, index]))
    return [...notes].sort((a, b) => {
      const aIndex = a.anchor_block_id ? (order.get(a.anchor_block_id) ?? Number.MAX_SAFE_INTEGER - 1) : Number.MAX_SAFE_INTEGER
      const bIndex = b.anchor_block_id ? (order.get(b.anchor_block_id) ?? Number.MAX_SAFE_INTEGER - 1) : Number.MAX_SAFE_INTEGER
      return aIndex - bIndex || a.sort_order - b.sort_order || a.created_at.localeCompare(b.created_at)
    })
  }, [notes, snapshot.blocks])

  const noteCounts = useMemo(() => notes.reduce<Record<string, number>>((counts, note) => {
    if (note.anchor_block_id && note.status !== 'archived') counts[note.anchor_block_id] = (counts[note.anchor_block_id] ?? 0) + 1
    return counts
  }, {}), [notes])
  const notePreviews = useMemo(() => notes.reduce<Record<string, string[]>>((previews, note) => {
    if (!note.anchor_block_id || note.status === 'archived' || note.status === 'orphaned') return previews
    const items = previews[note.anchor_block_id] ?? []
    previews[note.anchor_block_id] = [...items, note.body_json?.text?.trim() || '空便签']
    return previews
  }, {}), [notes])
  const outline = useMemo(() => getMemoOutline(snapshot.blocks), [snapshot.blocks])
  const memoStats = useMemo(() => getMemoStats(snapshot.markdown, snapshot.blocks), [snapshot])

  const setMode = (mode: MemoWorkspaceMode) => {
    setWorkspaceMode(mode)
    localStorage.setItem(WORKSPACE_MODE_KEY, mode)
    if (mode === 'focus') {
      setOutlineOpen(false)
      setNotesOpen(false)
    }
  }

  const startResize = (side: 'outline' | 'notes', event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault()
    const startX = event.clientX
    const startWidth = side === 'outline' ? outlineWidth : noteRailWidth
    let finalWidth = startWidth
    const handleMove = (moveEvent: PointerEvent) => {
      const delta = moveEvent.clientX - startX
      const next = side === 'outline'
        ? Math.min(280, Math.max(160, startWidth + delta))
        : Math.min(460, Math.max(320, startWidth - delta))
      finalWidth = next
      if (side === 'outline') setOutlineWidth(next)
      else setNoteRailWidth(next)
    }
    const handleUp = () => {
      window.removeEventListener('pointermove', handleMove)
      window.removeEventListener('pointerup', handleUp)
      localStorage.setItem(side === 'outline' ? OUTLINE_WIDTH_KEY : NOTE_RAIL_WIDTH_KEY, String(finalWidth))
    }
    window.addEventListener('pointermove', handleMove)
    window.addEventListener('pointerup', handleUp)
  }

  const handleImport = async (file: File) => {
    const content = await file.text()
    const extension = file.name.split('.').pop()?.toLowerCase()
    try {
      if (extension === 'json') {
        const parsed = JSON.parse(content) as unknown
        const blocks = Array.isArray(parsed) ? parsed : normalizeBlockDocument((parsed as { blocks?: unknown })?.blocks)
        editorRef.current?.replaceBlocks(blocks)
      } else if (extension === 'html' || extension === 'htm') {
        editorRef.current?.replaceFromHTML(content)
      } else {
        editorRef.current?.replaceFromMarkdown(content)
      }
      setStatus({ kind: 'success', message: `${file.name} 已导入，正在自动保存。` })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? `导入失败：${error.message}` : '导入失败。' })
    }
  }

  const saveConflictCopy = async () => {
    const current = documentRef.current
    if (!current) return
    try {
      const slug = `${memoSlug}-conflict-${Date.now()}`
      const created = await createMemoDocument({ slug, title: `${current.title}（冲突副本）` })
      await saveMemoDocument(created.document.id, {
        title: created.document.title,
        content_json: snapshotRef.current.blocks,
        content_markdown: snapshotRef.current.markdown,
        content_html: snapshotRef.current.html,
        schema_version: DOCUMENT_SCHEMA_VERSION,
        revision: created.document.revision,
        editor_type: 'blocknote',
      })
      setStatus({ kind: 'success', message: `当前内容已保存为副本 ${slug}。` })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? `创建冲突副本失败：${error.message}` : '创建冲突副本失败。' })
    }
  }

  if (!document) {
    return (
      <div className="memo-workspace-loading tool-workspace" data-layout-region="memo-workspace">
        <p>{status.message}</p>
      </div>
    )
  }

  const workspaceStyle = {
    '--memo-outline-width': `${outlineWidth}px`,
    '--memo-note-rail-width': `${noteRailWidth}px`,
  } as CSSProperties
  const hasNewerRecovery = recovery && recovery.revision >= document.revision && recovery.blocks.length > 0

  const recoverySnapshot = hasNewerRecovery ? recovery : null

  return (
    <div
      className={`memo-workspace memo-mode-${workspaceMode}`}
      data-layout-region="memo-workspace"
      data-workspace-mode={workspaceMode}
      style={workspaceStyle}
    >
      <header className="memo-workspace-toolbar">
        <div className="memo-toolbar-leading">
          <button aria-label="打开目录" className="memo-icon-button memo-panel-toggle" onClick={() => setOutlineOpen(true)} title="目录" type="button">☰</button>
          <input aria-label="文档标题" className="memo-document-title" onChange={(event) => handleTitleChange(event.target.value)} value={document.title} />
        </div>
        <div aria-label="编辑器宽度" className="memo-mode-control" role="group">
          {(['standard', 'focus', 'wide'] as MemoWorkspaceMode[]).map((mode) => (
            <button aria-pressed={workspaceMode === mode} key={mode} onClick={() => setMode(mode)} type="button">
              {{ standard: '标准', focus: '专注', wide: '宽屏' }[mode]}
            </button>
          ))}
        </div>
        <div className="memo-toolbar-actions">
          <button className="memo-command-button" onClick={() => importInputRef.current?.click()} type="button">导入</button>
          <button className="memo-command-button" onClick={() => downloadText(`${document.slug}.json`, JSON.stringify({ schema_version: DOCUMENT_SCHEMA_VERSION, blocks: snapshot.blocks }, null, 2), 'application/json')} type="button">JSON</button>
          <button className="memo-command-button" onClick={() => downloadText(`${document.slug}.md`, snapshot.markdown, 'text/markdown')} type="button">Markdown</button>
          <button className="memo-command-button" onClick={() => downloadText(`${document.slug}.html`, snapshot.html, 'text/html')} type="button">HTML</button>
          <button aria-label="打开便签栏" className="memo-icon-button memo-panel-toggle" onClick={() => setNotesOpen(true)} title="侧边便签" type="button">▤</button>
          <input
            accept=".json,.md,.markdown,.html,.htm,application/json,text/markdown,text/html"
            className="sr-only"
            onChange={(event) => {
              const file = event.target.files?.[0]
              if (file) void handleImport(file)
              event.target.value = ''
            }}
            ref={importInputRef}
            type="file"
          />
        </div>
      </header>

      {(hasNewerRecovery || conflict) && (
        <div className="memo-recovery-banner" role="status">
          <span>{conflict ? '远端文档已被修改。' : recoverySnapshot ? `发现 ${formatDate(recoverySnapshot.savedAt)} 的本地恢复内容。` : ''}</span>
          <div>
            {recoverySnapshot && <button onClick={() => { editorRef.current?.replaceBlocks(recoverySnapshot.blocks); setRecovery(null) }} type="button">恢复本地内容</button>}
            {hasNewerRecovery && <button onClick={() => { void clearMemoRecovery(document.id); setRecovery(null) }} type="button">忽略</button>}
            {conflict && <button onClick={() => void saveConflictCopy()} type="button">保存为副本</button>}
            {conflict && <button onClick={() => downloadText(`${document.slug}-conflict.json`, JSON.stringify(snapshotRef.current.blocks, null, 2), 'application/json')} type="button">导出当前 JSON</button>}
            {conflict && <button onClick={() => void reloadDocument()} type="button">加载远端</button>}
          </div>
        </div>
      )}

      <div className="memo-workbench" data-layout-region="memo-grid">
        <aside className={`memo-outline-panel${outlineOpen ? ' memo-panel-open' : ''}`} data-layout-region="memo-outline">
          <header className="memo-panel-header">
            <strong>目录</strong>
            <button aria-label="关闭目录" className="memo-icon-button memo-mobile-only" onClick={() => setOutlineOpen(false)} title="关闭" type="button">×</button>
          </header>
          <nav aria-label="文档目录" className="memo-outline-list">
            {outline.length > 0 ? outline.map((item) => (
              <button
                className={activeBlockId === item.id ? 'memo-outline-active' : ''}
                key={item.id}
                onClick={() => {
                  editorRef.current?.focusBlock(item.id)
                  setActiveBlockId(item.id)
                  setOutlineOpen(false)
                }}
                style={{ '--memo-outline-level': item.level } as CSSProperties}
                type="button"
              >{item.label}</button>
            )) : <p>暂无标题</p>}
          </nav>
        </aside>
        <div aria-hidden="true" className="memo-resize-handle memo-resize-outline" onPointerDown={(event) => startResize('outline', event)} />

        <main className="memo-document-surface" data-layout-region="memo-primary">
          <div className="memo-document-meta" aria-label="文档状态">
            <span>{memoStats.chars} 字</span>
            <span>{memoStats.blocks} 块</span>
            <span>{memoStats.images} 图</span>
            <span>{notes.filter((note) => note.status !== 'archived').length} 便签</span>
          </div>
          <BlockNoteMemoEditor
            activeNoteBlockId={activeNoteId ? notes.find((note) => note.id === activeNoteId)?.anchor_block_id : null}
            initialBlocks={editorSeed}
            key={editorKey}
            legacyHTML={document.editor_type === 'blocknote' ? '' : document.content_html}
            legacyMarkdown={document.editor_type === 'blocknote' ? '' : document.content_text}
            noteCounts={noteCounts}
            notePreviews={notePreviews}
            onActiveBlockChange={setActiveBlockId}
            onChange={handleEditorSnapshot}
            onDropSideNote={attachSideNote}
            onOpenBlockNotes={openBlockNotes}
            onReady={handleEditorReady}
            onUpload={handleUpload}
            ref={editorRef}
          />
          <StatusBanner
            right={document.updated_at ? `最后保存 ${formatDate(document.updated_at)}` : '尚未保存'}
            status={{ ...status, message: isSaving ? '正在保存。' : status.message }}
          />
        </main>

        <div aria-hidden="true" className="memo-resize-handle memo-resize-notes" onPointerDown={(event) => startResize('notes', event)} />
        <SideNoteRail
          activeBlockId={activeBlockId}
          activeNoteId={activeNoteId}
          notes={orderedNotes}
          onAdd={(anchor) => void addSideNote(anchor)}
          onAttach={attachSideNote}
          onClose={() => setNotesOpen(false)}
          onConvertToCallout={(note) => void convertSideNoteToCallout(note)}
          onDelete={(id) => void removeSideNote(id)}
          onReorder={reorderSideNotes}
          onSelect={selectSideNote}
          onUpdate={updateSideNote}
          open={notesOpen}
        />
      </div>
      {(outlineOpen || notesOpen) && <button aria-label="关闭面板" className="memo-panel-backdrop" onClick={() => { setOutlineOpen(false); setNotesOpen(false) }} type="button" />}
    </div>
  )
}
