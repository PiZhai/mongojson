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
  getMemoDocumentWebSocketUrl,
  listMemoDocuments,
  listMemoSideNotes,
  saveMemoDocument,
  saveMemoSideNote,
  uploadFile,
} from './api'
import { MemoDocumentDrawer } from './components/MemoDocumentDrawer'
import { MemoIcon } from './components/MemoIcon'
import { BlockNoteMemoEditor } from './editor/BlockNoteMemoEditor'
import type { MemoEditorHandle } from './editor/types'
import { getBlockOrder, getMemoOutline, getMemoStats, normalizeBlockDocument } from './lib/document'
import { clearMemoRecovery, loadMemoRecovery, saveMemoRecovery, type MemoRecoverySnapshot } from './lib/recovery'
import { SideNoteRail } from './side-notes/SideNoteRail'
import type { MemoDocumentRecord, MemoDocumentSummary, MemoEditorSnapshot, MemoSideNoteRecord, MemoSyncEvent, MemoSyncStatus, MemoWorkspaceMode } from './types'

const MEMO_SLUG = 'inbox'
const DOCUMENT_SCHEMA_VERSION = 1
const WORKSPACE_MODE_KEY = 'mongojson.memoDocs.workspaceMode'
const OUTLINE_WIDTH_KEY = 'mongojson.memoDocs.outlineWidth'
const NOTE_RAIL_WIDTH_KEY = 'mongojson.memoDocs.noteRailWidth'
const SAVE_DELAY_MS = 1000
const NOTE_SAVE_DELAY_MS = 650

function readMemoSlug() {
  return new URLSearchParams(window.location.search).get('slug')?.trim() || MEMO_SLUG
}

async function waitUntil(check: () => boolean, timeoutMs = 10_000) {
  const startedAt = Date.now()
  while (!check()) {
    if (Date.now() - startedAt >= timeoutMs) return false
    await new Promise((resolve) => window.setTimeout(resolve, 25))
  }
  return true
}

function createMemoClientId() {
  return typeof window.crypto?.randomUUID === 'function'
    ? window.crypto.randomUUID()
    : `memo-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function createMemoDocumentSlug() {
  const suffix = typeof window.crypto?.randomUUID === 'function'
    ? window.crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`
  return `memo-${suffix}`
}

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
  const [memoSlug, setMemoSlug] = useState(readMemoSlug)
  const editorRef = useRef<MemoEditorHandle | null>(null)
  const importInputRef = useRef<HTMLInputElement | null>(null)
  const archiveToggleRef = useRef<HTMLButtonElement | null>(null)
  const archiveCloseButtonRef = useRef<HTMLButtonElement | null>(null)
  const titleInputRef = useRef<HTMLInputElement | null>(null)
  const focusTitleOnLoadRef = useRef(false)
  const clientIdRef = useRef(createMemoClientId())
  const documentRef = useRef<MemoDocumentRecord | null>(null)
  const snapshotRef = useRef<MemoEditorSnapshot>({ blocks: [], markdown: '', html: '', activeBlockId: null })
  const notesRef = useRef<MemoSideNoteRecord[]>([])
  const saveTimerRef = useRef<number | null>(null)
  const saveInFlightRef = useRef(false)
  const saveQueuedRef = useRef(false)
  const documentSaveFailedRef = useRef(false)
  const noteTimersRef = useRef(new Map<string, number>())
  const noteSaveInFlightRef = useRef(new Set<string>())
  const sideNoteSaveFailuresRef = useRef(new Set<string>())
  const conflictRef = useRef(false)
  const [document, setDocument] = useState<MemoDocumentRecord | null>(null)
  const [editorSeed, setEditorSeed] = useState<unknown[]>([])
  const [editorKey, setEditorKey] = useState(0)
  const [snapshot, setSnapshot] = useState<MemoEditorSnapshot>({ blocks: [], markdown: '', html: '', activeBlockId: null })
  const [notes, setNotes] = useState<MemoSideNoteRecord[]>([])
  const [activeBlockId, setActiveBlockId] = useState<string | null>(null)
  const [activeNoteId, setActiveNoteId] = useState<string | null>(null)
  const [workspaceMode, setWorkspaceMode] = useState<MemoWorkspaceMode>(readStoredMode)
  const [archiveOpen, setArchiveOpen] = useState(false)
  const [archivedDocuments, setArchivedDocuments] = useState<MemoDocumentSummary[]>([])
  const [archiveLoading, setArchiveLoading] = useState(false)
  const [archiveError, setArchiveError] = useState('')
  const [switchingDocumentId, setSwitchingDocumentId] = useState<string>()
  const [creatingDocument, setCreatingDocument] = useState(false)
  const [outlineOpen, setOutlineOpen] = useState(false)
  const [notesOpen, setNotesOpen] = useState(false)
  const [outlineWidth, setOutlineWidth] = useState(() => readStoredWidth(OUTLINE_WIDTH_KEY, 200, 160, 280))
  const [noteRailWidth, setNoteRailWidth] = useState(() => readStoredWidth(NOTE_RAIL_WIDTH_KEY, 380, 320, 460))
  const [recovery, setRecovery] = useState<MemoRecoverySnapshot | null>(null)
  const [conflict, setConflict] = useState(false)
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '正在载入随手记。' })
  const [isSaving, setIsSaving] = useState(false)
  const [syncStatus, setSyncStatus] = useState<MemoSyncStatus>('connecting')

  const commitNotes = useCallback((updater: (current: MemoSideNoteRecord[]) => MemoSideNoteRecord[]) => {
    const next = updater(notesRef.current)
    notesRef.current = next
    setNotes(next)
    return next
  }, [])

  const loadArchivedDocuments = useCallback(async () => {
    setArchiveLoading(true)
    setArchiveError('')
    try {
      const response = await listMemoDocuments()
      setArchivedDocuments(response.documents)
    } catch (error) {
      setArchiveError(error instanceof Error ? `存档文件加载失败：${error.message}` : '存档文件加载失败。')
    } finally {
      setArchiveLoading(false)
    }
  }, [])

  const closeArchive = useCallback(() => {
    setArchiveOpen(false)
    window.setTimeout(() => archiveToggleRef.current?.focus(), 0)
  }, [])

  const toggleArchive = () => {
    setArchiveOpen((open) => {
      const next = !open
      if (next) {
        setOutlineOpen(false)
        setNotesOpen(false)
      }
      return next
    })
  }

  const reloadDocument = useCallback(async () => {
    if (saveTimerRef.current) {
      window.clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
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
      documentSaveFailedRef.current = false
      sideNoteSaveFailuresRef.current.clear()
      setDocument(loadedDocument)
      setEditorSeed(structuredBlocks)
      setSnapshot(snapshotRef.current)
      setNotes(noteResponse.notes)
      setActiveBlockId(null)
      setActiveNoteId(null)
      setEditorKey((value) => value + 1)
      conflictRef.current = false
      setConflict(false)
      setRecovery(await loadMemoRecovery(loadedDocument.id).catch(() => null))
      setStatus({ kind: 'success', message: loadedDocument.editor_type === 'blocknote' ? '结构化随手记已加载。' : '旧版随手记已加载，正在准备无损迁移。' })
    } catch (error) {
      focusTitleOnLoadRef.current = false
      setStatus({ kind: 'error', message: error instanceof Error ? error.message : '加载随手记失败。' })
    } finally {
      setSwitchingDocumentId(undefined)
    }
  }, [memoSlug])

  useEffect(() => {
    if (!document?.id || !focusTitleOnLoadRef.current) return
    focusTitleOnLoadRef.current = false
    const timer = window.setTimeout(() => {
      titleInputRef.current?.focus()
      titleInputRef.current?.select()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [document?.id])

  const refreshRemoteDocument = useCallback(async (minimumRevision?: number) => {
    const currentDocument = documentRef.current
    if (!currentDocument || (minimumRevision !== undefined && currentDocument.revision >= minimumRevision)) return

    const hasLocalChanges = saveTimerRef.current !== null || saveInFlightRef.current || saveQueuedRef.current
    if (hasLocalChanges) {
      if (saveTimerRef.current) {
        window.clearTimeout(saveTimerRef.current)
        saveTimerRef.current = null
      }
      conflictRef.current = true
      setConflict(true)
      setStatus({ kind: 'warning', message: '收到远端更新，但本地仍有未保存内容；已保留本地内容，请选择加载远端或保存为副本。' })
      return
    }

    try {
      const response = await getMemoDocument(memoSlug)
      const remoteDocument = response.document
      if (remoteDocument.id !== currentDocument.id || remoteDocument.revision <= currentDocument.revision) return
      const noteResponse = await listMemoSideNotes(remoteDocument.id)
      const structuredBlocks = remoteDocument.editor_type === 'blocknote'
        ? normalizeBlockDocument(remoteDocument.content_json)
        : []
      const remoteSnapshot = {
        blocks: structuredBlocks,
        markdown: remoteDocument.content_text,
        html: remoteDocument.content_html,
        activeBlockId: null,
      }
      documentRef.current = remoteDocument
      snapshotRef.current = remoteSnapshot
      notesRef.current = noteResponse.notes
      documentSaveFailedRef.current = false
      sideNoteSaveFailuresRef.current.clear()
      setDocument(remoteDocument)
      setEditorSeed(structuredBlocks)
      setSnapshot(remoteSnapshot)
      setNotes(noteResponse.notes)
      setActiveBlockId(null)
      setActiveNoteId(null)
      setEditorKey((value) => value + 1)
      await clearMemoRecovery(remoteDocument.id).catch(() => undefined)
      setRecovery(null)
      setStatus({ kind: 'success', message: '已实时同步远端修改。' })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? `同步远端修改失败：${error.message}` : '同步远端修改失败。' })
    }
  }, [memoSlug])

  const refreshRemoteNotes = useCallback(async () => {
    const currentDocument = documentRef.current
    if (!currentDocument) return
    if (noteTimersRef.current.size > 0 || noteSaveInFlightRef.current.size > 0) {
      setStatus({ kind: 'warning', message: '收到远端便签更新，但本地便签仍在保存；已保留本地编辑。' })
      return
    }
    try {
      const response = await listMemoSideNotes(currentDocument.id)
      commitNotes(() => response.notes)
      setStatus({ kind: 'success', message: '已实时同步远端便签。' })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? `同步远端便签失败：${error.message}` : '同步远端便签失败。' })
    }
  }, [commitNotes])

  useEffect(() => {
    const timer = window.setTimeout(() => void reloadDocument(), 0)
    return () => window.clearTimeout(timer)
  }, [reloadDocument])

  useEffect(() => {
    if (!archiveOpen) return
    const loadTimer = window.setTimeout(() => void loadArchivedDocuments(), 0)
    const focusTimer = window.setTimeout(() => archiveCloseButtonRef.current?.focus(), 0)
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeArchive()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      window.clearTimeout(loadTimer)
      window.clearTimeout(focusTimer)
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [archiveOpen, closeArchive, loadArchivedDocuments])

  useEffect(() => {
    const documentId = document?.id
    if (!documentId) return

    let disposed = false
    let socket: WebSocket | null = null
    let reconnectTimer: number | null = null
    let reconnectAttempt = 0

    const connect = () => {
      if (disposed) return
      setSyncStatus('connecting')
      socket = new WebSocket(getMemoDocumentWebSocketUrl(documentId))
      socket.addEventListener('open', () => {
        reconnectAttempt = 0
        setSyncStatus('connected')
        void refreshRemoteDocument()
      })
      socket.addEventListener('message', (event) => {
        try {
          const message = JSON.parse(String(event.data)) as MemoSyncEvent
          if (message.document_id !== documentId || message.actor_client_id === clientIdRef.current) return
          if (message.type === 'document_updated' && typeof message.revision === 'number') {
            void refreshRemoteDocument(message.revision)
          } else if (message.type === 'notes_updated') {
            void refreshRemoteNotes()
          } else if (message.type === 'document_deleted') {
            setStatus({ kind: 'error', message: '当前文档已被其他用户删除。' })
          }
        } catch {
          setSyncStatus('disconnected')
        }
      })
      socket.addEventListener('close', () => {
        if (disposed) return
        setSyncStatus('disconnected')
        const delay = Math.min(10_000, 1_000 * (2 ** reconnectAttempt))
        reconnectAttempt += 1
        reconnectTimer = window.setTimeout(connect, delay)
      })
      socket.addEventListener('error', () => setSyncStatus('disconnected'))
    }

    connect()
    return () => {
      disposed = true
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer)
      socket?.close()
    }
  }, [document?.id, refreshRemoteDocument, refreshRemoteNotes])

  useEffect(() => () => {
    if (saveTimerRef.current) window.clearTimeout(saveTimerRef.current)
    noteTimersRef.current.forEach((timer) => window.clearTimeout(timer))
  }, [])

  async function saveDocumentNow(nextSnapshot: MemoEditorSnapshot) {
    const currentDocument = documentRef.current
    if (!currentDocument || conflictRef.current) return false
    if (saveInFlightRef.current) {
      saveQueuedRef.current = true
      return false
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
      }, clientIdRef.current)
      documentRef.current = response.document
      setDocument(response.document)
      const synchronizedNotes = await listMemoSideNotes(response.document.id)
      commitNotes(() => synchronizedNotes.notes)
      await clearMemoRecovery(response.document.id).catch(() => undefined)
      setRecovery(null)
      documentSaveFailedRef.current = false
      setStatus({ kind: 'success', message: '文档已自动保存。' })
      setArchivedDocuments((items) => items.map((item) => item.id === response.document.id ? {
        ...item,
        title: response.document.title,
        revision: response.document.revision,
        editor_type: response.document.editor_type,
        updated_at: response.document.updated_at,
      } : item))
      return true
    } catch (error) {
      documentSaveFailedRef.current = true
      if (error instanceof ApiRequestError && error.status === 409) {
        conflictRef.current = true
        setConflict(true)
        setStatus({ kind: 'warning', message: '检测到远端版本冲突，当前内容仍保留在本地恢复区。' })
      } else {
        setStatus({ kind: 'error', message: error instanceof Error ? `保存失败：${error.message}` : '保存失败。' })
      }
      return false
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
    saveTimerRef.current = window.setTimeout(() => {
      saveTimerRef.current = null
      void saveDocumentNow(nextSnapshot)
    }, SAVE_DELAY_MS)
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
    if (noteSaveInFlightRef.current.has(noteId)) return false
    const requestNote = notesRef.current.find((note) => note.id === noteId)
    if (!requestNote) {
      sideNoteSaveFailuresRef.current.delete(noteId)
      return true
    }
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
      }, clientIdRef.current)
      const latest = notesRef.current.find((note) => note.id === noteId)
      const changedDuringSave = latest && latest.updated_at !== requestNote.updated_at
      commitNotes((items) => updateNoteList(items, noteId, (note) => changedDuringSave
        ? { ...note, revision: response.note.revision }
        : response.note))
      if (changedDuringSave) {
        window.setTimeout(() => void persistSideNote(noteId), 0)
      }
      sideNoteSaveFailuresRef.current.delete(noteId)
      return true
    } catch (error) {
      sideNoteSaveFailuresRef.current.add(noteId)
      setStatus({
        kind: error instanceof ApiRequestError && error.status === 409 ? 'warning' : 'error',
        message: error instanceof Error ? `便签保存失败：${error.message}` : '便签保存失败。',
      })
      return false
    } finally {
      noteSaveInFlightRef.current.delete(noteId)
    }
  }

  async function flushPendingChanges() {
    const documentSavePending = saveTimerRef.current !== null || saveQueuedRef.current || documentSaveFailedRef.current
    if (saveTimerRef.current) {
      window.clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    saveQueuedRef.current = false
    if (!await waitUntil(() => !saveInFlightRef.current)) return false
    if (documentSavePending && !await saveDocumentNow(snapshotRef.current)) return false

    const scheduledNoteIds = [...new Set([
      ...noteTimersRef.current.keys(),
      ...sideNoteSaveFailuresRef.current,
    ])]
    noteTimersRef.current.forEach((timer) => window.clearTimeout(timer))
    noteTimersRef.current.clear()
    if (!await waitUntil(() => noteSaveInFlightRef.current.size === 0)) return false
    const noteResults = await Promise.all(scheduledNoteIds.map((noteId) => persistSideNote(noteId)))
    await new Promise((resolve) => window.setTimeout(resolve, 0))
    if (!await waitUntil(() => noteSaveInFlightRef.current.size === 0)) return false
    return noteResults.every(Boolean)
  }

  const selectArchivedDocument = async (item: MemoDocumentSummary) => {
    if (item.id === documentRef.current?.id) {
      closeArchive()
      return
    }
    setSwitchingDocumentId(item.id)
    setStatus({ kind: 'idle', message: '正在保存当前文档并切换存档文件。' })
    const flushed = await flushPendingChanges()
    if (!flushed) {
      setSwitchingDocumentId(undefined)
      setStatus({ kind: 'warning', message: '当前文档或便签尚未保存成功，已取消切换以避免内容丢失。' })
      return
    }
    const url = new URL(window.location.href)
    if (item.slug === MEMO_SLUG) url.searchParams.delete('slug')
    else url.searchParams.set('slug', item.slug)
    window.history.replaceState(window.history.state, '', url)
    setMemoSlug(item.slug)
    closeArchive()
  }

  const createNewDocument = async () => {
    if (creatingDocument || switchingDocumentId) return
    setCreatingDocument(true)
    setStatus({ kind: 'idle', message: '正在保存当前文档并创建新文档。' })
    const flushed = await flushPendingChanges()
    if (!flushed) {
      setCreatingDocument(false)
      setStatus({ kind: 'warning', message: '当前文档或便签尚未保存成功，已取消新建以避免内容丢失。' })
      return
    }

    try {
      const response = await createMemoDocument({ slug: createMemoDocumentSlug(), title: '未命名文档' })
      const created = response.document
      setArchivedDocuments((items) => [{
        id: created.id,
        slug: created.slug,
        title: created.title,
        revision: created.revision,
        editor_type: created.editor_type,
        note_count: 0,
        created_at: created.created_at,
        updated_at: created.updated_at,
      }, ...items.filter((item) => item.id !== created.id)])
      setSwitchingDocumentId(created.id)
      focusTitleOnLoadRef.current = true
      const url = new URL(window.location.href)
      url.searchParams.set('slug', created.slug)
      window.history.replaceState(window.history.state, '', url)
      setMemoSlug(created.slug)
      setArchiveOpen(false)
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? `新建文档失败：${error.message}` : '新建文档失败。' })
    } finally {
      setCreatingDocument(false)
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
      }, clientIdRef.current)
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
      await deleteMemoSideNote(noteId, clientIdRef.current)
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
          <button
            aria-controls="memo-document-drawer"
            aria-expanded={archiveOpen}
            aria-label={archiveOpen ? '收起存档文件' : '展开存档文件'}
            className={`memo-icon-button memo-archive-toggle${archiveOpen ? ' memo-icon-button-active' : ''}`}
            onClick={toggleArchive}
            ref={archiveToggleRef}
            title={archiveOpen ? '收起存档文件' : '展开存档文件'}
            type="button"
          ><MemoIcon name="archive" /></button>
          <button aria-label="打开正文目录" className="memo-icon-button memo-panel-toggle" onClick={() => setOutlineOpen(true)} title="正文目录" type="button"><MemoIcon name="outline" /></button>
          <input aria-label="文档标题" className="memo-document-title" onChange={(event) => handleTitleChange(event.target.value)} ref={titleInputRef} value={document.title} />
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
          <button aria-label="打开便签栏" className="memo-icon-button memo-panel-toggle" onClick={() => setNotesOpen(true)} title="侧边便签" type="button"><MemoIcon name="notes" /></button>
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

      <MemoDocumentDrawer
        activeDocumentId={document.id}
        closeButtonRef={archiveCloseButtonRef}
        creating={creatingDocument}
        documents={archivedDocuments}
        error={archiveError}
        loading={archiveLoading}
        onClose={closeArchive}
        onCreate={() => void createNewDocument()}
        onSelect={(item) => void selectArchivedDocument(item)}
        open={archiveOpen}
        switchingDocumentId={switchingDocumentId}
      />

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
            <button aria-label="关闭目录" className="memo-icon-button memo-mobile-only" onClick={() => setOutlineOpen(false)} title="关闭" type="button"><MemoIcon name="close" /></button>
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
            <span>{syncStatus === 'connected' ? '实时同步' : syncStatus === 'connecting' ? '正在连接' : '同步断开'}</span>
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
      {(archiveOpen || outlineOpen || notesOpen) && (
        <button
          aria-label="关闭面板"
          className={`memo-panel-backdrop${archiveOpen ? ' memo-archive-backdrop' : ''}`}
          onClick={() => {
            if (archiveOpen) closeArchive()
            setOutlineOpen(false)
            setNotesOpen(false)
          }}
          type="button"
        />
      )}
    </div>
  )
}
