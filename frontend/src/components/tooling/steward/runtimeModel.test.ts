import { describe, expect, it } from 'vitest'
import type { StewardAgentRun } from '../../../types/tooling'
import {
  runtimeRunHasActiveApproval,
  runtimeRunIsTerminal,
  runtimeRunNeedsApproval,
  runtimeStatusText,
  runtimeStatusTone,
} from './runtimeModel'

function runFixture(): StewardAgentRun {
  return {
    id: 'run-1',
    goal: 'test',
    status: 'draft',
    mode: 'planned',
    plan_version: 1,
    plan_hash: 'hash-1',
    requested_by: 'local-user',
    target_device: 'local',
    data_level: 'D2',
    permission_ceiling: 'A3',
    planner: 'local-rules',
    planner_version: '2.0.0',
    policy_summary: {},
    cancel_requested: false,
    steps: [{
      id: 'step-1', run_id: 'run-1', key: 'step', position: 0, title: 'step', tool_name: 'runtime.echo',
      tool_version: '1.0.0', arguments: {}, depends_on: [], status: 'pending', attempt: 0, max_attempts: 1,
      timeout_seconds: 10, idempotency_key: 'key', tool_idempotency: 'inherent', policy_decision: 'approve',
      policy_reason: 'test', requires_approval: true, invocations: [], evidence: [], created_at: '', updated_at: '',
    }],
    approvals: [],
    created_at: '',
    updated_at: '',
  }
}

describe('runtime control presentation', () => {
  it('labels and classifies durable run states', () => {
    expect(runtimeStatusText('awaiting_approval')).toBe('等待审批')
    expect(runtimeStatusTone('blocked')).toBe('danger')
    expect(runtimeRunIsTerminal('blocked')).toBe(true)
    expect(runtimeRunIsTerminal('queued')).toBe(false)
  })

  it('binds approval state to the immutable plan hash', () => {
    const run = runFixture()
    expect(runtimeRunNeedsApproval(run)).toBe(true)
    run.approvals.push({
      id: 'approval-1', run_id: run.id, plan_hash: 'other-hash', scope: 'run', granted_by: 'local-user',
      status: 'active', created_at: '',
    })
    expect(runtimeRunHasActiveApproval(run)).toBe(false)
    run.approvals[0].plan_hash = run.plan_hash
    expect(runtimeRunHasActiveApproval(run)).toBe(true)
    expect(runtimeRunNeedsApproval(run)).toBe(false)
  })
})
