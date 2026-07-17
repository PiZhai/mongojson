import { useCallback, useEffect, useState } from 'react'
import {
  getStewardExecutionControl,
  getStewardModelSettings,
  getStewardOverview,
  getStewardRuntimePlanner,
  probeStewardModelConnection,
} from '../../../lib/api/client'
import type {
  StewardAgentStatus,
  StewardModelSettings,
  StewardRuntimeExecutionControl,
  StewardRuntimePlannerStatus,
} from '../../../types/tooling'

type Props = {
  refreshToken: number
}

type StatusSnapshot = {
  agent: StewardAgentStatus
  model: StewardModelSettings
  planner: StewardRuntimePlannerStatus
  control: StewardRuntimeExecutionControl
}

export function StewardStatusBar({ refreshToken }: Props) {
  const [snapshot, setSnapshot] = useState<StatusSnapshot | null>(null)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')
  const [checkedAt, setCheckedAt] = useState<Date | null>(null)

  const refresh = useCallback(async (probeModel = false) => {
    setChecking(true)
    setError('')
    try {
      if (probeModel) {
        const { probe } = await probeStewardModelConnection()
        if (!probe.ok) throw new Error(probe.error || '模型连接检查失败')
      }
      const [overview, model, planner, control] = await Promise.all([
        getStewardOverview(),
        getStewardModelSettings(),
        getStewardRuntimePlanner(),
        getStewardExecutionControl(),
      ])
      setSnapshot({
        agent: overview.overview.agent,
        model: model.settings,
        planner: planner.planner,
        control: control.control,
      })
      setCheckedAt(new Date())
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '状态检查失败')
    } finally {
      setChecking(false)
    }
  }, [])

  useEffect(() => {
    void refresh(false)
  }, [refresh, refreshToken])

  useEffect(() => {
    const timer = window.setInterval(() => void refresh(false), 10_000)
    return () => window.clearInterval(timer)
  }, [refresh])

  const stopped = snapshot?.control.stopped ?? snapshot?.control.paused ?? false
  const broker = snapshot?.control.broker
  const brokerReady = Boolean(broker?.configured && broker.reachable && !broker.error)
  const watchdogReady = Boolean(snapshot?.control.watchdog.enabled && snapshot.control.watchdog.stale_invocations === 0)
  const activityLoop = snapshot?.agent.background_loops?.find((loop) => loop.name === 'activity-sample')
  const proactiveLoop = snapshot?.agent.background_loops?.find((loop) => loop.name === 'proactive')
  const activityEnabled = snapshot?.agent.enabled_collectors?.includes('windows-activity') ?? false

  return (
    <section className="steward-status-bar" aria-label="管家运行状态">
      <div className="steward-status-summary">
        <div className="steward-status-heading">
          <span className={`steward-status-presence ${error ? 'is-danger' : checking ? 'is-checking' : 'is-ready'}`} aria-hidden="true" />
          <div>
            <strong>{error ? '状态检查异常' : checking && !snapshot ? '正在检查管家状态' : '管家在线'}</strong>
            <small>{checkedAt ? `最近检查 ${checkedAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}` : '等待首次检查'}</small>
          </div>
        </div>

        <div className="steward-status-chips" aria-live="polite">
          <StatusChip ready={snapshot?.agent.status === 'running'} label="Agent" value={agentLabel(snapshot?.agent.status)} />
          <StatusChip ready={snapshot?.model.advisor.enabled === true} label="模型" value={snapshot?.model.model || '未配置'} />
          <StatusChip ready={snapshot?.planner.enabled === true} label="规划" value={snapshot?.planner.enabled ? '可用' : '不可用'} />
          <StatusChip ready={activityEnabled && activityLoop?.running === true} label="活动采集" value={activityEnabled ? activityLoop?.running ? '运行中' : '等待' : '未启用'} />
          <StatusChip ready={proactiveLoop?.running === true} label="主动管家" value={proactiveLoop?.running ? '运行中' : '未启用'} />
          <StatusChip ready={!stopped} label="执行" value={stopped ? '已停止' : '允许'} />
          <StatusChip ready={watchdogReady} label="Watchdog" value={watchdogReady ? '正常' : '需检查'} />
          <StatusChip ready={brokerReady} label="Broker" value={!broker?.configured ? '未配置' : brokerReady ? '正常' : '异常'} />
        </div>
      </div>

      <button
        className="steward-button steward-status-check"
        disabled={checking}
        onClick={() => void refresh(true)}
        type="button"
      >
        {checking ? '检查中…' : '全面检查'}
      </button>

      {error ? <div className="steward-status-error" role="alert">{error}</div> : null}
    </section>
  )
}

function StatusChip({ label, ready, value }: { label: string; ready: boolean; value: string }) {
  return (
    <span className={`steward-status-chip ${ready ? 'is-ready' : 'is-warning'}`}>
      <span aria-hidden="true" />
      <strong>{label}</strong>
      <small>{value}</small>
    </span>
  )
}

function agentLabel(status?: StewardAgentStatus['status']) {
  if (status === 'running') return '运行中'
  if (status === 'degraded') return '降级'
  if (status === 'starting') return '启动中'
  if (status === 'stopping') return '停止中'
  if (status === 'error') return '异常'
  return '已停止'
}
