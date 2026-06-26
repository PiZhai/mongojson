import Vditor from 'vditor'
import 'vditor/dist/index.css'
import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'

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

export const VditorMemoEditor = forwardRef<VditorMemoEditorHandle, VditorMemoEditorProps>(
  function VditorMemoEditor({ onChange, onUpload, placeholder = '', value }, ref) {
    const hostRef = useRef<HTMLDivElement | null>(null)
    const editorRef = useRef<Vditor | null>(null)
    const initialValueRef = useRef(value)
    const onChangeRef = useRef(onChange)
    const onUploadRef = useRef(onUpload)
    const valueRef = useRef(value)

    onChangeRef.current = onChange
    onUploadRef.current = onUpload

    useEffect(() => {
      if (!hostRef.current || editorRef.current) return

      const editor = new Vditor(hostRef.current, {
        cache: {
          enable: false,
        },
        height: 'auto',
        input: (nextValue: string) => {
          valueRef.current = nextValue
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
            if (!uploadImage) return '未配置图片上传。'

            const uploadDone: Promise<null> = Promise.all(
              files.map(async (file) => {
                const imageMarkdown = await uploadImage(file)
                editor.insertValue(`${imageMarkdown}\n\n`)
              }),
            ).then(() => null)

            return uploadDone
          },
          multiple: true,
        },
        value: initialValueRef.current,
      })

      editorRef.current = editor

      return () => {
        editor.destroy()
        editorRef.current = null
      }
    }, [placeholder])

    useEffect(() => {
      const editor = editorRef.current
      if (!editor || value === valueRef.current || value === editor.getValue()) return
      valueRef.current = value
      editor.setValue(value, true)
    }, [value])

    useImperativeHandle(ref, () => ({
      focus() {
        editorRef.current?.focus()
      },
      getHtml() {
        return editorRef.current?.getHTML() ?? ''
      },
      getMarkdown() {
        return editorRef.current?.getValue() ?? valueRef.current
      },
      insertText(text) {
        const editor = editorRef.current
        if (!editor) return
        editor.insertValue(text)
        valueRef.current = editor.getValue()
        onChangeRef.current(valueRef.current)
      },
      isUploading() {
        return editorRef.current?.isUploading() ?? false
      },
    }))

    return <div className="memo-vditor-editor" ref={hostRef} />
  },
)
