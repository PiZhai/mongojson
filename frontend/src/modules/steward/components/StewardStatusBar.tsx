import { useCallback, useEffect, useState } from 'react'
import {
  getStewardBackgroundStatus,
  getStewardExecutionControl,
  getStewardRuntimePlanner,
  probeStewardModelConnection,
  stewardModelProbeError,
} from '../api'
import type {
  StewardBackgroundStatus,
  StewardRuntimeExecutionControl,
  StewardRuntimePlannerStatus,
} from '../types'
import { ToolLibraryDialog } from './ToolLibraryDialog'
import { NotificationCenterDialog } from './NotificationCenterDialog'
import { PersonalIntelligenceDialog } from './PersonalIntelligenceDialog'
import { findPersonalIntelligenceLoop } from './personalIntelligencePresentation'

type Props = {
  refreshToken: number
}

type StatusSnapshot = {
	background: StewardBackgroundStatus
	planner?: StewardRuntimePlannerStatus
	control?: StewardRuntimeExecutionControl
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
		if (probeModel) {
			const [planner, control] = await Promise.all([getStewardRuntimePlanner(), getStewardExecutionControl()])
			setSnapshot({ background: background.status, planner: planner.planner, control: control.control })
		} else {
			setSnapshot((current) => ({ background: background.status, planner: current?.planner, control: current?.control }))
		}
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
	const stopped = snapshot?.control?.stopped ?? snapshot?.control?.paused ?? false
	const broker = snapshot?.control?.broker
  const brokerReady = Boolean(broker?.configured && broker.reachable && !broker.error)
	const watchdogReady = Boolean(snapshot?.control?.watchdog.enabled && snapshot.control.watchdog.stale_invocations === 0)
	const proactiveLoop = findPersonalIntelligenceLoop(background?.loops)
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
			<StatusChip ready={background?.agent.status === 'running'} label="Agent" value={agentLabel(background?.agent.status)} />
			<StatusChip ready={modelReady} label="模型" title={background?.model.last_error} value={modelLabel} />
			<StatusChip ready={snapshot?.planner?.enabled === true} label="规划" value={snapshot?.planner ? snapshot.planner.enabled ? '可用' : '不可用' : '全面检查'} />
			<StatusChip ready={activityReady} label="活动采集" value={!background?.pipeline.enabled ? '未启用' : sourceCount === 0 ? '无心跳' : `${freshSourceCount}/${sourceCount} 新鲜`} />
			<StatusChip ready={Boolean(proactiveLoop?.enabled && proactiveLoop.consecutive_failures === 0)} label="主动管家" value={!proactiveLoop?.enabled ? '未启用' : proactiveLoop.consecutive_failures > 0 ? `失败 ${proactiveLoop.consecutive_failures} 次` : '循环正常'} />
          <StatusChip ready={!stopped} label="执行" value={stopped ? '已停止' : '允许'} />
			<StatusChip ready={watchdogReady} label="Watchdog" value={!snapshot?.control ? '全面检查' : watchdogReady ? '正常' : '需检查'} />
			<StatusChip ready={brokerReady} label="Broker" value={!snapshot?.control ? '全面检查' : !broker?.configured ? '未配置' : brokerReady ? '正常' : '异常'} />
        </div>
      </div>

      <div className="steward-status-actions">
		<button className="steward-button steward-button-secondary steward-status-check" onClick={() => setIntelligenceOpen(true)} type="button">个人智能</button>
        <button className="steward-button steward-button-secondary steward-status-check" onClick={() => setNotificationsOpen(true)} type="button">通知</button>
        <button className="steward-button steward-button-secondary steward-status-check" onClick={() => setToolsOpen(true)} type="button">工具库</button>
        <button className="steward-button steward-status-check" disabled={checking} onClick={() => void refresh(true)} type="button">{checking ? '检查中…' : '全面检查'}</button>
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

function agentLabel(status?: StewardBackgroundStatus['agent']['status']) {
  if (status === 'running') return '运行中'
  if (status === 'degraded') return '降级'
  if (status === 'starting') return '启动中'
  if (status === 'stopping') return '停止中'
  if (status === 'error') return '异常'
  return '已停止'
}

function backgroundStateLabel(state?: StewardBackgroundStatus['state']) {
	if (state === 'healthy') return '管家后台正常'
	if (state === 'degraded') return '管家部分降级'
	if (state === 'unhealthy') return '管家后台异常'
	if (state === 'disabled') return '持续智能已关闭'
	return '管家在线'
}
