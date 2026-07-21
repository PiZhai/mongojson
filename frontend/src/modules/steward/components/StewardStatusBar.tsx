import { useCallback, useEffect, useState } from 'react'
import {
  getStewardBackgroundStatus,
  probeStewardModelConnection,
  stewardModelProbeError,
} from '../api'
import type { StewardBackgroundStatus } from '../types'
import { ToolLibraryDialog } from './ToolLibraryDialog'
import { NotificationCenterDialog } from './NotificationCenterDialog'
import { PersonalIntelligenceDialog } from './PersonalIntelligenceDialog'

type Props = {
  refreshToken: number
}

type StatusSnapshot = {
	background: StewardBackgroundStatus
}

export function StewardStatusBar({ refreshToken }: Props) {
  const [snapshot, setSnapshot] = useState<StatusSnapshot | null>(null)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')
  const [checkedAt, setCheckedAt] = useState<Date | null>(null)
  const [toolsOpen, setToolsOpen] = useState(false)
  const [notificationsOpen, setNotificationsOpen] = useState(false)
	const [intelligenceOpen, setIntelligenceOpen] = useState(false)

  const refresh = useCallback(async (probeModel = false) => {
    setChecking(true)
    setError('')
    try {
      if (probeModel) {
        const { probe } = await probeStewardModelConnection()
        if (!probe.ok) throw new Error(stewardModelProbeError(probe))
      }
		const background = await getStewardBackgroundStatus()
		setSnapshot({ background: background.status })
      setCheckedAt(new Date())
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '状态检查失败')
    } finally {
      setChecking(false)
    }
  }, [])

  useEffect(() => {
    const timer = window.setTimeout(() => void refresh(false), 0)
    return () => window.clearTimeout(timer)
  }, [refresh, refreshToken])

  useEffect(() => {
    const timer = window.setInterval(() => void refresh(false), 10_000)
    return () => window.clearInterval(timer)
  }, [refresh])

	const background = snapshot?.background
	const sourceCount = background?.pipeline.sources.length ?? 0
	const freshSourceCount = background?.pipeline.sources.filter((source) => source.fresh).length ?? 0
	const activityReady = Boolean(background?.pipeline.enabled && sourceCount > 0 && sourceCount === freshSourceCount)
	const modelFailures = background?.model.consecutive_failures ?? 0
	const modelReady = background?.model.enabled === true && !background?.model.circuit_open && modelFailures === 0
	const modelLabel = background?.model.circuit_open
    ? '已熔断'
    : modelFailures > 0
      ? `连续失败 ${modelFailures} 次`
			: background?.model.model || '未配置'

  return (
    <section className="steward-status-bar" aria-label="管家运行状态">
      <div className="steward-status-summary">
        <div className="steward-status-heading">
			<span className={`steward-status-presence ${error || background?.state === 'unhealthy' ? 'is-danger' : checking ? 'is-checking' : background?.state === 'healthy' ? 'is-ready' : ''}`} aria-hidden="true" />
          <div>
			<strong>{error ? '状态检查异常' : checking && !snapshot ? '正在检查管家状态' : backgroundStateLabel(background?.state)}</strong>
            <small>{checkedAt ? `最近检查 ${checkedAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}` : '等待首次检查'}</small>
          </div>
        </div>

        <div className="steward-status-chips" aria-live="polite">
			<StatusChip ready={modelReady} label="模型" title={background?.model.last_error} value={modelLabel} />
			<StatusChip ready={activityReady} label="自动采集" value={!background?.pipeline.enabled ? '未启用' : sourceCount === 0 ? '等待数据' : '运行中'} />
        </div>
      </div>

      <div className="steward-status-actions">
		<button className="steward-button steward-button-secondary steward-status-check" onClick={() => setIntelligenceOpen(true)} type="button">个人智能</button>
        <button className="steward-button steward-button-secondary steward-status-check" onClick={() => setNotificationsOpen(true)} type="button">通知</button>
        <button className="steward-button steward-button-secondary steward-status-check" onClick={() => setToolsOpen(true)} type="button">工具库</button>
        <button className="steward-button steward-status-check" disabled={checking} onClick={() => void refresh(true)} type="button">{checking ? '检查中…' : '检查连接'}</button>
      </div>

      {error ? <div className="steward-status-error" role="alert">{error}</div> : null}
      <ToolLibraryDialog onClose={() => setToolsOpen(false)} open={toolsOpen} />
      <NotificationCenterDialog onClose={() => setNotificationsOpen(false)} open={notificationsOpen} />
		<PersonalIntelligenceDialog onClose={() => setIntelligenceOpen(false)} open={intelligenceOpen} />
    </section>
  )
}

function StatusChip({ label, ready, title, value }: { label: string; ready: boolean; title?: string; value: string }) {
  return (
    <span className={`steward-status-chip ${ready ? 'is-ready' : 'is-warning'}`} title={title}>
      <span aria-hidden="true" />
      <strong>{label}</strong>
      <small>{value}</small>
    </span>
  )
}

function backgroundStateLabel(state?: StewardBackgroundStatus['state']) {
	if (state === 'healthy') return '管家在线'
	if (state === 'degraded') return '管家需要检查'
	if (state === 'unhealthy') return '管家离线'
	if (state === 'disabled') return '自动采集已关闭'
	return '管家在线'
}
