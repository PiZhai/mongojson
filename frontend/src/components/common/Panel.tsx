import type { PropsWithChildren, ReactNode } from 'react'

type PanelProps = PropsWithChildren<{
  eyebrow?: string
  title: string
  subtitle?: string
  actions?: ReactNode
}>

export function Panel({ eyebrow, title, actions, children }: PanelProps) {
  return (
    <section className="panel">
      <div className="panel-header">
        <div className="panel-header-copy">
          {eyebrow ? <div className="panel-eyebrow">{eyebrow}</div> : null}
          <h2 className="panel-title">{title}</h2>
        </div>
        {actions ? <div className="toolbar">{actions}</div> : null}
      </div>
      {children}
    </section>
  )
}
