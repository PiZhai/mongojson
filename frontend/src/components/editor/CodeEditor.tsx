import { lazy, Suspense } from 'react'
import type { MongoDiagnostic } from '../../lib/mongodb-core'

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
  diagnostics?: MongoDiagnostic[]
}

export function CodeEditor({
  value,
  onChange,
  language = 'json',
  readOnly = false,
  minimap = false,
  height = '100%',
  focusLine = null,
  diagnostics = [],
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
          diagnostics={diagnostics}
        />
      </Suspense>
    </div>
  )
}
