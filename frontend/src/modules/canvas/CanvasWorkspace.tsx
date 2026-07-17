import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Excalidraw,
  convertToExcalidrawElements,
  exportToBlob,
  exportToSvg,
  loadFromBlob,
  serializeAsJSON,
} from '@excalidraw/excalidraw'
import type { BinaryFiles, ExcalidrawImperativeAPI } from '@excalidraw/excalidraw/types'
import type { ExcalidrawElement } from '@excalidraw/excalidraw/element/types'
import type { AppState } from '@excalidraw/excalidraw/types'
import '@excalidraw/excalidraw/index.css'
import {
  createCanvasBoard,
  deleteCanvasBoard,
  getCanvasBoard,
  listCanvasBoards,
  saveCanvasBoard,
  uploadCanvasAsset,
} from './api'
import type { CanvasBoard, CanvasScene, SaveState } from './types'
import { externalizeCanvasFiles } from './lib/scenePersistence'
import './styles.css'

const ACTIVE_BOARD_KEY = 'mongojson.canvas.active-board'
const recoveryKey = (id: string) => `mongojson.canvas.recovery.${id}`
const EMBEDDABLE_HOSTS = ['youtube.com', 'youtu.be', 'figma.com', 'github.com']
const CANVAS_UI_OPTIONS = { canvasActions: { loadScene: false, saveToActiveFile: false } } as const

type EditorState = {
  elements: readonly ExcalidrawElement[]
  appState: AppState
  files: BinaryFiles
}

type RecoveryRecord = {
  revision: number
  title: string
  scene: CanvasScene
  savedAt: string
}

function Icon({ name }: { name: 'boards' | 'plus' | 'trash' | 'note' | 'file' | 'import' | 'json' | 'image' | 'eye' | 'edit' }) {
  const paths: Record<typeof name, string> = {
    boards: 'M4 5h16v14H4zM8 5v14',
    plus: 'M12 5v14M5 12h14',
    trash: 'M5 7h14M9 7V4h6v3M8 10v7M12 10v7M16 10v7',
    note: 'M5 5h14v14H5zM8 9h8M8 12h6M8 15h7',
    file: 'M6 3h8l4 4v14H6zM14 3v5h5',
    import: 'M12 3v12M7 10l5 5 5-5M4 20h16',
    json: 'M9 4C7 6 7 8 7 12s0 6-2 8M15 4c2 2 2 4 2 8s0 6 2 8',
    image: 'M4 5h16v14H4zM7 16l4-5 3 3 2-2 3 4M16 9h.01',
    eye: 'M3 12s3-5 9-5 9 5 9 5-3 5-9 5-9-5-9-5zM12 10a2 2 0 1 0 0 4 2 2 0 0 0 0-4',
    edit: 'M4 20l4-1 10-10-3-3L5 16zM13 6l3 3',
  }
  return (
    <svg aria-hidden="true" className="canvas-command-icon" viewBox="0 0 24 24">
      <path d={paths[name]} />
    </svg>
  )
}

function downloadBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = name
  anchor.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}

function safeFileName(value: string) {
  return value.trim().replace(/[\\/:*?"<>|]+/g, '-').replace(/\s+/g, '-') || '无界画布'
}

function readRecovery(board: CanvasBoard): RecoveryRecord | null {
  try {
    const raw = localStorage.getItem(recoveryKey(board.id))
    if (!raw) return null
    const parsed = JSON.parse(raw) as RecoveryRecord
    return parsed.revision === board.revision && parsed.scene ? parsed : null
  } catch {
    return null
  }
}

function saveStateLabel(state: SaveState) {
  if (state === 'dirty') return '有未保存更改'
  if (state === 'saving') return '正在保存'
  if (state === 'saved') return '已保存'
  if (state === 'error') return '保存失败'
  if (state === 'conflict') return '检测到版本冲突'
  return '等待编辑'
}

export function CanvasWorkspace() {
  const [boards, setBoards] = useState<CanvasBoard[]>([])
  const [activeBoard, setActiveBoard] = useState<CanvasBoard | null>(null)
  const [title, setTitle] = useState('')
  const [saveState, setSaveState] = useState<SaveState>('idle')
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(true)
  const [drawerOpen, setDrawerOpen] = useState(true)
  const [viewMode, setViewMode] = useState(false)
  const [editorKey, setEditorKey] = useState(0)
  const apiRef = useRef<ExcalidrawImperativeAPI | null>(null)
  const latestEditorStateRef = useRef<EditorState | null>(null)
  const activeBoardRef = useRef<CanvasBoard | null>(null)
  const titleRef = useRef('')
  const revisionRef = useRef(0)
  const changeVersionRef = useRef(0)
  const savingRef = useRef(false)
  const suppressChangesRef = useRef(true)
  const saveTimerRef = useRef<number | null>(null)
  const recoveryTimerRef = useRef<number | null>(null)
  const externalAssetUrlsRef = useRef(new Map<string, string>())
  const importInputRef = useRef<HTMLInputElement | null>(null)
  const assetInputRef = useRef<HTMLInputElement | null>(null)
  const bootstrapPromiseRef = useRef<Promise<CanvasBoard[]> | null>(null)

  useEffect(() => {
    activeBoardRef.current = activeBoard
    revisionRef.current = activeBoard?.revision ?? 0
  }, [activeBoard])

  useEffect(() => {
    titleRef.current = title
  }, [title])

  const serializeCurrentScene = useCallback((): CanvasScene | null => {
    const editor = latestEditorStateRef.current
    if (!editor) return activeBoardRef.current?.scene ?? null
    return JSON.parse(serializeAsJSON(editor.elements, editor.appState, editor.files, 'database')) as CanvasScene
  }, [])

  const externalizeSceneFiles = useCallback(async (boardId: string, scene: CanvasScene) => {
    return externalizeCanvasFiles(boardId, scene, externalAssetUrlsRef.current, uploadCanvasAsset)
  }, [])

  const flushSave = useCallback(async () => {
    const board = activeBoardRef.current
    if (!board || savingRef.current || suppressChangesRef.current) return
    const scene = serializeCurrentScene()
    if (!scene) return
    const capturedVersion = changeVersionRef.current
    savingRef.current = true
    setSaveState('saving')
    try {
      const persistedScene = await externalizeSceneFiles(board.id, scene)
      const saved = await saveCanvasBoard(board.id, titleRef.current, persistedScene, revisionRef.current)
      if (activeBoardRef.current?.id !== board.id) return
      revisionRef.current = saved.revision
      const activeMetadata = { ...saved, scene: activeBoardRef.current.scene }
      activeBoardRef.current = activeMetadata
      setActiveBoard(activeMetadata)
      setBoards((items) => [{ ...saved, scene: undefined }, ...items.filter((item) => item.id !== saved.id)])
      if (capturedVersion === changeVersionRef.current) {
        localStorage.removeItem(recoveryKey(board.id))
        setSaveState('saved')
      } else {
        setSaveState('dirty')
      }
    } catch (error) {
      if (error instanceof Error && error.message === 'REVISION_CONFLICT') {
        setSaveState('conflict')
        setMessage('画板已在其他页面更新。请导出当前内容后重新载入服务器版本。')
      } else {
        setSaveState('error')
        setMessage(error instanceof Error ? error.message : '画板保存失败。')
      }
    } finally {
      savingRef.current = false
    }
  }, [externalizeSceneFiles, serializeCurrentScene])

  const scheduleSave = useCallback(() => {
    if (saveTimerRef.current) window.clearTimeout(saveTimerRef.current)
    saveTimerRef.current = window.setTimeout(() => void flushSave(), 1200)
  }, [flushSave])

  const persistRecovery = useCallback(() => {
    const board = activeBoardRef.current
    const scene = serializeCurrentScene()
    if (!board || !scene) return
    try {
      const recovery: RecoveryRecord = {
        revision: revisionRef.current,
        title: titleRef.current,
        scene,
        savedAt: new Date().toISOString(),
      }
      localStorage.setItem(recoveryKey(board.id), JSON.stringify(recovery))
    } catch {
      // Large image scenes can exceed localStorage; the server save remains authoritative.
    }
  }, [serializeCurrentScene])

  const markChanged = useCallback(() => {
    if (suppressChangesRef.current) return
    changeVersionRef.current += 1
    setSaveState('dirty')
    if (recoveryTimerRef.current) window.clearTimeout(recoveryTimerRef.current)
    recoveryTimerRef.current = window.setTimeout(persistRecovery, 350)
    scheduleSave()
  }, [persistRecovery, scheduleSave])

  const loadBoard = useCallback(async (id: string) => {
    if (saveTimerRef.current) window.clearTimeout(saveTimerRef.current)
    suppressChangesRef.current = true
    setLoading(true)
    setMessage('')
    try {
      const board = await getCanvasBoard(id)
      const recovery = readRecovery(board)
      const loaded = recovery ? { ...board, title: recovery.title, scene: recovery.scene } : board
      externalAssetUrlsRef.current.clear()
      latestEditorStateRef.current = null
      activeBoardRef.current = loaded
      revisionRef.current = board.revision
      titleRef.current = loaded.title
      setActiveBoard(loaded)
      setTitle(loaded.title)
      setEditorKey((value) => value + 1)
      setSaveState(recovery ? 'dirty' : 'saved')
      localStorage.setItem(ACTIVE_BOARD_KEY, id)
      window.setTimeout(() => {
        suppressChangesRef.current = false
        if (recovery) scheduleSave()
      }, 300)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '画板加载失败。')
    } finally {
      setLoading(false)
    }
  }, [scheduleSave])

  useEffect(() => {
    let cancelled = false
    if (!bootstrapPromiseRef.current) {
      bootstrapPromiseRef.current = (async () => {
        const items = await listCanvasBoards()
        return items.length > 0 ? items : [await createCanvasBoard('我的画板')]
      })()
    }
    void (async () => {
      try {
        const items = await bootstrapPromiseRef.current!
        if (cancelled) return
        setBoards(items)
        const remembered = localStorage.getItem(ACTIVE_BOARD_KEY)
        const initial = items.find((item) => item.id === remembered) ?? items[0]
        await loadBoard(initial.id)
      } catch (error) {
        if (!cancelled) {
          setLoading(false)
          setMessage(error instanceof Error ? error.message : '无界画布初始化失败。')
        }
      }
    })()
    return () => {
      cancelled = true
      if (saveTimerRef.current) window.clearTimeout(saveTimerRef.current)
      if (recoveryTimerRef.current) window.clearTimeout(recoveryTimerRef.current)
    }
  }, [loadBoard])

  const createBoard = useCallback(async () => {
    try {
      await flushSave()
      const board = await createCanvasBoard(`新画板 ${boards.length + 1}`)
      setBoards((items) => [board, ...items])
      await loadBoard(board.id)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '创建画板失败。')
    }
  }, [boards.length, flushSave, loadBoard])

  const removeBoard = useCallback(async (board: CanvasBoard) => {
    if (!window.confirm(`确定删除“${board.title}”及其所有附件吗？`)) return
    try {
      await deleteCanvasBoard(board.id)
      localStorage.removeItem(recoveryKey(board.id))
      const remaining = boards.filter((item) => item.id !== board.id)
      if (remaining.length === 0) {
        const replacement = await createCanvasBoard('我的画板')
        setBoards([replacement])
        await loadBoard(replacement.id)
      } else {
        setBoards(remaining)
        if (activeBoardRef.current?.id === board.id) await loadBoard(remaining[0].id)
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '删除画板失败。')
    }
  }, [boards, loadBoard])

  const insertTemplate = useCallback((kind: 'note' | 'file', assetName?: string, assetUrl?: string, assetId?: string) => {
    const api = apiRef.current
    if (!api) return
    const appState = api.getAppState()
    const x = -appState.scrollX + appState.width / 2 - 140
    const y = -appState.scrollY + appState.height / 2 - 90
    const groupId = crypto.randomUUID()
    const isNote = kind === 'note'
    const label = isNote ? '双击编辑便签' : assetName ?? '附件'
    const elements = convertToExcalidrawElements([
      {
        type: 'rectangle', x, y, width: 280, height: 180, groupIds: [groupId],
        backgroundColor: isNote ? '#fff3bf' : '#e7f5ff', strokeColor: isNote ? '#d29b00' : '#1971c2',
        fillStyle: 'solid', roughness: 0, roundness: { type: 3 }, link: assetUrl ?? null,
        customData: isNote ? { template: 'sticky-note' } : { template: 'file-card', assetId },
      },
      {
        type: 'text', x: x + 20, y: y + 24, width: 240, height: 100, text: label,
        fontSize: isNote ? 24 : 20, strokeColor: '#1f2937', groupIds: [groupId],
      },
    ] as Parameters<typeof convertToExcalidrawElements>[0])
    api.updateScene({ elements: [...api.getSceneElements(), ...elements] })
    markChanged()
  }, [markChanged])

  const uploadFileCard = useCallback(async (file: File) => {
    const board = activeBoardRef.current
    if (!board) return
    try {
      const asset = await uploadCanvasAsset(board.id, file, crypto.randomUUID())
      insertTemplate('file', asset.originalName, asset.url, asset.id)
      setMessage(`已插入附件：${asset.originalName}`)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '附件上传失败。')
    }
  }, [insertTemplate])

  const importScene = useCallback(async (file: File) => {
    const api = apiRef.current
    if (!api) return
    try {
      const restored = await loadFromBlob(file, api.getAppState(), api.getSceneElements())
      api.updateScene({ elements: restored.elements ?? [], appState: restored.appState ?? undefined })
      if (restored.files) api.addFiles(Object.values(restored.files))
      markChanged()
      setMessage(`已导入 ${file.name}`)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '无法导入该画板文件。')
    }
  }, [markChanged])

  const exportJSON = useCallback(() => {
    const scene = serializeCurrentScene()
    if (!scene) return
    downloadBlob(new Blob([JSON.stringify(scene, null, 2)], { type: 'application/json' }), `${safeFileName(titleRef.current)}.excalidraw`)
  }, [serializeCurrentScene])

  const exportImage = useCallback(async (format: 'png' | 'svg') => {
    const api = apiRef.current
    if (!api) return
    const elements = api.getSceneElements()
    if (elements.length === 0) {
      setMessage('当前画板没有可导出的内容。')
      return
    }
    if (format === 'png') {
      const blob = await exportToBlob({ elements, appState: { ...api.getAppState(), exportBackground: true }, files: api.getFiles(), mimeType: 'image/png' })
      downloadBlob(blob, `${safeFileName(titleRef.current)}.png`)
      return
    }
    const svg = await exportToSvg({ elements, appState: { ...api.getAppState(), exportBackground: true }, files: api.getFiles() })
    downloadBlob(new Blob([svg.outerHTML], { type: 'image/svg+xml' }), `${safeFileName(titleRef.current)}.svg`)
  }, [])

  const activeScene = activeBoard?.scene
  const initialData = useMemo(() => activeScene ? {
    elements: activeScene.elements,
    appState: activeScene.appState,
    files: activeScene.files,
  } : undefined, [activeScene])

  const handleEditorAPI = useCallback((api: ExcalidrawImperativeAPI) => {
    apiRef.current = api
  }, [])

  const handleEditorChange = useCallback((elements: readonly ExcalidrawElement[], appState: AppState, files: BinaryFiles) => {
    latestEditorStateRef.current = { elements, appState, files }
    markChanged()
  }, [markChanged])

  return (
    <section className={`canvas-workspace${drawerOpen ? '' : ' canvas-workspace-drawer-closed'}`}>
      <aside className="canvas-board-drawer" aria-label="画板列表">
        <div className="canvas-drawer-heading">
          <div><span>Boards</span><strong>画板</strong></div>
          <button aria-label="新建画板" className="canvas-icon-button" onClick={() => void createBoard()} title="新建画板" type="button"><Icon name="plus" /></button>
        </div>
        <div className="canvas-board-list">
          {boards.map((board) => (
            <div className={`canvas-board-row${board.id === activeBoard?.id ? ' is-active' : ''}`} key={board.id}>
              <button aria-label={`打开 ${board.title}`} className="canvas-board-select" onClick={() => void loadBoard(board.id)} type="button">
                <span className="canvas-board-preview"><Icon name="boards" /></span>
                <span className="canvas-board-copy"><strong>{board.title}</strong><small>{new Date(board.updatedAt).toLocaleString()}</small></span>
              </button>
              <button aria-label={`删除 ${board.title}`} className="canvas-row-delete" onClick={() => void removeBoard(board)} title="删除画板" type="button"><Icon name="trash" /></button>
            </div>
          ))}
        </div>
      </aside>

      <div className="canvas-main">
        <header className="canvas-command-bar">
          <button aria-label={drawerOpen ? '收起画板列表' : '展开画板列表'} className="canvas-icon-button" onClick={() => setDrawerOpen((value) => !value)} title={drawerOpen ? '收起画板列表' : '展开画板列表'} type="button"><Icon name="boards" /></button>
          <input aria-label="画板名称" className="canvas-title-input" disabled={!activeBoard || viewMode} maxLength={120} onChange={(event) => { setTitle(event.target.value); titleRef.current = event.target.value; markChanged() }} value={title} />
          <span className={`canvas-save-state is-${saveState}`}>{saveStateLabel(saveState)}</span>
          <div className="canvas-command-separator" />
          <button aria-label="插入便签" className="canvas-icon-button" disabled={viewMode} onClick={() => insertTemplate('note')} title="插入便签" type="button"><Icon name="note" /></button>
          <button aria-label="插入附件卡片" className="canvas-icon-button" disabled={viewMode} onClick={() => assetInputRef.current?.click()} title="插入附件卡片" type="button"><Icon name="file" /></button>
          <button aria-label="导入画板" className="canvas-icon-button" disabled={viewMode} onClick={() => importInputRef.current?.click()} title="导入画板" type="button"><Icon name="import" /></button>
          <button aria-label="导出 JSON" className="canvas-icon-button" onClick={exportJSON} title="导出 JSON" type="button"><Icon name="json" /></button>
          <button aria-label="导出 PNG" className="canvas-icon-button" onClick={() => void exportImage('png')} title="导出 PNG" type="button"><Icon name="image" /></button>
          <button aria-label="导出 SVG" className="canvas-text-command" onClick={() => void exportImage('svg')} title="导出 SVG" type="button">SVG</button>
          <div className="canvas-command-separator" />
          <button aria-label={viewMode ? '退出只读预览' : '只读预览'} className={`canvas-icon-button${viewMode ? ' is-active' : ''}`} onClick={() => setViewMode((value) => !value)} title={viewMode ? '退出只读预览' : '只读预览'} type="button"><Icon name={viewMode ? 'edit' : 'eye'} /></button>
        </header>

        <div className="canvas-stage">
          {message ? <div className="canvas-message" role="status"><span>{message}</span><button aria-label="关闭提示" onClick={() => setMessage('')} type="button">×</button></div> : null}
          {loading || !activeBoard ? <div className="canvas-loading">正在加载画板...</div> : (
            <Excalidraw
              autoFocus
              excalidrawAPI={handleEditorAPI}
              initialData={initialData as never}
              key={editorKey}
              langCode="zh-CN"
              name={title}
              onChange={handleEditorChange}
              UIOptions={CANVAS_UI_OPTIONS}
              validateEmbeddable={EMBEDDABLE_HOSTS}
              viewModeEnabled={viewMode}
            />
          )}
        </div>
      </div>
      <input accept=".excalidraw,.json" className="canvas-hidden-input" onChange={(event) => { const file = event.target.files?.[0]; if (file) void importScene(file); event.target.value = '' }} ref={importInputRef} type="file" />
      <input className="canvas-hidden-input" onChange={(event) => { const file = event.target.files?.[0]; if (file) void uploadFileCard(file); event.target.value = '' }} ref={assetInputRef} type="file" />
    </section>
  )
}
