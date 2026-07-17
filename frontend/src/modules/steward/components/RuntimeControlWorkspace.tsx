import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import {
  approveStewardAgentRun,
  getStewardAgentRun,
  getStewardAgentRunEvidence,
  getStewardAgentRuns,
  getStewardExecutionControl,
  getStewardRuntimePlanner,
  planStewardAgentRun,
  setStewardExecutionStopped,
  streamStewardAgentRunEvents,
  transitionStewardAgentRun,
} from '../api'
import type {
  StewardAgentRun,
  StewardAgentRunSummary,
  StewardEvidenceArtifact,
  StewardRunEvent,
  StewardRuntimeExecutionControl,
  StewardRuntimePlannerStatus,
  StewardSignedApprovalProof,
} from '../types'
import { formatDate } from './model'
import {
  runtimeRunHasActiveApproval,
  runtimeRunNeedsApproval,
  runtimeStatusText,
  runtimeStatusTone,
} from './runtimeModel'
import {
  authorityMatchesOrigin,
  createWebAuthnPolicyAuthority,
  issueWebAuthnApprovalProof,
} from './webauthnApproval'

const RUN_FILTERS = [
  ['', '全部状态'],
  ['draft', '计划草稿'],
  ['awaiting_approval', '等待审批'],
  ['queued', '已入队'],
  ['running', '执行中'],
  ['succeeded', '已完成'],
  ['failed', '执行失败'],
  ['blocked', '安全阻断'],
] as const

function errorMessage(error: unknown) {
  if (error instanceof Error) return error.message
  return String(error)
}

function prettyJSON(value: unknown) {
  return JSON.stringify(value ?? {}, null, 2)
}

export function RuntimeControlWorkspace() {
  const [planner, setPlanner] = useState<StewardRuntimePlannerStatus | null>(null)
  const [control, setControl] = useState<StewardRuntimeExecutionControl | null>(null)
  const [runs, setRuns] = useState<StewardAgentRunSummary[]>([])
  const [selectedRun, setSelectedRun] = useState<StewardAgentRun | null>(null)
  const [events, setEvents] = useState<StewardRunEvent[]>([])
  const [instruction, setInstruction] = useState('')
  const [dataLevel, setDataLevel] = useState('D2')
  const [permissionCeiling, setPermissionCeiling] = useState('A3')
  const [statusFilter, setStatusFilter] = useState('')
  const [approvalReason, setApprovalReason] = useState('')
  const [approvalProof, setApprovalProof] = useState('')
  const [webAuthnRegistration, setWebAuthnRegistration] = useState('')
  const [controlReason, setControlReason] = useState('')
  const [confirmControl, setConfirmControl] = useState(false)
  const [evidenceDetails, setEvidenceDetails] = useState<Record<string, StewardEvidenceArtifact>>({})
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const [streaming, setStreaming] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const lastEventSequence = useRef(0)

  const refreshRuns = useCallback(async (filter = statusFilter) => {
    const response = await getStewardAgentRuns(40, filter)
    setRuns(response.runs)
    return response.runs
  }, [statusFilter])

  const refreshRun = useCallback(async (runId: string) => {
    const response = await getStewardAgentRun(runId)
    setSelectedRun((current) => (current?.id === runId ? response.run : current))
    return response.run
  }, [])

  useEffect(() => {
    let active = true
    void Promise.all([
      getStewardRuntimePlanner(),
      getStewardExecutionControl(),
      getStewardAgentRuns(40, statusFilter),
    ])
      .then(async ([plannerResponse, controlResponse, runResponse]) => {
        if (!active) return
        setPlanner(plannerResponse.planner)
        setControl(controlResponse.control)
        setRuns(runResponse.runs)
        if (runResponse.runs.length > 0) {
          const detail = await getStewardAgentRun(runResponse.runs[0].id)
          if (!active) return
          setEvents([])
          setStreaming(false)
          setSelectedRun(detail.run)
        } else {
          setSelectedRun(null)
        }
      })
      .catch((caught) => {
        if (active) setError(errorMessage(caught))
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [statusFilter])

  useEffect(() => {
    let active = true
    const timer = window.setInterval(() => {
      void getStewardExecutionControl()
        .then((response) => {
          if (active) setControl(response.control)
        })
        .catch(() => {
          // The main load and explicit controls surface actionable errors;
          // background refresh stays quiet during transient restarts.
        })
    }, 2000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    const runId = selectedRun?.id
    if (!runId) return
    const controller = new AbortController()
    lastEventSequence.current = 0
    void streamStewardAgentRunEvents(
      runId,
      0,
      (event) => {
        lastEventSequence.current = Math.max(lastEventSequence.current, event.sequence)
        setEvents((current) => {
          if (current.some((item) => item.sequence === event.sequence)) return current
          return [...current, event].slice(-100)
        })
        void refreshRun(runId).catch((caught) => setError(errorMessage(caught)))
        void refreshRuns().catch((caught) => setError(errorMessage(caught)))
      },
      controller.signal,
      () => setStreaming(true),
    )
      .catch((caught) => {
        if (!controller.signal.aborted) setError(`实时状态连接中断：${errorMessage(caught)}`)
      })
      .finally(() => {
        if (!controller.signal.aborted) setStreaming(false)
      })
    return () => controller.abort()
  }, [selectedRun?.id, refreshRun, refreshRuns])

  async function selectRun(runId: string) {
    setBusy(`select:${runId}`)
    setError(null)
    try {
      const response = await getStewardAgentRun(runId)
      setEvents([])
      setStreaming(false)
      setSelectedRun(response.run)
      setEvidenceDetails({})
      setApprovalReason('')
      setApprovalProof('')
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(null)
    }
  }

  async function submitPlan(event: FormEvent) {
    event.preventDefault()
    if (!instruction.trim()) return
    setBusy('plan')
    setError(null)
    try {
      const response = await planStewardAgentRun({
        instruction: instruction.trim(),
        data_level: dataLevel,
        permission_ceiling: permissionCeiling,
        auto_start: false,
      })
      setEvents([])
      setStreaming(false)
      setSelectedRun(response.run)
      setEvidenceDetails({})
      setInstruction('')
      await refreshRuns()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(null)
    }
  }

  async function runTransition(action: 'start' | 'cancel' | 'resume') {
    if (!selectedRun) return
    setBusy(action)
    setError(null)
    try {
      const response = await transitionStewardAgentRun(selectedRun.id, action)
      setSelectedRun(response.run)
      await refreshRuns()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(null)
    }
  }

  async function approveRun() {
    if (!selectedRun || !approvalReason.trim()) return
    setBusy('approve')
    setError(null)
    try {
      const privileged = selectedRun.steps.some((step) => step.tool_name === 'privilege.execute')
      let parsedProof: StewardSignedApprovalProof | undefined
      if (privileged) {
        parsedProof = JSON.parse(approvalProof) as StewardSignedApprovalProof
      }
      const response = await approveStewardAgentRun(selectedRun.id, selectedRun.plan_hash, approvalReason.trim(), parsedProof)
      setSelectedRun(response.run)
      setApprovalReason('')
      setApprovalProof('')
      await refreshRuns()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(null)
    }
  }

  async function issueWebAuthnProof() {
    if (!selectedRun || !selectedCapability || !approvalReason.trim() || !webAuthnAuthority || !control) return
    setBusy('webauthn-approve')
    setError(null)
    try {
      const proof = await issueWebAuthnApprovalProof({
        authority: webAuthnAuthority,
        subject: `runtime:${selectedRun.id}`,
        planHash: selectedRun.plan_hash,
        capability: selectedCapability,
        controlGeneration: control.generation,
        grantedBy: 'local-user',
        reason: approvalReason,
        origin: window.location.origin,
      })
      setApprovalProof(prettyJSON(proof))
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(null)
    }
  }

  async function registerWebAuthnAuthority() {
    setBusy('webauthn-register')
    setError(null)
    try {
      const authority = await createWebAuthnPolicyAuthority({
        name: 'local-platform-authenticator',
        origin: window.location.origin,
      })
      setWebAuthnRegistration(prettyJSON(authority))
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(null)
    }
  }

  async function copyWebAuthnRegistration() {
    if (!webAuthnRegistration) return
    try {
      await navigator.clipboard.writeText(webAuthnRegistration)
    } catch (caught) {
      setError(errorMessage(caught))
    }
  }

  async function applyGlobalControl() {
    const stopped = !(control?.stopped ?? control?.paused)
    if (!controlReason.trim()) return
    setBusy('control')
    setError(null)
    try {
      const response = await setStewardExecutionStopped(stopped, controlReason.trim())
      setControl(response.control)
      setControlReason('')
      setConfirmControl(false)
      await refreshRuns()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(null)
    }
  }

  async function revealEvidence(evidence: StewardEvidenceArtifact) {
    if (!selectedRun || !evidence.payload_available) return
    setBusy(`evidence:${evidence.id}`)
    setError(null)
    try {
      const response = await getStewardAgentRunEvidence(selectedRun.id, evidence.id)
      setEvidenceDetails((current) => ({ ...current, [evidence.id]: response.evidence }))
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(null)
    }
  }

  const activeApproval = selectedRun ? runtimeRunHasActiveApproval(selectedRun) : false
  const needsApproval = selectedRun ? runtimeRunNeedsApproval(selectedRun) : false
  const canCancel = selectedRun && ['draft', 'awaiting_approval', 'queued', 'running', 'verifying'].includes(selectedRun.status)
  const canResume = selectedRun && ['failed', 'cancelled', 'blocked'].includes(selectedRun.status)
  const executionStopped = control?.stopped ?? control?.paused ?? false
  const broker = control?.broker
  const brokerPreparedForResume = Boolean(
    executionStopped && broker?.configured && broker.reachable && !broker.stopped &&
    broker.generation === (control?.generation ?? -1) + 1,
  )
  const brokerHealthy = Boolean(
    broker?.configured && broker.reachable && !broker.error &&
    broker.stopped === executionStopped && broker.generation === control?.generation,
  ) || brokerPreparedForResume
  const brokerStatus = !broker?.configured
    ? '未配置'
    : !broker.reachable
      ? '不可达'
      : broker.error
        ? '状态不一致'
        : brokerPreparedForResume
          ? `独立恢复已授权 · 代际 ${broker.generation}`
          : broker.stopped
          ? `已停止 · 代际 ${broker.generation}`
          : `${broker.capability_count} 项能力 · ${broker.active_executions} 项执行中`
  const permissionOptions = broker?.configured
    ? ['A0', 'A1', 'A2', 'A3', 'A4', 'A5', 'A6', 'A7']
    : ['A0', 'A1', 'A2', 'A3']
  const exampleCapability = broker?.capabilities?.[0]?.name
  const selectedPrivilegeStep = selectedRun?.steps.find((step) => step.tool_name === 'privilege.execute')
  const selectedCapability = typeof selectedPrivilegeStep?.arguments.capability === 'string' ? selectedPrivilegeStep.arguments.capability : ''
  const currentOrigin = typeof window === 'undefined' ? '' : window.location.origin
  const webAuthnAuthority = broker?.approval_authorities.find((authority) => authorityMatchesOrigin(authority, currentOrigin))

  return (
    <section aria-labelledby="runtime-control-title" className="steward-runtime-control">
      <header className="steward-runtime-header">
        <div>
          <p className="steward-eyebrow">R3.2 WebAuthn Approval Plane</p>
          <h2 id="runtime-control-title">执行控制面</h2>
          <p>统一紧急停止同时门控 Runtime V2、S4 与独立 Privilege Broker；高权限动作必须同时持有独立审批票据与签名能力令牌。</p>
        </div>
        <div className="steward-runtime-health" aria-live="polite">
          <span className={`steward-runtime-chip ${planner?.enabled ? 'is-ready' : 'is-danger'}`}>
            规划器：{planner?.enabled ? `${planner.provider} ${planner.version}` : planner?.reason || '不可用'}
          </span>
          <span className={`steward-runtime-chip ${executionStopped ? 'is-danger' : 'is-ready'}`}>
            执行安全层：{executionStopped ? (control?.draining ? '已停止，正在回收' : '已紧急停止') : '允许执行'}
          </span>
          <span className={`steward-runtime-chip ${control?.watchdog.stale_invocations ? 'is-danger' : 'is-ready'}`}>
            Watchdog：{control ? `${control.watchdog.active_invocations} 活跃 / ${control.watchdog.stale_invocations} 过期` : '加载中'}
          </span>
          <span className={`steward-runtime-chip ${brokerHealthy ? 'is-ready' : 'is-danger'}`} title={broker?.error || broker?.policy_digest || ''}>
            Broker：{brokerStatus}
          </span>
          <button
            className={`steward-button ${executionStopped ? '' : 'steward-runtime-stop-button'}`}
            disabled={busy !== null || !control}
            onClick={() => setConfirmControl(true)}
            type="button"
          >
            {executionStopped ? '恢复统一执行' : '统一紧急停止'}
          </button>
        </div>
      </header>

      {error ? <div className="steward-runtime-error" role="alert">{error}</div> : null}
      {broker?.configured && broker.error ? <div className="steward-runtime-error" role="alert">Privilege Broker：{broker.error}</div> : null}

      {confirmControl ? (
        <div className={`steward-runtime-confirm ${executionStopped ? 'is-resume' : 'is-pause'}`} role="group" aria-label="确认统一执行控制">
          <div>
            <strong>{executionStopped ? '恢复 Runtime V2、S4 与 Privilege Broker' : '紧急停止 Runtime V2、S4 与 Privilege Broker'}</strong>
            <p>
              {executionStopped
                ? 'R3.3 要求独立管理员先恢复 Broker 到下一代际，工作台验证签名状态后才恢复 Runtime V2/S4。watchdog 已阻断的未知非幂等结果仍需人工恢复和重新审批。'
                : '立即拒绝新执行、撤销未使用令牌、由 Broker 取消高权限进程树，并等待 S4 在途动作退出安全屏障；任务记录、对话、记忆与同步仍继续工作。'}
            </p>
          </div>
          {executionStopped && broker?.configured ? (
            <div className="steward-runtime-code-grid">
              <div>
                <span>{brokerPreparedForResume ? 'Broker 已由独立控制身份恢复，可以提交本地恢复' : '先在受保护的管理员终端执行'}</span>
                <pre>{`$env:STEWARD_BROKER_CONTROL_KEY = '<受保护的独立 control key>'\nsteward-broker control resume --generation ${(control?.generation ?? 0) + 1} --reason '<恢复原因>' --changed-by 'local-admin'\nRemove-Item Env:STEWARD_BROKER_CONTROL_KEY`}</pre>
              </div>
            </div>
          ) : null}
          <label>
            <span>操作原因（写入审计）</span>
            <input
              autoFocus
              onChange={(event) => setControlReason(event.target.value)}
              placeholder={executionStopped ? '例如：检查完成，恢复受控执行' : '例如：发现异常输出，需要立即隔离执行'}
              value={controlReason}
            />
          </label>
          <div className="steward-runtime-inline-actions">
            <button className="steward-button steward-button-secondary" onClick={() => setConfirmControl(false)} type="button">取消</button>
            <button
              className={`steward-button ${executionStopped ? '' : 'steward-runtime-stop-button'}`}
              disabled={!controlReason.trim() || busy !== null || (executionStopped && Boolean(broker?.configured) && !brokerPreparedForResume)}
              onClick={() => void applyGlobalControl()}
              type="button"
            >
              确认{executionStopped ? '恢复统一执行' : '紧急停止'}
            </button>
          </div>
        </div>
      ) : null}

      {control ? (
        <details className="steward-runtime-control-audit">
          <summary>
            最近统一控制 · {executionStopped ? '当前停止' : '当前运行'} · 代际 {control.generation} · {formatDate(control.changed_at)}
          </summary>
          <div className="steward-runtime-control-current">
            <strong>{control.reason || '未填写原因'}</strong>
            <small>{control.changed_by} · 作用域 {control.scopes.join(' + ')} · 租约 {control.watchdog.lease_ttl_seconds}s</small>
            {broker?.configured ? <small>Broker {broker.key_id || '未知密钥'} · 策略 {broker.policy_digest?.slice(0, 12) || '未知'} · 实例 {broker.instance_id?.slice(0, 12) || '不可达'}</small> : null}
			{broker?.approval_proof_required ? <small>独立审批机构：{broker.approval_authorities.map((authority) => `${authority.name} (${authority.key_id})`).join('、')}</small> : null}
            <div className="steward-runtime-inline-actions">
              <button className="steward-button steward-button-secondary" disabled={busy !== null} onClick={() => void registerWebAuthnAuthority()} type="button">
                生成 WebAuthn 登记材料
              </button>
              {webAuthnRegistration ? <button className="steward-button steward-button-secondary" onClick={() => void copyWebAuthnRegistration()} type="button">复制登记材料</button> : null}
            </div>
            {webAuthnRegistration ? (
              <div className="steward-runtime-code-grid">
                <div>
                  <span>由管理员审核后加入 Broker policy 的 approval_authorities（不会自动写入）</span>
                  <pre>{webAuthnRegistration}</pre>
                </div>
              </div>
            ) : null}
          </div>
          {(broker?.capabilities ?? []).map((capability) => (
            <div className="steward-runtime-control-event" key={capability.name}>
              <strong>{capability.name}</strong>
              <span>{capability.description || capability.executable_name}</span>
              <small>{capability.permission_level} · {capability.risk_level} · {capability.argument_count} 个固定参数 · {capability.timeout_seconds}s</small>
            </div>
          ))}
          {control.events.length === 0 ? <p>尚无统一停止或恢复记录。</p> : control.events.map((event) => (
            <div className="steward-runtime-control-event" key={event.sequence}>
              <strong>{event.action === 'resumed' ? '恢复' : '紧急停止'}</strong>
              <span>{event.reason || '未填写原因'}</span>
              <small>{event.changed_by} · {formatDate(event.created_at)}</small>
            </div>
          ))}
        </details>
      ) : null}

      <form className="steward-runtime-plan-form" onSubmit={submitPlan}>
        <label className="steward-runtime-instruction">
          <span>要交给本机管家的任务</span>
          <textarea
            disabled={!planner?.enabled || busy !== null}
            onChange={(event) => setInstruction(event.target.value)}
            placeholder={`例如：读取文件 "C:\\work\\notes.txt"\n例如：创建文件 "C:\\work\\result.txt" 内容 "已完成"${exampleCapability ? `\n例如：执行高权限能力 ${exampleCapability}` : ''}`}
            rows={3}
            value={instruction}
          />
          <small>生成计划不会立即操作系统；高权限能力必须填写 Broker 显示的精确 tool: 标识，并经过计划绑定审批。</small>
        </label>
        <label>
          <span>数据级别</span>
          <select onChange={(event) => setDataLevel(event.target.value)} value={dataLevel}>
            {['D0', 'D1', 'D2', 'D3', 'D4', 'D5', 'D6'].map((level) => <option key={level}>{level}</option>)}
          </select>
        </label>
        <label>
          <span>权限上限</span>
          <select onChange={(event) => setPermissionCeiling(event.target.value)} value={permissionCeiling}>
            {permissionOptions.map((level) => <option key={level}>{level}</option>)}
          </select>
        </label>
        <button className="steward-button" disabled={!instruction.trim() || !planner?.enabled || busy !== null} type="submit">
          {busy === 'plan' ? '正在编译计划…' : '生成计划预览'}
        </button>
      </form>

      {loading ? (
        <div className="steward-runtime-loading" role="status">正在加载执行控制面…</div>
      ) : (
        <div className="steward-runtime-layout">
          <aside className="steward-runtime-run-list" aria-label="最近执行计划">
            <div className="steward-runtime-list-header">
              <div>
                <strong>最近计划</strong>
                <small>{runs.length} 条</small>
              </div>
              <select aria-label="按状态筛选" onChange={(event) => setStatusFilter(event.target.value)} value={statusFilter}>
                {RUN_FILTERS.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
              </select>
            </div>
            <div className="steward-runtime-run-scroll">
              {runs.length === 0 ? <p className="steward-empty">还没有匹配的执行计划。</p> : runs.map((run) => (
                <button
                  aria-current={selectedRun?.id === run.id ? 'true' : undefined}
                  className="steward-runtime-run-item"
                  disabled={busy === `select:${run.id}`}
                  key={run.id}
                  onClick={() => void selectRun(run.id)}
                  type="button"
                >
                  <span className={`steward-runtime-status is-${runtimeStatusTone(run.status)}`}>{runtimeStatusText(run.status)}</span>
                  <strong>{run.goal}</strong>
                  <small>{run.completed_steps}/{run.step_count} 步 · {run.permission_ceiling} · {formatDate(run.updated_at)}</small>
                  {run.failure_summary ? <em>{run.failure_summary}</em> : null}
                </button>
              ))}
            </div>
          </aside>

          <div className="steward-runtime-detail">
            {!selectedRun ? (
              <div className="steward-empty">生成计划或从左侧选择一条记录，查看执行详情。</div>
            ) : (
              <>
                <div className="steward-runtime-detail-header">
                  <div>
                    <span className={`steward-runtime-status is-${runtimeStatusTone(selectedRun.status)}`}>{runtimeStatusText(selectedRun.status)}</span>
                    <h3>{selectedRun.goal}</h3>
                    <p>{selectedRun.plan_summary || selectedRun.source_instruction || '手工结构化计划'}</p>
                  </div>
                  <div className="steward-runtime-inline-actions">
                    {selectedRun.status === 'draft' && (!needsApproval || activeApproval) ? (
                      <button className="steward-button" disabled={busy !== null} onClick={() => void runTransition('start')} type="button">启动执行</button>
                    ) : null}
                    {canCancel ? (
                      <button className="steward-button steward-button-secondary steward-danger" disabled={busy !== null} onClick={() => void runTransition('cancel')} type="button">取消任务</button>
                    ) : null}
                    {canResume ? (
                      <button className="steward-button" disabled={busy !== null} onClick={() => void runTransition('resume')} type="button">恢复任务</button>
                    ) : null}
                  </div>
                </div>

                <dl className="steward-runtime-metadata">
                  <div><dt>计划哈希</dt><dd title={selectedRun.plan_hash}>{selectedRun.plan_hash.slice(0, 16)}…</dd></div>
                  <div><dt>规划器</dt><dd>{selectedRun.planner} {selectedRun.planner_version}</dd></div>
                  <div><dt>安全边界</dt><dd>{selectedRun.permission_ceiling} / {selectedRun.data_level}</dd></div>
                  <div><dt>实时连接</dt><dd>{streaming ? '已连接' : '已结束或未连接'}</dd></div>
                </dl>

                {needsApproval && ['draft', 'awaiting_approval'].includes(selectedRun.status) ? (
                  <div className="steward-runtime-approval">
                    <div>
                      <strong>{selectedPrivilegeStep ? '需要独立签名审批' : '需要人工审批'}</strong>
                      <p>{selectedPrivilegeStep ? '高权限票据由独立 Approval Authority 签发；主服务只能转发，无法自行伪造。' : '审批只对当前计划哈希有效；任何计划变化都会使旧审批失效。'}</p>
                    </div>
                    <label>
                      <span>审批理由</span>
                      <textarea
                        onChange={(event) => setApprovalReason(event.target.value)}
                        placeholder="说明为什么允许这组操作及其影响范围"
                        rows={2}
                        value={approvalReason}
                      />
                    </label>
					{selectedPrivilegeStep ? (
					  <>
					    <div className="steward-runtime-code-grid">
					      <div>
					        <span>在隔离的审批终端签发（理由必须与上方完全一致）</span>
					        <pre>{`steward-approval issue --approve --subject "runtime:${selectedRun.id}" --plan-hash "${selectedRun.plan_hash}" --capability "${selectedCapability}" --generation ${control?.generation ?? 0} --granted-by "local-user" --reason "<审批理由>"`}</pre>
					      </div>
					    </div>
					    <label>
					      <span>签名审批票据 JSON</span>
					      <textarea onChange={(event) => setApprovalProof(event.target.value)} placeholder="使用下方 WebAuthn 按钮生成，或粘贴 steward-approval issue 输出" rows={8} value={approvalProof} />
					    </label>
					    {webAuthnAuthority ? (
                          <button className="steward-button steward-button-secondary" disabled={!approvalReason.trim() || busy !== null || !control} onClick={() => void issueWebAuthnProof()} type="button">
                            使用 Windows Hello/安全密钥审批
                          </button>
                        ) : null}
					  </>
					) : null}
                    <button className="steward-button" disabled={!approvalReason.trim() || (Boolean(selectedPrivilegeStep) && !approvalProof.trim()) || busy !== null} onClick={() => void approveRun()} type="button">
                      批准当前计划
                    </button>
                  </div>
                ) : null}

                {selectedRun.failure_summary ? <div className="steward-runtime-failure" role="status"><strong>停止原因</strong><p>{selectedRun.failure_summary}</p></div> : null}

                <div className="steward-runtime-steps">
                  {selectedRun.steps.map((step, index) => (
                    <article className="steward-runtime-step" key={step.id}>
                      <div className="steward-runtime-step-head">
                        <span>{String(index + 1).padStart(2, '0')}</span>
                        <div>
                          <strong>{step.title}</strong>
                          <small>{step.tool_name}@{step.tool_version}</small>
                        </div>
                        <span className={`steward-runtime-status is-${runtimeStatusTone(step.status)}`}>{runtimeStatusText(step.status)}</span>
                      </div>
                      <div className="steward-runtime-policy-grid">
                        <div><span>权限策略</span><strong>{step.policy_decision} · {step.requires_approval ? '需审批' : '无需审批'}</strong></div>
                        <div><span>幂等性</span><strong>{step.tool_idempotency}</strong></div>
                        <div><span>尝试</span><strong>{step.attempt}/{step.max_attempts}</strong></div>
                      </div>
                      <p className="steward-runtime-policy-reason">{step.policy_reason}</p>
                      {step.last_error ? <p className="steward-runtime-step-error">{step.last_error}</p> : null}
                      <details>
                        <summary>查看工具参数与预期结果</summary>
                        <div className="steward-runtime-code-grid">
                          <div><span>arguments</span><pre>{prettyJSON(step.arguments)}</pre></div>
                          <div><span>expected_output</span><pre>{prettyJSON(step.expected_output)}</pre></div>
                        </div>
                      </details>
                      {(step.invocations.length > 0 || step.evidence.length > 0) ? (
                        <details className="steward-runtime-evidence">
                          <summary>执行证据 · {step.invocations.length} 次调用 / {step.evidence.length} 项证据</summary>
                          {step.invocations.map((invocation) => (
                            <div className="steward-runtime-invocation" key={invocation.id}>
                              <strong>调用 #{invocation.attempt} · {runtimeStatusText(invocation.status)}</strong>
                              <small>
                                {formatDate(invocation.started_at)} · control generation {invocation.control_generation}
                                {invocation.lease_expires_at ? ` · lease ${formatDate(invocation.lease_expires_at)}` : ''}
                              </small>
                              {invocation.error_summary ? <p>{invocation.error_summary}</p> : null}
                              {invocation.output ? <><span className="steward-runtime-evidence-label">受治理输出预览</span><pre>{prettyJSON(invocation.output)}</pre></> : null}
                            </div>
                          ))}
                          {step.evidence.map((evidence) => (
                            <div className="steward-runtime-evidence-item" key={evidence.id}>
                              <strong>{evidence.kind} · {evidence.summary}</strong>
                              <small>
                                {evidence.data_level} · {evidence.payload_state} · {evidence.size_bytes} bytes · sha256 {evidence.sha256.slice(0, 16)}…
                                {evidence.redacted ? ' · 已脱敏' : ''} · {formatDate(evidence.created_at)}
                              </small>
                              {evidence.payload_available ? (
                                <button
                                  className="steward-button steward-button-secondary steward-runtime-evidence-reveal"
                                  disabled={busy !== null}
                                  onClick={() => void revealEvidence(evidence)}
                                  type="button"
                                >
                                  {busy === `evidence:${evidence.id}` ? '正在读取…' : evidenceDetails[evidence.id] ? '重新读取受限详情' : '加载受限详情'}
                                </button>
                              ) : <p className="steward-runtime-evidence-notice">正文未持久化：超过大小上限，或敏感级别缺少本机加密密钥。</p>}
                              {evidenceDetails[evidence.id]?.payload ? (
                                <div className="steward-runtime-evidence-detail">
                                  <span>显式读取的证据正文</span>
                                  <pre>{prettyJSON(evidenceDetails[evidence.id].payload)}</pre>
                                </div>
                              ) : null}
                            </div>
                          ))}
                        </details>
                      ) : null}
                    </article>
                  ))}
                </div>

                <details className="steward-runtime-event-log">
                  <summary>实时事件日志 · {events.length} 条</summary>
                  {events.length === 0 ? <p>等待执行事件…</p> : events.map((event) => (
                    <div key={event.sequence}>
                      <code>#{event.sequence} {event.type}</code>
                      <span>{event.message}</span>
                      <small>{formatDate(event.created_at)}</small>
                    </div>
                  ))}
                </details>
              </>
            )}
          </div>
        </div>
      )}
    </section>
  )
}
