import { lazy, Suspense } from 'react'

const MonacoEditorHost = lazy(() =>
  import('./MonacoEditorHost').then((module) => ({ default: module.MonacoEditorHost })),
)

export type CodeEditorProps = {
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
  return (
    <div className="editor-host">
      <Suspense
        fallback={
          <div className="editor-loading" role="status">
            正在加载编辑器...
          </div>
        }
      >
        <MonacoEditorHost
          focusLine={focusLine}
          height={height}
          language={language}
          minimap={minimap}
          onChange={onChange}
          readOnly={readOnly}
          value={value}
        />
      </Suspense>
    </div>
  )
}
