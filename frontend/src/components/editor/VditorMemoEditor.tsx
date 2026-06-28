import Vditor from 'vditor'
import 'vditor/dist/index.css'
import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef } from 'react'
import { ensureTrailingNewlines, getTailClickLine } from '../../lib/memo/editorTail'

export type VditorMemoEditorHandle = {
  focus: () => void
  getHtml: () => string
  getMarkdown: () => string
  insertText: (text: string) => void
  isUploading: () => boolean
}

export type VditorMemoMode = 'wysiwyg' | 'ir' | 'sv'
export type VditorMemoTheme = 'classic'
export type VditorMemoContentTheme = 'ant-design' | 'light' | 'wechat'
export type VditorMemoCodeTheme = string

type VditorMemoEditorProps = {
  codeTheme?: VditorMemoCodeTheme
  contentTheme?: VditorMemoContentTheme
  mode?: VditorMemoMode
  onChange: (value: string) => void
  onUpload?: (file: File) => Promise<string>
  placeholder?: string
  theme?: VditorMemoTheme
  value: string
}

type WindowWithHljs = Window & {
  hljs?: {
    listLanguages?: () => string[]
  }
}

type VditorRuntime = Vditor['vditor']
type MemoOutlineEntry = {
  heading?: HTMLElement
  outlineItem: HTMLElement
  top: number
  targetId: string
}
type MemoOutlineController = {
  dispose: () => void
  sync: () => void
}

const MEMO_OUTLINE_ACTIVE_CLASS = 'memo-outline-item-active'
const MEMO_OUTLINE_SCROLL_OFFSET = 72
const MEMO_TAIL_SINGLE_CLICK_DELAY = 180

const vditorCodeLanguageAliases = [
  'abc',
  'plantuml',
  'mermaid',
  'flowchart',
  'echarts',
  'mindmap',
  'graphviz',
  'math',
  'markmap',
  'smiles',
  'js',
  'ts',
  'html',
  'toml',
  'c#',
  'bat',
] as const

const fallbackCodeLanguages = [
  'plaintext',
  'json',
  'javascript',
  'typescript',
  'html',
  'css',
  'scss',
  'bash',
  'shell',
  'powershell',
  'sql',
  'java',
  'kotlin',
  'python',
  'go',
  'rust',
  'c',
  'cpp',
  'csharp',
  'php',
  'ruby',
  'swift',
  'dart',
  'yaml',
  'toml',
  'xml',
  'markdown',
  'dockerfile',
  'nginx',
  'diff',
  'graphql',
  'regex',
  'mermaid',
  'plantuml',
  'flowchart',
  'echarts',
  'mindmap',
  'graphviz',
  'math',
  'markmap',
  'smiles',
] as const

function isHeadingElement(element: Element | null): element is HTMLElement {
  return element instanceof HTMLElement && /^H[1-6]$/i.test(element.tagName)
}

function getActiveEditorScrollElement(vditor: VditorRuntime) {
  return vditor[vditor.currentMode]?.element ?? null
}

function getEditorLineHeight(editorElement: HTMLElement) {
  const styles = window.getComputedStyle(editorElement)
  const lineHeight = Number.parseFloat(styles.lineHeight)
  if (Number.isFinite(lineHeight) && lineHeight > 0) {
    return lineHeight
  }

  const fontSize = Number.parseFloat(styles.fontSize)
  return Number.isFinite(fontSize) && fontSize > 0 ? fontSize * 1.65 : 22
}

function getLastEditorContentBlock(editorElement: HTMLElement) {
  const blocks = Array.from(editorElement.querySelectorAll<HTMLElement>('[data-block="0"]'))
    .filter((block) => editorElement.contains(block))
  return blocks.length > 0 ? blocks[blocks.length - 1] : null
}

function getEditorTailClickLine(vditor: VditorRuntime, event: MouseEvent) {
  if (event.button !== 0) return null

  const editorElement = getActiveEditorScrollElement(vditor)
  if (!(event.target instanceof Node) || !editorElement?.contains(event.target)) return null

  const lastBlock = getLastEditorContentBlock(editorElement)
  const contentBottom = lastBlock?.getBoundingClientRect().bottom ?? editorElement.getBoundingClientRect().top
  return getTailClickLine(event.clientY, contentBottom, getEditorLineHeight(editorElement))
}

function collapseSelectionToElementEnd(editorElement: HTMLElement) {
  const selection = window.getSelection()
  if (!selection) return

  const range = document.createRange()
  const lastBlock = getLastEditorContentBlock(editorElement)
  const target = lastBlock ?? editorElement
  range.selectNodeContents(target)
  range.collapse(false)
  selection.removeAllRanges()
  selection.addRange(range)
}

function getOutlineContentElement(vditor: VditorRuntime) {
  if (vditor.preview?.element.style.display === 'block') {
    return vditor.preview.previewElement
  }
  return getActiveEditorScrollElement(vditor)
}

function getScrollElementForHeading(vditor: VditorRuntime, heading: HTMLElement) {
  const activeEditor = getActiveEditorScrollElement(vditor)
  if (activeEditor?.contains(heading)) {
    return activeEditor
  }
  if (vditor.preview?.previewElement.contains(heading)) {
    return vditor.preview.element
  }
  return activeEditor
}

function escapeHTML(value: string) {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function getSvLineTop(vditor: VditorRuntime, lineIndex: number) {
  const svElement = vditor.sv?.element
  if (!svElement) return 0
  const styles = window.getComputedStyle(svElement)
  const lineHeight = Number.parseFloat(styles.lineHeight) || 22
  const paddingTop = Number.parseFloat(styles.paddingTop) || 0
  return paddingTop + lineIndex * lineHeight
}

function renderSvFallbackOutline(vditor: VditorRuntime) {
  const svElement = vditor.sv?.element
  const outlineContent = vditor.outline?.element.querySelector<HTMLElement>('.vditor-outline__content')
  if (!svElement || !outlineContent) return

  type SvOutlineNode = {
    children: SvOutlineNode[]
    level: number
    lineIndex: number
    targetId: string
    text: string
    top: number
  }

  const root: SvOutlineNode = { children: [], level: 0, lineIndex: 0, targetId: '', text: '', top: 0 }
  const stack = [root]
  const lines = (svElement.textContent ?? '').split('\n')

  lines.forEach((line, lineIndex) => {
    const match = /^(#{1,6})\s+(.+?)\s*$/.exec(line)
    if (!match) return

    const node: SvOutlineNode = {
      children: [],
      level: match[1].length,
      lineIndex,
      targetId: `memo-sv-heading-${lineIndex}`,
      text: match[2],
      top: getSvLineTop(vditor, lineIndex),
    }

    while (stack.length > 1 && stack[stack.length - 1].level >= node.level) {
      stack.pop()
    }
    stack[stack.length - 1].children.push(node)
    stack.push(node)
  })

  const renderNodes = (nodes: SvOutlineNode[]): string => {
    if (nodes.length === 0) return ''
    return `<ul>${nodes.map((node) => {
      return [
        '<li>',
        `<span data-memo-outline-source="sv" data-memo-outline-top="${node.top}" data-target-id="${node.targetId}">`,
        '<svg></svg>',
        `<span>${escapeHTML(node.text)}</span>`,
        '</span>',
        renderNodes(node.children),
        '</li>',
      ].join('')
    }).join('')}</ul>`
  }

  outlineContent.innerHTML = renderNodes(root.children)
}

function collectMemoOutlineEntries(vditor: VditorRuntime) {
  const contentElement = getOutlineContentElement(vditor)
  const outlineElement = vditor.outline?.element
  if (!contentElement || !outlineElement) return []

  let entries = Array.from(outlineElement.querySelectorAll<HTMLElement>('[data-target-id]'))
    .map((outlineItem): MemoOutlineEntry | null => {
      const targetId = outlineItem.getAttribute('data-target-id')
      if (!targetId) return null
      if (outlineItem.dataset.memoOutlineSource === 'sv') {
        return {
          outlineItem,
          targetId,
          top: Number.parseFloat(outlineItem.dataset.memoOutlineTop ?? '0') || 0,
        }
      }

      const heading = document.getElementById(targetId)
      if (!heading || !contentElement.contains(heading)) return null
      return { heading, outlineItem, targetId, top: getHeadingTop(heading) }
    })
    .filter((entry): entry is MemoOutlineEntry => Boolean(entry))

  if (entries.length === 0 && vditor.currentMode === 'sv') {
    renderSvFallbackOutline(vditor)
    entries = Array.from(outlineElement.querySelectorAll<HTMLElement>('[data-memo-outline-source="sv"][data-target-id]'))
      .map((outlineItem): MemoOutlineEntry | null => {
        const targetId = outlineItem.getAttribute('data-target-id')
        if (!targetId) return null
        return {
          outlineItem,
          targetId,
          top: Number.parseFloat(outlineItem.dataset.memoOutlineTop ?? '0') || 0,
        }
      })
      .filter((entry): entry is MemoOutlineEntry => Boolean(entry))
  }

  return entries
}

function getHeadingTop(heading: HTMLElement) {
  return heading.offsetTop
}

function getFirstHeadingEntry(entries: MemoOutlineEntry[]) {
  return entries.length > 0 ? entries[0] : null
}

function findActiveEntryForScroll(entries: MemoOutlineEntry[], scrollTop: number) {
  if (entries.length === 0) return null
  const targetTop = scrollTop + MEMO_OUTLINE_SCROLL_OFFSET
  let activeEntry = entries[0]

  for (const entry of entries) {
    if (entry.top > targetTop) break
    activeEntry = entry
  }

  return activeEntry
}

function findHeadingEntryForClick(entries: MemoOutlineEntry[], target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return getFirstHeadingEntry(entries)
  const contentElement = getOutlineContentElementFromEntries(entries)
  if (!contentElement?.contains(target)) return getFirstHeadingEntry(entries)

  let block: HTMLElement | null = target
  while (block && block.parentElement !== contentElement) {
    block = block.parentElement
  }

  if (!block) return getFirstHeadingEntry(entries)
  if (isHeadingElement(block)) {
    return entries.find((entry) => entry.heading === block) ?? getFirstHeadingEntry(entries)
  }

  const clickedBlock: HTMLElement = block
  let previousElement: Element | null = clickedBlock.previousElementSibling
  while (previousElement) {
    if (isHeadingElement(previousElement)) {
      return entries.find((entry) => entry.heading === previousElement) ?? getFirstHeadingEntry(entries)
    }
    previousElement = previousElement.previousElementSibling
  }

  return getFirstHeadingEntry(entries)
}

function getOutlineContentElementFromEntries(entries: MemoOutlineEntry[]) {
  const firstHeading = entries.find((entry) => entry.heading)?.heading
  return firstHeading?.parentElement ?? null
}

function ensureOutlineItemVisible(outlineItem: HTMLElement) {
  const outlineElement = outlineItem.closest('.vditor-outline')
  if (!(outlineElement instanceof HTMLElement)) return

  const stickyTitle = outlineElement.querySelector<HTMLElement>('.vditor-outline__title')
  const topInset = (stickyTitle?.offsetHeight ?? 0) + 8
  const itemTop = outlineItem.offsetTop
  const itemBottom = itemTop + outlineItem.offsetHeight
  const visibleTop = outlineElement.scrollTop + topInset
  const visibleBottom = outlineElement.scrollTop + outlineElement.clientHeight

  if (itemTop < visibleTop) {
    outlineElement.scrollTop = Math.max(0, itemTop - topInset)
    return
  }

  if (itemBottom > visibleBottom) {
    outlineElement.scrollTop = itemBottom - outlineElement.clientHeight + 12
  }
}

function createMemoOutlineController(editor: Vditor): MemoOutlineController {
  const vditor = editor.vditor
  const rootElement = vditor.element
  let activeTargetId: string | null = null
  let outlineRetryCount = 0
  let previewRenderRetryId = 0
  let scrollElement: HTMLElement | null = null
  let scrollRafId = 0

  const setActiveOutlineTarget = (targetId: string | null, shouldReveal = true) => {
    const outlineElement = vditor.outline?.element
    if (!outlineElement) return

    outlineElement
      .querySelectorAll<HTMLElement>(`.${MEMO_OUTLINE_ACTIVE_CLASS}`)
      .forEach((item) => item.classList.remove(MEMO_OUTLINE_ACTIVE_CLASS))

    activeTargetId = targetId
    if (!targetId) return

    const activeItem = Array.from(outlineElement.querySelectorAll<HTMLElement>('[data-target-id]'))
      .find((item) => item.getAttribute('data-target-id') === targetId)

    activeItem?.classList.add(MEMO_OUTLINE_ACTIVE_CLASS)
    if (activeItem && shouldReveal) {
      ensureOutlineItemVisible(activeItem)
    }
  }

  const syncOutlineState = () => {
    const entries = collectMemoOutlineEntries(vditor)
    if (entries.length > 0 && previewRenderRetryId) {
      window.clearTimeout(previewRenderRetryId)
      previewRenderRetryId = 0
    }
    if (entries.length > 0) {
      outlineRetryCount = 0
    }

    if (entries.length === 0 && outlineRetryCount < 8 && !previewRenderRetryId) {
      outlineRetryCount += 1
      const currentMarkdown = getEditorMarkdown(editor)
      editor.renderPreview(currentMarkdown)
      vditor.preview?.render(vditor, currentMarkdown)
      previewRenderRetryId = window.setTimeout(() => {
        previewRenderRetryId = 0
        vditor.outline.render(vditor)
        syncOutlineState()
      }, 120)
    }

    const nextScrollElement = entries[0]?.heading ? getScrollElementForHeading(vditor, entries[0].heading) : getActiveEditorScrollElement(vditor)
    if (nextScrollElement !== scrollElement) {
      scrollElement?.removeEventListener('scroll', handleScroll)
      scrollElement = nextScrollElement
      scrollElement?.addEventListener('scroll', handleScroll, { passive: true })
    }

    if (entries.length === 0) {
      setActiveOutlineTarget(null, false)
      return
    }

    const activeEntry = activeTargetId
      ? entries.find((entry) => entry.targetId === activeTargetId)
      : findActiveEntryForScroll(entries, scrollElement?.scrollTop ?? 0)

    setActiveOutlineTarget(activeEntry?.targetId ?? entries[0].targetId, false)
  }

  const scheduleScrollSync = () => {
    if (scrollRafId) return
    scrollRafId = window.requestAnimationFrame(() => {
      scrollRafId = 0
      const entries = collectMemoOutlineEntries(vditor)
      const activeEntry = findActiveEntryForScroll(entries, scrollElement?.scrollTop ?? 0)
      setActiveOutlineTarget(activeEntry?.targetId ?? null)
    })
  }

  function handleScroll() {
    scheduleScrollSync()
  }

  const scrollToHeading = (targetId: string) => {
    const entries = collectMemoOutlineEntries(vditor)
    const entry = entries.find((item) => item.targetId === targetId)
    if (!entry) return

    const targetScrollElement = entry.heading ? getScrollElementForHeading(vditor, entry.heading) : getActiveEditorScrollElement(vditor)
    if (!targetScrollElement) return

    setActiveOutlineTarget(targetId)
    targetScrollElement.scrollTo({
      top: Math.max(0, entry.top - MEMO_OUTLINE_SCROLL_OFFSET),
    })
  }

  const handleRootClick = (event: MouseEvent) => {
    const target = event.target
    if (!(target instanceof HTMLElement)) return

    const outlineElement = vditor.outline?.element
    const outlineAction = target.closest('.vditor-outline__action')
    if (outlineAction && outlineElement?.contains(outlineAction)) return

    const outlineTarget = target.closest<HTMLElement>('.vditor-outline [data-target-id]')
    if (outlineTarget) {
      const targetId = outlineTarget.getAttribute('data-target-id')
      if (!targetId) return

      event.preventDefault()
      event.stopPropagation()
      event.stopImmediatePropagation()
      scrollToHeading(targetId)
      return
    }

    const entries = collectMemoOutlineEntries(vditor)
    const contentElement = getOutlineContentElementFromEntries(entries)
    if (!contentElement?.contains(target)) return

    const activeEntry = findHeadingEntryForClick(entries, target)
    setActiveOutlineTarget(activeEntry?.targetId ?? null)
  }

  rootElement.addEventListener('click', handleRootClick, true)
  window.setTimeout(syncOutlineState, 0)

  return {
    dispose() {
      rootElement.removeEventListener('click', handleRootClick, true)
      scrollElement?.removeEventListener('scroll', handleScroll)
      if (scrollRafId) {
        window.cancelAnimationFrame(scrollRafId)
      }
      if (previewRenderRetryId) {
        window.clearTimeout(previewRenderRetryId)
      }
    },
    sync: syncOutlineState,
  }
}

function destroyEditor(editor: Vditor, host: HTMLDivElement | null) {
  try {
    editor.destroy()
  } catch {
    host?.replaceChildren()
    host?.removeAttribute('style')
    host?.classList.remove('vditor')
  }
}

function getEditorMarkdown(editor: Vditor) {
  try {
    return editor.getValue()
  } catch {
    return ''
  }
}

function getCodeLanguage(code: HTMLElement) {
  const languageClass = Array.from(code.classList).find((className) => className.startsWith('language-'))
  return languageClass?.replace('language-', '') || 'plaintext'
}

function getLanguageLabel(language: string) {
  return language === 'plaintext' ? 'text' : language
}

function getAvailableCodeLanguages(currentLanguage: string) {
  const hljsLanguages = ((window as WindowWithHljs).hljs?.listLanguages?.() ?? []).filter(Boolean)
  const languages = new Set<string>([
    currentLanguage,
    'plaintext',
    ...vditorCodeLanguageAliases,
    ...hljsLanguages,
    ...fallbackCodeLanguages,
  ])
  languages.delete('')

  return [
    'plaintext',
    ...Array.from(languages)
      .filter((language) => language !== 'plaintext')
      .sort((left, right) => left.localeCompare(right)),
  ]
}

function getRenderedCodeBlockIndex(code: HTMLElement) {
  const root = code.closest('.vditor') ?? document
  const codeBlocks = Array.from(root.querySelectorAll('pre > code')).filter((item): item is HTMLElement => {
    if (!(item instanceof HTMLElement)) return false
    const parent = item.parentElement
    return Boolean(parent && !parent.classList.contains('vditor-wysiwyg__pre') && !parent.classList.contains('vditor-ir__marker--pre'))
  })
  return codeBlocks.indexOf(code)
}

function replaceCodeFenceLanguage(markdown: string, targetIndex: number, nextLanguage: string) {
  const lines = markdown.split('\n')
  let blockIndex = -1
  let fenceMarker = ''
  let fenceLength = 0
  let inFence = false

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    if (!inFence) {
      const openMatch = /^(\s*)(`{3,}|~{3,})(.*)$/.exec(line)
      if (!openMatch) continue

      blockIndex += 1
      fenceMarker = openMatch[2][0]
      fenceLength = openMatch[2].length
      inFence = true

      if (blockIndex === targetIndex) {
        const nextInfo = nextLanguage === 'plaintext' ? '' : nextLanguage
        lines[index] = `${openMatch[1]}${openMatch[2]}${nextInfo}`
      }
      continue
    }

    const closePattern = new RegExp(`^\\s*\\${fenceMarker}{${fenceLength},}\\s*$`)
    if (closePattern.test(line)) {
      inFence = false
      fenceMarker = ''
      fenceLength = 0
    }
  }

  return blockIndex >= targetIndex ? lines.join('\n') : markdown
}

function renderCodeLanguageMenu(
  code: HTMLElement,
  menuElement: HTMLElement,
  onLanguageChange: (blockIndex: number, language: string) => void,
) {
  const currentLanguage = getCodeLanguage(code)

  const details = document.createElement('details')
  details.className = 'memo-code-format-menu'
  details.onclick = (event) => event.stopPropagation()

  const summary = document.createElement('summary')
  summary.className = 'memo-code-format-trigger'
  summary.setAttribute('aria-label', '代码块格式')
  summary.textContent = getLanguageLabel(currentLanguage)
  details.appendChild(summary)

  const list = document.createElement('div')
  list.className = 'memo-code-format-list'
  list.setAttribute('role', 'listbox')
  details.appendChild(list)

  const renderOptions = () => {
    list.replaceChildren()
    getAvailableCodeLanguages(getCodeLanguage(code)).forEach((language) => {
      const item = document.createElement('button')
      item.className = `memo-code-format-option${language === currentLanguage ? ' memo-code-format-option-active' : ''}`
      item.setAttribute('role', 'option')
      item.setAttribute('aria-selected', String(language === currentLanguage))
      item.textContent = getLanguageLabel(language)
      item.type = 'button'
      item.onclick = (event) => {
        event.preventDefault()
        event.stopPropagation()
        const blockIndex = getRenderedCodeBlockIndex(code)
        if (blockIndex >= 0) {
          onLanguageChange(blockIndex, language)
        }
        details.open = false
      }
      list.appendChild(item)
    })
  }

  summary.onclick = () => {
    renderOptions()
  }

  const separator = document.createElement('i')
  separator.className = 'memo-code-format-separator'
  separator.setAttribute('aria-hidden', 'true')
  separator.textContent = '|'

  const hiddenTextarea = menuElement.querySelector('textarea')
  hiddenTextarea?.insertAdjacentElement('beforebegin', details)
  details.insertAdjacentElement('afterend', separator)
}

export const VditorMemoEditor = forwardRef<VditorMemoEditorHandle, VditorMemoEditorProps>(
  function VditorMemoEditor({
    codeTheme = 'github',
    contentTheme = 'light',
    mode = 'ir',
    onChange,
    onUpload,
    placeholder = '',
    theme = 'classic',
    value,
  }, ref) {
    const hostRef = useRef<HTMLDivElement | null>(null)
    const editorRef = useRef<Vditor | null>(null)
    const codeThemeRef = useRef(codeTheme)
    const contentThemeRef = useRef(contentTheme)
    const isReadyRef = useRef(false)
    const onChangeRef = useRef(onChange)
    const onUploadRef = useRef(onUpload)
    const outlineControllerRef = useRef<MemoOutlineController | null>(null)
    const outlineSyncTimerRef = useRef<number | null>(null)
    const pendingValueRef = useRef<string | null>(null)
    const suppressInputRef = useRef(false)
    const tailClickTimerRef = useRef<number | null>(null)
    const themeRef = useRef(theme)
    const valueRef = useRef(value)

    valueRef.current = value
    codeThemeRef.current = codeTheme
    contentThemeRef.current = contentTheme
    onChangeRef.current = onChange
    onUploadRef.current = onUpload
    themeRef.current = theme

    const disposeOutlineController = useCallback(() => {
      outlineControllerRef.current?.dispose()
      outlineControllerRef.current = null
      if (outlineSyncTimerRef.current) {
        window.clearTimeout(outlineSyncTimerRef.current)
        outlineSyncTimerRef.current = null
      }
    }, [])

    const syncOutlineController = useCallback(() => {
      const editor = editorRef.current
      if (!editor || !isReadyRef.current) return
      if (!outlineControllerRef.current) {
        outlineControllerRef.current = createMemoOutlineController(editor)
        return
      }
      outlineControllerRef.current.sync()
    }, [])

    const scheduleOutlineSync = useCallback(() => {
      if (outlineSyncTimerRef.current) {
        window.clearTimeout(outlineSyncTimerRef.current)
      }
      outlineSyncTimerRef.current = window.setTimeout(() => {
        outlineSyncTimerRef.current = null
        syncOutlineController()
      }, 0)
    }, [syncOutlineController])

    const clearTailClickTimer = useCallback(() => {
      if (!tailClickTimerRef.current) return
      window.clearTimeout(tailClickTimerRef.current)
      tailClickTimerRef.current = null
    }, [])

    const focusEditorTail = useCallback((editor: Vditor) => {
      window.requestAnimationFrame(() => {
        if (editorRef.current !== editor || !isReadyRef.current) return
        const editorElement = getActiveEditorScrollElement(editor.vditor)
        if (!editorElement) return

        editorElement.focus()
        collapseSelectionToElementEnd(editorElement)
      })
    }, [])

    const continueFromEditorTail = useCallback(
      (targetLine: number) => {
        const editor = editorRef.current
        if (!editor || !isReadyRef.current) return

        const currentMarkdown = getEditorMarkdown(editor)
        const nextMarkdown = ensureTrailingNewlines(currentMarkdown, targetLine)

        if (nextMarkdown !== currentMarkdown) {
          suppressInputRef.current = true
          editor.setValue(nextMarkdown, false)
          window.setTimeout(() => {
            suppressInputRef.current = false
          }, 0)
          valueRef.current = nextMarkdown
          onChangeRef.current(nextMarkdown)
          scheduleOutlineSync()
        }

        focusEditorTail(editor)
      },
      [focusEditorTail, scheduleOutlineSync],
    )

    const applyEditorValue = useCallback((editor: Vditor, nextValue: string) => {
      suppressInputRef.current = true
      editor.setValue(nextValue, true)
      window.setTimeout(() => {
        suppressInputRef.current = false
      }, 0)
      scheduleOutlineSync()
    }, [scheduleOutlineSync])

    const handleCodeLanguageChange = useCallback(
      (blockIndex: number, nextLanguage: string) => {
        const editor = editorRef.current
        if (!editor || !isReadyRef.current) return
        const currentMarkdown = getEditorMarkdown(editor)
        const nextMarkdown = replaceCodeFenceLanguage(currentMarkdown, blockIndex, nextLanguage)
        if (nextMarkdown === currentMarkdown) return

        valueRef.current = nextMarkdown
        applyEditorValue(editor, nextMarkdown)
        onChangeRef.current(nextMarkdown)
      },
      [applyEditorValue],
    )

    useEffect(() => {
      if (!hostRef.current || editorRef.current) return
      const host = hostRef.current
      let disposed = false
      let editor: Vditor | null = null
      let tailRootElement: HTMLElement | null = null
      let handleTailClick: ((event: MouseEvent) => void) | null = null
      let handleTailDoubleClick: ((event: MouseEvent) => void) | null = null

      const initTimer = window.setTimeout(() => {
        if (disposed) return

        editor = new Vditor(host, {
          cache: {
            enable: false,
          },
          after: () => {
            if (disposed || !editor) return
            isReadyRef.current = true
            const pendingValue = pendingValueRef.current
            const nextValue = pendingValue ?? valueRef.current

            pendingValueRef.current = null
            window.requestAnimationFrame(() => {
              if (disposed || editorRef.current !== editor || !isReadyRef.current || !editor) return
              if (getEditorMarkdown(editor) === nextValue) return
              applyEditorValue(editor, nextValue)
              scheduleOutlineSync()
            })
            scheduleOutlineSync()
          },
          height: 'calc(100vh - 242px)',
          input: (nextValue: string) => {
            valueRef.current = nextValue
            if (suppressInputRef.current) return
            onChangeRef.current(nextValue)
            scheduleOutlineSync()
          },
          lang: 'zh_CN',
          mode,
          outline: {
            enable: true,
            position: 'left',
          },
          placeholder,
          theme: themeRef.current,
          preview: {
            theme: {
              current: contentThemeRef.current,
            },
            hljs: {
              style: codeThemeRef.current,
              renderMenu: (code, menuElement) => {
                renderCodeLanguageMenu(code, menuElement, handleCodeLanguageChange)
              },
            },
          },
          upload: {
            accept: 'image/*',
            handler: (files: File[]) => {
              const uploadImage = onUploadRef.current
              const activeEditor = editorRef.current
              if (!uploadImage) return '未配置图片上传。'
              if (!activeEditor) return '编辑器尚未初始化。'

              const uploadDone: Promise<null> = Promise.all(
                files.map(async (file) => {
                  const imageMarkdown = await uploadImage(file)
                  activeEditor.insertValue(`${imageMarkdown}\n\n`)
                }),
              ).then(() => null)

              return uploadDone
            },
            multiple: true,
          },
          value: valueRef.current,
        })

        editorRef.current = editor

        tailRootElement = editor.vditor.element
        handleTailClick = (event: MouseEvent) => {
          if (!editor || editorRef.current !== editor || !isReadyRef.current) return

          const targetLine = getEditorTailClickLine(editor.vditor, event)
          if (!targetLine) return

          event.preventDefault()
          event.stopPropagation()
          event.stopImmediatePropagation()
          clearTailClickTimer()
          tailClickTimerRef.current = window.setTimeout(() => {
            tailClickTimerRef.current = null
            continueFromEditorTail(1)
          }, MEMO_TAIL_SINGLE_CLICK_DELAY)
        }
        handleTailDoubleClick = (event: MouseEvent) => {
          if (!editor || editorRef.current !== editor || !isReadyRef.current) return

          const targetLine = getEditorTailClickLine(editor.vditor, event)
          if (!targetLine) return

          event.preventDefault()
          event.stopPropagation()
          event.stopImmediatePropagation()
          clearTailClickTimer()
          continueFromEditorTail(targetLine)
        }
        tailRootElement.addEventListener('click', handleTailClick, true)
        tailRootElement.addEventListener('dblclick', handleTailDoubleClick, true)
      }, 0)

      return () => {
        disposed = true
        window.clearTimeout(initTimer)
        clearTailClickTimer()
        if (tailRootElement && handleTailClick && handleTailDoubleClick) {
          tailRootElement.removeEventListener('click', handleTailClick, true)
          tailRootElement.removeEventListener('dblclick', handleTailDoubleClick, true)
        }
        isReadyRef.current = false
        pendingValueRef.current = null
        disposeOutlineController()
        if (editor) {
          destroyEditor(editor, host)
        } else {
          host.replaceChildren()
        }
        if (editorRef.current === editor) {
          editorRef.current = null
        }
      }
    }, [
      applyEditorValue,
      clearTailClickTimer,
      continueFromEditorTail,
      disposeOutlineController,
      handleCodeLanguageChange,
      mode,
      placeholder,
      scheduleOutlineSync,
    ])

    useEffect(() => {
      const editor = editorRef.current
      if (!editor || !isReadyRef.current) return
      editor.setTheme(theme, contentTheme, codeTheme)
      scheduleOutlineSync()
    }, [codeTheme, contentTheme, scheduleOutlineSync, theme])

    useEffect(() => {
      const editor = editorRef.current
      if (!editor) return
      valueRef.current = value
      if (!isReadyRef.current) {
        pendingValueRef.current = value
        return
      }
      if (value === getEditorMarkdown(editor)) return
      window.requestAnimationFrame(() => {
        if (editorRef.current !== editor || !isReadyRef.current) return
        if (value === getEditorMarkdown(editor)) return
        applyEditorValue(editor, value)
      })
    }, [applyEditorValue, value])

    useEffect(() => {
      return () => {
        disposeOutlineController()
      }
    }, [disposeOutlineController])

    useImperativeHandle(ref, () => ({
      focus() {
        editorRef.current?.focus()
      },
      getHtml() {
        if (!isReadyRef.current) return ''
        return editorRef.current?.getHTML() ?? ''
      },
      getMarkdown() {
        if (!isReadyRef.current) return valueRef.current
        return editorRef.current ? getEditorMarkdown(editorRef.current) : valueRef.current
      },
      insertText(text) {
        const editor = editorRef.current
        if (!editor || !isReadyRef.current) return
        editor.insertValue(text)
        valueRef.current = getEditorMarkdown(editor)
        onChangeRef.current(valueRef.current)
      },
      isUploading() {
        return editorRef.current?.isUploading() ?? false
      },
    }))

    return <div className="memo-vditor-editor" ref={hostRef} />
  },
)
