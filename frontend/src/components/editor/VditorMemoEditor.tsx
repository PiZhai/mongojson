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
type MemoSlashCommandController = {
  dispose: () => void
}
type MemoSelectionToolbarController = {
  dispose: () => void
}
type MemoSlashCommand = {
  category: '基础' | '常用'
  icon: string
  keywords: string[]
  label: string
  value: string
}
type MemoSelectionAction = {
  command: string
  label: string
  title: string
}

const MEMO_OUTLINE_ACTIVE_CLASS = 'memo-outline-item-active'
const MEMO_OUTLINE_SCROLL_OFFSET = 72
const MEMO_TAIL_SINGLE_CLICK_DELAY = 260
const MEMO_SELECTION_TOOLBAR_OFFSET = 12
const MEMO_ZERO_WIDTH_SPACE = '\u200b'

const memoSlashCommands: MemoSlashCommand[] = [
  {
    category: '基础',
    icon: 'T',
    keywords: ['text', 'paragraph', 'wenben', 'duanluo'],
    label: '文本',
    value: '段落\n',
  },
  {
    category: '基础',
    icon: 'H1',
    keywords: ['h1', 'heading1', 'title', 'biaoti', 'yiji'],
    label: '一级标题',
    value: '# 一级标题\n',
  },
  {
    category: '基础',
    icon: 'H2',
    keywords: ['h2', 'heading2', 'title', 'biaoti', 'erji'],
    label: '二级标题',
    value: '## 二级标题\n',
  },
  {
    category: '基础',
    icon: 'H3',
    keywords: ['h3', 'heading3', 'title', 'biaoti', 'sanji'],
    label: '三级标题',
    value: '### 三级标题\n',
  },
  {
    category: '基础',
    icon: 'H4',
    keywords: ['h4', 'heading4', 'title', 'biaoti', 'siji'],
    label: '四级标题',
    value: '#### 四级标题\n',
  },
  {
    category: '基础',
    icon: '1.',
    keywords: ['ordered', 'number', 'list', 'youxu', 'liebiao'],
    label: '有序列表',
    value: '1. 列表项\n',
  },
  {
    category: '基础',
    icon: '-',
    keywords: ['bullet', 'unordered', 'list', 'wuxu', 'liebiao'],
    label: '无序列表',
    value: '- 列表项\n',
  },
  {
    category: '基础',
    icon: '{}',
    keywords: ['code', 'block', 'daima'],
    label: '代码块',
    value: '```\n代码\n```\n',
  },
  {
    category: '基础',
    icon: '>',
    keywords: ['quote', 'blockquote', 'yinyong'],
    label: '引用',
    value: '> 引用\n',
  },
  {
    category: '基础',
    icon: '--',
    keywords: ['line', 'divider', 'hr', 'fengexian'],
    label: '分割线',
    value: '---\n',
  },
  {
    category: '常用',
    icon: '[ ]',
    keywords: ['task', 'todo', 'check', 'renwu'],
    label: '任务',
    value: '- [ ] 任务\n',
  },
  {
    category: '常用',
    icon: 'url',
    keywords: ['link', 'url', 'lianjie'],
    label: '链接',
    value: '[链接文本](https://)\n',
  },
  {
    category: '常用',
    icon: 'img',
    keywords: ['image', 'photo', 'tupian'],
    label: '图片',
    value: '![图片描述]()\n',
  },
  {
    category: '常用',
    icon: 'tbl',
    keywords: ['table', 'biaoge'],
    label: '表格',
    value: '| 列 A | 列 B |\n| --- | --- |\n| 内容 | 内容 |\n',
  },
]

const memoSelectionActions: MemoSelectionAction[] = [
  { command: 'bold', label: 'B', title: '粗体' },
  { command: 'italic', label: 'I', title: '斜体' },
  { command: 'strike', label: 'S', title: '删除线' },
  { command: 'link', label: 'url', title: '链接' },
  { command: 'inline-code', label: '</>', title: '行内代码' },
  { command: 'list', label: '-', title: '无序列表' },
  { command: 'ordered-list', label: '1.', title: '有序列表' },
  { command: 'check', label: '[ ]', title: '任务' },
  { command: 'quote', label: '>', title: '引用' },
]

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

function escapeHTML(value: string) {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function renderSlashCommand(command: MemoSlashCommand) {
  return [
    '<span class="memo-slash-command">',
    `<span class="memo-slash-command-icon">${escapeHTML(command.icon)}</span>`,
    '<span class="memo-slash-command-text">',
    `<span class="memo-slash-command-category">${escapeHTML(command.category)}</span>`,
    `<span class="memo-slash-command-label">${escapeHTML(command.label)}</span>`,
    '</span>',
    '</span>',
  ].join('')
}

function getMemoSlashCommands(keyword: string) {
  const normalizedKeyword = keyword.trim().toLowerCase()
  return normalizedKeyword
    ? memoSlashCommands.filter((command) => {
        const searchText = [
          command.category,
          command.label,
          command.icon,
          ...command.keywords,
        ].join(' ').toLowerCase()
        return searchText.includes(normalizedKeyword)
      })
    : memoSlashCommands
}

function getSelectionRangeInside(rootElement: HTMLElement) {
  const selection = window.getSelection()
  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) return null

  const anchorNode = selection.anchorNode
  const focusNode = selection.focusNode
  if (!anchorNode || !focusNode) return null
  if (!rootElement.contains(anchorNode) || !rootElement.contains(focusNode)) return null

  return selection.getRangeAt(0).cloneRange()
}

function getRangeAnchorRect(range: Range) {
  const rects = Array.from(range.getClientRects()).filter((rect) => rect.width > 0 || rect.height > 0)
  if (rects.length > 0) return rects[0]

  const rect = range.getBoundingClientRect()
  return rect.width > 0 || rect.height > 0 ? rect : null
}

function restoreSelectionRange(range: Range) {
  const selection = window.getSelection()
  if (!selection) return

  selection.removeAllRanges()
  selection.addRange(range)
}

function focusEditorEnd(editor: Vditor) {
  window.requestAnimationFrame(() => {
    const editorElement = getActiveEditorScrollElement(editor.vditor)
    if (!editorElement) return

    editorElement.focus()
    collapseSelectionToElementEnd(editorElement)
  })
}

function replaceTrailingSlashCommand(
  markdown: string,
  triggerToken: string,
  commandMarkdown: string,
) {
  if (!triggerToken || !markdown.endsWith(triggerToken)) return null

  const baseMarkdown = markdown.slice(0, -triggerToken.length)
  const separator = baseMarkdown.length > 0 && !baseMarkdown.endsWith('\n') ? '\n' : ''
  return `${baseMarkdown}${separator}${commandMarkdown}`
}

function getSlashTriggerInside(rootElement: HTMLElement) {
  const selection = window.getSelection()
  if (!selection || selection.rangeCount === 0 || !selection.isCollapsed) return null

  const range = selection.getRangeAt(0)
  const startContainer = range.startContainer
  if (!rootElement.contains(startContainer) || startContainer.nodeType !== Node.TEXT_NODE) return null

  const currentLineValue = startContainer.textContent?.substring(0, range.startOffset) ?? ''
  const slashIndex = currentLineValue.lastIndexOf('/')
  if (slashIndex < 0) return null

  const keyword = currentLineValue.slice(slashIndex + 1)
  if (keyword.length > 32 || /\s/.test(keyword)) return null

  const slashRange = range.cloneRange()
  slashRange.setStart(startContainer, slashIndex)

  return { keyword, range: slashRange }
}

function createMemoSlashCommandController(
  editor: Vditor,
  onCommandExecuted: () => void,
): MemoSlashCommandController {
  const rootElement = editor.vditor.element
  const menuElement = document.createElement('div')
  let activeIndex = 0
  let currentCommands: MemoSlashCommand[] = []
  let savedTriggerRange: Range | null = null
  let updateRafId = 0

  menuElement.className = 'memo-slash-menu'
  menuElement.setAttribute('role', 'listbox')
  menuElement.setAttribute('aria-label', '插入命令')
  document.body.appendChild(menuElement)

  const hideMenu = () => {
    menuElement.classList.remove('memo-slash-menu-visible')
    menuElement.replaceChildren()
    currentCommands = []
    savedTriggerRange = null
    activeIndex = 0
  }

  const setActiveIndex = (nextIndex: number) => {
    if (currentCommands.length === 0) return
    activeIndex = (nextIndex + currentCommands.length) % currentCommands.length
    Array.from(menuElement.querySelectorAll<HTMLElement>('.memo-slash-menu-item')).forEach((item, index) => {
      item.classList.toggle('memo-slash-menu-item-active', index === activeIndex)
      item.setAttribute('aria-selected', String(index === activeIndex))
    })
  }

  const applyCommand = (command: MemoSlashCommand) => {
    if (!savedTriggerRange) return

    const triggerToken = savedTriggerRange.toString()
    const currentMarkdown = getEditorMarkdown(editor)
    const nextMarkdown = replaceTrailingSlashCommand(currentMarkdown, triggerToken, command.value)

    if (nextMarkdown !== null) {
      editor.setValue(nextMarkdown, false)
      onCommandExecuted()
      hideMenu()
      focusEditorEnd(editor)
      return
    }

    restoreSelectionRange(savedTriggerRange)
    savedTriggerRange.deleteContents()
    savedTriggerRange.collapse(false)
    restoreSelectionRange(savedTriggerRange)
    editor.insertMD(command.value)
    onCommandExecuted()
    hideMenu()
    focusEditorEnd(editor)
  }

  const renderMenu = (commands: MemoSlashCommand[]) => {
    menuElement.replaceChildren()
    commands.forEach((command, index) => {
      const button = document.createElement('button')
      button.type = 'button'
      button.className = 'memo-slash-menu-item'
      button.setAttribute('role', 'option')
      button.innerHTML = renderSlashCommand(command)
      button.addEventListener('mousedown', (event) => {
        event.preventDefault()
      })
      button.addEventListener('click', (event) => {
        event.preventDefault()
        event.stopPropagation()
        applyCommand(command)
      })
      menuElement.appendChild(button)
      if (index === activeIndex) {
        button.classList.add('memo-slash-menu-item-active')
        button.setAttribute('aria-selected', 'true')
      } else {
        button.setAttribute('aria-selected', 'false')
      }
    })
  }

  const updateMenu = () => {
    updateRafId = 0
    const trigger = getSlashTriggerInside(rootElement)
    if (!trigger) {
      hideMenu()
      return
    }

    const commands = getMemoSlashCommands(trigger.keyword)
    if (commands.length === 0) {
      hideMenu()
      return
    }

    savedTriggerRange = trigger.range
    currentCommands = commands
    activeIndex = Math.min(activeIndex, commands.length - 1)
    renderMenu(commands)

    const rect = getRangeAnchorRect(trigger.range)
    const rootRect = rootElement.getBoundingClientRect()
    const anchorLeft = rect?.left ?? rootRect.left + 24
    const anchorBottom = rect?.bottom ?? rootRect.top + 48

    menuElement.classList.add('memo-slash-menu-visible')
    const menuRect = menuElement.getBoundingClientRect()
    const left = Math.min(Math.max(8, anchorLeft), window.innerWidth - menuRect.width - 8)
    const top = Math.min(anchorBottom + 8, window.innerHeight - menuRect.height - 8)

    menuElement.style.left = `${left}px`
    menuElement.style.top = `${Math.max(8, top)}px`
  }

  const scheduleMenuUpdate = () => {
    if (updateRafId) return
    updateRafId = window.requestAnimationFrame(updateMenu)
  }

  const handleKeyDown = (event: KeyboardEvent) => {
    if (!menuElement.classList.contains('memo-slash-menu-visible')) return
    if (!getSlashTriggerInside(rootElement)) {
      hideMenu()
      return
    }

    if (event.key === 'ArrowDown') {
      event.preventDefault()
      event.stopPropagation()
      setActiveIndex(activeIndex + 1)
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      event.stopPropagation()
      setActiveIndex(activeIndex - 1)
      return
    }
    if (event.key === 'Enter' && currentCommands[activeIndex]) {
      event.preventDefault()
      event.stopPropagation()
      applyCommand(currentCommands[activeIndex])
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      hideMenu()
    }
  }

  const handleDocumentMouseDown = (event: MouseEvent) => {
    const target = event.target
    if (!(target instanceof Node)) {
      hideMenu()
      return
    }
    if (menuElement.contains(target) || rootElement.contains(target)) return
    hideMenu()
  }

  document.addEventListener('selectionchange', scheduleMenuUpdate)
  document.addEventListener('mousedown', handleDocumentMouseDown, true)
  rootElement.addEventListener('input', scheduleMenuUpdate, true)
  rootElement.addEventListener('keyup', scheduleMenuUpdate, true)
  rootElement.addEventListener('keydown', handleKeyDown, true)
  window.addEventListener('scroll', scheduleMenuUpdate, true)
  window.addEventListener('resize', scheduleMenuUpdate)

  return {
    dispose() {
      document.removeEventListener('selectionchange', scheduleMenuUpdate)
      document.removeEventListener('mousedown', handleDocumentMouseDown, true)
      rootElement.removeEventListener('input', scheduleMenuUpdate, true)
      rootElement.removeEventListener('keyup', scheduleMenuUpdate, true)
      rootElement.removeEventListener('keydown', handleKeyDown, true)
      window.removeEventListener('scroll', scheduleMenuUpdate, true)
      window.removeEventListener('resize', scheduleMenuUpdate)
      if (updateRafId) {
        window.cancelAnimationFrame(updateRafId)
      }
      menuElement.remove()
    },
  }
}

function dispatchVditorToolbarCommand(editor: Vditor, command: string) {
  const toolbarItem = editor.vditor.toolbar?.elements?.[command]
  const commandElement = toolbarItem?.children[0]
  if (!(commandElement instanceof HTMLElement)) return false

  commandElement.dispatchEvent(new CustomEvent('click', { bubbles: true, cancelable: true }))
  return true
}

function createMemoSelectionToolbarController(
  editor: Vditor,
  onCommandExecuted: () => void,
): MemoSelectionToolbarController {
  const rootElement = editor.vditor.element
  const toolbarElement = document.createElement('div')
  let savedRange: Range | null = null
  let updateRafId = 0

  toolbarElement.className = 'memo-selection-toolbar'
  toolbarElement.setAttribute('role', 'toolbar')
  toolbarElement.setAttribute('aria-label', '文本格式工具')

  const hideToolbar = () => {
    toolbarElement.classList.remove('memo-selection-toolbar-visible')
    savedRange = null
  }

  memoSelectionActions.forEach((action) => {
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'memo-selection-toolbar-button'
    button.dataset.command = action.command
    button.setAttribute('aria-label', action.title)
    button.title = action.title
    button.textContent = action.label
    button.addEventListener('mousedown', (event) => {
      event.preventDefault()
    })
    button.addEventListener('click', (event) => {
      event.preventDefault()
      event.stopPropagation()
      if (!savedRange) return

      restoreSelectionRange(savedRange)
      if (dispatchVditorToolbarCommand(editor, action.command)) {
        onCommandExecuted()
      }
      hideToolbar()
    })
    toolbarElement.appendChild(button)
  })

  document.body.appendChild(toolbarElement)

  const updateToolbar = () => {
    updateRafId = 0
    const range = getSelectionRangeInside(rootElement)
    if (!range) {
      hideToolbar()
      return
    }

    const rect = getRangeAnchorRect(range)
    if (!rect) {
      hideToolbar()
      return
    }

    savedRange = range
    toolbarElement.classList.add('memo-selection-toolbar-visible')

    const toolbarRect = toolbarElement.getBoundingClientRect()
    const left = Math.min(
      Math.max(8, rect.left + rect.width / 2 - toolbarRect.width / 2),
      window.innerWidth - toolbarRect.width - 8,
    )
    const top = rect.top - toolbarRect.height - MEMO_SELECTION_TOOLBAR_OFFSET
    const fallbackTop = rect.bottom + MEMO_SELECTION_TOOLBAR_OFFSET

    toolbarElement.style.left = `${left}px`
    toolbarElement.style.top = `${Math.max(8, top > 8 ? top : fallbackTop)}px`
  }

  const scheduleToolbarUpdate = () => {
    if (updateRafId) return
    updateRafId = window.requestAnimationFrame(updateToolbar)
  }

  const handleDocumentMouseDown = (event: MouseEvent) => {
    const target = event.target
    if (!(target instanceof Node)) {
      hideToolbar()
      return
    }
    if (toolbarElement.contains(target) || rootElement.contains(target)) return
    hideToolbar()
  }

  const handleKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') hideToolbar()
  }

  document.addEventListener('selectionchange', scheduleToolbarUpdate)
  document.addEventListener('mousedown', handleDocumentMouseDown, true)
  document.addEventListener('keydown', handleKeyDown, true)
  window.addEventListener('scroll', scheduleToolbarUpdate, true)
  window.addEventListener('resize', scheduleToolbarUpdate)

  return {
    dispose() {
      document.removeEventListener('selectionchange', scheduleToolbarUpdate)
      document.removeEventListener('mousedown', handleDocumentMouseDown, true)
      document.removeEventListener('keydown', handleKeyDown, true)
      window.removeEventListener('scroll', scheduleToolbarUpdate, true)
      window.removeEventListener('resize', scheduleToolbarUpdate)
      if (updateRafId) {
        window.cancelAnimationFrame(updateRafId)
      }
      toolbarElement.remove()
    },
  }
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
  const codeBlock = code.closest('pre')

  menuElement.classList.add('memo-code-block-actions')

  const title = document.createElement('span')
  title.className = 'memo-code-block-title'
  title.textContent = '代码块'

  const toolbar = document.createElement('span')
  toolbar.className = 'memo-code-block-toolbar'

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

  const wrapButton = document.createElement('button')
  wrapButton.className = 'memo-code-wrap-toggle'
  wrapButton.type = 'button'
  wrapButton.setAttribute('aria-label', '自动换行')
  wrapButton.setAttribute('aria-pressed', 'false')
  wrapButton.textContent = '自动换行'
  wrapButton.onclick = (event) => {
    event.preventDefault()
    event.stopPropagation()
    if (!codeBlock) return

    const isWrapped = codeBlock.classList.toggle('memo-code-wrap-enabled')
    wrapButton.setAttribute('aria-pressed', String(isWrapped))
  }

  const wrapSeparator = document.createElement('i')
  wrapSeparator.className = 'memo-code-format-separator'
  wrapSeparator.setAttribute('aria-hidden', 'true')
  wrapSeparator.textContent = '|'

  const hiddenTextarea = menuElement.querySelector('textarea')
  const copyButton = menuElement.querySelector('span')

  toolbar.append(details, separator, wrapButton, wrapSeparator)
  if (hiddenTextarea) {
    toolbar.append(hiddenTextarea)
  }
  if (copyButton) {
    copyButton.classList.add('memo-code-copy-action')
    toolbar.append(copyButton)
  }

  menuElement.replaceChildren(title, toolbar)
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
    const selectionToolbarControllerRef = useRef<MemoSelectionToolbarController | null>(null)
    const slashCommandControllerRef = useRef<MemoSlashCommandController | null>(null)
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

    const disposeSelectionToolbarController = useCallback(() => {
      selectionToolbarControllerRef.current?.dispose()
      selectionToolbarControllerRef.current = null
    }, [])

    const disposeSlashCommandController = useCallback(() => {
      slashCommandControllerRef.current?.dispose()
      slashCommandControllerRef.current = null
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

    const handleSelectionToolbarCommand = useCallback(() => {
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

      const attachTailClickHandlers = () => {
        if (!editor || tailRootElement) return
        const runtime = editor.vditor
        if (!runtime?.element) return

        tailRootElement = runtime.element
        handleTailClick = (event: MouseEvent) => {
          if (!editor || editorRef.current !== editor || !isReadyRef.current) return
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
            if (!slashCommandControllerRef.current) {
              slashCommandControllerRef.current = createMemoSlashCommandController(
                editor,
                handleSelectionToolbarCommand,
              )
            }
            if (!selectionToolbarControllerRef.current) {
              selectionToolbarControllerRef.current = createMemoSelectionToolbarController(
                editor,
                handleSelectionToolbarCommand,
              )
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
          toolbarConfig: {
            hide: true,
            pin: false,
          },
          preview: {
            theme: {
              current: contentThemeRef.current,
            },
            hljs: {
              lineNumber: true,
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
        disposeSlashCommandController()
        disposeSelectionToolbarController()
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
      disposeSelectionToolbarController,
      disposeSlashCommandController,
      handleCodeLanguageChange,
      handleSelectionToolbarCommand,
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
        disposeSlashCommandController()
        disposeSelectionToolbarController()
        disposeOutlineController()
      }
    }, [disposeOutlineController, disposeSelectionToolbarController, disposeSlashCommandController])

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
