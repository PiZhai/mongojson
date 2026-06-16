import { DiffEditor } from '@monaco-editor/react'
import { useEffect, useRef } from 'react'
import type * as Monaco from 'monaco-editor'

type DiffEditorPanelProps = {
  original: string
  modified: string
  onOriginalChange: (value: string) => void
  onModifiedChange: (value: string) => void
  focus?: {
    side: 'left' | 'right'
    line: number
    key: string
  } | null
}

export function DiffEditorPanel({
  original,
  modified,
  onOriginalChange,
  onModifiedChange,
  focus,
}: DiffEditorPanelProps) {
  const editorRef = useRef<Monaco.editor.IStandaloneDiffEditor | null>(null)

  useEffect(() => {
    if (!focus || !editorRef.current) return
    const targetEditor = focus.side === 'left' ? editorRef.current.getOriginalEditor() : editorRef.current.getModifiedEditor()
    targetEditor.revealLineInCenter(focus.line)
    targetEditor.setPosition({ lineNumber: focus.line, column: 1 })
    targetEditor.focus()
  }, [focus])

  return (
    <div className="editor-shell">
      <DiffEditor
        height="520px"
        language="json"
        modified={modified}
        onMount={(editor) => {
          editorRef.current = editor
          const originalEditor = editor.getOriginalEditor()
          const modifiedEditor = editor.getModifiedEditor()
          originalEditor.onDidChangeModelContent(() => onOriginalChange(originalEditor.getValue()))
          modifiedEditor.onDidChangeModelContent(() => onModifiedChange(modifiedEditor.getValue()))
        }}
        options={{
          fontSize: 13,
          lineHeight: 22,
          renderSideBySide: true,
          originalEditable: true,
          readOnly: false,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
          wordWrap: 'on',
        }}
        original={original}
        theme="vs-dark"
      />
    </div>
  )
}
