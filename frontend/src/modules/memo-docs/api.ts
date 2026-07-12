import { API_BASE, apiRequest } from '../../platform/http/client'
import type { FileSummary, MemoFloatingCardRecord, MemoRecord } from './types'

export async function uploadFile(file: File) {
  const formData = new FormData()
  formData.append('file', file)
  return apiRequest<{ file: FileSummary }>(`${API_BASE}/files`, {
    method: 'POST',
    body: formData,
  })
}

export function getFileDownloadUrl(id: string) {
  return `${API_BASE}/files/${id}/download`
}

export async function getMemo(slug = 'inbox') {
  const query = slug ? `?slug=${encodeURIComponent(slug)}` : ''
  return apiRequest<{ memo: MemoRecord }>(`${API_BASE}/memo${query}`)
}

export async function saveMemo(payload: {
  slug?: string
  title: string
  content_html: string
  content_text: string
  floating_cards?: MemoFloatingCardRecord[]
}) {
  return apiRequest<{ memo: MemoRecord }>(`${API_BASE}/memo`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}
