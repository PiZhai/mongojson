import { useCallback, useEffect, useState } from 'react'
import {
  createStewardAutonomyProposal,
  executeStewardAutonomyProposal,
  getStewardDataPolicies,
  getStewardModelDispatches,
  getStewardPermissionPolicies,
  getStewardToolDefinitions,
  runStewardModelDispatches,
  updateStewardDataPolicy,
  updateStewardPermissionPolicy,
  updateStewardToolDefinition,
} from '../../../lib/api/client'
import type {
  StewardDataPolicy,
  StewardModelDispatch,
  StewardPermissionPolicy,
  StewardToolDefinition,
} from '../../../types/tooling'
import { dataLevels, formatDate } from './model'
import { EmptyState, Panel } from './presentation'

type Props = {
  onDataChanged: () => Promise<void>
}

const permissionLevels = Array.from({ length: 10 }, (_, rank) => `A${rank}`)
const brokerPermissionLevels = ['A4', 'A5', 'A6', 'A7']

const permissionNames: Record<string, string> = {
  A0: '无操作',
  A1: '读取元数据',
  A2: '读取内容',
  A3: '低风险本地写入',
  A4: '高风险本地写入',
  A5: '不可逆操作',
  A6: '对外发送',
  A7: '系统控制',
  A8: '凭据与身份',
  A9: '高风险专业决策',
}

export function AutomationPolicyWorkspace({ onDataChanged }: Props) {
  const [dataPolicies, setDataPolicies] = useState<StewardDataPolicy[]>([])
  const [permissionPolicies, setPermissionPolicies] = useState<StewardPermissionPolicy[]>([])
  const [dispatches, setDispatches] = useState<StewardModelDispatch[]>([])
  const [tools, setTools] = useState<StewardToolDefinition[]>([])
  const [dataOverrideLevel, setDataOverrideLevel] = useState('D3')
  const [dataOverridePattern, setDataOverridePattern] = useState('adapter:*')
  const [permissionOverrideLevel, setPermissionOverrideLevel] = useState('A6')
  const [permissionOverridePattern, setPermissionOverridePattern] = useState('model:*')
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    const [dataResult, permissionResult, dispatchResult, toolResult] = await Promise.all([
      getStewardDataPolicies(),
      getStewardPermissionPolicies(),
      getStewardModelDispatches(60),
      getStewardToolDefinitions(),
    ])
    setDataPolicies(dataResult.data_policies)
    setPermissionPolicies(permissionResult.permission_policies)
    setDispatches(dispatchResult.model_dispatches)
    setTools(toolResult.tools)
  }, [])

  useEffect(() => {
    void Promise.resolve()
      .then(reload)
      .catch((cause: unknown) => {
        setError(cause instanceof Error ? cause.message : '加载自动化权限策略失败')
      })
  }, [reload])

  const run = async (label: string, action: () => Promise<void>) => {
    setBusy(label)
    setError(null)
    try {
      await action()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : `${label}失败`)
    } finally {
      setBusy(null)
    }
  }

  const afterPolicySave = async () => {
    await Promise.all([reload(), onDataChanged()])
  }

  const createDataOverride = () => run('新增数据策略', async () => {
    const pattern = dataOverridePattern.trim()
    if (!pattern) throw new Error('来源匹配不能为空')
    await updateStewardDataPolicy({
      data_level: dataOverrideLevel,
      source_pattern: pattern,
      collect_mode: 'deny',
      model_mode: 'deny',
      model_content_mode: 'redacted',
      allow_local_persistence: true,
      allow_sync: false,
      require_encryption: ['D4', 'D5', 'D6'].includes(dataOverrideLevel),
      description: '来源级独立策略',
    })
    await afterPolicySave()
  })

  const createPermissionOverride = () => run('新增权限策略', async () => {
    const pattern = permissionOverridePattern.trim()
    if (!pattern) throw new Error('动作匹配不能为空')
    await updateStewardPermissionPolicy({
      permission_level: permissionOverrideLevel,
      action_pattern: pattern,
      execution_mode: 'deny',
      require_simulation: Number(permissionOverrideLevel.slice(1)) >= 4,
      require_rollback: Number(permissionOverrideLevel.slice(1)) >= 4,
      max_batch_size: 1,
      cooldown_seconds: 0,
      description: '动作级独立策略',
    })
    await afterPolicySave()
  })

  return (
    <>
      {error ? <div className="steward-alert" role="alert">{error}</div> : null}
      <section className="steward-policy-workspace">
        <Panel
          help="采集、发送和发送内容形态分别授权。来源匹配支持 * 通配符；更具体的规则优先于全局规则。D5 内容会先被识别并升级为 D5，再按策略处理。"
          title="数据采集与模型发送"
        >
          <div className="steward-automation-toolbar">
            <select aria-label="覆盖规则数据等级" disabled={busy !== null} onChange={(event) => setDataOverrideLevel(event.target.value)} value={dataOverrideLevel}>
              {dataLevels.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
            <input aria-label="来源匹配" disabled={busy !== null} onChange={(event) => setDataOverridePattern(event.target.value)} placeholder="collector:*" value={dataOverridePattern} />
            <button className="steward-button steward-button-secondary" disabled={busy !== null} onClick={createDataOverride} type="button">新增来源规则</button>
          </div>
          <div className="steward-automation-table" role="table" aria-label="数据采集与模型发送策略">
            <div className="steward-automation-head" role="row">
              <span>等级与来源</span><span>采集</span><span>模型</span><span>发送内容</span><span>存储</span><span>同步</span><span>加密</span><span>操作</span>
            </div>
            {dataPolicies.map((policy) => (
              <DataPolicyRow
                busy={busy !== null}
                key={`${policy.id}:${policy.updated_at}`}
                onSave={(payload) => run('保存数据策略', async () => {
                  await updateStewardDataPolicy(payload)
                  await afterPolicySave()
                })}
                policy={policy}
              />
            ))}
          </div>
        </Panel>

        <Panel
          help="工具定义只执行固定绝对路径与固定参数数组，不解析自然语言或 shell 字符串。子进程仅继承最小系统环境，不继承模型 API Key。工具、权限策略和全局自动权限必须同时允许。"
          title="高权限本地工具"
        >
          <ToolDefinitionEditor
            busy={busy !== null}
            key={tools.map((tool) => tool.updated_at).join(':')}
            onRun={(tool) => run('运行高权限工具', async () => {
              const confirmed = window.confirm(`将创建并执行 ${tool.action}（${tool.permission_level}）。确认继续？`)
              if (!confirmed) return
              const created = await createStewardAutonomyProposal({
                source_entity_type: 'tool_definition',
                source_entity_id: tool.id,
                action: tool.action,
                title: `运行 ${tool.name}`,
                summary: tool.description,
                trigger_reason: '用户在自动化权限工作台发起',
                suggested_action: `运行已登记工具 ${tool.action}`,
                risk_level: tool.risk_level,
                permission_level: tool.permission_level,
                data_level: 'D0',
                policy: 'auto',
                impact_summary: `${tool.permission_level} 本地工具执行`,
              })
              await executeStewardAutonomyProposal(created.proposal.id)
              await afterPolicySave()
            })}
            onSave={(payload) => run('保存高权限工具', async () => {
              await updateStewardToolDefinition(payload)
              await afterPolicySave()
            })}
            tools={tools}
          />
        </Panel>

        <Panel
          help="权限等级只决定最终动作门禁。要真正自动执行，还必须同时满足自主模式、动作规则、已注册执行器、模拟和回滚要求；没有执行器的动作不会被当作任意系统命令运行。"
          title="操作权限与自动执行"
        >
          <div className="steward-automation-toolbar">
            <select aria-label="覆盖规则权限等级" disabled={busy !== null} onChange={(event) => setPermissionOverrideLevel(event.target.value)} value={permissionOverrideLevel}>
              {permissionLevels.map((level) => <option key={level} value={level}>{level} {permissionNames[level]}</option>)}
            </select>
            <input aria-label="动作匹配" disabled={busy !== null} onChange={(event) => setPermissionOverridePattern(event.target.value)} placeholder="model:*" value={permissionOverridePattern} />
            <button className="steward-button steward-button-secondary" disabled={busy !== null} onClick={createPermissionOverride} type="button">新增动作规则</button>
          </div>
          <div className="steward-permission-table" role="table" aria-label="操作权限自动执行策略">
            <div className="steward-permission-head" role="row">
              <span>等级与动作</span><span>执行</span><span>模拟</span><span>回滚</span><span>批量</span><span>冷却/秒</span><span>操作</span>
            </div>
            {permissionPolicies.map((policy) => (
              <PermissionPolicyRow
                busy={busy !== null}
                key={`${policy.id}:${policy.updated_at}`}
                onSave={(payload) => run('保存权限策略', async () => {
                  await updateStewardPermissionPolicy(payload)
                  await afterPolicySave()
                })}
                policy={policy}
              />
            ))}
          </div>
        </Panel>

        <Panel
          actions={<button className="steward-button steward-button-secondary" disabled={busy !== null} onClick={() => run('运行模型发送队列', async () => {
            await runStewardModelDispatches(20)
            await reload()
          })} type="button">立即处理</button>}
          help="只有数据策略允许自动模型发送、A6 对应模型动作允许自动执行、且模型自身最高数据等级覆盖该数据时，队列才会发送。每次发送都会写 A6 审计。"
          title="模型发送队列"
        >
          <div className="steward-table-list steward-model-dispatch-list">
            {dispatches.slice(0, 20).map((dispatch) => <ModelDispatchRow dispatch={dispatch} key={dispatch.id} />)}
            {dispatches.length === 0 ? <EmptyState text="暂无模型发送记录" /> : null}
          </div>
        </Panel>
      </section>
    </>
  )
}

type ToolDraft = Omit<StewardToolDefinition, 'id' | 'created_at' | 'updated_at'>

const emptyToolDraft: ToolDraft = {
  action: 'tool:', name: '', description: '', executable: '', arguments: [],
  working_directory: '', permission_level: 'A4', risk_level: 'high', enabled: false,
  timeout_seconds: 60, rollback_executable: '', rollback_arguments: [],
}

function ToolDefinitionEditor({ tools, busy, onSave, onRun }: {
  tools: StewardToolDefinition[]
  busy: boolean
  onSave: (payload: ToolDraft) => Promise<void>
  onRun: (tool: StewardToolDefinition) => Promise<void>
}) {
  const [selectedAction, setSelectedAction] = useState(tools[0]?.action ?? '__new__')
  const selected = tools.find((tool) => tool.action === selectedAction)
  const [draft, setDraft] = useState<ToolDraft>(selected ? toolDraft(selected) : emptyToolDraft)
  const [argumentsText, setArgumentsText] = useState((selected?.arguments ?? []).join('\n'))
  const [rollbackArgumentsText, setRollbackArgumentsText] = useState((selected?.rollback_arguments ?? []).join('\n'))

  const selectTool = (action: string) => {
    setSelectedAction(action)
    const tool = tools.find((item) => item.action === action)
    const next = tool ? toolDraft(tool) : emptyToolDraft
    setDraft(next)
    setArgumentsText(next.arguments.join('\n'))
    setRollbackArgumentsText(next.rollback_arguments.join('\n'))
  }

  const payload = (): ToolDraft => ({
    ...draft,
    arguments: splitArguments(argumentsText),
    rollback_arguments: splitArguments(rollbackArgumentsText),
  })

  return (
    <div className="steward-tool-editor">
      <p className="steward-broker-boundary-note">
        R3.1 中这里只保存能力的管理元数据；独立 Privilege Broker 的签名策略、可执行文件哈希、固定参数与独立审批票据才是执行授权源。Broker 仅接受 A4–A7，A8/A9 暂不开放。
      </p>
      <div className="steward-automation-toolbar">
        <select aria-label="选择高权限工具" disabled={busy} onChange={(event) => selectTool(event.target.value)} value={selectedAction}>
          {tools.map((tool) => <option key={tool.id} value={tool.action}>{tool.action} · {tool.name}</option>)}
          <option value="__new__">新增工具</option>
        </select>
        <input aria-label="工具动作标识" disabled={busy || Boolean(selected)} onChange={(event) => setDraft({ ...draft, action: event.target.value })} placeholder="tool:backup-project" value={draft.action} />
        <label className="steward-compact-check"><input checked={draft.enabled} disabled={busy} onChange={(event) => setDraft({ ...draft, enabled: event.currentTarget.checked })} type="checkbox" /><span>启用</span></label>
      </div>
      <div className="steward-tool-form">
        <label><span>名称</span><input disabled={busy} onChange={(event) => setDraft({ ...draft, name: event.target.value })} value={draft.name} /></label>
        <label><span>绝对可执行路径</span><input disabled={busy} onChange={(event) => setDraft({ ...draft, executable: event.target.value })} placeholder="C:\\Tools\\backup.exe" value={draft.executable} /></label>
        <label><span>工作目录</span><input disabled={busy} onChange={(event) => setDraft({ ...draft, working_directory: event.target.value })} value={draft.working_directory} /></label>
        <label><span>权限</span><select disabled={busy} onChange={(event) => setDraft({ ...draft, permission_level: event.target.value })} value={draft.permission_level}>{brokerPermissionLevels.map((level) => <option key={level} value={level}>{level} {permissionNames[level]}</option>)}</select></label>
        <label><span>风险</span><select disabled={busy} onChange={(event) => setDraft({ ...draft, risk_level: event.target.value })} value={draft.risk_level}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="critical">关键</option></select></label>
        <label><span>超时/秒</span><input disabled={busy} max={3600} min={1} onChange={(event) => setDraft({ ...draft, timeout_seconds: Number(event.target.value) })} type="number" value={draft.timeout_seconds} /></label>
        <label className="steward-tool-wide"><span>固定参数，每行一个</span><textarea disabled={busy} onChange={(event) => setArgumentsText(event.target.value)} value={argumentsText} /></label>
        <label className="steward-tool-wide"><span>说明</span><input disabled={busy} onChange={(event) => setDraft({ ...draft, description: event.target.value })} value={draft.description} /></label>
        <label><span>回滚可执行路径</span><input disabled={busy} onChange={(event) => setDraft({ ...draft, rollback_executable: event.target.value })} value={draft.rollback_executable} /></label>
        <label className="steward-tool-wide"><span>回滚固定参数，每行一个</span><textarea disabled={busy} onChange={(event) => setRollbackArgumentsText(event.target.value)} value={rollbackArgumentsText} /></label>
      </div>
      <div className="steward-panel-actions">
        <button className="steward-button steward-button-secondary" disabled={busy || !draft.name.trim() || !draft.executable.trim()} onClick={() => onSave(payload())} type="button">保存工具</button>
        <button className="steward-button" disabled={busy || !selected?.enabled} onClick={() => selected && onRun(selected)} type="button">运行一次</button>
      </div>
    </div>
  )
}

function toolDraft(tool: StewardToolDefinition): ToolDraft {
  return {
    action: tool.action,
    name: tool.name,
    description: tool.description,
    executable: tool.executable,
    arguments: [...(tool.arguments ?? [])],
    working_directory: tool.working_directory,
    permission_level: tool.permission_level,
    risk_level: tool.risk_level,
    enabled: tool.enabled,
    timeout_seconds: tool.timeout_seconds,
    rollback_executable: tool.rollback_executable,
    rollback_arguments: [...(tool.rollback_arguments ?? [])],
  }
}

function splitArguments(value: string) {
  return value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean)
}

function DataPolicyRow({ policy, busy, onSave }: {
  policy: StewardDataPolicy
  busy: boolean
  onSave: (payload: Partial<StewardDataPolicy> & { data_level: string; source_pattern: string }) => Promise<void>
}) {
  const [collectMode, setCollectMode] = useState(policy.collect_mode)
  const [modelMode, setModelMode] = useState(policy.model_mode)
  const [contentMode, setContentMode] = useState(policy.model_content_mode)
  const [persist, setPersist] = useState(policy.allow_local_persistence)
  const [sync, setSync] = useState(policy.allow_sync)
  const [encrypt, setEncrypt] = useState(policy.require_encryption)

  const save = async () => {
    if ((['D4', 'D5', 'D6'].includes(policy.data_level) && modelMode === 'auto') || contentMode === 'raw') {
      const confirmed = window.confirm(`${policy.data_level} ${policy.source_pattern} 将允许自动向已配置模型发送 ${contentMode === 'raw' ? '原文' : '高敏数据'}。确认继续？`)
      if (!confirmed) return
    }
    await onSave({
      data_level: policy.data_level,
      source_pattern: policy.source_pattern,
      collect_mode: collectMode,
      model_mode: modelMode,
      model_content_mode: contentMode,
      allow_local_persistence: persist,
      allow_sync: sync,
      require_encryption: encrypt,
    })
  }

  return (
    <div className="steward-automation-row" role="row">
      <span><strong>{policy.data_level}</strong><small>{policy.source_pattern}</small></span>
      <select aria-label={`${policy.data_level} ${policy.source_pattern} 采集模式`} disabled={busy} onChange={(event) => setCollectMode(event.target.value as StewardDataPolicy['collect_mode'])} value={collectMode}>{modeOptions()}</select>
      <select aria-label={`${policy.data_level} ${policy.source_pattern} 模型模式`} disabled={busy} onChange={(event) => setModelMode(event.target.value as StewardDataPolicy['model_mode'])} value={modelMode}>{modeOptions()}</select>
      <select aria-label={`${policy.data_level} ${policy.source_pattern} 模型内容`} disabled={busy} onChange={(event) => setContentMode(event.target.value as StewardDataPolicy['model_content_mode'])} value={contentMode}>
        <option value="metadata">仅元数据</option><option value="summary">摘要</option><option value="redacted">脱敏内容</option><option value="raw">原文</option>
      </select>
      <PolicyCheckbox checked={persist} disabled={busy} label="本地存储" onChange={setPersist} />
      <PolicyCheckbox checked={sync} disabled={busy} label="跨端同步" onChange={setSync} />
      <PolicyCheckbox checked={encrypt} disabled={busy || ['D4', 'D5', 'D6'].includes(policy.data_level)} label="强制加密" onChange={setEncrypt} />
      <button className="steward-icon-button steward-button-secondary" disabled={busy} onClick={save} type="button">保存</button>
    </div>
  )
}

function PermissionPolicyRow({ policy, busy, onSave }: {
  policy: StewardPermissionPolicy
  busy: boolean
  onSave: (payload: Partial<StewardPermissionPolicy> & { permission_level: string; action_pattern: string }) => Promise<void>
}) {
  const [executionMode, setExecutionMode] = useState(policy.execution_mode)
  const [simulation, setSimulation] = useState(policy.require_simulation)
  const [rollback, setRollback] = useState(policy.require_rollback)
  const [batchSize, setBatchSize] = useState(policy.max_batch_size)
  const [cooldown, setCooldown] = useState(policy.cooldown_seconds)

  const save = async () => {
    if (executionMode === 'auto' && Number(policy.permission_level.slice(1)) >= 4) {
      const confirmed = window.confirm(`${policy.permission_level} ${policy.action_pattern} 将允许自动执行高权限动作。确认继续？`)
      if (!confirmed) return
    }
    await onSave({
      permission_level: policy.permission_level,
      action_pattern: policy.action_pattern,
      execution_mode: executionMode,
      require_simulation: simulation,
      require_rollback: rollback,
      max_batch_size: batchSize,
      cooldown_seconds: cooldown,
    })
  }

  return (
    <div className="steward-permission-row" role="row">
      <span><strong>{policy.permission_level} {permissionNames[policy.permission_level]}</strong><small>{policy.action_pattern}</small></span>
      <select aria-label={`${policy.permission_level} ${policy.action_pattern} 执行模式`} disabled={busy} onChange={(event) => setExecutionMode(event.target.value as StewardPermissionPolicy['execution_mode'])} value={executionMode}>{modeOptions()}</select>
      <PolicyCheckbox checked={simulation} disabled={busy} label="执行前模拟" onChange={setSimulation} />
      <PolicyCheckbox checked={rollback} disabled={busy} label="要求回滚" onChange={setRollback} />
      <input aria-label={`${policy.permission_level} 最大批量`} disabled={busy} max={10000} min={1} onChange={(event) => setBatchSize(Number(event.target.value))} type="number" value={batchSize} />
      <input aria-label={`${policy.permission_level} 冷却秒数`} disabled={busy} max={31536000} min={0} onChange={(event) => setCooldown(Number(event.target.value))} type="number" value={cooldown} />
      <button className="steward-icon-button steward-button-secondary" disabled={busy} onClick={save} type="button">保存</button>
    </div>
  )
}

function PolicyCheckbox({ checked, disabled, label, onChange }: { checked: boolean; disabled: boolean; label: string; onChange: (value: boolean) => void }) {
  return <label className="steward-compact-check"><input checked={checked} disabled={disabled} onChange={(event) => onChange(event.currentTarget.checked)} type="checkbox" /><span>{label}</span></label>
}

function modeOptions() {
  return <><option value="deny">禁止</option><option value="manual">手动</option><option value="auto">自动</option></>
}

function ModelDispatchRow({ dispatch }: { dispatch: StewardModelDispatch }) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{dispatch.data_level} · {dispatch.source}</strong>
        <p>{dispatch.response_summary || dispatch.last_error || dispatch.request_summary || '等待发送'}</p>
        <small>{dispatch.status} · {dispatch.content_mode} · 尝试 {dispatch.attempts} · {dispatch.provider || '未调用'} {dispatch.model}</small>
      </div>
      <div className="steward-row-meta"><span>{formatDate(dispatch.created_at)}</span><span>{dispatch.audit_id ? `审计 ${dispatch.audit_id.slice(0, 8)}` : '尚无审计号'}</span></div>
    </article>
  )
}
