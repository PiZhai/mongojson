import { useEffect, useMemo, useRef, useState } from 'react'
import { getFileDownloadUrl, getMemo, saveMemo, uploadFile } from '../../lib/api/client'
import type { FileSummary, MemoRecord, ToolStatus } from '../../types/tooling'
import { Panel } from '../common/Panel'
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
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '正在载入随手记。' })
  const [isSaving, setIsSaving] = useState(false)
  const [lastSavedAt, setLastSavedAt] = useState('')
  const [attachments, setAttachments] = useState<FileSummary[]>([])

  const contentHtml = memo?.content_html ?? ''
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
    setAttachments((current) => [response.file, ...current])
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
    <div className="page-shell memo-docs-shell">
      <div className="page-hero memo-hero memo-hero-cloud">
        <div className="page-hero-main">
          <h2 className="page-hero-title">随手记</h2>
          <p className="page-hero-copy">
            一个随时打开就能写的云端笔记区。支持标题、富文本、图片、自动保存，以及在任意网络下继续看到同一份内容。
          </p>
          <div className="page-hero-meta">
            <span className="meta-chip">自动保存到云端</span>
            <span className="meta-chip">图片上传</span>
            <span className="meta-chip">富文本编辑</span>
            <span className="meta-chip">跨设备同步</span>
          </div>
        </div>
        <div className="page-hero-side">
          <div className="hero-stat-grid">
            <article className="hero-stat">
              <span className="hero-stat-label">字数</span>
              <strong className="hero-stat-value">{memoStats.words}</strong>
            </article>
            <article className="hero-stat">
              <span className="hero-stat-label">图片</span>
              <strong className="hero-stat-value">{memoStats.images}</strong>
            </article>
            <article className="hero-stat hero-stat-wide">
              <span className="hero-stat-label">当前状态</span>
              <strong className="hero-stat-value">{isSaving ? '保存中' : lastSavedAt ? `已更新 ${formatDate(lastSavedAt)}` : '准备就绪'}</strong>
            </article>
          </div>
        </div>
      </div>

      <Panel
        actions={
          <>
            <button className="button button-ghost" onClick={() => applyCommand('bold')} type="button">
              粗体
            </button>
            <button className="button button-ghost" onClick={() => applyCommand('italic')} type="button">
              斜体
            </button>
            <button className="button button-ghost" onClick={() => applyCommand('underline')} type="button">
              下划线
            </button>
            <button className="button button-ghost" onClick={() => applyCommand('insertUnorderedList')} type="button">
              清单
            </button>
            <button className="button button-ghost" onClick={() => applyCommand('formatBlock')} type="button">
              引用
            </button>
            <button className="button" onClick={() => fileInputRef.current?.click()} type="button">
              插入图片
            </button>
          </>
        }
        eyebrow="Memo"
        subtitle="编辑区支持键盘输入、格式按钮、粘贴图片和本地上传图片。"
        title="云端随手记"
      >
        <div className="memo-cloud-layout">
          <div className="memo-editor-shell">
            <input
              className="memo-title-input"
              onChange={(event) => handleTitleChange(event.target.value)}
              placeholder="写下今天要记住的事"
              value={title}
            />
            <div className="memo-toolbar-hint">
              内容会在停顿后自动保存，离开页面后重新打开仍会是同一份记录。
            </div>
            <div
              className="memo-rich-editor"
              contentEditable
              dangerouslySetInnerHTML={{ __html: contentHtml }}
              onInput={handleEditorInput}
              ref={editorRef}
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

          <aside className="memo-side-panel">
            <section className="memo-side-card">
              <p className="memo-side-label">摘要</p>
              <p className="memo-side-text">{contentText || '这里会显示正文提取的文字摘要。'}</p>
            </section>
            <section className="memo-side-card">
              <p className="memo-side-label">信息</p>
              <div className="memo-side-metrics">
                <span>阅读 {memoStats.readingMinutes} 分钟</span>
                <span>图片 {memoStats.images}</span>
              </div>
            </section>
            <section className="memo-side-card">
              <p className="memo-side-label">附件</p>
              <div className="memo-attachment-list">
                {attachments.length > 0 ? attachments.map((file) => (
                  <button
                    className="memo-attachment-item"
                    key={file.id}
                    onClick={() => insertImageUrl(getFileDownloadUrl(file.id), file.original_name)}
                    type="button"
                  >
                    {file.original_name}
                  </button>
                )) : <p className="memo-side-text">图片上传后会出现在这里，点击可再次插入正文。</p>}
              </div>
            </section>
          </aside>
        </div>
        <StatusBanner
          right={lastSavedAt ? `最后保存 ${formatDate(lastSavedAt)}` : '尚未保存'}
          status={status}
        />
      </Panel>
    </div>
  )
}
