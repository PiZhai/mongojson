import type { PropsWithChildren, ReactNode } from 'react'

type PanelProps = PropsWithChildren<{
  eyebrow?: string
  title: string
  subtitle?: string
  actions?: ReactNode
}>

export function Panel({ eyebrow, title, subtitle, actions, children }: PanelProps) {
  return (
    <section className="panel layout-cell" data-layout-region="panel">
      <div className="panel-header">
        <div className="panel-header-copy">
          {eyebrow ? <div className="panel-eyebrow">{eyebrow}</div> : null}
          <h2 className="panel-title">{title}</h2>
          {subtitle ? <p className="panel-subtitle">{subtitle}</p> : null}
        </div>
        {actions ? <div className="toolbar layout-toolbar">{actions}</div> : null}
      </div>
      {children}
    </section>
  )
}
