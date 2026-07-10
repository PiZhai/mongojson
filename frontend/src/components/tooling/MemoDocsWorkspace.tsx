import { useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent } from 'react'
import { getFileDownloadUrl, getMemo, saveMemo, uploadFile } from '../../lib/api/client'
import {
  DEFAULT_FLOATING_CARD_COLOR,
  createFloatingCard,
  deserializeFloatingCards,
  hasMeaningfulFloatingCards,
  loadFloatingCardsFromStorage,
  normalizeFloatingCardColor,
  saveFloatingCardsToStorage,
  serializeFloatingCards,
  type FloatingCard,
} from '../../lib/memo/floatingCards'
import type { ToolStatus } from '../../types/tooling'
import { StatusBanner } from '../common/StatusBanner'
import {
  VditorMemoEditor,
  type VditorMemoEditorHandle,
} from '../editor/VditorMemoEditor'

const MEMO_SLUG = 'inbox'

type FloatingCardDropPosition = 'before' | 'after'
type FloatingCardColorOption = {
  border: string
  label: string
  value: string
}
type MemoSaveMessages = {
  error: string
  pending: string
  success: string
}

const floatingCardColors: FloatingCardColorOption[] = [
  { label: '便签黄', value: '#fff7d6', border: '#f0dc91' },
  { label: '天空蓝', value: '#eaf6ff', border: '#badfff' },
  { label: '樱花粉', value: '#fff0f5', border: '#ffc8d8' },
  { label: '薄荷绿', value: '#edfbf2', border: '#bce8cc' },
  { label: '淡紫色', value: '#f4efff', border: '#d8c7ff' },
]

const DEFAULT_EDITOR_MODE = 'ir'
const DEFAULT_EDITOR_THEME = 'classic'
const DEFAULT_CONTENT_THEME = 'light'
const DEFAULT_CODE_THEME = 'atom-one-light'

function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

function countVisibleChars(markdown: string) {
  return Array.from(markdown.replace(/\s+/g, '')).length
}

function countImages(markdown: string) {
  return (markdown.match(/!\[[^\]]*\]\([^)]+\)/g) ?? []).length
}

function parseHexColor(value: string) {
  const normalized = normalizeFloatingCardColor(value).slice(1)
  return {
    blue: Number.parseInt(normalized.slice(4, 6), 16),
    green: Number.parseInt(normalized.slice(2, 4), 16),
    red: Number.parseInt(normalized.slice(0, 2), 16),
  }
}

function toHexChannel(value: number) {
  return Math.max(0, Math.min(255, Math.round(value))).toString(16).padStart(2, '0')
}

function mixHexColor(source: string, target: string, targetWeight: number) {
  const sourceRgb = parseHexColor(source)
  const targetRgb = parseHexColor(target)
  const sourceWeight = 1 - targetWeight
  return `#${toHexChannel(sourceRgb.red * sourceWeight + targetRgb.red * targetWeight)}${toHexChannel(
    sourceRgb.green * sourceWeight + targetRgb.green * targetWeight,
  )}${toHexChannel(sourceRgb.blue * sourceWeight + targetRgb.blue * targetWeight)}`
}

function getFloatingCardBorderColor(color: string) {
  return floatingCardColors.find((item) => item.value === normalizeFloatingCardColor(color))?.border ?? mixHexColor(color, '#6f7d91', 0.28)
}

function getFloatingCardTextColor(color: string) {
  const { red, green, blue } = parseHexColor(color)
  const luminance = (0.2126 * red + 0.7152 * green + 0.0722 * blue) / 255
  return luminance < 0.56 ? '#f8fbff' : '#111827'
}

function getFloatingCardMutedColor(color: string) {
  return getFloatingCardTextColor(color) === '#f8fbff' ? '#e7edf6' : '#4f5f76'
}

function getFloatingCardStyle(card: FloatingCard): CSSProperties {
  const color = normalizeFloatingCardColor(card.color)
  return {
    '--memo-card-bg': color,
    '--memo-card-border': getFloatingCardBorderColor(color),
    '--memo-card-muted': getFloatingCardMutedColor(color),
    '--memo-card-text': getFloatingCardTextColor(color),
  } as CSSProperties
}

function formatCardCreatedAt(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return '---- -- -- -- -- --'
  }
  const pad = (nextValue: number) => String(nextValue).padStart(2, '0')
  const year = String(date.getFullYear())
  const month = pad(date.getMonth() + 1)
  const day = pad(date.getDate())
  const hour = pad(date.getHours())
  const minute = pad(date.getMinutes())
  const second = pad(date.getSeconds())
  return `${year}-${month}-${day} ${hour}-${minute}-${second}`
}

type FloatingCardColorPickerProps = {
  id: string
  label: string
  onChange: (color: string) => void
  value: string
}

function FloatingCardColorPicker({ id, label, onChange, value }: FloatingCardColorPickerProps) {
  const normalizedValue = normalizeFloatingCardColor(value)

  return (
    <div
      aria-label={label}
      className="memo-card-color-picker"
      onDragStart={(event) => event.stopPropagation()}
      onMouseDown={(event) => event.stopPropagation()}
      role="group"
    >
      {floatingCardColors.map((color) => {
        const isActive = normalizedValue === color.value
        return (
          <button
            aria-label={`${label}：${color.label}`}
            aria-pressed={isActive}
            className={`memo-card-color-swatch${isActive ? ' memo-card-color-swatch-active' : ''}`}
            key={color.value}
            onClick={() => onChange(color.value)}
            style={{ '--memo-card-swatch-color': color.value } as CSSProperties}
            title={color.label}
            type="button"
          />
        )
      })}
      <label className="memo-card-custom-color" style={{ '--memo-card-swatch-color': normalizedValue } as CSSProperties} title={`${label}：自定义`}>
        <span className="sr-only">{`${label}：自定义颜色`}</span>
        <input
          aria-label={`${label}：自定义颜色`}
          id={id}
          onChange={(event) => onChange(normalizeFloatingCardColor(event.target.value))}
          type="color"
          value={normalizedValue}
        />
      </label>
    </div>
  )
}

export function MemoDocsWorkspace() {
  const editorRef = useRef<VditorMemoEditorHandle | null>(null)
  const saveTimerRef = useRef<number | null>(null)
  const statsTimerRef = useRef<number | null>(null)
  const hasLoadedRemoteMemoRef = useRef(false)
  const [title, setTitle] = useState('')
  const [editorMarkdown, setEditorMarkdown] = useState('')
  const [documentRevision, setDocumentRevision] = useState('')
  const [floatingCards, setFloatingCards] = useState<FloatingCard[]>(() => loadFloatingCardsFromStorage())
  const titleRef = useRef(title)
  const editorMarkdownRef = useRef(editorMarkdown)
  const floatingCardsRef = useRef(floatingCards)
  const legacyContentHTMLRef = useRef('')
  const [draggingCardId, setDraggingCardId] = useState<string | null>(null)
  const [dragOverCardId, setDragOverCardId] = useState<string | null>(null)
  const [dragOverPosition, setDragOverPosition] = useState<FloatingCardDropPosition>('after')
  const [newFloatingCardColor, setNewFloatingCardColor] = useState(DEFAULT_FLOATING_CARD_COLOR)
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
    titleRef.current = title
  }, [title])

  useEffect(() => {
    floatingCardsRef.current = floatingCards
  }, [floatingCards])

  useEffect(() => {
    void (async () => {
      try {
        const localCards = floatingCardsRef.current
        const response = await getMemo(MEMO_SLUG)
        const hasMarkdown = response.memo.content_text?.trim().length > 0
        const nextMarkdown = hasMarkdown ? response.memo.content_text : ''
        legacyContentHTMLRef.current = hasMarkdown ? '' : response.memo.content_html
        const remoteCards = deserializeFloatingCards(response.memo.floating_cards)
        const shouldUploadLocalCards = remoteCards.length === 0 && hasMeaningfulFloatingCards(localCards)

        titleRef.current = response.memo.title
        editorMarkdownRef.current = nextMarkdown
        setTitle(response.memo.title)
        setEditorMarkdown(nextMarkdown)
        setDocumentRevision(response.memo.updated_at)
        setLastSavedAt(response.memo.updated_at)
        hasLoadedRemoteMemoRef.current = true

        if (shouldUploadLocalCards) {
          saveFloatingCardsToStorage(localCards)
          await saveMemoNow(response.memo.title, nextMarkdown, localCards, response.memo.content_html, {
            error: '悬浮卡片同步失败，本地已保留。',
            pending: '正在把本地悬浮卡片同步到云端。',
            success: '本地悬浮卡片已同步到云端。',
          })
          return
        }

        floatingCardsRef.current = remoteCards
        setFloatingCards(remoteCards)
        saveFloatingCardsToStorage(remoteCards)
        setStatus({ kind: 'success', message: '随手记已从云端加载。' })
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
      if (statsTimerRef.current) {
        window.clearTimeout(statsTimerRef.current)
      }
    }
  }, [])

  async function saveMemoNow(
    nextTitle: string,
    nextMarkdown: string,
    nextFloatingCards: FloatingCard[],
    contentHTML: string,
    messages: MemoSaveMessages,
  ) {
    setIsSaving(true)
    setStatus({ kind: 'idle', message: messages.pending })
    try {
      const response = await saveMemo({
        slug: MEMO_SLUG,
        title: nextTitle || '随手记',
        content_html: contentHTML,
        content_text: nextMarkdown,
        floating_cards: serializeFloatingCards(nextFloatingCards),
      })
      if (nextMarkdown.trim().length > 0) {
        legacyContentHTMLRef.current = ''
      }
      setLastSavedAt(response.memo.updated_at)
      setStatus({ kind: 'success', message: messages.success })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? `${messages.error} ${error.message}` : messages.error })
    } finally {
      setIsSaving(false)
    }
  }

  function scheduleSave(
    nextTitle: string,
    nextMarkdown: string,
    nextFloatingCards = floatingCardsRef.current,
    messages: MemoSaveMessages = {
      error: '保存失败。',
      pending: '正在自动保存。',
      success: '已自动保存。',
    },
  ) {
    if (saveTimerRef.current) {
      window.clearTimeout(saveTimerRef.current)
    }
    saveTimerRef.current = window.setTimeout(async () => {
      const contentHTML = nextMarkdown.trim().length > 0 ? '' : legacyContentHTMLRef.current
      await saveMemoNow(nextTitle, nextMarkdown, nextFloatingCards, contentHTML, messages)
    }, 700)
  }

  const scheduleStatsRefresh = () => {
    if (statsTimerRef.current) return
    statsTimerRef.current = window.setTimeout(() => {
      statsTimerRef.current = null
      setEditorMarkdown(editorMarkdownRef.current)
    }, 400)
  }

  const commitMarkdown = (nextMarkdown: string) => {
    editorMarkdownRef.current = nextMarkdown
    scheduleStatsRefresh()
    setStatus({ kind: 'idle', message: '正在编辑，稍后自动保存。' })
    scheduleSave(titleRef.current, nextMarkdown, floatingCardsRef.current)
  }

  const handleUpload = async (file: File) => {
    setStatus({ kind: 'idle', message: '图片上传中。' })
    const response = await uploadFile(file)
    const markdown = `![${file.name}](${getFileDownloadUrl(response.file.id)})`
    setStatus({ kind: 'success', message: '图片已插入并保存到云端。' })
    return markdown
  }

  const persistFloatingCards = (updater: (cards: FloatingCard[]) => FloatingCard[]) => {
    const nextCards = updater(floatingCardsRef.current)
    floatingCardsRef.current = nextCards
    setFloatingCards(nextCards)
    saveFloatingCardsToStorage(nextCards)

    if (!hasLoadedRemoteMemoRef.current) {
      setStatus({ kind: 'warning', message: '悬浮卡片已保留在本地，云端随手记加载成功后再同步。' })
      return
    }

    scheduleSave(titleRef.current, editorMarkdownRef.current, nextCards, {
      error: '悬浮卡片同步失败，本地已保留。',
      pending: '悬浮卡片正在同步。',
      success: '悬浮卡片已同步。',
    })
  }

  const addFloatingCard = () => {
    persistFloatingCards((cards) => [...cards, createFloatingCard('', newFloatingCardColor)])
  }

  const updateFloatingCard = (cardId: string, content: string) => {
    const updatedAt = new Date().toISOString()
    persistFloatingCards((cards) => cards.map((card) => (card.id === cardId ? { ...card, content, updatedAt } : card)))
  }

  const updateFloatingCardColor = (cardId: string, color: string) => {
    const updatedAt = new Date().toISOString()
    persistFloatingCards((cards) =>
      cards.map((card) => (card.id === cardId ? { ...card, color: normalizeFloatingCardColor(color), updatedAt } : card)),
    )
  }

  const removeFloatingCard = (cardId: string) => {
    persistFloatingCards((cards) => cards.filter((card) => card.id !== cardId))
  }

  const reorderFloatingCard = (sourceCardId: string, targetCardId: string, position: FloatingCardDropPosition) => {
    persistFloatingCards((cards) => {
      if (sourceCardId === targetCardId) {
        return cards
      }
      const currentIndex = cards.findIndex((card) => card.id === sourceCardId)
      const targetIndex = cards.findIndex((card) => card.id === targetCardId)
      if (currentIndex < 0 || targetIndex < 0) {
        return cards
      }

      const nextCards = [...cards]
      const [card] = nextCards.splice(currentIndex, 1)
      const nextTargetIndex = nextCards.findIndex((nextCard) => nextCard.id === targetCardId)
      const insertIndex = position === 'after' ? nextTargetIndex + 1 : nextTargetIndex
      nextCards.splice(insertIndex, 0, card)
      return nextCards
    })
  }

  const handleFloatingCardDragStart = (cardId: string, event: DragEvent<HTMLDivElement>) => {
    setDraggingCardId(cardId)
    setDragOverCardId(null)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', cardId)
  }

  const handleFloatingCardDragOver = (cardId: string, event: DragEvent<HTMLElement>) => {
    event.preventDefault()
    if (!draggingCardId || draggingCardId === cardId) {
      setDragOverCardId(null)
      return
    }

    const bounds = event.currentTarget.getBoundingClientRect()
    const position = event.clientY - bounds.top < bounds.height / 2 ? 'before' : 'after'
    setDragOverCardId(cardId)
    setDragOverPosition(position)
    event.dataTransfer.dropEffect = 'move'
  }

  const handleFloatingCardDrop = (targetCardId: string, event: DragEvent<HTMLElement>) => {
    event.preventDefault()
    const sourceCardId = draggingCardId ?? event.dataTransfer.getData('text/plain')
    const bounds = event.currentTarget.getBoundingClientRect()
    const position = event.clientY - bounds.top < bounds.height / 2 ? 'before' : 'after'
    if (sourceCardId) {
      reorderFloatingCard(sourceCardId, targetCardId, position)
    }
    setDraggingCardId(null)
    setDragOverCardId(null)
  }

  const handleFloatingCardDragEnd = () => {
    setDraggingCardId(null)
    setDragOverCardId(null)
  }

  return (
    <div className="memo-focus-shell tool-workspace">
      <div className="memo-focus-body tool-layout">
        <section className="memo-focus-card tool-primary" aria-labelledby="memo-page-title">
          <div className="memo-editor-shell">
            <div className="memo-title-row">
              <div className="memo-focus-heading">
                <h2 className="memo-focus-title" id="memo-page-title">随手记</h2>
              </div>
              <div className="memo-focus-stats" aria-label="随手记状态">
                <span>{memoStats.chars} 字</span>
                <span>{memoStats.images} 图</span>
                <span>{memoStats.lines} 行</span>
                <span>{isSaving ? '保存中' : lastSavedAt ? formatDate(lastSavedAt) : '准备就绪'}</span>
              </div>
            </div>

            <VditorMemoEditor
              codeTheme={DEFAULT_CODE_THEME}
              contentTheme={DEFAULT_CONTENT_THEME}
              documentRevision={documentRevision}
              initialValue={editorMarkdown}
              mode={DEFAULT_EDITOR_MODE}
              onChange={commitMarkdown}
              onUpload={handleUpload}
              placeholder="从标题开始写，#、##、-、> 和图片链接都可以直接输入。"
              ref={editorRef}
              theme={DEFAULT_EDITOR_THEME}
            />
          </div>
          <StatusBanner
            right={lastSavedAt ? `最后保存 ${formatDate(lastSavedAt)}` : '尚未保存'}
            status={status}
          />
        </section>

        <aside className="memo-floating-panel tool-secondary" aria-label="随手记悬浮卡片栏">
          <div className="memo-floating-panel-header">
            <span className="memo-floating-panel-title">悬浮卡片</span>
            <div className="memo-floating-panel-actions">
              <FloatingCardColorPicker
                id="memo-new-floating-card-color"
                label="新卡片颜色"
                onChange={setNewFloatingCardColor}
                value={newFloatingCardColor}
              />
              <button
                aria-label="添加悬浮卡片"
                className="memo-floating-icon-button"
                onClick={addFloatingCard}
                title="添加卡片"
                type="button"
              >
                +
              </button>
            </div>
          </div>
          <div className="memo-floating-card-list">
            {floatingCards.map((card, index) => {
              const isDragging = draggingCardId === card.id
              const isDropTarget = dragOverCardId === card.id
              const cardClassName = [
                'memo-floating-card',
                isDragging ? 'memo-floating-card-dragging' : '',
                isDropTarget ? `memo-floating-card-drop-${dragOverPosition}` : '',
              ].filter(Boolean).join(' ')

              return (
                <article
                  className={cardClassName}
                  key={card.id}
                  onDragOver={(event) => handleFloatingCardDragOver(card.id, event)}
                  onDrop={(event) => handleFloatingCardDrop(card.id, event)}
                  style={getFloatingCardStyle(card)}
                >
                  <div
                    aria-label={`拖拽排序卡片 ${index + 1}`}
                    className="memo-floating-card-toolbar"
                    draggable
                    onDragEnd={handleFloatingCardDragEnd}
                    onDragStart={(event) => handleFloatingCardDragStart(card.id, event)}
                    title="拖拽排序"
                  >
                    <span className="memo-floating-card-time">{formatCardCreatedAt(card.createdAt)}</span>
                    <div className="memo-floating-card-actions">
                      <FloatingCardColorPicker
                        id={`memo-floating-card-color-${card.id}`}
                        label={`卡片 ${index + 1} 颜色`}
                        onChange={(color) => updateFloatingCardColor(card.id, color)}
                        value={card.color}
                      />
                      <button
                        aria-label={`删除卡片 ${index + 1}`}
                        className="memo-floating-delete-button"
                        onClick={() => removeFloatingCard(card.id)}
                        title="删除"
                        type="button"
                      >
                        ×
                      </button>
                    </div>
                  </div>
                  <label className="sr-only" htmlFor={`memo-floating-card-input-${card.id}`}>卡片内容</label>
                  <textarea
                    className="memo-floating-card-input"
                    id={`memo-floating-card-input-${card.id}`}
                    onChange={(event) => updateFloatingCard(card.id, event.target.value)}
                    value={card.content}
                  />
                </article>
              )
            })}
          </div>
          <button className="memo-floating-bottom-add" onClick={addFloatingCard} type="button">
            + 添加卡片
          </button>
        </aside>
      </div>
    </div>
  )
}
