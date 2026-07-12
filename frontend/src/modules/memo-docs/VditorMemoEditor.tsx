import Vditor, { markdownSlashCommandDefinitions } from '@mongojson/vditor-core'
import type { VditorMode } from '@mongojson/vditor-core'
import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef } from 'react'

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
    const pendingValueRef = useRef<string | null>(null)
    const suppressInputRef = useRef(false)
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

    const applyEditorValue = useCallback((editor: Vditor, nextValue: string) => {
      suppressInputRef.current = true
      editor.setDocument(nextValue, { clearStack: true })
      window.setTimeout(() => {
        suppressInputRef.current = false
      }, 0)
    }, [])

    useEffect(() => {
      if (!hostRef.current || editorRef.current) return
      const host = hostRef.current
      let disposed = false
      let editor: Vditor | null = null
      let unsubscribeTransaction: (() => void) | null = null

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
                const previousValue = valueRef.current
                valueRef.current = transaction.markdown
                if (suppressInputRef.current) return
                if (transaction.source === 'input' || transaction.source === 'command' || transaction.source === 'insert-value') {
                  if (transaction.markdown === previousValue) return
                  onChangeRef.current(transaction.markdown)
                }
              })
            }
            const pendingValue = pendingValueRef.current
            const nextValue = pendingValue ?? valueRef.current

            pendingValueRef.current = null
            window.requestAnimationFrame(() => {
              if (disposed || editorRef.current !== editor || !isReadyRef.current || !editor) return
              if (getEditorMarkdown(editor) !== nextValue) {
                applyEditorValue(editor, nextValue)
              }
            })
          },
          height: 'calc(100vh - 242px)',
          lang: 'zh_CN',
          mode: initialModeRef.current,
          outline: {
            activeClass: 'memo-outline-item-active',
            enhanced: true,
            enable: true,
            position: 'left',
            scrollOffset: 72,
          },
          command: markdownSlashCommandDefinitions,
          editorTail: {
            enable: true,
            ignoreSelector: '.vditor-code-language-menu',
            lines: 3,
            singleClickDelay: 260,
          },
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
          preview: {
            theme: {
              current: contentThemeRef.current,
            },
            hljs: {
              codeLanguageMenu: {
                enable: true,
              },
              style: codeThemeRef.current,
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
      placeholder,
    ])

    useEffect(() => {
      const editor = editorRef.current
      if (!editor || !isReadyRef.current) return
      editor.setTheme(theme, contentTheme, codeTheme)
    }, [codeTheme, contentTheme, theme])

    useEffect(() => {
      const editor = editorRef.current
      if (!editor || !isReadyRef.current) return
      editor.setMode(mode)
    }, [mode])

    useEffect(() => {
      const editor = editorRef.current
      const nextValue = initialValueRef.current
      valueRef.current = nextValue
      if (!editor) return
      if (!isReadyRef.current) {
        pendingValueRef.current = nextValue
        return
      }
      const valueToApply = nextValue
      if (valueToApply === getEditorMarkdown(editor)) {
        return
      }
      window.requestAnimationFrame(() => {
        if (editorRef.current !== editor || !isReadyRef.current) return
        if (valueToApply === getEditorMarkdown(editor)) return
        applyEditorValue(editor, valueToApply)
      })
    }, [applyEditorValue, documentRevision])

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
      },
      isUploading() {
        return editorRef.current?.isUploading() ?? false
      },
    }))

    return <div className="memo-vditor-editor" ref={hostRef} />
  },
)
