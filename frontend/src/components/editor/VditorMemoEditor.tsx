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

type VditorMemoEditorProps = {
  onChange: (value: string) => void
  onUpload?: (file: File) => Promise<string>
  placeholder?: string
  value: string
}

const toolbar = [
  'emoji',
  'headings',
  'bold',
  'italic',
  'strike',
  '|',
  'line',
  'quote',
  'list',
  'ordered-list',
  'check',
  '|',
  'code',
  'inline-code',
  'link',
  'table',
  'upload',
  '|',
  'undo',
  'redo',
  'preview',
  'fullscreen',
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

export const VditorMemoEditor = forwardRef<VditorMemoEditorHandle, VditorMemoEditorProps>(
  function VditorMemoEditor({ onChange, onUpload, placeholder = '', value }, ref) {
    const hostRef = useRef<HTMLDivElement | null>(null)
    const editorRef = useRef<Vditor | null>(null)
    const initialValueRef = useRef(value)
    const isReadyRef = useRef(false)
    const onChangeRef = useRef(onChange)
    const onUploadRef = useRef(onUpload)
    const pendingValueRef = useRef<string | null>(null)
    const suppressInputRef = useRef(false)
    const valueRef = useRef(value)

    valueRef.current = value
    onChangeRef.current = onChange
    onUploadRef.current = onUpload

    const applyEditorValue = useCallback((editor: Vditor, nextValue: string) => {
      suppressInputRef.current = true
      editor.setValue(nextValue, true)
      window.setTimeout(() => {
        suppressInputRef.current = false
      }, 0)
    }, [])

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
          mode: 'ir',
          placeholder,
          theme: 'classic',
          toolbar: [...toolbar],
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
          value: initialValueRef.current,
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
    }, [applyEditorValue, placeholder])

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
