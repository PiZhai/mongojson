import { describe, expect, it, vi } from 'vitest'
import type { CanvasAsset, CanvasScene } from '../types'
import { externalizeCanvasFiles } from './scenePersistence'

const scene: CanvasScene = {
  elements: [],
  appState: {},
  files: {
    image1: {
      id: 'image1',
      dataURL: 'data:image/png;base64,aGVsbG8=',
      mimeType: 'image/png',
      created: 1,
    },
  },
}

describe('externalizeCanvasFiles', () => {
  it('uploads inline files and persists only the remote asset URL', async () => {
    const upload = vi.fn(async (_boardId: string, file: File, fileId: string) => ({
      id: 'asset1', boardId: 'board1', canvasFileId: fileId, originalName: file.name,
      mimeType: file.type, sizeBytes: file.size, createdAt: '', url: '/api/canvas/assets/asset1/content',
    }) satisfies CanvasAsset)

    const result = await externalizeCanvasFiles('board1', scene, new Map(), upload)

    expect(upload).toHaveBeenCalledOnce()
    expect(upload.mock.calls[0][1].name).toBe('image1.png')
    expect(result.files.image1.dataURL).toBe('/api/canvas/assets/asset1/content')
    expect(scene.files.image1.dataURL).toContain('data:image/png')
  })

  it('reuses a known URL without uploading the file again', async () => {
    const upload = vi.fn()
    const result = await externalizeCanvasFiles('board1', scene, new Map([['image1', '/cached/image1']]), upload)

    expect(upload).not.toHaveBeenCalled()
    expect(result.files.image1.dataURL).toBe('/cached/image1')
  })
})
