import type { ReactNode } from 'react'
import { dataLevels, helpText, isSensitiveLevel, metricHelp } from './model'

export function DataLevelSelect({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return (
    <label className="steward-field-with-help">
      <span>
        数据级别
        <HelpIcon text={helpText.dataLevel} />
      </span>
      <select
        className={isSensitiveLevel(value) ? 'steward-sensitive-select' : ''}
        onChange={(event) => onChange(event.target.value)}
        value={value}
      >
        {dataLevels.map(([level, label]) => (
          <option key={level} value={level}>
            {label}
          </option>
        ))}
      </select>
    </label>
  )
}

export function Panel({
  title,
  help,
  actions,
  children,
}: {
  title: string
  help?: string
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <section className="steward-panel">
      <div className="steward-panel-header">
        <h2>
          {title}
          {help ? <HelpIcon text={help} /> : null}
        </h2>
        {actions ? <div className="steward-panel-actions">{actions}</div> : null}
      </div>
      {children}
    </section>
  )
}

export function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="steward-metric">
      <span>
        {label}
        <HelpIcon text={metricHelp[label]} />
      </span>
      <strong>{value}</strong>
    </div>
  )
}

export function InfoTerm({ label, help }: { label: string; help: string }) {
  return (
    <strong className="steward-term-title">
      {label}
      <HelpIcon text={help} />
    </strong>
  )
}

export function HelpIcon({ text }: { text?: string }) {
  if (!text) {
    return null
  }
  return (
    <span aria-label={`说明：${text}`} className="steward-help" tabIndex={0}>
      ?
      <span className="steward-help-popover" role="tooltip">
        {text}
      </span>
    </span>
  )
}

export function EmptyState({ text }: { text: string }) {
  return <div className="steward-empty">{text}</div>
}
