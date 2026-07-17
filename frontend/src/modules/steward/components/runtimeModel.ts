import type { StewardAgentRun } from '../types'

const runtimeStatusLabels: Record<string, string> = {
  draft: '计划草稿',
  planning: '规划中',
  awaiting_approval: '等待审批',
  queued: '已入队',
  running: '执行中',
  verifying: '验证中',
  succeeded: '已完成',
  failed: '执行失败',
  cancelled: '已取消',
  compensating: '回滚中',
  blocked: '安全阻断',
  pending: '待执行',
}

export function runtimeStatusText(status: string) {
  return runtimeStatusLabels[status] ?? status
}

export function runtimeStatusTone(status: string) {
  if (status === 'succeeded') return 'success'
  if (status === 'failed' || status === 'blocked') return 'danger'
  if (status === 'awaiting_approval' || status === 'cancelled') return 'warning'
  if (status === 'running' || status === 'verifying' || status === 'queued') return 'active'
  return 'neutral'
}

export function runtimeRunIsTerminal(status: string) {
  return ['succeeded', 'failed', 'cancelled', 'blocked'].includes(status)
}

export function runtimeRunHasActiveApproval(run: StewardAgentRun) {
  return run.approvals.some(
    (approval) =>
      approval.status === 'active' &&
      approval.plan_hash === run.plan_hash &&
      (!approval.expires_at || new Date(approval.expires_at).getTime() > Date.now()),
  )
}

export function runtimeRunNeedsApproval(run: StewardAgentRun) {
  return run.steps.some((step) => step.requires_approval) && !runtimeRunHasActiveApproval(run)
}
