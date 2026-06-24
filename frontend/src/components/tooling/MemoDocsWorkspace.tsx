import { useEffect, useMemo, useRef, useState } from 'react'
import { getFileDownloadUrl, getMemo, saveMemo, uploadFile } from '../../lib/api/client'
import type { ToolStatus } from '../../types/tooling'
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
        .map((line: string) => `> ${line}`)
        .join('\n') + '\n\n'
    })
    .replace(/<li>(.*?)<\/li>/gi, '- $1\n')
    .replace(/<p>(.*?)<\/p>/gi, '$1\n\n')
    .replace(/<figure>\s*<img src="(.*?)" alt="(.*?)" \/>\s*<figcaption>(.*?)<\/figcaption>\s*<\/figure>/gis, '![ $2 ]($1)\n\n*$3*\n\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&nbsp;/g, ' ')
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

function countVisibleChars(markdown: string) {
  return Array.from(markdown.replace(/\s+/g, '')).length
}

function countImages(markdown: string) {
  return (markdown.match(/!\[[^\]]*\]\([^)]+\)/g) ?? []).length
}

function insertAtSelection(
  textarea: HTMLTextAreaElement,
  value: string,
  before: string,
  after = '',
  placeholder = '',
) {
  const start = textarea.selectionStart ?? value.length
  const end = textarea.selectionEnd ?? value.length
  const selected = value.slice(start, end)
  const content = selected || placeholder
  const nextValue = `${value.slice(0, start)}${before}${content}${after}${value.slice(end)}`
  const cursorStart = start + before.length
  const cursorEnd = cursorStart + content.length
  return { nextValue, cursorStart, cursorEnd }
}

type SlashCommand = {
  id: string
  label: string
  hint: string
  insert: (selection: { value: string; start: number; end: number }) => {
    value: string
    cursorStart: number
    cursorEnd: number
  }
}

const slashCommands: SlashCommand[] = [
  {
    id: 'text',
    label: '文本',
    hint: '普通段落',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}${value.slice(end)}`,
      cursorStart: start,
      cursorEnd: start,
    }),
  },
  {
    id: 'h1',
    label: '一级标题',
    hint: '# 标题',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}# ${value.slice(end)}`,
      cursorStart: start + 2,
      cursorEnd: start + 2,
    }),
  },
  {
    id: 'h2',
    label: '二级标题',
    hint: '## 标题',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}## ${value.slice(end)}`,
      cursorStart: start + 3,
      cursorEnd: start + 3,
    }),
  },
  {
    id: 'h3',
    label: '三级标题',
    hint: '### 标题',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}### ${value.slice(end)}`,
      cursorStart: start + 4,
      cursorEnd: start + 4,
    }),
  },
  {
    id: 'ul',
    label: '无序列表',
    hint: '- 项目',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}- ${value.slice(end)}`,
      cursorStart: start + 2,
      cursorEnd: start + 2,
    }),
  },
  {
    id: 'ol',
    label: '有序列表',
    hint: '1. 项目',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}1. ${value.slice(end)}`,
      cursorStart: start + 3,
      cursorEnd: start + 3,
    }),
  },
  {
    id: 'quote',
    label: '引用',
    hint: '> 引用内容',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}> ${value.slice(end)}`,
      cursorStart: start + 2,
      cursorEnd: start + 2,
    }),
  },
  {
    id: 'code',
    label: '代码块',
    hint: '```',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}\`\`\`\n${value.slice(end)}\n\`\`\``,
      cursorStart: start + 4,
      cursorEnd: start + 4,
    }),
  },
  {
    id: 'divider',
    label: '分隔线',
    hint: '---',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}---\n${value.slice(end)}`,
      cursorStart: start + 4,
      cursorEnd: start + 4,
    }),
  },
  {
    id: 'link',
    label: '链接',
    hint: '[文本](url)',
    insert: ({ value, start, end }) => ({
      value: `${value.slice(0, start)}[${value.slice(start, end) || '链接文本'}](url)${value.slice(end)}`,
      cursorStart: start + 1,
      cursorEnd: start + 1 + (value.slice(start, end) || '链接文本').length,
    }),
  },
]

export function MemoDocsWorkspace() {
  const textareaRef = useRef<HTMLTextAreaElement | null>(null)
  const saveTimerRef = useRef<number | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const [title, setTitle] = useState('')
  const [editorMarkdown, setEditorMarkdown] = useState('')
  const [viewMode, setViewMode] = useState<'edit' | 'source'>('edit')
  const [slashOpen, setSlashOpen] = useState(false)
  const [slashQuery, setSlashQuery] = useState('')
  const [slashIndex, setSlashIndex] = useState(0)
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '正在载入随手记。' })
  const [isSaving, setIsSaving] = useState(false)
  const [lastSavedAt, setLastSavedAt] = useState('')

  const memoStats = useMemo(() => {
    const chars = countVisibleChars(editorMarkdown)
    const images = countImages(editorMarkdown)
    const lines = editorMarkdown ? editorMarkdown.split('\n').length : 0
    return { chars, images, lines }
  }, [editorMarkdown])

  const slashMatches = useMemo(() => {
    const normalized = slashQuery.trim().toLowerCase()
    if (!slashOpen) return []
    if (!normalized) return slashCommands
    return slashCommands.filter((command) => {
      return `${command.label} ${command.hint}`.toLowerCase().includes(normalized)
    })
  }, [slashOpen, slashQuery])

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
          content_html: markdownToHtml(nextMarkdown),
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

  const applyMarkdownInsert = (before: string, after = '', placeholder = '') => {
    const textarea = textareaRef.current
    if (!textarea) return
    const { nextValue, cursorStart, cursorEnd } = insertAtSelection(textarea, editorMarkdown, before, after, placeholder)
    setEditorMarkdown(nextValue)
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(title, nextValue)
    window.requestAnimationFrame(() => {
      textarea.focus()
      textarea.setSelectionRange(cursorStart, cursorEnd)
    })
  }

  const handleTitleChange = (value: string) => {
    setTitle(value)
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(value, editorMarkdown)
  }

  const handleMarkdownChange = (value: string) => {
    setEditorMarkdown(value)
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(title, value)

    const textarea = textareaRef.current
    if (!textarea) return
    const cursor = textarea.selectionStart ?? value.length
    const beforeCursor = value.slice(0, cursor)
    const slashStart = beforeCursor.lastIndexOf('/')
    const token = slashStart >= 0 ? beforeCursor.slice(slashStart + 1) : ''
    const shouldOpen =
      slashStart >= 0 &&
      beforeCursor[slashStart - 1] !== '/' &&
      !/\s/.test(beforeCursor.slice(slashStart + 1, cursor))
    setSlashOpen(shouldOpen)
    setSlashQuery(shouldOpen ? token : '')
    setSlashIndex(0)
  }

  const handleUpload = async (file: File) => {
    setStatus({ kind: 'idle', message: '图片上传中。' })
    const response = await uploadFile(file)
    applyMarkdownInsert(`![${file.name}](${getFileDownloadUrl(response.file.id)})\n\n`, '', file.name)
    setStatus({ kind: 'success', message: '图片已插入并保存到云端。' })
  }

  const closeSlashMenu = () => {
    setSlashOpen(false)
    setSlashQuery('')
    setSlashIndex(0)
  }

  const applySlashCommand = (command: SlashCommand) => {
    const textarea = textareaRef.current
    if (!textarea) return
    const value = editorMarkdown
    const cursor = textarea.selectionStart ?? value.length
    const beforeCursor = value.slice(0, cursor)
    const slashStart = beforeCursor.lastIndexOf('/')
    const selection = {
      value,
      start: slashStart >= 0 ? slashStart : cursor,
      end: cursor,
    }
    const result = command.insert(selection)
    const nextValue = result.value
    setEditorMarkdown(nextValue)
    closeSlashMenu()
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(title, nextValue)
    window.requestAnimationFrame(() => {
      textarea.focus()
      const start = result.cursorStart ?? selection.start
      const end = result.cursorEnd ?? start
      textarea.setSelectionRange(start, end)
    })
  }

  const previewText = editorMarkdown.trim() || '从标题开始写，#、##、-、> 和图片链接都可以直接输入。'

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

          <div className="memo-mode-switch" role="tablist" aria-label="显示模式">
            <button
              aria-selected={viewMode === 'edit'}
              className={`memo-mode-button${viewMode === 'edit' ? ' memo-mode-button-active' : ''}`}
              onClick={() => setViewMode('edit')}
              role="tab"
              type="button"
            >
              编辑
            </button>
            <button
              aria-selected={viewMode === 'source'}
              className={`memo-mode-button${viewMode === 'source' ? ' memo-mode-button-active' : ''}`}
              onClick={() => setViewMode('source')}
              role="tab"
              type="button"
            >
              源码
            </button>
          </div>

          <div className="memo-editor-toolbar" aria-label="编辑工具栏">
            <button className="memo-tool-button" onClick={() => applyMarkdownInsert('**', '**', '加粗文本')} type="button" aria-label="加粗">
              <strong>B</strong>
            </button>
            <button className="memo-tool-button" onClick={() => applyMarkdownInsert('*', '*', '斜体文本')} type="button" aria-label="斜体">
              <em>I</em>
            </button>
            <button className="memo-tool-button" onClick={() => applyMarkdownInsert('> ', '', '引用内容')} type="button" aria-label="引用">
              <span aria-hidden="true">“</span>
            </button>
            <button className="memo-tool-button" onClick={() => applyMarkdownInsert('- ', '', '清单项')} type="button" aria-label="清单">
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

          {viewMode === 'edit' ? (
            <textarea
              aria-label="随手记内容"
              className="memo-rich-editor memo-markdown-editor"
              onChange={(event) => handleMarkdownChange(event.target.value)}
              onKeyDown={(event) => {
                if (!slashOpen) return
                if (event.key === 'ArrowDown') {
                  event.preventDefault()
                  setSlashIndex((value) => (slashMatches.length === 0 ? 0 : (value + 1) % slashMatches.length))
                  return
                }
                if (event.key === 'ArrowUp') {
                  event.preventDefault()
                  setSlashIndex((value) => (slashMatches.length === 0 ? 0 : (value - 1 + slashMatches.length) % slashMatches.length))
                  return
                }
                if (event.key === 'Enter' || event.key === 'Tab') {
                  if (slashMatches[slashIndex]) {
                    event.preventDefault()
                    applySlashCommand(slashMatches[slashIndex])
                    return
                  }
                }
                if (event.key === 'Escape') {
                  event.preventDefault()
                  closeSlashMenu()
                }
              }}
              ref={textareaRef}
              value={editorMarkdown}
            />
          ) : (
            <pre aria-label="随手记源码" className="memo-source-view">
              <code>{editorMarkdown || '空文档'}</code>
            </pre>
          )}
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
            <span>{viewMode === 'edit' ? 'Markdown 编辑' : '源码显示'}</span>
            <span>{previewText}</span>
          </div>
          {slashOpen && viewMode === 'edit' ? (
            <div className="memo-slash-menu" role="listbox" aria-label="命令选择">
              <div className="memo-slash-search">/{slashQuery || '输入关键字'}</div>
              {slashMatches.length > 0 ? (
                slashMatches.map((command, index) => (
                  <button
                    aria-selected={index === slashIndex}
                    className={`memo-slash-item${index === slashIndex ? ' memo-slash-item-active' : ''}`}
                    key={command.id}
                    onMouseEnter={() => setSlashIndex(index)}
                    onMouseDown={(event) => {
                      event.preventDefault()
                      applySlashCommand(command)
                    }}
                    role="option"
                    type="button"
                  >
                    <span className="memo-slash-item-main">{command.label}</span>
                    <span className="memo-slash-item-hint">{command.hint}</span>
                  </button>
                ))
              ) : (
                <div className="memo-slash-empty">没有匹配的命令</div>
              )}
            </div>
          ) : null}
        </div>
        <StatusBanner
          right={lastSavedAt ? `最后保存 ${formatDate(lastSavedAt)}` : '尚未保存'}
          status={status}
        />
      </section>
    </div>
  )
}
