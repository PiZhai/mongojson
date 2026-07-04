import Editor from '@monaco-editor/react'
import { useEffect, useRef } from 'react'
import type * as Monaco from 'monaco-editor'
import { ensureMongoLanguage, MONGO_LANGUAGE_ID } from '../../lib/editor/mongoLanguage'
import type { CodeEditorProps } from './CodeEditor'

export function MonacoEditorHost({
  value,
  onChange,
  language = 'json',
  readOnly = false,
  minimap = false,
  height = '100%',
  focusLine = null,
  diagnostics = [],
}: CodeEditorProps) {
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null)
  const monacoRef = useRef<typeof Monaco | null>(null)

  useEffect(() => {
    if (!focusLine || !editorRef.current) return
    editorRef.current.revealLineInCenter(focusLine)
    editorRef.current.setPosition({ lineNumber: focusLine, column: 1 })
    editorRef.current.focus()
  }, [focusLine])

  useEffect(() => {
    const editor = editorRef.current
    const monaco = monacoRef.current
    const model = editor?.getModel()
    if (!editor || !monaco || !model) return

    const text = model.getValue()
    const markers = diagnostics.map((diagnostic) => {
      const position = typeof diagnostic.offset === 'number'
        ? model.getPositionAt(Math.max(0, Math.min(text.length, diagnostic.offset)))
        : {
            lineNumber: diagnostic.line ?? 1,
            column: diagnostic.column ?? 1,
          }
      const severity =
        diagnostic.severity === 'error'
          ? monaco.MarkerSeverity.Error
          : diagnostic.severity === 'warning'
            ? monaco.MarkerSeverity.Warning
            : monaco.MarkerSeverity.Info

      return {
        severity,
        message: diagnostic.message,
        source: diagnostic.source,
        startLineNumber: position.lineNumber,
        startColumn: position.column,
        endLineNumber: position.lineNumber,
        endColumn: Math.max(position.column + 1, position.column),
      }
    })

    monaco.editor.setModelMarkers(model, language, markers)
  }, [diagnostics, language, value])

  return (
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
        monacoRef.current = monaco
        if (editor.getModel()) {
          monaco.editor.setModelMarkers(editor.getModel()!, language, [])
        }
      }}
      onChange={(next) => onChange?.(next ?? '')}
      options={{
        automaticLayout: true,
        minimap: { enabled: minimap },
        readOnly,
        fontSize: 13,
        lineHeight: 22,
        wordWrap: 'on',
        scrollBeyondLastLine: false,
        smoothScrolling: true,
        padding: { top: 16, bottom: 16 },
        fontFamily: "'JetBrains Mono', 'SFMono-Regular', 'SF Mono', Menlo, Consolas, monospace",
      }}
      theme={language === MONGO_LANGUAGE_ID ? 'mongodb-light' : 'vs'}
      value={value}
    />
  )
}
