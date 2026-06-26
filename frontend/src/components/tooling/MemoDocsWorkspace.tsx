import { useEffect, useMemo, useRef, useState } from 'react'
import { getFileDownloadUrl, getMemo, saveMemo, uploadFile } from '../../lib/api/client'
import type { ToolStatus } from '../../types/tooling'
import { StatusBanner } from '../common/StatusBanner'
import { VditorMemoEditor, type VditorMemoEditorHandle } from '../editor/VditorMemoEditor'

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
        .map((line: string) => `> ${line}`)
        .join('\n') + '\n\n'
    })
    .replace(/<li>(.*?)<\/li>/gi, '- $1\n')
    .replace(/<p>(.*?)<\/p>/gi, '$1\n\n')
    .replace(/<figure>\s*<img src="(.*?)" alt="(.*?)" \/>\s*<figcaption>(.*?)<\/figcaption>\s*<\/figure>/gis, '![$2]($1)\n\n*$3*\n\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&nbsp;/g, ' ')
}

function countVisibleChars(markdown: string) {
  return Array.from(markdown.replace(/\s+/g, '')).length
}

function countImages(markdown: string) {
  return (markdown.match(/!\[[^\]]*\]\([^)]+\)/g) ?? []).length
}

export function MemoDocsWorkspace() {
  const editorRef = useRef<VditorMemoEditorHandle | null>(null)
  const saveTimerRef = useRef<number | null>(null)
  const [title, setTitle] = useState('')
  const [editorMarkdown, setEditorMarkdown] = useState('')
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '正在载入随手记。' })
  const [isSaving, setIsSaving] = useState(false)
  const [lastSavedAt, setLastSavedAt] = useState('')

  const memoStats = useMemo(() => {
    const chars = countVisibleChars(editorMarkdown)
    const images = countImages(editorMarkdown)
    const lines = editorMarkdown ? editorMarkdown.split('\n').length : 0
    return { chars, images, lines }
  }, [editorMarkdown])

  useEffect(() => {
    void (async () => {
      try {
        const response = await getMemo(MEMO_SLUG)
        const nextMarkdown =
          response.memo.content_text?.trim().length > 0
            ? response.memo.content_text
            : toMarkdown(response.memo.content_html)
        setTitle(response.memo.title)
        setEditorMarkdown(nextMarkdown)
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

  const scheduleSave = (nextTitle: string, nextMarkdown: string) => {
    if (saveTimerRef.current) {
      window.clearTimeout(saveTimerRef.current)
    }
    saveTimerRef.current = window.setTimeout(async () => {
      setIsSaving(true)
      try {
        const response = await saveMemo({
          slug: MEMO_SLUG,
          title: nextTitle || '随手记',
          content_html: editorRef.current?.getHtml() ?? '',
          content_text: nextMarkdown,
        })
        setLastSavedAt(response.memo.updated_at)
        setStatus({ kind: 'success', message: '已自动保存。' })
      } catch (error) {
        setStatus({ kind: 'error', message: error instanceof Error ? error.message : '保存失败。' })
      } finally {
        setIsSaving(false)
      }
    }, 700)
  }

  const commitMarkdown = (nextMarkdown: string) => {
    setEditorMarkdown(nextMarkdown)
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(title, nextMarkdown)
  }

  const handleTitleChange = (value: string) => {
    setTitle(value)
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(value, editorMarkdown)
  }

  const handleUpload = async (file: File) => {
    setStatus({ kind: 'idle', message: '图片上传中。' })
    const response = await uploadFile(file)
    const markdown = `![${file.name}](${getFileDownloadUrl(response.file.id)})`
    setStatus({ kind: 'success', message: '图片已插入并保存到云端。' })
    return markdown
  }

  return (
    <div className="memo-focus-shell">
      <section className="memo-focus-header" aria-labelledby="memo-page-title">
        <div className="memo-focus-heading">
          <p className="memo-focus-kicker">Cloud Memo</p>
          <h2 className="memo-focus-title" id="memo-page-title">随手记</h2>
        </div>
        <div className="memo-focus-stats" aria-label="随手记状态">
          <span>{memoStats.chars} 字</span>
          <span>{memoStats.images} 图</span>
          <span>{memoStats.lines} 行</span>
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

          <VditorMemoEditor
            onChange={commitMarkdown}
            onUpload={handleUpload}
            placeholder="从标题开始写，#、##、-、> 和图片链接都可以直接输入。"
            ref={editorRef}
            value={editorMarkdown}
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
