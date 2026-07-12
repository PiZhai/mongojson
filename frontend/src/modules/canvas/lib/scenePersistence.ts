import type { CanvasAsset, CanvasScene } from '../types'

type AssetUploader = (boardId: string, file: File, canvasFileId: string) => Promise<CanvasAsset>

export async function externalizeCanvasFiles(
  boardId: string,
  scene: CanvasScene,
  knownUrls: Map<string, string>,
  upload: AssetUploader,
) {
  const files = { ...scene.files }
  for (const [fileId, file] of Object.entries(files)) {
    if (!file.dataURL?.startsWith('data:')) continue
    const cached = knownUrls.get(fileId)
    if (cached) {
      files[fileId] = { ...file, dataURL: cached }
      continue
    }
    const blob = await fetch(file.dataURL).then((response) => response.blob())
    const extension = file.mimeType.split('/')[1]?.replace('jpeg', 'jpg') ?? 'bin'
    const asset = await upload(boardId, new File([blob], `${fileId}.${extension}`, { type: file.mimeType }), fileId)
    knownUrls.set(fileId, asset.url)
    files[fileId] = { ...file, dataURL: asset.url }
  }
  return { ...scene, files }
}
