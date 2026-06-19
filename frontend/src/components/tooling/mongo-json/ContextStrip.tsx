import type { ReactNode } from 'react'
import type { ContextTrail } from './types'

type ContextStripProps = {
  trail: ContextTrail
  actions?: ReactNode
}

export function ContextStrip({ trail, actions }: ContextStripProps) {
  return (
    <section className="context-strip" aria-label="当前上下文">
      <div className="context-strip-copy">
        <p className="context-strip-label">Current Context</p>
        <div className="context-breadcrumb" role="list">
          {trail.crumb.map((item, index) => (
            <span className="context-breadcrumb-item" key={`${item}-${index}`} role="listitem">
              {index > 0 ? <span className="context-breadcrumb-separator">/</span> : null}
              <span>{item}</span>
            </span>
          ))}
        </div>
        <p className="context-strip-text">{trail.helper}</p>
      </div>
      {actions ? <div className="context-strip-actions">{actions}</div> : null}
    </section>
  )
}
