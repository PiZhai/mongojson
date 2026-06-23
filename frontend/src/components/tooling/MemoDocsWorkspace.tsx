import { useEffect, useMemo, useRef, useState } from 'react'
import { getFileDownloadUrl, getMemo, saveMemo, uploadFile } from '../../lib/api/client'
import type { MemoRecord, ToolStatus } from '../../types/tooling'
import { StatusBanner } from '../common/StatusBanner'

const MEMO_SLUG = 'inbox'

function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

function toMarkdown(html: string) {
  return html
    .replace(/<h1>(.*?)<\/h1>/gi, '# $1\n\n')
    .replace(/<h2>(.*?)<\/h2>/gi, '## $1\n\n')
    .replace(/<h3>(.*?)<\/h3>/gi, '### $1\n\n')
    .replace(/<blockquote>(.*?)<\/blockquote>/gis, (_match, content) => {
      return content
        .replace(/<[^>]+>/g, '')
        .split('\n')
        .map((line: string) => `> ${line.trim()}`)
        .join('\n') + '\n\n'
    })
    .replace(/<li>(.*?)<\/li>/gi, '- $1\n')
    .replace(/<p>(.*?)<\/p>/gi, '$1\n\n')
    .replace(/<figure>\s*<img src="(.*?)" alt="(.*?)" \/>\s*<figcaption>(.*?)<\/figcaption>\s*<\/figure>/gis, '![ $2 ]($1)\n\n*$3*\n\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&nbsp;/g, ' ')
    .trim()
}

function markdownToHtml(markdown: string) {
  const lines = markdown.split('\n')
  const html: string[] = []
  let inList = false
  let inQuote = false

  const closeBlocks = () => {
    if (inList) {
      html.push('</ul>')
      inList = false
    }
    if (inQuote) {
      html.push('</blockquote>')
      inQuote = false
    }
  }

  for (const rawLine of lines) {
    const line = rawLine.trimEnd()
    if (!line.trim()) {
      closeBlocks()
      continue
    }

    if (line.startsWith('### ')) {
      closeBlocks()
      html.push(`<h3>${line.slice(4)}</h3>`)
      continue
    }
    if (line.startsWith('## ')) {
      closeBlocks()
      html.push(`<h2>${line.slice(3)}</h2>`)
      continue
    }
    if (line.startsWith('# ')) {
      closeBlocks()
      html.push(`<h1>${line.slice(2)}</h1>`)
      continue
    }
    if (line.startsWith('> ')) {
      if (!inQuote) {
        closeBlocks()
        html.push('<blockquote>')
        inQuote = true
      }
      html.push(line.slice(2))
      continue
    }
    if (line.startsWith('- ')) {
      if (!inList) {
        closeBlocks()
        html.push('<ul>')
        inList = true
      }
      html.push(`<li>${line.slice(2)}</li>`)
      continue
    }
    if (line.startsWith('![')) {
      closeBlocks()
      const match = line.match(/^!\[(.*?)\]\((.*?)\)$/)
      if (match) {
        html.push(`<figure><img src="${match[2]}" alt="${match[1]}" /><figcaption>${match[1]}</figcaption></figure>`)
      }
      continue
    }

    closeBlocks()
    html.push(`<p>${line}</p>`)
  }

  closeBlocks()
  return html.join('\n')
}

function stripHtml(html: string) {
  const doc = new DOMParser().parseFromString(html, 'text/html')
  return (doc.body.textContent ?? '').replace(/\u00a0/g, ' ').trim()
}

function safeInsertMarkdown(markdown: string) {
  const selection = window.getSelection()
  if (!selection || selection.rangeCount === 0) return false
  const range = selection.getRangeAt(0)
  range.deleteContents()
  const fragment = range.createContextualFragment(markdownToHtml(markdown))
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
  const [editorMarkdown, setEditorMarkdown] = useState('')
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '正在载入随手记。' })
  const [isSaving, setIsSaving] = useState(false)
  const [lastSavedAt, setLastSavedAt] = useState('')

  const contentHtml = useMemo(() => markdownToHtml(editorMarkdown || toMarkdown(memo?.content_html ?? '')), [editorMarkdown, memo?.content_html])
  const contentText = useMemo(() => stripHtml(contentHtml), [contentHtml])

  const syncEditorContent = (markdown: string) => {
    const html = markdownToHtml(markdown)
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
        const nextMarkdown = toMarkdown(response.memo.content_html)
        setEditorMarkdown(nextMarkdown)
        syncEditorContent(nextMarkdown)
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

  const scheduleSave = (nextTitle?: string, nextMarkdown?: string) => {
    if (saveTimerRef.current) {
      window.clearTimeout(saveTimerRef.current)
    }
    const pendingTitle = nextTitle ?? title
    const pendingMarkdown = nextMarkdown ?? editorMarkdown
    saveTimerRef.current = window.setTimeout(async () => {
      setIsSaving(true)
      try {
        const html = markdownToHtml(pendingMarkdown)
        const response = await saveMemo({
          slug: MEMO_SLUG,
          title: pendingTitle || '随手记',
          content_html: html,
          content_text: stripHtml(html),
        })
        setMemo(response.memo)
        const nextMarkdownValue = toMarkdown(response.memo.content_html)
        setEditorMarkdown(nextMarkdownValue)
        syncEditorContent(nextMarkdownValue)
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
    const markdown = toMarkdown(html)
    setEditorMarkdown(markdown)
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(undefined, markdown)
  }

  const insertMarkdown = (snippet: string) => {
    editorRef.current?.focus()
    if (safeInsertMarkdown(snippet)) {
      handleEditorInput()
    }
  }

  const handleUpload = async (file: File) => {
    setStatus({ kind: 'idle', message: '图片上传中。' })
    const response = await uploadFile(file)
    insertMarkdown(`![${file.name}](${getFileDownloadUrl(response.file.id)})`)
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
              className="memo-title-input"
              id="memo-title-input"
              onChange={(event) => handleTitleChange(event.target.value)}
              placeholder="写下今天要记住的事"
              value={title}
            />
          </div>

          <div className="memo-editor-toolbar" aria-label="编辑工具栏">
            <button className="memo-tool-button" onClick={() => insertMarkdown('**加粗文本**')} type="button" aria-label="加粗">
              <strong>B</strong>
            </button>
            <button className="memo-tool-button" onClick={() => insertMarkdown('*斜体文本*')} type="button" aria-label="斜体">
              <em>I</em>
            </button>
            <button className="memo-tool-button" onClick={() => insertMarkdown('> 引用内容\n')} type="button" aria-label="引用">
              <span aria-hidden="true">“</span>
            </button>
            <button className="memo-tool-button" onClick={() => insertMarkdown('- 清单项\n')} type="button" aria-label="清单">
              <span aria-hidden="true">•</span>
            </button>
            <button className="memo-tool-button memo-tool-button-wide" onClick={() => fileInputRef.current?.click()} type="button" aria-label="插入图片">
              <svg aria-hidden="true" className="memo-tool-icon" viewBox="0 0 24 24">
                <rect height="14" rx="2" width="16" x="4" y="5" />
                <path d="M8 14l2.5-2.5 2.2 2.2 1.5-1.5L17 15" />
                <circle cx="9" cy="9" r="1" />
              </svg>
            </button>
          </div>

          <div
            aria-label="随手记内容"
            className="memo-rich-editor memo-markdown-editor"
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

          <div className="memo-editor-footer">
            <span>Markdown 文档</span>
            <span>{contentText || '从标题开始写，图片会自动转成 Markdown 链接。'}</span>
          </div>
        </div>
        <StatusBanner
          right={lastSavedAt ? `最后保存 ${formatDate(lastSavedAt)}` : '尚未保存'}
          status={status}
        />
      </section>
    </div>
  )
}
