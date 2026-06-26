import { basicSetup } from 'codemirror'
import { markdown } from '@codemirror/lang-markdown'
import { EditorSelection, EditorState, Prec, RangeSetBuilder, StateField, type Extension } from '@codemirror/state'
import {
  Decoration,
  EditorView,
  keymap,
  placeholder as cmPlaceholder,
  WidgetType,
  type DecorationSet,
} from '@codemirror/view'
import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef } from 'react'

export type MarkdownSlashCommand = {
  insert: (selection: { value: string; start: number; end: number }) => {
    value: string
    cursorStart: number
    cursorEnd: number
  }
}

export type MarkdownLiveEditorHandle = {
  applySlashCommand: (command: MarkdownSlashCommand) => void
  focus: () => void
  insertText: (text: string) => void
  wrapSelection: (before: string, after?: string, placeholder?: string) => void
}

type SlashState = {
  open: boolean
  query: string
}

type MarkdownCommandKey = 'ArrowDown' | 'ArrowUp' | 'Enter' | 'Tab' | 'Escape'

type MarkdownLiveEditorProps = {
  onChange: (value: string) => void
  onCommandKey?: (key: MarkdownCommandKey) => boolean
  onSlashChange?: (state: SlashState) => void
  placeholder?: string
  value: string
}

type FenceBlockRange = {
  fromLine: number
  toLine: number
  after: number
}

const CODE_LANGUAGES = [
  'HTTP',
  'Haskell',
  'JSON',
  'Java',
  'JavaScript',
  'Julia',
  'Kotlin',
  'LaTeX',
  'Lisp',
  'Lua',
  'MATLAB',
  'Makefile',
  'Markdown',
  'Nginx',
  'Objective-C',
  'OpenGL Shading Language',
  'PHP',
  'Perl',
  'PowerShell',
  'Prolog',
  'Properties',
  'ProtoBuf',
  'Python',
  'R',
  'Ruby',
  'Rust',
  'SAS',
  'Scala',
  'Shell',
  'SQL',
  'TypeScript',
] as const

const hiddenSyntax = Decoration.replace({ class: 'cm-md-hidden-syntax' })
const boldMark = Decoration.mark({ class: 'cm-md-bold' })
const italicMark = Decoration.mark({ class: 'cm-md-italic' })
const inlineCodeMark = Decoration.mark({ class: 'cm-md-inline-code' })
const linkMark = Decoration.mark({ class: 'cm-md-link' })

function normalizeLanguage(language: string) {
  return language.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'code'
}

function getLanguageLabel(language: string) {
  const normalized = normalizeLanguage(language)
  const known = CODE_LANGUAGES.find((item) => normalizeLanguage(item) === normalized)
  return known ?? (language.trim() || 'code').toUpperCase()
}

function getFenceLanguage(label: string) {
  const normalized = normalizeLanguage(label)
  if (normalized === 'javascript') return 'javascript'
  if (normalized === 'typescript') return 'typescript'
  if (normalized === 'open-gl-shading-language') return 'glsl'
  if (normalized === 'objective-c') return 'objective-c'
  return normalized
}

function appendHighlightedCode(lineElement: HTMLElement, code: string, language: string) {
  const normalizedLanguage = normalizeLanguage(language)
  const tokenPattern =
    normalizedLanguage === 'http'
      ? /\b(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|HTTP\/\d(?:\.\d)?|\d{3})\b|("(?:\\.|[^"\\])*")|(:[^\s]+)|\/\/.*|#.*/g
      : /("(?:\\.|[^"\\])*"(?=\s*:)|"(?:\\.|[^"\\])*"|\/\/.*|#.*|\b(?:abstract|async|await|break|case|catch|class|const|def|else|enum|export|extends|false|final|fn|for|from|func|function|if|import|in|interface|let|match|null|package|private|protected|public|return|static|struct|switch|throw|true|try|type|val|var|void|while)\b|-?\b\d+(?:\.\d+)?\b)/g
  let cursor = 0
  let match: RegExpExecArray | null

  while ((match = tokenPattern.exec(code))) {
    if (match.index > cursor) {
      lineElement.append(document.createTextNode(code.slice(cursor, match.index)))
    }

    const token = match[0]
    const span = document.createElement('span')
    if (token.startsWith('//') || token.startsWith('#')) {
      span.className = 'cm-md-code-token-comment'
    } else if (token.startsWith('"') && code.slice(match.index + token.length).trimStart().startsWith(':')) {
      span.className = 'cm-md-code-token-key'
    } else if (token.startsWith('"')) {
      span.className = 'cm-md-code-token-string'
    } else if (/^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|HTTP\/\d(?:\.\d)?|\d{3})$/.test(token)) {
      span.className = 'cm-md-code-token-keyword'
    } else if (/^(true|false|null)$/.test(token)) {
      span.className = 'cm-md-code-token-literal'
    } else if (/^-?\d/.test(token)) {
      span.className = 'cm-md-code-token-number'
    } else {
      span.className = 'cm-md-code-token-keyword'
    }
    span.textContent = token
    lineElement.append(span)
    cursor = match.index + token.length
  }

  if (cursor < code.length) {
    lineElement.append(document.createTextNode(code.slice(cursor)))
  }
}

class CodeBlockPreviewWidget extends WidgetType {
  private readonly language: string
  private readonly code: string
  private readonly editFrom: number
  private readonly fenceLineTo: number
  private readonly codeFrom: number
  private readonly codeTo: number

  constructor(language: string, code: string, editFrom: number, fenceLineTo: number, codeFrom: number, codeTo: number) {
    super()
    this.language = language
    this.code = code
    this.editFrom = editFrom
    this.fenceLineTo = fenceLineTo
    this.codeFrom = codeFrom
    this.codeTo = codeTo
  }

  eq(other: CodeBlockPreviewWidget) {
    return other.language === this.language && other.code === this.code && other.editFrom === this.editFrom && other.fenceLineTo === this.fenceLineTo && other.codeFrom === this.codeFrom && other.codeTo === this.codeTo
  }

  toDOM(view: EditorView) {
    const container = document.createElement('div')
    container.className = `cm-md-code-preview cm-md-code-preview-lang-${normalizeLanguage(this.language)}`

    const header = document.createElement('div')
    header.className = 'cm-md-code-preview-header'

    const title = document.createElement('div')
    title.className = 'cm-md-code-preview-title'
    title.textContent = '代码块'

    const controls = document.createElement('div')
    controls.className = 'cm-md-code-preview-controls'

    const languageLabel = getLanguageLabel(this.language)
    const languageButton = document.createElement('button')
    languageButton.className = 'cm-md-code-language-button'
    languageButton.type = 'button'
    languageButton.textContent = languageLabel
    languageButton.title = '切换代码语言'
    languageButton.setAttribute('aria-haspopup', 'listbox')
    languageButton.setAttribute('aria-expanded', 'false')
    languageButton.addEventListener('mousedown', (event) => {
      event.preventDefault()
      event.stopPropagation()
    })
    languageButton.addEventListener('click', (event) => {
      event.preventDefault()
      event.stopPropagation()
      const open = languageMenu.hidden
      languageMenu.hidden = !open
      languageButton.setAttribute('aria-expanded', String(open))
      if (open) {
        searchInput.focus()
      }
    })

    const languageMenu = document.createElement('div')
    languageMenu.className = 'cm-md-code-language-menu'
    languageMenu.hidden = true
    languageMenu.setAttribute('role', 'listbox')

    const searchInput = document.createElement('input')
    searchInput.className = 'cm-md-code-language-search'
    searchInput.type = 'search'
    searchInput.placeholder = '搜索'
    searchInput.addEventListener('mousedown', (event) => {
      event.stopPropagation()
    })

    const languageList = document.createElement('div')
    languageList.className = 'cm-md-code-language-list'

    const renderLanguages = (query = '') => {
      languageList.replaceChildren()
      const normalizedQuery = query.trim().toLowerCase()
      CODE_LANGUAGES.filter((item) => item.toLowerCase().includes(normalizedQuery)).forEach((item) => {
        const option = document.createElement('button')
        option.className = 'cm-md-code-language-option'
        option.type = 'button'
        option.textContent = item
        option.setAttribute('role', 'option')
        option.setAttribute('aria-selected', String(normalizeLanguage(item) === normalizeLanguage(this.language)))
        option.addEventListener('mousedown', (event) => {
          event.preventDefault()
          event.stopPropagation()
        })
        option.addEventListener('click', (event) => {
          event.preventDefault()
          event.stopPropagation()
          const nextLanguage = getFenceLanguage(item)
          view.dispatch({
            changes: { from: this.editFrom, to: this.fenceLineTo, insert: `\`\`\`${nextLanguage}` },
          })
        })
        languageList.append(option)
      })
    }

    searchInput.addEventListener('input', () => renderLanguages(searchInput.value))
    renderLanguages()
    languageMenu.append(searchInput, languageList)

    const body = document.createElement('div')
    body.className = 'cm-md-code-preview-body'
    let wrapEnabled = false

    const renderPreviewBody = (code: string) => {
      body.replaceChildren()
      body.className = `cm-md-code-preview-body${wrapEnabled ? ' cm-md-code-preview-body-wrap' : ''}`
      const lines = code.length > 0 ? code.split('\n') : ['']
      lines.forEach((line, index) => {
        const row = document.createElement('div')
        row.className = 'cm-md-code-preview-row'

        const number = document.createElement('span')
        number.className = 'cm-md-code-preview-line-number'
        number.textContent = String(index + 1)

        const codeLine = document.createElement('code')
        codeLine.className = 'cm-md-code-preview-line'
        appendHighlightedCode(codeLine, line || ' ', this.language)

        row.append(number, codeLine)
        body.append(row)
      })
    }

    const renderCodeEditor = () => {
      body.replaceChildren()
      body.className = `cm-md-code-preview-body cm-md-code-preview-editor${wrapEnabled ? ' cm-md-code-preview-body-wrap' : ''}`

      const gutter = document.createElement('div')
      gutter.className = 'cm-md-code-editor-gutter'

      const textarea = document.createElement('textarea')
      textarea.className = 'cm-md-code-editor-textarea'
      textarea.value = this.code
      textarea.rows = Math.max(1, this.code.split('\n').length)
      textarea.spellcheck = false

      const syncGutter = () => {
        const lineCount = Math.max(1, textarea.value.split('\n').length)
        gutter.replaceChildren()
        for (let index = 1; index <= lineCount; index += 1) {
          const number = document.createElement('span')
          number.textContent = String(index)
          gutter.append(number)
        }
        textarea.rows = lineCount
      }

      textarea.addEventListener('mousedown', (event) => {
        event.stopPropagation()
      })
      textarea.addEventListener('input', syncGutter)
      textarea.addEventListener('blur', () => {
        if (textarea.value !== this.code) {
          view.dispatch({
            changes: { from: this.codeFrom, to: this.codeTo, insert: textarea.value },
          })
        } else {
          renderPreviewBody(this.code)
        }
      })

      syncGutter()
      body.append(gutter, textarea)
      window.requestAnimationFrame(() => textarea.focus())
    }

    const wrapButton = document.createElement('button')
    wrapButton.className = 'cm-md-code-wrap-button'
    wrapButton.type = 'button'
    wrapButton.textContent = '自动换行'
    wrapButton.setAttribute('aria-pressed', 'false')
    wrapButton.addEventListener('mousedown', (event) => {
      event.preventDefault()
      event.stopPropagation()
    })
    wrapButton.addEventListener('click', (event) => {
      event.preventDefault()
      event.stopPropagation()
      wrapEnabled = !wrapEnabled
      body.classList.toggle('cm-md-code-preview-body-wrap', wrapEnabled)
      wrapButton.textContent = wrapEnabled ? '取消自动换行' : '自动换行'
      wrapButton.setAttribute('aria-pressed', String(wrapEnabled))
    })

    const copyButton = document.createElement('button')
    copyButton.className = 'cm-md-code-copy cm-md-code-copy-action'
    copyButton.type = 'button'
    copyButton.textContent = '复制'
    copyButton.title = '复制代码块'
    copyButton.setAttribute('aria-label', '复制代码块')
    copyButton.addEventListener('mousedown', (event) => {
      event.preventDefault()
      event.stopPropagation()
    })
    copyButton.addEventListener('click', async (event) => {
      event.preventDefault()
      event.stopPropagation()
      await navigator.clipboard.writeText(this.code)
      copyButton.classList.add('cm-md-code-copy-done')
      window.setTimeout(() => copyButton.classList.remove('cm-md-code-copy-done'), 900)
      view.focus()
    })

    controls.append(languageButton, languageMenu, wrapButton, copyButton)
    header.append(title, controls)

    renderPreviewBody(this.code)

    container.addEventListener('mousedown', (event) => {
      if (event.target instanceof HTMLElement && event.target.closest('button')) return
      event.preventDefault()
      renderCodeEditor()
    })

    container.append(header, body)
    return container
  }
}

function lineClass(name: string) {
  return Decoration.line({ class: name })
}

function findFenceBlockAtLine(state: EditorState, targetLineNumber: number): FenceBlockRange | null {
  let fenceStartLine: number | null = null

  for (let lineNumber = 1; lineNumber <= state.doc.lines; lineNumber += 1) {
    const line = state.doc.line(lineNumber)
    if (!line.text.trim().startsWith('```')) continue

    if (fenceStartLine === null) {
      fenceStartLine = lineNumber
      continue
    }

    if (targetLineNumber >= fenceStartLine && targetLineNumber <= lineNumber) {
      return {
        fromLine: fenceStartLine,
        toLine: lineNumber,
        after: line.to < state.doc.length ? line.to + 1 : line.to,
      }
    }
    fenceStartLine = null
  }

  return null
}

function collapseActiveCodeBlock(view: EditorView) {
  const activeLineNumber = view.state.doc.lineAt(view.state.selection.main.head).number
  const activeBlock = findFenceBlockAtLine(view.state, activeLineNumber)
  if (!activeBlock) return false

  view.dispatch({
    selection: EditorSelection.cursor(activeBlock.after),
    scrollIntoView: false,
  })
  return true
}

function addInlineDecorations(lineText: string, lineFrom: number, builder: RangeSetBuilder<Decoration>) {
  const boldPattern = /\*\*([^*\n]+)\*\*/g
  for (const match of lineText.matchAll(boldPattern)) {
    const index = match.index ?? 0
    const from = lineFrom + index
    const to = from + match[0].length
    builder.add(from, from + 2, hiddenSyntax)
    builder.add(from + 2, to - 2, boldMark)
    builder.add(to - 2, to, hiddenSyntax)
  }

  const italicPattern = /(^|[^*])\*([^*\n]+)\*/g
  for (const match of lineText.matchAll(italicPattern)) {
    const index = (match.index ?? 0) + match[1].length
    const from = lineFrom + index
    const to = from + match[0].length - match[1].length
    builder.add(from, from + 1, hiddenSyntax)
    builder.add(from + 1, to - 1, italicMark)
    builder.add(to - 1, to, hiddenSyntax)
  }

  const codePattern = /`([^`\n]+)`/g
  for (const match of lineText.matchAll(codePattern)) {
    const index = match.index ?? 0
    const from = lineFrom + index
    const to = from + match[0].length
    builder.add(from, from + 1, hiddenSyntax)
    builder.add(from + 1, to - 1, inlineCodeMark)
    builder.add(to - 1, to, hiddenSyntax)
  }

  const linkPattern = /\[([^\]\n]+)\]\(([^)\n]+)\)/g
  for (const match of lineText.matchAll(linkPattern)) {
    const index = match.index ?? 0
    const from = lineFrom + index
    const textStart = from + 1
    const textEnd = textStart + match[1].length
    const urlStart = textEnd + 2
    const to = from + match[0].length
    builder.add(from, textStart, hiddenSyntax)
    builder.add(textStart, textEnd, linkMark)
    builder.add(textEnd, urlStart, hiddenSyntax)
    builder.add(urlStart, to, hiddenSyntax)
  }
}

function buildPreviewDecorations(state: EditorState) {
  const builder = new RangeSetBuilder<Decoration>()
  const activeLineNumber = state.doc.lineAt(state.selection.main.head).number
  let fenceStart:
    | {
        from: number
        lineNumber: number
        language: string
      }
    | null = null

  for (let lineNumber = 1; lineNumber <= state.doc.lines; lineNumber += 1) {
    const line = state.doc.line(lineNumber)
    const text = line.text
    const trimmed = text.trim()
    const isActive = line.number === activeLineNumber
    const fence = trimmed.match(/^```\s*(.*)$/)

    if (fence) {
      if (!fenceStart) {
        fenceStart = {
          from: line.from,
          lineNumber: line.number,
          language: fence[1]?.trim() || '',
        }
      } else {
        const codeFrom = state.doc.line(fenceStart.lineNumber).to + 1
        const codeTo = line.from > codeFrom ? line.from - 1 : line.from
        const code = state.doc.sliceString(codeFrom, codeTo)
        builder.add(
          fenceStart.from,
          line.to,
          Decoration.replace({
            block: true,
            widget: new CodeBlockPreviewWidget(fenceStart.language, code, fenceStart.from, state.doc.line(fenceStart.lineNumber).to, codeFrom, codeTo),
          }),
        )
        fenceStart = null
      }
      continue
    }

    if (fenceStart) {
      continue
    }

    if (isActive || !trimmed) continue

    const heading = text.match(/^(#{1,6})\s+/)
    if (heading) {
      const markerEnd = line.from + heading[0].length
      builder.add(line.from, line.from, lineClass(`cm-md-heading cm-md-heading-${heading[1].length}`))
      builder.add(line.from, markerEnd, hiddenSyntax)
    } else if (/^\s*>\s?/.test(text)) {
      const marker = text.match(/^\s*>\s?/)?.[0] ?? ''
      builder.add(line.from, line.from, lineClass('cm-md-quote-line'))
      builder.add(line.from, line.from + marker.length, hiddenSyntax)
    } else if (/^\s*([-*+]|\d+\.)\s+/.test(text)) {
      builder.add(line.from, line.from, lineClass('cm-md-list-line'))
    } else if (/^\s*---+\s*$/.test(text)) {
      builder.add(line.from, line.from, lineClass('cm-md-divider-line'))
    }

    addInlineDecorations(text, line.from, builder)
  }

  return builder.finish()
}

const previewDecorationsField = StateField.define<DecorationSet>({
  create(state) {
    return buildPreviewDecorations(state)
  },
  update(decorations, transaction) {
    if (transaction.docChanged || transaction.selection) {
      return buildPreviewDecorations(transaction.state)
    }
    return decorations
  },
  provide: (field) => EditorView.decorations.from(field),
})

function livePreviewExtension(): Extension {
  return [
    previewDecorationsField,
    EditorView.domEventHandlers({
      mousemove(event, view) {
        if (event.buttons !== 0) return false
        const target = event.target
        if (!(target instanceof HTMLElement)) return false
        if (target.closest('.cm-md-code-line, .cm-md-code-fence')) return false
        return collapseActiveCodeBlock(view)
      },
      mouseleave(_event, view) {
        return collapseActiveCodeBlock(view)
      },
    }),
  ]
}

function updateSlashState(view: EditorView, onSlashChange?: (state: SlashState) => void) {
  if (!onSlashChange) return
  const cursor = view.state.selection.main.head
  const beforeCursor = view.state.doc.sliceString(0, cursor)
  const slashStart = beforeCursor.lastIndexOf('/')
  const token = slashStart >= 0 ? beforeCursor.slice(slashStart + 1) : ''
  const open =
    slashStart >= 0 &&
    beforeCursor[slashStart - 1] !== '/' &&
    !/\s/.test(beforeCursor.slice(slashStart + 1, cursor))
  onSlashChange({ open, query: open ? token : '' })
}

export const MarkdownLiveEditor = forwardRef<MarkdownLiveEditorHandle, MarkdownLiveEditorProps>(
  function MarkdownLiveEditor({ onChange, onCommandKey, onSlashChange, placeholder = '', value }, ref) {
    const hostRef = useRef<HTMLDivElement | null>(null)
    const viewRef = useRef<EditorView | null>(null)
    const initialPlaceholderRef = useRef(placeholder)
    const initialValueRef = useRef(value)
    const currentValueRef = useRef(value)
    const onChangeRef = useRef(onChange)
    const onCommandKeyRef = useRef(onCommandKey)
    const onSlashChangeRef = useRef(onSlashChange)

    onChangeRef.current = onChange
    onCommandKeyRef.current = onCommandKey
    onSlashChangeRef.current = onSlashChange

    const editorTheme = useMemo(
      () =>
        EditorView.theme({
          '&': {
            minHeight: '100%',
          },
          '.cm-scroller': {
            fontFamily: 'var(--font-sans)',
            lineHeight: '1.8',
            overflow: 'visible',
          },
          '.cm-content': {
            minHeight: 'calc(100vh - 280px)',
            padding: '28px 0 96px',
          },
          '.cm-focused': {
            outline: 'none',
          },
          '& .cm-selectionBackground': {
            background: 'rgba(147, 197, 253, 0.34)',
            borderRadius: '6px',
            boxShadow: '0 0 0 1px rgba(96, 165, 250, 0.12)',
          },
          '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground': {
            background: 'rgba(147, 197, 253, 0.42)',
            borderRadius: '6px',
            boxShadow: '0 0 0 1px rgba(96, 165, 250, 0.16)',
          },
          '& .cm-line::selection, & .cm-line ::selection': {
            backgroundColor: 'transparent !important',
          },
        }),
      [],
    )

    useEffect(() => {
      if (!hostRef.current || viewRef.current) return

      const updateListener = EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          const nextValue = update.state.doc.toString()
          currentValueRef.current = nextValue
          onChangeRef.current(nextValue)
        }
        if (update.docChanged || update.selectionSet) {
          updateSlashState(update.view, onSlashChangeRef.current)
        }
      })

      const state = EditorState.create({
        doc: initialValueRef.current,
        selection: EditorSelection.cursor(initialValueRef.current.length),
        extensions: [
          basicSetup,
          markdown(),
          EditorView.lineWrapping,
          editorTheme,
          updateListener,
          Prec.highest(
            keymap.of([
              {
                key: 'ArrowDown',
                run: () => Boolean(onCommandKeyRef.current?.('ArrowDown')),
              },
              {
                key: 'ArrowUp',
                run: () => Boolean(onCommandKeyRef.current?.('ArrowUp')),
              },
              {
                key: 'Enter',
                run: () => Boolean(onCommandKeyRef.current?.('Enter')),
              },
              {
                key: 'Tab',
                run: () => Boolean(onCommandKeyRef.current?.('Tab')),
              },
              {
                key: 'Escape',
                run: () => Boolean(onCommandKeyRef.current?.('Escape')),
              },
            ]),
          ),
          livePreviewExtension(),
          EditorView.contentAttributes.of({ 'aria-label': '随手记 Markdown 编辑器' }),
          initialPlaceholderRef.current ? cmPlaceholder(initialPlaceholderRef.current) : [],
        ],
      })

      viewRef.current = new EditorView({
        parent: hostRef.current,
        state,
      })

      return () => {
        viewRef.current?.destroy()
        viewRef.current = null
      }
    }, [editorTheme])

    useEffect(() => {
      const view = viewRef.current
      if (!view) return
      const current = view.state.doc.toString()
      if (value === current || value === currentValueRef.current) return
      currentValueRef.current = value
      view.dispatch({
        changes: { from: 0, to: current.length, insert: value },
        selection: EditorSelection.cursor(value.length),
      })
    }, [value])

    useImperativeHandle(ref, () => ({
      applySlashCommand(command) {
        const view = viewRef.current
        if (!view) return
        const value = view.state.doc.toString()
        const cursor = view.state.selection.main.head
        const beforeCursor = value.slice(0, cursor)
        const slashStart = beforeCursor.lastIndexOf('/')
        const result = command.insert({
          value,
          start: slashStart >= 0 ? slashStart : cursor,
          end: cursor,
        })
        view.dispatch({
          changes: { from: 0, to: value.length, insert: result.value },
          selection: EditorSelection.range(result.cursorStart, result.cursorEnd),
          scrollIntoView: true,
        })
        view.focus()
      },
      focus() {
        viewRef.current?.focus()
      },
      insertText(text) {
        const view = viewRef.current
        if (!view) return
        const transaction = view.state.changeByRange((range) => ({
          changes: { from: range.from, to: range.to, insert: text },
          range: EditorSelection.cursor(range.from + text.length),
        }))
        view.dispatch(transaction)
        view.focus()
      },
      wrapSelection(before, after = '', placeholderText = '') {
        const view = viewRef.current
        if (!view) return
        const transaction = view.state.changeByRange((range) => {
          const selected = view.state.doc.sliceString(range.from, range.to)
          const content = selected || placeholderText
          const insert = `${before}${content}${after}`
          const cursorStart = range.from + before.length
          const cursorEnd = cursorStart + content.length
          return {
            changes: { from: range.from, to: range.to, insert },
            range: EditorSelection.range(cursorStart, cursorEnd),
          }
        })
        view.dispatch(transaction)
        view.focus()
      },
    }))

    return <div className="markdown-live-editor" ref={hostRef} />
  },
)
