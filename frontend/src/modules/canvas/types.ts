export type CanvasScene = {
  type?: string
  version?: number
  source?: string
  elements: unknown[]
  appState: Record<string, unknown>
  files: Record<string, CanvasSceneFile>
}

export type CanvasSceneFile = {
  id: string
  dataURL: string
  mimeType: string
  created: number
  lastRetrieved?: number
}

export type CanvasBoard = {
  id: string
  title: string
  scene?: CanvasScene
  revision: number
  createdAt: string
  updatedAt: string
}

export type CanvasAsset = {
  id: string
  boardId: string
  canvasFileId: string
  originalName: string
  mimeType: string
  sizeBytes: number
  createdAt: string
  url: string
}

export type SaveState = 'idle' | 'dirty' | 'saving' | 'saved' | 'error' | 'conflict'
