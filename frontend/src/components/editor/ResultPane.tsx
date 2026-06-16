import type { ReactNode } from 'react'
import { CodeEditor } from './CodeEditor'

type ResultPaneProps = {
  title: string
  language?: string
  value: string
  placeholder: string
  actions?: ReactNode
}

export function ResultPane({
  title,
  language = 'json',
  value,
  placeholder,
  actions,
}: ResultPaneProps) {
  return (
    <div className="editor-pane">
      <div className="editor-pane-header">
        <span className="editor-pane-title">{title}</span>
        {actions ? <div className="editor-pane-actions">{actions}</div> : null}
      </div>
      {value ? (
        <CodeEditor height="100%" language={language} readOnly value={value} />
      ) : (
        <div className="empty-state">{placeholder}</div>
      )}
    </div>
  )
}
