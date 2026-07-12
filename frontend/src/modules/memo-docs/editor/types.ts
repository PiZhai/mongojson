import type { MemoEditorSnapshot } from '../types'

export type MemoEditorHandle = {
  focus: () => void
  focusBlock: (blockId: string) => void
  getSnapshot: () => MemoEditorSnapshot
  insertCallout: (text: string) => void
  replaceBlocks: (blocks: unknown[]) => void
  replaceFromHTML: (html: string) => void
  replaceFromMarkdown: (markdown: string) => void
}

export type MemoEditorProps = {
  activeNoteBlockId?: string | null
  initialBlocks: unknown[]
  legacyHTML: string
  legacyMarkdown: string
  noteCounts: Record<string, number>
  notePreviews: Record<string, string[]>
  onActiveBlockChange: (blockId: string | null) => void
  onChange: (snapshot: MemoEditorSnapshot) => void
  onDropSideNote: (noteId: string, blockId: string) => void
  onOpenBlockNotes: (blockId: string) => void
  onReady: (snapshot: MemoEditorSnapshot) => void
  onUpload: (file: File) => Promise<string>
}
