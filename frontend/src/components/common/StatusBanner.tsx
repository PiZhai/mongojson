import type { ToolStatus } from '../../shared/ui/toolStatus'

const classMap: Record<ToolStatus['kind'], string> = {
  idle: '',
  success: 'success',
  warning: 'warning',
  error: 'error',
}

type StatusBannerProps = {
  status: ToolStatus
  right?: string
}

export function StatusBanner({ status, right }: StatusBannerProps) {
  return (
    <div aria-live="polite" className="status-banner">
      <span className="status-inline">
        <span className={`status-dot ${classMap[status.kind]}`} />
        <span>{status.message}</span>
      </span>
      {right ? <span>{right}</span> : <span className="u-muted">Ready</span>}
    </div>
  )
}
