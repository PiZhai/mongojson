export type FileSummary = {
  id: string
  original_name: string
  stored_name: string
  mime_type: string
  size_bytes: number
  category: string
  expires_at?: string | null
  created_at: string
}

export type MemoFloatingCardRecord = {
  id: string
  content: string
  color: string
  created_at: string
  updated_at: string
}

export type MemoDocumentRecord = {
  id: string
  slug: string
  title: string
  content_json: unknown[]
  content_html: string
  content_text: string
  floating_cards: MemoFloatingCardRecord[]
  schema_version: number
  revision: number
  editor_type: 'vditor' | 'blocknote' | string
  created_at: string
  updated_at: string
}

export type MemoSideNoteBody = {
  text: string
}

export type MemoSideNoteStatus = 'active' | 'orphaned' | 'archived'

export type MemoSideNoteRecord = {
  id: string
  document_id: string
  anchor_block_id?: string | null
  body_json: MemoSideNoteBody
  color: string
  sort_order: number
  collapsed: boolean
  status: MemoSideNoteStatus
  revision: number
  created_at: string
  updated_at: string
}

export type MemoEditorSnapshot = {
  blocks: unknown[]
  markdown: string
  html: string
  activeBlockId: string | null
}

export type MemoWorkspaceMode = 'standard' | 'focus' | 'wide'
export type MemoRecord = MemoDocumentRecord
