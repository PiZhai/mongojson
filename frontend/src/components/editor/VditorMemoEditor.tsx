import Vditor from 'vditor'
import 'vditor/dist/index.css'
import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef } from 'react'

export type VditorMemoEditorHandle = {
  focus: () => void
  getHtml: () => string
  getMarkdown: () => string
  insertText: (text: string) => void
  isUploading: () => boolean
}

export type VditorMemoMode = 'wysiwyg' | 'ir' | 'sv'
export type VditorMemoTheme = 'classic' | 'dark'
export type VditorMemoContentTheme = 'ant-design' | 'light' | 'dark' | 'wechat'
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
    const pendingValueRef = useRef<string | null>(null)
    const suppressInputRef = useRef(false)
    const themeRef = useRef(theme)
    const valueRef = useRef(value)

    valueRef.current = value
    codeThemeRef.current = codeTheme
    contentThemeRef.current = contentTheme
    onChangeRef.current = onChange
    onUploadRef.current = onUpload
    themeRef.current = theme

    const applyEditorValue = useCallback((editor: Vditor, nextValue: string) => {
      suppressInputRef.current = true
      editor.setValue(nextValue, true)
      window.setTimeout(() => {
        suppressInputRef.current = false
      }, 0)
    }, [])

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
            })
          },
          height: 'auto',
          input: (nextValue: string) => {
            valueRef.current = nextValue
            if (suppressInputRef.current) return
            onChangeRef.current(nextValue)
          },
          lang: 'zh_CN',
          mode,
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
      }, 0)

      return () => {
        disposed = true
        window.clearTimeout(initTimer)
        isReadyRef.current = false
        pendingValueRef.current = null
        if (editor) {
          destroyEditor(editor, host)
        } else {
          host.replaceChildren()
        }
        if (editorRef.current === editor) {
          editorRef.current = null
        }
      }
    }, [applyEditorValue, handleCodeLanguageChange, mode, placeholder])

    useEffect(() => {
      const editor = editorRef.current
      if (!editor || !isReadyRef.current) return
      editor.setTheme(theme, contentTheme, codeTheme)
    }, [codeTheme, contentTheme, theme])

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
