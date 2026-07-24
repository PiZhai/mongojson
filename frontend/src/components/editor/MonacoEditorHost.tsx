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
  allowParentWheelScroll = false,
}: CodeEditorProps) {
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null)
  const monacoRef = useRef<typeof Monaco | null>(null)
  const wheelCleanupRef = useRef<(() => void) | null>(null)

  useEffect(() => () => {
    wheelCleanupRef.current?.()
  }, [])

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
        wheelCleanupRef.current?.()
        wheelCleanupRef.current = null

        if (allowParentWheelScroll) {
          const editorNode = editor.getDomNode()
          const handleBoundaryWheel = (event: WheelEvent) => {
            if (!editorNode || event.deltaY === 0) return

            const scrollTop = editor.getScrollTop()
            const maxScrollTop = Math.max(0, editor.getScrollHeight() - editor.getLayoutInfo().height)
            const isLeavingTop = event.deltaY < 0 && scrollTop <= 1
            const isLeavingBottom = event.deltaY > 0 && scrollTop >= maxScrollTop - 1
            if (!isLeavingTop && !isLeavingBottom) return

            let scrollParent = editorNode.parentElement
            while (scrollParent) {
              const { overflowY } = window.getComputedStyle(scrollParent)
              if (
                (overflowY === 'auto' || overflowY === 'scroll') &&
                scrollParent.scrollHeight > scrollParent.clientHeight
              ) {
                break
              }
              scrollParent = scrollParent.parentElement
            }
            if (!scrollParent) return

            const deltaMultiplier =
              event.deltaMode === WheelEvent.DOM_DELTA_LINE
                ? 16
                : event.deltaMode === WheelEvent.DOM_DELTA_PAGE
                  ? scrollParent.clientHeight
                  : 1

            event.preventDefault()
            event.stopPropagation()
            scrollParent.scrollTop += event.deltaY * deltaMultiplier
          }

          editorNode?.addEventListener('wheel', handleBoundaryWheel, {
            capture: true,
            passive: false,
          })
          wheelCleanupRef.current = () => {
            editorNode?.removeEventListener('wheel', handleBoundaryWheel, { capture: true })
          }
        }

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
        scrollbar: { alwaysConsumeMouseWheel: true },
        padding: { top: 16, bottom: 16 },
        fontFamily: "'JetBrains Mono', 'SFMono-Regular', 'SF Mono', Menlo, Consolas, monospace",
      }}
      theme={language === MONGO_LANGUAGE_ID ? 'mongodb-light' : 'vs'}
      value={value}
    />
  )
}
