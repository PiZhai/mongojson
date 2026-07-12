import { API_BASE, apiRequest } from '../../platform/http/client'
import type {
  FileSummary,
  MemoDocumentRecord,
  MemoFloatingCardRecord,
  MemoSideNoteBody,
  MemoSideNoteRecord,
  MemoSideNoteStatus,
} from './types'

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

export async function getMemoDocument(slug = 'inbox') {
  return apiRequest<{ document: MemoDocumentRecord }>(`${API_BASE}/memo/documents/${encodeURIComponent(slug)}`)
}

export async function createMemoDocument(payload: { slug: string; title: string }) {
  return apiRequest<{ document: MemoDocumentRecord }>(`${API_BASE}/memo/documents`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function saveMemoDocument(id: string, payload: {
  title: string
  content_json: unknown[]
  content_markdown: string
  content_html: string
  schema_version: number
  revision: number
  editor_type: 'blocknote'
}) {
  return apiRequest<{ document: MemoDocumentRecord }>(`${API_BASE}/memo/documents/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function listMemoSideNotes(documentId: string) {
  return apiRequest<{ notes: MemoSideNoteRecord[] }>(`${API_BASE}/memo/documents/${encodeURIComponent(documentId)}/notes`)
}

export type MemoSideNotePayload = {
  id?: string
  anchor_block_id?: string | null
  body_json: MemoSideNoteBody
  color: string
  sort_order: number
  collapsed: boolean
  status: MemoSideNoteStatus
  revision?: number
}

export async function createMemoSideNote(documentId: string, payload: MemoSideNotePayload) {
  return apiRequest<{ note: MemoSideNoteRecord }>(`${API_BASE}/memo/documents/${encodeURIComponent(documentId)}/notes`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function saveMemoSideNote(id: string, payload: MemoSideNotePayload & { revision: number }) {
  return apiRequest<{ note: MemoSideNoteRecord }>(`${API_BASE}/memo/notes/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function deleteMemoSideNote(id: string) {
  await fetch(`${API_BASE}/memo/notes/${encodeURIComponent(id)}`, { method: 'DELETE' }).then(async (response) => {
    if (!response.ok) throw new Error((await response.text()) || `Request failed: ${response.status}`)
  })
}

// Compatibility API retained for one rollback cycle.
export async function getMemo(slug = 'inbox') {
  const query = slug ? `?slug=${encodeURIComponent(slug)}` : ''
  return apiRequest<{ memo: MemoDocumentRecord }>(`${API_BASE}/memo${query}`)
}

export async function saveMemo(payload: {
  slug?: string
  title: string
  content_html: string
  content_text: string
  floating_cards?: MemoFloatingCardRecord[]
}) {
  return apiRequest<{ memo: MemoDocumentRecord }>(`${API_BASE}/memo`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}
