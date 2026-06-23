import { useEffect, useMemo, useRef, useState } from 'react'
import { getFileDownloadUrl, getMemo, saveMemo, uploadFile } from '../../lib/api/client'
import type { MemoRecord, ToolStatus } from '../../types/tooling'
import { StatusBanner } from '../common/StatusBanner'

const MEMO_SLUG = 'inbox'

function stripHtml(html: string) {
  const doc = new DOMParser().parseFromString(html, 'text/html')
  return (doc.body.textContent ?? '').replace(/\u00a0/g, ' ').trim()
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

function safeInsertHtml(html: string) {
  const selection = window.getSelection()
  if (!selection || selection.rangeCount === 0) return false
  const range = selection.getRangeAt(0)
  range.deleteContents()
  const fragment = range.createContextualFragment(html)
  const lastNode = fragment.lastChild
  range.insertNode(fragment)
  if (lastNode) {
    range.setStartAfter(lastNode)
    range.setEndAfter(lastNode)
    selection.removeAllRanges()
    selection.addRange(range)
  }
  return true
}

export function MemoDocsWorkspace() {
  const editorRef = useRef<HTMLDivElement | null>(null)
  const saveTimerRef = useRef<number | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const [memo, setMemo] = useState<MemoRecord | null>(null)
  const [title, setTitle] = useState('')
  const [editorHtml, setEditorHtml] = useState('')
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '正在载入随手记。' })
  const [isSaving, setIsSaving] = useState(false)
  const [lastSavedAt, setLastSavedAt] = useState('')

  const contentHtml = editorHtml || memo?.content_html || ''
  const contentText = useMemo(() => stripHtml(contentHtml), [contentHtml])

  const syncEditorContent = (html: string) => {
    if (editorRef.current && editorRef.current.innerHTML !== html) {
      editorRef.current.innerHTML = html
    }
  }

  useEffect(() => {
    void (async () => {
      try {
        const response = await getMemo(MEMO_SLUG)
        setMemo(response.memo)
        setTitle(response.memo.title)
        setEditorHtml(response.memo.content_html)
        syncEditorContent(response.memo.content_html)
        setStatus({ kind: 'success', message: '随手记已从云端加载。' })
        setLastSavedAt(response.memo.updated_at)
      } catch (error) {
        setStatus({ kind: 'error', message: error instanceof Error ? error.message : '加载随手记失败。' })
      }
    })()
  }, [])

  useEffect(() => {
    return () => {
      if (saveTimerRef.current) {
        window.clearTimeout(saveTimerRef.current)
      }
    }
  }, [])

  const scheduleSave = (nextTitle?: string, nextHtml?: string) => {
    if (saveTimerRef.current) {
      window.clearTimeout(saveTimerRef.current)
    }
    const pendingTitle = nextTitle ?? title
    const pendingHtml = nextHtml ?? editorRef.current?.innerHTML ?? ''
    saveTimerRef.current = window.setTimeout(async () => {
      setIsSaving(true)
      try {
        const response = await saveMemo({
          slug: MEMO_SLUG,
          title: pendingTitle || '随手记',
          content_html: pendingHtml,
          content_text: stripHtml(pendingHtml),
        })
        setMemo(response.memo)
        setEditorHtml(response.memo.content_html)
        setLastSavedAt(response.memo.updated_at)
        setStatus({ kind: 'success', message: '已自动保存。' })
      } catch (error) {
        setStatus({ kind: 'error', message: error instanceof Error ? error.message : '保存失败。' })
      } finally {
        setIsSaving(false)
      }
    }, 700)
  }

  const handleTitleChange = (value: string) => {
    setTitle(value)
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(value)
  }

  const handleEditorInput = () => {
    const html = editorRef.current?.innerHTML ?? ''
    setEditorHtml(html)
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(undefined, html)
  }

  const applyCommand = (command: 'bold' | 'italic' | 'underline' | 'insertOrderedList' | 'insertUnorderedList' | 'formatBlock') => {
    editorRef.current?.focus()
    if (command === 'formatBlock') {
      document.execCommand(command, false, 'blockquote')
    } else {
      document.execCommand(command)
    }
    handleEditorInput()
  }

  const insertImageUrl = (url: string, alt: string) => {
    editorRef.current?.focus()
    safeInsertHtml(`<figure><img src="${url}" alt="${alt}" /><figcaption>${alt}</figcaption></figure><p></p>`)
    handleEditorInput()
  }

  const handleUpload = async (file: File) => {
    setStatus({ kind: 'idle', message: '图片上传中。' })
    const response = await uploadFile(file)
    insertImageUrl(getFileDownloadUrl(response.file.id), file.name)
    setStatus({ kind: 'success', message: '图片已插入并保存到云端。' })
  }

  const memoStats = useMemo(() => {
    const text = contentText
    const images = (contentHtml.match(/<img\b/gi) ?? []).length
    const words = text ? text.split(/\s+/).filter(Boolean).length : 0
    return {
      words,
      images,
      readingMinutes: Math.max(1, Math.ceil(Math.max(words, text.length / 2) / 220)),
    }
  }, [contentHtml, contentText])

  return (
    <div className="memo-focus-shell">
      <section className="memo-focus-header" aria-labelledby="memo-page-title">
        <div className="memo-focus-heading">
          <p className="memo-focus-kicker">Cloud Memo</p>
          <h2 className="memo-focus-title" id="memo-page-title">随手记</h2>
        </div>
        <div className="memo-focus-stats" aria-label="随手记状态">
          <span>{memoStats.words} 字</span>
          <span>{memoStats.images} 图</span>
          <span>{isSaving ? '保存中' : lastSavedAt ? formatDate(lastSavedAt) : '准备就绪'}</span>
        </div>
      </section>

      <section className="memo-focus-card" aria-label="随手记编辑器">
        <div className="memo-editor-shell">
          <div className="memo-title-row">
            <label className="sr-only" htmlFor="memo-title-input">标题</label>
            <input
              id="memo-title-input"
              className="memo-title-input"
              onChange={(event) => handleTitleChange(event.target.value)}
              placeholder="写下今天要记住的事"
              value={title}
            />
          </div>

          <div className="memo-editor-toolbar" aria-label="编辑工具栏">
            <button aria-label="加粗" className="memo-tool-button" onClick={() => applyCommand('bold')} type="button">
              <strong>B</strong>
            </button>
            <button aria-label="斜体" className="memo-tool-button" onClick={() => applyCommand('italic')} type="button">
              <em>I</em>
            </button>
            <button aria-label="下划线" className="memo-tool-button" onClick={() => applyCommand('underline')} type="button">
              <span className="memo-tool-underline">U</span>
            </button>
            <button aria-label="无序清单" className="memo-tool-button" onClick={() => applyCommand('insertUnorderedList')} type="button">
              <span aria-hidden="true">•</span>
            </button>
            <button aria-label="引用" className="memo-tool-button" onClick={() => applyCommand('formatBlock')} type="button">
              <span aria-hidden="true">“</span>
            </button>
            <button aria-label="插入图片" className="memo-tool-button memo-tool-button-wide" onClick={() => fileInputRef.current?.click()} type="button">
              <svg aria-hidden="true" className="memo-tool-icon" viewBox="0 0 24 24">
                <rect height="14" rx="2" width="16" x="4" y="5" />
                <path d="M8 14l2.5-2.5 2.2 2.2 1.5-1.5L17 15" />
                <circle cx="9" cy="9" r="1" />
              </svg>
            </button>
          </div>

          <div
            aria-label="随手记内容"
            className="memo-rich-editor"
            contentEditable
            onInput={handleEditorInput}
            ref={editorRef}
            role="textbox"
            suppressContentEditableWarning
          />
          <input
            accept="image/*"
            className="memo-file-input"
            onChange={async (event) => {
              const file = event.target.files?.[0]
              if (!file) return
              try {
                await handleUpload(file)
              } finally {
                event.target.value = ''
              }
            }}
            ref={fileInputRef}
            type="file"
          />
        </div>
        <StatusBanner
          right={lastSavedAt ? `最后保存 ${formatDate(lastSavedAt)}` : '尚未保存'}
          status={status}
        />
      </section>
    </div>
  )
}
