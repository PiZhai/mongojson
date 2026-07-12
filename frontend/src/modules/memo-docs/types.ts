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

export type MemoRecord = {
  id: string
  slug: string
  title: string
  content_html: string
  content_text: string
  floating_cards: MemoFloatingCardRecord[]
  created_at: string
  updated_at: string
}

export type MemoFloatingCardRecord = {
  id: string
  content: string
  color: string
  created_at: string
  updated_at: string
}
