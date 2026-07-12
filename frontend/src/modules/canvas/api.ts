import { apiRequest, resolveApiUrl } from '../../platform/http/client'
import type { CanvasAsset, CanvasBoard, CanvasScene } from './types'

type RemoteBoard = {
  id: string
  title: string
  scene?: CanvasScene
  revision: number
  created_at: string
  updated_at: string
}

type RemoteAsset = {
  id: string
  board_id: string
  canvas_file_id: string
  original_name: string
  mime_type: string
  size_bytes: number
  created_at: string
}

function toBoard(board: RemoteBoard): CanvasBoard {
  return {
    id: board.id,
    title: board.title,
    scene: board.scene,
    revision: board.revision,
    createdAt: board.created_at,
    updatedAt: board.updated_at,
  }
}

export async function listCanvasBoards() {
  const response = await apiRequest<{ boards: RemoteBoard[] }>(resolveApiUrl('/canvas/boards').toString())
  return response.boards.map(toBoard)
}

export async function createCanvasBoard(title: string) {
  const response = await apiRequest<{ board: RemoteBoard }>(resolveApiUrl('/canvas/boards').toString(), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title }),
  })
  return toBoard(response.board)
}

export async function getCanvasBoard(id: string) {
  const response = await apiRequest<{ board: RemoteBoard }>(resolveApiUrl(`/canvas/boards/${id}`).toString())
  return toBoard(response.board)
}

export async function saveCanvasBoard(id: string, title: string, scene: CanvasScene, revision: number) {
  const response = await fetch(resolveApiUrl(`/canvas/boards/${id}`).toString(), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title, scene, revision }),
  })
  if (response.status === 409) throw new Error('REVISION_CONFLICT')
  if (!response.ok) throw new Error((await response.text()) || `Request failed: ${response.status}`)
  const payload = (await response.json()) as { board: RemoteBoard }
  return toBoard(payload.board)
}

export async function deleteCanvasBoard(id: string) {
  const response = await fetch(resolveApiUrl(`/canvas/boards/${id}`).toString(), { method: 'DELETE' })
  if (!response.ok) throw new Error((await response.text()) || `Request failed: ${response.status}`)
}

export async function uploadCanvasAsset(boardId: string, file: File, canvasFileId: string) {
  const body = new FormData()
  body.set('file', file)
  body.set('canvas_file_id', canvasFileId)
  const response = await apiRequest<{ asset: RemoteAsset }>(resolveApiUrl(`/canvas/boards/${boardId}/assets`).toString(), {
    method: 'POST',
    body,
  })
  const asset = response.asset
  return {
    id: asset.id,
    boardId: asset.board_id,
    canvasFileId: asset.canvas_file_id,
    originalName: asset.original_name,
    mimeType: asset.mime_type,
    sizeBytes: asset.size_bytes,
    createdAt: asset.created_at,
    url: resolveApiUrl(`/canvas/assets/${asset.id}/content`).toString(),
  } satisfies CanvasAsset
}
