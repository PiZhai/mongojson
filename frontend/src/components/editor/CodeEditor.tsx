import Editor from '@monaco-editor/react'
import { useEffect, useRef } from 'react'
import type * as Monaco from 'monaco-editor'
import { ensureMongoLanguage, MONGO_LANGUAGE_ID } from '../../lib/editor/mongoLanguage'

type CodeEditorProps = {
  value: string
  onChange?: (value: string) => void
  language?: string
  readOnly?: boolean
  minimap?: boolean
  height?: string
  focusLine?: number | null
}

export function CodeEditor({
  value,
  onChange,
  language = 'json',
  readOnly = false,
  minimap = false,
  height = '100%',
  focusLine = null,
}: CodeEditorProps) {
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null)

  useEffect(() => {
    if (!focusLine || !editorRef.current) return
    editorRef.current.revealLineInCenter(focusLine)
    editorRef.current.setPosition({ lineNumber: focusLine, column: 1 })
    editorRef.current.focus()
  }, [focusLine])

  return (
    <div className="editor-host">
      <Editor
        beforeMount={(monaco) => {
          if (language === MONGO_LANGUAGE_ID) {
            ensureMongoLanguage(monaco)
          }
        }}
        height={height}
        language={language}
        onMount={(editor, monaco) => {
          editorRef.current = editor
          if (language === MONGO_LANGUAGE_ID && editor.getModel()) {
            monaco.editor.setModelMarkers(editor.getModel()!, MONGO_LANGUAGE_ID, [])
          }
        }}
        onChange={(next) => onChange?.(next ?? '')}
        options={{
          minimap: { enabled: minimap },
          readOnly,
          fontSize: 13,
          lineHeight: 22,
          wordWrap: 'on',
          scrollBeyondLastLine: false,
          smoothScrolling: true,
          padding: { top: 16, bottom: 16 },
          fontFamily:
            "'JetBrains Mono', 'SFMono-Regular', 'SF Mono', Menlo, Consolas, monospace",
        }}
        theme={language === MONGO_LANGUAGE_ID ? 'mongodb-vs-dark' : 'vs-dark'}
        value={value}
      />
    </div>
  )
}
