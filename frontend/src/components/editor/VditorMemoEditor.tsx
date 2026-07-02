import Vditor, { memoSlashCommandDefinitions } from '@mongojson/vditor-core'
import type { VditorMode, VditorOutlineEntry } from '@mongojson/vditor-core'
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
  documentRevision?: string
  initialValue: string
  mode?: VditorMemoMode
  onChange: (value: string) => void
  onUpload?: (file: File) => Promise<string>
  placeholder?: string
  theme?: VditorMemoTheme
}

type WindowWithHljs = Window & {
  hljs?: {
    listLanguages?: () => string[]
  }
}

type VditorRuntime = Vditor['vditor']
type MemoOutlineEntry = {
  heading?: HTMLElement
  index: number
  line: number
  outlineItem: HTMLElement
  top: number
  targetId: string
}
type MemoOutlineNode = VditorOutlineEntry & {
  children: MemoOutlineNode[]
  index: number
  targetId: string
}
type MemoOutlineController = {
  dispose: () => void
  sync: () => void
}
const MEMO_OUTLINE_ACTIVE_CLASS = 'memo-outline-item-active'
const MEMO_OUTLINE_SCROLL_OFFSET = 72
const MEMO_TAIL_SINGLE_CLICK_DELAY = 260
const MEMO_ZERO_WIDTH_SPACE = '\u200b'

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

function isEmptyEditorBlock(block: HTMLElement) {
  return (block.textContent ?? '').replaceAll(MEMO_ZERO_WIDTH_SPACE, '').trim().length === 0
}

function getEditorTailBlocks(editorElement: HTMLElement) {
  const blocks = Array.from(editorElement.children)
    .filter((child): child is HTMLElement => {
      return child instanceof HTMLElement && child.dataset.block === '0'
    })
  let firstTailIndex = blocks.length

  while (firstTailIndex > 0 && isEmptyEditorBlock(blocks[firstTailIndex - 1])) {
    firstTailIndex -= 1
  }

  return {
    blocks,
    tailBlocks: blocks.slice(firstTailIndex),
    tailStartBlock: firstTailIndex > 0 ? blocks[firstTailIndex - 1] : null,
  }
}

function getEditorTailClickLine(vditor: VditorRuntime, event: MouseEvent) {
  if (event.button !== 0) return null

  const editorElement = getActiveEditorScrollElement(vditor)
  if (!(event.target instanceof Node) || !editorElement) return null

  const contentElement = vditor.element.querySelector<HTMLElement>('.vditor-content')
  if (!editorElement.contains(event.target) && !contentElement?.contains(event.target)) return null

  const editorRect = editorElement.getBoundingClientRect()
  if (
    event.clientX < editorRect.left ||
    event.clientX > editorRect.right ||
    event.clientY < editorRect.top ||
    event.clientY > editorRect.bottom
  ) {
    return null
  }

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

function collapseSelectionToBlockStart(block: HTMLElement) {
  const selection = window.getSelection()
  if (!selection) return

  const range = document.createRange()
  const textNode = Array.from(block.childNodes).find((node) => node.nodeType === Node.TEXT_NODE)
  if (textNode) {
    range.setStart(textNode, 0)
  } else {
    range.setStart(block, 0)
  }
  range.collapse(true)
  selection.removeAllRanges()
  selection.addRange(range)
}

function createEditableTailBlock() {
  const paragraph = document.createElement('p')
  paragraph.dataset.block = '0'
  paragraph.appendChild(document.createTextNode(MEMO_ZERO_WIDTH_SPACE))
  return paragraph
}

function placeCaretInBlockEditorTail(editorElement: HTMLElement, targetLine: number) {
  const normalizedLine = Math.max(1, Math.floor(targetLine))
  const { blocks, tailBlocks, tailStartBlock } = getEditorTailBlocks(editorElement)
  let insertAfter = tailBlocks.length > 0
    ? tailBlocks[tailBlocks.length - 1]
    : tailStartBlock ?? blocks[blocks.length - 1] ?? null
  const nextTailBlocks = [...tailBlocks]

  while (nextTailBlocks.length < normalizedLine) {
    const tailBlock = createEditableTailBlock()
    if (insertAfter) {
      insertAfter.insertAdjacentElement('afterend', tailBlock)
    } else {
      editorElement.appendChild(tailBlock)
    }
    nextTailBlocks.push(tailBlock)
    insertAfter = tailBlock
  }

  const targetBlock = nextTailBlocks[normalizedLine - 1] ?? nextTailBlocks[nextTailBlocks.length - 1]
  if (!targetBlock) {
    editorElement.focus()
    collapseSelectionToElementEnd(editorElement)
    return
  }

  editorElement.focus()
  collapseSelectionToBlockStart(targetBlock)
}

function placeCaretInSourceEditorTail(editor: Vditor, targetLine: number) {
  const currentMarkdown = getEditorMarkdown(editor)
  const nextMarkdown = ensureTrailingNewlines(currentMarkdown, targetLine)
  const editorElement = getActiveEditorScrollElement(editor.vditor)
  if (!editorElement) return nextMarkdown

  if (nextMarkdown !== currentMarkdown) {
    editor.setValue(nextMarkdown, false)
  }

  window.requestAnimationFrame(() => {
    editorElement.focus()
    collapseSelectionToElementEnd(editorElement)
  })

  return nextMarkdown
}

function placeCaretInEditorTail(editor: Vditor, targetLine: number) {
  if (editor.vditor.currentMode === 'sv') {
    return placeCaretInSourceEditorTail(editor, targetLine)
  }

  const editorElement = getActiveEditorScrollElement(editor.vditor)
  if (!editorElement) return getEditorMarkdown(editor)

  placeCaretInBlockEditorTail(editorElement, targetLine)
  return getEditorMarkdown(editor)
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

const memoSelectionToolbarActions = [
  {command: 'bold', label: 'B', title: '粗体'},
  {command: 'italic', label: 'I', title: '斜体'},
  {command: 'strike', label: 'S', title: '删除线'},
  {command: 'link', label: 'url', title: '链接'},
  {command: 'inline-code', label: '</>', title: '行内代码'},
  {command: 'list', label: '-', title: '无序列表'},
  {command: 'ordered-list', label: '1.', title: '有序列表'},
  {command: 'check', label: '[ ]', title: '任务'},
  {command: 'quote', label: '>', title: '引用'},
]

function getSvLineTop(vditor: VditorRuntime, lineIndex: number) {
  const svElement = vditor.sv?.element
  if (!svElement) return 0
  const styles = window.getComputedStyle(svElement)
  const lineHeight = Number.parseFloat(styles.lineHeight) || 22
  const paddingTop = Number.parseFloat(styles.paddingTop) || 0
  return paddingTop + lineIndex * lineHeight
}

function getMemoOutlineTargetId(entry: VditorOutlineEntry, index: number) {
  return `memo-outline-${entry.line}-${index}-${entry.id}`
}

function escapeHTML(value: string) {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function buildMemoOutlineTree(outline: VditorOutlineEntry[]) {
  const root: MemoOutlineNode = {
    children: [],
    id: '',
    index: -1,
    level: 0,
    line: 0,
    targetId: '',
    text: '',
  }
  const stack = [root]

  outline.forEach((entry, index) => {
    const node: MemoOutlineNode = {
      ...entry,
      children: [],
      index,
      targetId: getMemoOutlineTargetId(entry, index),
    }

    while (stack.length > 1 && stack[stack.length - 1].level >= node.level) {
      stack.pop()
    }
    stack[stack.length - 1].children.push(node)
    stack.push(node)
  })

  return root.children
}

function renderMemoOutlineNodes(nodes: MemoOutlineNode[]): string {
  if (nodes.length === 0) return ''
  return `<ul>${nodes.map((node) => {
    const actionIcon = node.children.length > 0
      ? '<svg class="vditor-outline__action" viewBox="0 0 32 32"><path d="M3.76 6.12l12.24 12.213 12.24-12.213 3.76 3.76-16 16-16-16 3.76-3.76z"></path></svg>'
      : '<svg></svg>'

    return [
      '<li>',
      `<span data-memo-outline-index="${node.index}" data-memo-outline-line="${node.line}" data-target-id="${escapeHTML(node.targetId)}">`,
      actionIcon,
      `<span>${escapeHTML(node.text)}</span>`,
      '</span>',
      renderMemoOutlineNodes(node.children),
      '</li>',
    ].join('')
  }).join('')}</ul>`
}

function renderMemoOutline(editor: Vditor) {
  const outlineContent = editor.vditor.outline?.element.querySelector<HTMLElement>('.vditor-outline__content')
  if (!outlineContent) return

  const outline = editor.getOutlineModel()
  outlineContent.innerHTML = renderMemoOutlineNodes(buildMemoOutlineTree(outline))
}

function getRenderedHeadingElements(vditor: VditorRuntime) {
  const contentElement = getOutlineContentElement(vditor)
  if (!contentElement) return []
  return Array.from(contentElement.querySelectorAll<HTMLElement>('h1,h2,h3,h4,h5,h6'))
}

function collectMemoOutlineEntries(editor: Vditor, shouldRender = false) {
  const vditor = editor.vditor
  const contentElement = getOutlineContentElement(vditor)
  const outlineElement = vditor.outline?.element
  if (!contentElement || !outlineElement) return []
  if (shouldRender) {
    renderMemoOutline(editor)
  }

  const headings = getRenderedHeadingElements(vditor)
  const entries = Array.from(outlineElement.querySelectorAll<HTMLElement>('[data-target-id]'))
    .map((outlineItem): MemoOutlineEntry | null => {
      const targetId = outlineItem.getAttribute('data-target-id')
      if (!targetId) return null
      const index = Number.parseInt(outlineItem.dataset.memoOutlineIndex ?? '', 10)
      const line = Number.parseInt(outlineItem.dataset.memoOutlineLine ?? '', 10)
      if (!Number.isFinite(index) || !Number.isFinite(line)) return null
      if (vditor.currentMode === 'sv') {
        return {
          index,
          line,
          outlineItem,
          targetId,
          top: getSvLineTop(vditor, line),
        }
      }

      const heading = headings[index]
      if (!heading || !contentElement.contains(heading)) return null
      return { heading, index, line, outlineItem, targetId, top: getHeadingTop(heading) }
    })
    .filter((entry): entry is MemoOutlineEntry => Boolean(entry))

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
    const entries = collectMemoOutlineEntries(editor, true)

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
      const entries = collectMemoOutlineEntries(editor)
      const activeEntry = findActiveEntryForScroll(entries, scrollElement?.scrollTop ?? 0)
      setActiveOutlineTarget(activeEntry?.targetId ?? null)
    })
  }

  function handleScroll() {
    scheduleScrollSync()
  }

  const scrollToHeading = (targetId: string) => {
    const entries = collectMemoOutlineEntries(editor)
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
    if (isCodeFormatMenuTarget(target)) return

    const outlineElement = vditor.outline?.element
    const outlineAction = target.closest('.vditor-outline__action')
    if (outlineAction && outlineElement?.contains(outlineAction)) {
      const actionElement = outlineAction as HTMLElement
      const childList = actionElement.parentElement?.nextElementSibling
      if (childList instanceof HTMLElement) {
        const isClosed = actionElement.classList.toggle('vditor-outline__action--close')
        childList.style.display = isClosed ? 'none' : 'block'
      }
      event.preventDefault()
      event.stopPropagation()
      event.stopImmediatePropagation()
      return
    }

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

    const entries = collectMemoOutlineEntries(editor)
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

function isCodeFormatMenuTarget(target: EventTarget | null) {
  return target instanceof HTMLElement && Boolean(target.closest('.memo-code-format-menu'))
}

function getCodeLanguageValue(value: string) {
  const language = value.trim().toLowerCase()
  return language === '' || language === 'text' ? 'plaintext' : language
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
  const stopMenuEvent = (event: Event) => {
    event.stopPropagation()
  }

  const details = document.createElement('details')
  details.className = 'memo-code-format-menu'
  details.onclick = stopMenuEvent
  details.onkeydown = stopMenuEvent
  details.onmousedown = stopMenuEvent

  const summary = document.createElement('summary')
  summary.className = 'memo-code-format-trigger'
  summary.setAttribute('aria-label', '代码块格式')
  summary.textContent = getLanguageLabel(currentLanguage)
  details.appendChild(summary)

  const panel = document.createElement('div')
  panel.className = 'memo-code-format-panel'
  panel.onmousedown = stopMenuEvent
  panel.onclick = stopMenuEvent
  details.appendChild(panel)

  const searchInput = document.createElement('input')
  searchInput.className = 'memo-code-format-search'
  searchInput.type = 'text'
  searchInput.autocomplete = 'off'
  searchInput.spellcheck = false
  searchInput.setAttribute('aria-label', '搜索或输入代码块格式')
  searchInput.placeholder = '搜索或输入格式'
  searchInput.value = currentLanguage === 'plaintext' ? '' : currentLanguage
  searchInput.addEventListener('beforeinput', stopMenuEvent)
  searchInput.addEventListener('compositionstart', stopMenuEvent)
  searchInput.addEventListener('compositionend', stopMenuEvent)
  searchInput.addEventListener('focus', stopMenuEvent)
  searchInput.addEventListener('focusin', stopMenuEvent)
  searchInput.addEventListener('keyup', stopMenuEvent)
  searchInput.addEventListener('paste', stopMenuEvent)
  panel.appendChild(searchInput)

  const list = document.createElement('div')
  list.className = 'memo-code-format-list'
  list.setAttribute('role', 'listbox')
  panel.appendChild(list)

  const renderOptions = () => {
    list.replaceChildren()
    const searchValue = searchInput.value.trim().toLowerCase()
    const availableLanguages = getAvailableCodeLanguages(getCodeLanguage(code))
    const filteredLanguages = availableLanguages.filter((language) => {
      if (!searchValue) return true
      return language.includes(searchValue) || getLanguageLabel(language).includes(searchValue)
    })
    const typedLanguage = getCodeLanguageValue(searchInput.value)
    const visibleLanguages = typedLanguage && !filteredLanguages.includes(typedLanguage)
      ? [typedLanguage, ...filteredLanguages]
      : filteredLanguages

    if (visibleLanguages.length === 0) {
      const empty = document.createElement('div')
      empty.className = 'memo-code-format-empty'
      empty.textContent = '回车使用当前输入'
      list.appendChild(empty)
      return
    }

    visibleLanguages.forEach((language) => {
      const item = document.createElement('button')
      item.className = `memo-code-format-option${language === currentLanguage ? ' memo-code-format-option-active' : ''}`
      item.setAttribute('role', 'option')
      item.setAttribute('aria-selected', String(language === currentLanguage))
      item.textContent = getLanguageLabel(language)
      item.type = 'button'
      item.onclick = (event) => {
        event.preventDefault()
        event.stopPropagation()
        applyLanguage(language)
        details.open = false
      }
      list.appendChild(item)
    })
  }

  const applyLanguage = (language: string) => {
    const blockIndex = getRenderedCodeBlockIndex(code)
    if (blockIndex >= 0) {
      onLanguageChange(blockIndex, language)
    }
  }

  details.ontoggle = () => {
    menuElement.classList.toggle('memo-code-format-menu-active', details.open)
    if (!details.open) return
    searchInput.value = currentLanguage === 'plaintext' ? '' : currentLanguage
    renderOptions()
    window.requestAnimationFrame(() => {
      searchInput.focus()
      searchInput.select()
    })
  }

  searchInput.oninput = (event) => {
    event.stopPropagation()
    renderOptions()
  }
  searchInput.onkeydown = (event) => {
    event.stopPropagation()
    if (event.key === 'Enter') {
      event.preventDefault()
      const currentOption = list.querySelector<HTMLButtonElement>('.memo-code-format-option')
      const nextLanguage = currentOption?.textContent
        ? getCodeLanguageValue(currentOption.textContent)
        : getCodeLanguageValue(searchInput.value)
      applyLanguage(nextLanguage)
      details.open = false
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      details.open = false
      summary.focus()
      return
    }
    if (event.key === 'ArrowDown') {
      const currentOption = list.querySelector<HTMLButtonElement>('.memo-code-format-option')
      if (currentOption) {
        event.preventDefault()
        currentOption.focus()
      }
    }
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
    documentRevision = '',
    initialValue,
    mode = 'ir',
    onChange,
    onUpload,
    placeholder = '',
    theme = 'classic',
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
    const initialValueRef = useRef(initialValue)
    const initialModeRef = useRef<VditorMode>(mode)
    const valueRef = useRef(initialValue)

    codeThemeRef.current = codeTheme
    contentThemeRef.current = contentTheme
    initialValueRef.current = initialValue
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

    const handleEditorCommandExecuted = useCallback((
      _command: unknown,
      context: { phase?: "before" | "after" },
    ) => {
      if (!isReadyRef.current || context.phase !== "after") {
        return
      }

      window.setTimeout(() => {
        const editor = editorRef.current
        if (!editor || !isReadyRef.current) return

        const nextValue = getEditorMarkdown(editor)
        if (nextValue !== valueRef.current) {
          valueRef.current = nextValue
          onChangeRef.current(nextValue)
        }
        scheduleOutlineSync()
      }, 0)
    }, [scheduleOutlineSync])

    const clearTailClickTimer = useCallback(() => {
      if (!tailClickTimerRef.current) return
      window.clearTimeout(tailClickTimerRef.current)
      tailClickTimerRef.current = null
    }, [])

    const continueFromEditorTail = useCallback(
      (targetLine: number) => {
        const editor = editorRef.current
        if (!editor || !isReadyRef.current) return

        const nextMarkdown = placeCaretInEditorTail(editor, targetLine)

        if (nextMarkdown !== valueRef.current) {
          valueRef.current = nextMarkdown
          onChangeRef.current(nextMarkdown)
          scheduleOutlineSync()
        }
      },
      [scheduleOutlineSync],
    )

    const applyEditorValue = useCallback((editor: Vditor, nextValue: string) => {
      suppressInputRef.current = true
      editor.setDocument(nextValue, { clearStack: true })
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
      let unsubscribeTransaction: (() => void) | null = null

      const attachTailClickHandlers = () => {
        if (!editor || tailRootElement) return
        const runtime = editor.vditor
        if (!runtime?.element) return

        tailRootElement = runtime.element
        handleTailClick = (event: MouseEvent) => {
          if (!editor || editorRef.current !== editor || !isReadyRef.current) return
          if (isCodeFormatMenuTarget(event.target)) return
          if (event.detail > 1) {
            clearTailClickTimer()
            return
          }

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
          if (isCodeFormatMenuTarget(event.target)) return

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
      }

      const initTimer = window.setTimeout(() => {
        if (disposed) return

        editor = new Vditor(host, {
          cache: {
            enable: false,
          },
          after: () => {
            if (disposed || !editor) return
            isReadyRef.current = true
            if (!unsubscribeTransaction) {
              unsubscribeTransaction = editor.onTransaction((transaction) => {
                valueRef.current = transaction.markdown
                if (suppressInputRef.current) return
                if (transaction.source === 'input') {
                  onChangeRef.current(transaction.markdown)
                  scheduleOutlineSync()
                }
              })
            }
            attachTailClickHandlers()
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
          lang: 'zh_CN',
          mode: initialModeRef.current,
          outline: {
            enable: true,
            position: 'left',
          },
          command: memoSlashCommandDefinitions,
          placeholder,
          theme: themeRef.current,
          hint: {
            extend: [{key: '/'}],
          },
          toolbarConfig: {
            hide: true,
            pin: false,
          },
          selectionToolbar: {
            actions: memoSelectionToolbarActions,
          },
          onEditorCommandExecuted: handleEditorCommandExecuted,
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
        unsubscribeTransaction?.()
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
      handleEditorCommandExecuted,
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
      if (!editor || !isReadyRef.current) return
      editor.setMode(mode)
      scheduleOutlineSync()
    }, [mode, scheduleOutlineSync])

    useEffect(() => {
      const editor = editorRef.current
      const nextValue = initialValueRef.current
      valueRef.current = nextValue
      if (!editor) return
      if (!isReadyRef.current) {
        pendingValueRef.current = nextValue
        return
      }
      if (nextValue === getEditorMarkdown(editor)) return
      window.requestAnimationFrame(() => {
        if (editorRef.current !== editor || !isReadyRef.current) return
        if (nextValue === getEditorMarkdown(editor)) return
        applyEditorValue(editor, nextValue)
      })
    }, [applyEditorValue, documentRevision])

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
