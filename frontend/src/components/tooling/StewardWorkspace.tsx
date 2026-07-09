import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from 'react'
import {
  acceptStewardIntent,
  approveStewardApprovalRequest,
  approveStewardAutonomyProposal,
  archiveStewardMemory,
  cancelStewardTask,
  completeStewardTask,
  convertStewardEvent,
  correctStewardMemory,
  createStewardEvent,
  createStewardIntent,
  createStewardKnowledgeItem,
  createStewardMemory,
  createStewardTag,
  createStewardTask,
  deleteStewardEvent,
  deleteStewardIntent,
  deleteStewardKnowledgeItem,
  deleteStewardMemory,
  deleteStewardTask,
  deleteStewardTimelineSegment,
  dismissStewardAutonomyProposal,
  dismissStewardAutonomyProposals,
  dismissStewardIntent,
  executeStewardAutonomyProposal,
  exportStewardData,
  getStewardMemoryVersions,
  getStewardOverview,
  getStewardSourceRefs,
  hideStewardEvent,
  muteStewardIntent,
  rejectStewardApprovalRequest,
  resolveStewardSyncConflict,
  revokeStewardDevice,
  runStewardAutonomyCycle,
  searchStewardData,
  simulateStewardAutonomyProposal,
  startStewardAgent,
  stopStewardAgent,
  syncStewardDevice,
  updateStewardDevicePermission,
  updateStewardAutonomyRule,
  updateStewardAutonomySettings,
  updateStewardCollector,
  verifyStewardDeviceTrust,
} from '../../lib/api/client'
import type {
  StewardApprovalRequest,
  StewardAuditLog,
  StewardAutonomousRun,
  StewardAutonomyAdvisorStatus,
  StewardAutonomyProposal,
  StewardAutonomyRule,
  StewardDeviceCapability,
  StewardDevicePermission,
  StewardDevice,
  StewardSyncChange,
  StewardSyncConflict,
  StewardEvent,
  StewardIntent,
  StewardKnowledgeItem,
  StewardMemory,
  StewardMemoryVersion,
  StewardOverview,
  StewardSearchResult,
  StewardSourceRef,
  StewardTask,
} from '../../types/tooling'

type EventDraft = {
  title: string
  summary: string
  type: string
  dataLevel: string
}

type TaskDraft = {
  title: string
  description: string
  priority: 'low' | 'normal' | 'high'
  dueAt: string
  dataLevel: string
}

type IntentDraft = {
  title: string
  summary: string
  reason: string
  suggestedAction: string
  dataLevel: string
}

type MemoryDraft = {
  title: string
  summary: string
  content: string
  scope: string
  dataLevel: string
}

type KnowledgeDraft = {
  title: string
  summary: string
  originalUri: string
  type: string
  dataLevel: string
  allowIndex: boolean
}

type TagDraft = {
  name: string
  type: string
}

type CorrectionDraft = {
  memory: StewardMemory
  title: string
  summary: string
  content: string
  reason: string
}

const emptyEventDraft: EventDraft = {
  title: '',
  summary: '',
  type: 'manual_note',
  dataLevel: 'D0',
}

const emptyTaskDraft: TaskDraft = {
  title: '',
  description: '',
  priority: 'normal',
  dueAt: '',
  dataLevel: 'D0',
}

const emptyIntentDraft: IntentDraft = {
  title: '',
  summary: '',
  reason: '',
  suggestedAction: '',
  dataLevel: 'D0',
}

const emptyMemoryDraft: MemoryDraft = {
  title: '',
  summary: '',
  content: '',
  scope: 'global',
  dataLevel: 'D0',
}

const emptyKnowledgeDraft: KnowledgeDraft = {
  title: '',
  summary: '',
  originalUri: '',
  type: 'note',
  dataLevel: 'D0',
  allowIndex: true,
}

const emptyTagDraft: TagDraft = {
  name: '',
  type: 'normal',
}

const dataLevels = [
  ['D0', 'D0 临时/低敏'],
  ['D1', 'D1 公开资料'],
  ['D2', 'D2 本地元数据'],
  ['D3', 'D3 用户内容'],
  ['D4', 'D4 敏感内容'],
  ['D5', 'D5 凭据'],
  ['D6', 'D6 高风险个人数据'],
] as const

const helpText: Record<string, string> = {
  localSteward: '本机上的私人管家进程。S2 只负责本地数据底座，不做真实跨端同步或高风险自动执行。',
  events: '事件是最小事实单元，用来记录你手动输入、导入或采集到的一次信息。',
  timeline: '时间线把多个事件聚合成一个时间片段，方便回看某段时间发生了什么。',
  tasks: '任务是你确认需要处理的事项，可以手动创建，也可以由事件或意图转化。',
  intents: '意图是系统或你自己记录的候选想法，不会直接执行，需要确认后才能转成任务。',
  memories: '记忆保存长期事实、偏好、决策和项目上下文，需要来源或用户确认。',
  knowledge: '知识保存资料、笔记、网页或文件摘要，不等同于关于你的长期事实。',
  sources: '来源引用记录一个任务、记忆或知识从哪里来，用于追溯和纠错。',
  audit: '审计日志记录关键数据变更、导出、删除、权限和工具动作。',
  collectors: '采集器是数据入口。S2 默认只启用手动输入，其他采集器仍需明确授权。',
  search: '统一搜索会在事件、时间线、任务、意图、记忆和知识中查找标题与摘要。',
  dataManagement: '数据管理用于导出、标签和敏感标记，不会默认导出高敏内容。',
  sync: '三端同步在 S3 采用本地优先变更队列、设备身份、权限和冲突显式处理；当前不做静默覆盖。',
  devices: '设备列表展示本地和已登记的对端设备，撤销后该设备不能继续参与同步。',
  syncChanges: '同步变更是可传输的最小数据包，记录实体、版本、来源设备和应用状态。',
  conflicts: '冲突需要人工处理；系统不会用远端版本静默覆盖本地数据。',
  autonomy: '自主能力在 S4 只允许受控低风险执行；高风险、外发、删除、付款、系统配置等默认阻断。',
  autonomyRules: '规则定义候选任务的触发、策略和最高权限，可设置建议、确认、自动或永不执行。',
  autonomyProposals: '候选建议展示触发原因、影响范围和策略，执行前可模拟或审批。',
  approvals: '审批队列用于高风险或需确认的动作，批准记录不等于自动绕过高风险红线。',
  manualEvent: '手动事件适合记录一个事实、想法、链接或待整理信息。',
  manualTask: '手动任务适合记录已经确认要做的事情。',
  candidateIntent: '候选意图适合先放入一个可能要跟进的想法，之后再决定是否转任务。',
  manualMemory: '手动记忆适合写入明确、长期有效、你愿意让管家复用的事实。',
  knowledgeImport: '知识导入适合保存资料摘要、文件路径或 URL，并保留来源。',
  memoryCorrection: '记忆纠正会保留旧版本，新的用户纠正版本优先作为当前事实。',
  memoryLibrary: '记忆库展示当前可用的长期上下文，并支持纠正、来源查看和归档。',
  knowledgeLibrary: '知识库展示导入的资料条目，可选择是否允许进入检索索引。',
  sourcePanel: '来源面板展示当前选中实体的来源引用，帮助确认结论从哪里来。',
  tags: '标签用于分类、敏感标记和生命周期管理。',
  dataLevel: '数据级别来自 S0 安全基线，D4-D6 会被视为敏感或高风险数据。',
  indexing: '允许检索表示该知识可进入本地搜索索引。敏感数据默认不应进入检索。',
  manualInput: '只保存你主动提交的事件、任务、记忆和知识。',
  browserLink: '保存你主动导入的网页链接和摘要，不做后台自动抓取。',
  clipboardSummary: '在授权后保存剪贴板摘要，并避免保存疑似敏感字段原文。',
  systemStatus: '保存本机 Agent、磁盘、网络等低风险状态摘要。',
  watchedDirectory: '在授权目录内记录文件新增、修改、删除的元数据。',
}

const metricHelp: Record<string, string> = {
  事件: helpText.events,
  时间线: helpText.timeline,
  任务: helpText.tasks,
  意图: helpText.intents,
  记忆: helpText.memories,
  知识: helpText.knowledge,
  来源: helpText.sources,
  审计: helpText.audit,
}

const collectorHelp: Record<string, string> = {
  'manual-input': helpText.manualInput,
  'browser-link': helpText.browserLink,
  'clipboard-summary': helpText.clipboardSummary,
  'system-status': helpText.systemStatus,
  'watched-directory': helpText.watchedDirectory,
}

function formatDate(value?: string | null) {
  if (!value) {
    return '未记录'
  }
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

function statusText(status: string) {
  const map: Record<string, string> = {
    running: '运行中',
    stopped: '已停止',
    degraded: '部分异常',
    error: '异常',
    open: '待处理',
    in_progress: '处理中',
    waiting: '等待中',
    done: '已完成',
    canceled: '已取消',
    archived: '已归档',
    active: '可见',
    hidden: '已隐藏',
    candidate: '候选',
    accepted: '已接受',
    dismissed: '已忽略',
    muted: '已静音',
    allow: '允许',
    deny: '拒绝',
    draft: '草稿',
    stale: '待复核',
    disputed: '有冲突',
    trusted: '可信',
    revoked: '已撤销',
    pending: '待处理',
    applied: '已应用',
    stored: '已存档',
    conflict: '有冲突',
    resolved: '已处理',
    approved: '已批准',
    executed: '已执行',
    blocked: '已阻断',
    suggest: '仅建议',
    confirm: '需确认',
    auto: '低风险自动',
    never: '禁止',
    success: '成功',
    failed: '失败',
    simulate: '模拟',
    execute: '执行',
    suggest_only: '仅建议模式',
    controlled: '受控执行模式',
  }
  return map[status] ?? status
}

function advisorStatusText(advisor?: StewardAutonomyAdvisorStatus | null) {
  if (!advisor) {
    return '模型建议器状态未返回'
  }
  if (!advisor.enabled) {
    return `模型建议器关闭${advisor.reason ? ` · ${advisor.reason}` : ''}`
  }
  if (advisor.circuit_open) {
    return [
      '模型建议器熔断中',
      advisor.provider,
      advisor.retry_at ? `重试 ${formatDate(advisor.retry_at)}` : '',
      advisor.last_error ? `最近错误 ${advisor.last_error}` : '',
    ]
      .filter(Boolean)
      .join(' · ')
  }
  return [
    '模型建议器已启用',
    advisor.provider,
    advisor.model,
    advisor.max_data_level ? `最高数据 ${advisor.max_data_level}` : '',
    advisor.consecutive_failures ? `连续失败 ${advisor.consecutive_failures}` : '',
  ]
    .filter(Boolean)
    .join(' · ')
}

function priorityText(priority: string) {
  const map: Record<string, string> = {
    low: '低',
    normal: '普通',
    high: '高',
  }
  return map[priority] ?? priority
}

function entityText(entityType: string) {
  const map: Record<string, string> = {
    event: '事件',
    timeline_segment: '时间线',
    task: '任务',
    intent: '意图',
    memory: '记忆',
    knowledge_item: '知识',
    data_tag: '标签',
    source_ref: '来源',
  }
  return map[entityType] ?? entityType
}

function isSensitiveLevel(level: string) {
  return ['D4', 'D5', 'D6'].includes(level)
}

export function StewardWorkspace() {
  const [overview, setOverview] = useState<StewardOverview | null>(null)
  const [eventDraft, setEventDraft] = useState<EventDraft>(emptyEventDraft)
  const [taskDraft, setTaskDraft] = useState<TaskDraft>(emptyTaskDraft)
  const [intentDraft, setIntentDraft] = useState<IntentDraft>(emptyIntentDraft)
  const [memoryDraft, setMemoryDraft] = useState<MemoryDraft>(emptyMemoryDraft)
  const [knowledgeDraft, setKnowledgeDraft] = useState<KnowledgeDraft>(emptyKnowledgeDraft)
  const [tagDraft, setTagDraft] = useState<TagDraft>(emptyTagDraft)
  const [correctionDraft, setCorrectionDraft] = useState<CorrectionDraft | null>(null)
  const [sourceRefs, setSourceRefs] = useState<StewardSourceRef[] | null>(null)
  const [sourceTarget, setSourceTarget] = useState<string>('最近来源')
  const [memoryVersions, setMemoryVersions] = useState<StewardMemoryVersion[]>([])
  const [searchQuery, setSearchQuery] = useState('')
  const [searchType, setSearchType] = useState('')
  const [searchResults, setSearchResults] = useState<StewardSearchResult[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const refresh = async () => {
    setError(null)
    const result = await getStewardOverview()
    setOverview(result.overview)
  }

  useEffect(() => {
    let alive = true
    getStewardOverview()
      .then((result) => {
        if (alive) {
          setOverview(result.overview)
        }
      })
      .catch((err: unknown) => {
        if (alive) {
          setError(err instanceof Error ? err.message : '加载私人管家数据失败')
        }
      })
      .finally(() => {
        if (alive) {
          setLoading(false)
        }
      })
    return () => {
      alive = false
    }
  }, [])

  const counts = useMemo(() => overview?.counts ?? {}, [overview])
  const collectors = overview?.collectors ?? []
  const events = overview?.events ?? []
  const timelineSegments = overview?.timeline_segments ?? []
  const tasks = overview?.tasks ?? []
  const intents = overview?.intents ?? []
  const memories = overview?.memories ?? []
  const knowledgeItems = overview?.knowledge_items ?? []
  const displayedSourceRefs = sourceRefs ?? overview?.source_refs ?? []
  const tags = overview?.tags ?? []
  const auditLogs = overview?.audit_logs ?? []
  const sync = overview?.sync ?? null
  const autonomy = overview?.autonomy ?? null
  const candidateProposalCount = (autonomy?.proposals ?? []).filter((proposal) => proposal.status === 'candidate').length
  const devicesById = useMemo(() => new Map((sync?.devices ?? []).map((device) => [device.id, device])), [sync?.devices])
  const capabilitiesByKey = useMemo(
    () =>
      new Map(
        (sync?.capabilities ?? []).map((capability) => [
          `${capability.device_id}:${capability.capability}`,
          capability,
        ]),
      ),
    [sync?.capabilities],
  )
  const permissionRows = useMemo(
    () =>
      [...(sync?.permissions ?? [])].sort((left, right) => {
        const byDevice = left.device_id.localeCompare(right.device_id)
        return byDevice === 0 ? left.capability.localeCompare(right.capability) : byDevice
      }),
    [sync?.permissions],
  )

  const runAction = async (label: string, action: () => Promise<unknown>) => {
    setBusy(label)
    setError(null)
    try {
      await action()
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : `${label}失败`)
    } finally {
      setBusy(null)
    }
  }

  const submitEvent = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!eventDraft.title.trim()) {
      setError('事件标题不能为空')
      return
    }
    await runAction('创建事件', async () => {
      await createStewardEvent({
        type: eventDraft.type,
        title: eventDraft.title.trim(),
        summary: eventDraft.summary.trim(),
        source: 'manual',
        data_level: eventDraft.dataLevel,
        permission_level: 'A3',
        user_confirmed: true,
      })
      setEventDraft(emptyEventDraft)
    })
  }

  const submitTask = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!taskDraft.title.trim()) {
      setError('任务标题不能为空')
      return
    }
    await runAction('创建任务', async () => {
      await createStewardTask({
        type: 'manual',
        title: taskDraft.title.trim(),
        description: taskDraft.description.trim(),
        priority: taskDraft.priority,
        due_at: taskDraft.dueAt ? new Date(taskDraft.dueAt).toISOString() : null,
        source: 'manual',
        data_level: taskDraft.dataLevel,
        permission_level: 'A3',
        risk_level: isSensitiveLevel(taskDraft.dataLevel) ? 'medium' : 'low',
        user_confirmed: true,
      })
      setTaskDraft(emptyTaskDraft)
    })
  }

  const submitIntent = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!intentDraft.title.trim()) {
      setError('意图标题不能为空')
      return
    }
    await runAction('创建意图', async () => {
      await createStewardIntent({
        type: 'follow_up',
        title: intentDraft.title.trim(),
        summary: intentDraft.summary.trim(),
        reason: intentDraft.reason.trim(),
        suggested_action: intentDraft.suggestedAction.trim() || '确认后转任务',
        risk_level: isSensitiveLevel(intentDraft.dataLevel) ? 'medium' : 'low',
        source: 'manual',
        data_level: intentDraft.dataLevel,
        permission_level: 'A3',
        confidence: 0.7,
        user_confirmed: false,
      })
      setIntentDraft(emptyIntentDraft)
    })
  }

  const submitMemory = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!memoryDraft.title.trim()) {
      setError('记忆标题不能为空')
      return
    }
    await runAction('创建记忆', async () => {
      await createStewardMemory({
        type: 'project_fact',
        title: memoryDraft.title.trim(),
        summary: memoryDraft.summary.trim(),
        content: memoryDraft.content.trim() || memoryDraft.summary.trim(),
        scope: memoryDraft.scope.trim() || 'global',
        source: 'manual',
        data_level: memoryDraft.dataLevel,
        permission_level: 'A3',
        confidence: 1,
        user_confirmed: true,
      })
      setMemoryDraft(emptyMemoryDraft)
    })
  }

  const submitKnowledge = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!knowledgeDraft.title.trim()) {
      setError('知识标题不能为空')
      return
    }
    await runAction('导入知识', async () => {
      await createStewardKnowledgeItem({
        type: knowledgeDraft.type,
        title: knowledgeDraft.title.trim(),
        summary: knowledgeDraft.summary.trim(),
        source: 'manual',
        original_uri: knowledgeDraft.originalUri.trim(),
        import_method: 'manual',
        data_level: knowledgeDraft.dataLevel,
        permission_level: 'A3',
        allow_index: knowledgeDraft.allowIndex && !isSensitiveLevel(knowledgeDraft.dataLevel),
        user_confirmed: true,
      })
      setKnowledgeDraft(emptyKnowledgeDraft)
    })
  }

  const submitTag = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!tagDraft.name.trim()) {
      setError('标签名不能为空')
      return
    }
    await runAction('保存标签', async () => {
      await createStewardTag({
        name: tagDraft.name.trim(),
        type: tagDraft.type,
      })
      setTagDraft(emptyTagDraft)
    })
  }

  const submitCorrection = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!correctionDraft) {
      return
    }
    await runAction('纠正记忆', async () => {
      await correctStewardMemory(correctionDraft.memory.id, {
        title: correctionDraft.title,
        summary: correctionDraft.summary,
        content: correctionDraft.content,
        reason: correctionDraft.reason || '用户纠正',
      })
      setCorrectionDraft(null)
      const versions = await getStewardMemoryVersions(correctionDraft.memory.id)
      setMemoryVersions(versions.versions)
    })
  }

  const showSources = async (entityType: string, id: string, title: string) => {
    setBusy('加载来源')
    setError(null)
    try {
      const result = await getStewardSourceRefs(entityType, id)
      setSourceRefs(result.source_refs)
      setSourceTarget(`${entityText(entityType)} · ${title}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载来源失败')
    } finally {
      setBusy(null)
    }
  }

  const showMemoryVersions = async (memory: StewardMemory) => {
    setBusy('加载版本')
    setError(null)
    try {
      const result = await getStewardMemoryVersions(memory.id)
      setMemoryVersions(result.versions)
      setCorrectionDraft({
        memory,
        title: memory.title,
        summary: memory.summary,
        content: memory.content,
        reason: '',
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载记忆版本失败')
    } finally {
      setBusy(null)
    }
  }

  const runSearch = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy('搜索')
    setError(null)
    try {
      const result = await searchStewardData({
        q: searchQuery,
        entity_type: searchType,
        limit: 40,
      })
      setSearchResults(result.results)
    } catch (err) {
      setError(err instanceof Error ? err.message : '搜索失败')
    } finally {
      setBusy(null)
    }
  }

  const downloadExport = async () => {
    await runAction('导出数据', async () => {
      const result = await exportStewardData(false)
      const blob = new Blob([JSON.stringify(result.export, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = `steward-s2-export-${new Date().toISOString().slice(0, 10)}.json`
      anchor.click()
      URL.revokeObjectURL(url)
    })
  }

  if (loading) {
    return <div className="steward-loading">正在加载私人管家工作台...</div>
  }

  return (
    <div className="steward-workspace">
      {error ? (
        <div className="steward-alert" role="alert">
          {error}
        </div>
      ) : null}

      <section className="steward-overview-band">
        <div className="steward-agent-card">
          <div>
            <p className="steward-eyebrow">Local Steward · S2</p>
            <h2>
              {statusText(overview?.agent.status ?? 'stopped')}
              <HelpIcon text={helpText.localSteward} />
            </h2>
            <p>
              {overview?.agent.device_name ?? 'local-device'} · {overview?.agent.platform ?? 'windows'} ·{' '}
              {overview?.agent.version ?? 's2-data-foundation'}
            </p>
          </div>
          <div className="steward-agent-actions">
            <button
              className="steward-button"
              disabled={busy !== null}
              onClick={() => runAction('启动 Agent', startStewardAgent)}
              type="button"
            >
              启动
            </button>
            <button
              className="steward-button steward-button-secondary"
              disabled={busy !== null}
              onClick={() => runAction('停止 Agent', stopStewardAgent)}
              type="button"
            >
              停止
            </button>
          </div>
        </div>
        <div className="steward-metrics steward-metrics-s2">
          <Metric label="事件" value={counts.events ?? 0} />
          <Metric label="时间线" value={counts.timeline_segments ?? 0} />
          <Metric label="任务" value={counts.tasks ?? 0} />
          <Metric label="意图" value={counts.candidate_intents ?? counts.intents ?? 0} />
          <Metric label="记忆" value={counts.memories ?? 0} />
          <Metric label="知识" value={counts.knowledge_items ?? 0} />
          <Metric label="来源" value={counts.source_refs ?? 0} />
          <Metric label="审计" value={counts.audit_logs ?? 0} />
        </div>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel help={helpText.sync} title="三端同步">
          {sync ? (
            <div className="steward-table-list">
              <article className="steward-list-item">
                <div>
                  <strong>{sync.local_device.device_name || 'local-device'}</strong>
                  <p>
                    {sync.local_device.platform} · {statusText(sync.local_device.trust_status)} ·{' '}
                    {sync.local_device.sync_enabled ? '同步开启' : '同步关闭'}
                  </p>
                  <small>
                    待同步 {sync.pending_changes} · 待补关系 {sync.pending_relations} · 冲突{' '}
                    {sync.conflict_count} · 最近 {formatDate(sync.last_change_at)}
                  </small>
                </div>
              </article>
              {sync.security ? (
                <article className="steward-compact-item">
                  <strong>同步安全链</strong>
                  <small>
                    鉴权{sync.security.auth_required ? '默认要求' : '不安全兼容'} · HMAC
                    {sync.security.hmac_secret_configured ? '已配置' : '未配置'} · 设备签名
                    {sync.security.device_signing_ready ? '可用' : '未就绪'} · 传输加密
                    {sync.security.sync_encryption_configured ? '已启用' : '未启用'} · 本地加密
                    {sync.security.local_encryption_configured ? '已启用' : '未启用'}
                  </small>
                  <small>
                    管理面 {sync.security.management_api_addr} · Peer 面{' '}
                    {sync.security.peer_api_enabled ? sync.security.peer_api_addr : '未启用'} · 对外地址{' '}
                    {sync.security.peer_api_advertised ? sync.security.public_api_base : '未发布'}
                  </small>
                  {sync.security.insecure_mode_active ? (
                    <small className="steward-error-text">同步接口正在允许未认证请求</small>
                  ) : null}
                  {sync.security.config_errors.length > 0 ? (
                    <small className="steward-error-text">{sync.security.config_errors.join('；')}</small>
                  ) : null}
                </article>
              ) : null}
              <div className="steward-compact-list">
                {sync.recent_changes.slice(0, 5).map((change) => (
                  <SyncChangeRow change={change} key={change.id} />
                ))}
                {sync.recent_changes.length === 0 ? <EmptyState text="暂无同步变更" /> : null}
              </div>
            </div>
          ) : (
            <EmptyState text="同步状态未加载" />
          )}
        </Panel>

        <Panel help={helpText.devices} title="设备权限">
          <div className="steward-table-list">
            {(sync?.devices ?? []).map((device) => (
              <DeviceRow
                busy={busy !== null}
                device={device}
                key={device.id}
                onRevoke={(id) => runAction('撤销设备', () => revokeStewardDevice(id))}
                onSync={(id) => runAction('同步设备', () => syncStewardDevice(id))}
                onVerify={(id) => runAction('验证设备', () => verifyStewardDeviceTrust(id))}
              />
            ))}
            {(sync?.devices ?? []).length === 0 ? <EmptyState text="暂无设备" /> : null}
            {permissionRows.map((permission) => (
              <DevicePermissionRow
                busy={busy !== null}
                capability={capabilitiesByKey.get(`${permission.device_id}:${permission.capability}`) ?? null}
                device={devicesById.get(permission.device_id) ?? null}
                key={permission.id}
                onUpdate={(deviceId, capability, payload) =>
                  runAction('更新设备权限', () => updateStewardDevicePermission(deviceId, capability, payload))
                }
                permission={permission}
              />
            ))}
            {permissionRows.length === 0 ? <EmptyState text="暂无设备权限策略" /> : null}
            {(sync?.capabilities ?? []).length > 0 ? (
              <div className="steward-compact-list">
                {(sync?.capabilities ?? []).map((capability) => (
                  <article
                    className="steward-compact-item"
                    key={`${capability.device_id}:${capability.capability}`}
                  >
                    <strong>{capability.capability}</strong>
                    <span>
                      {capability.device_id} · {capability.target_type || '未声明目标'} ·{' '}
                      {capability.risk_level} · 最高 {capability.max_permission_level}
                    </span>
                    <small>{capability.description || '无能力说明'}</small>
                  </article>
                ))}
              </div>
            ) : (
              <EmptyState text="暂无设备能力声明" />
            )}
          </div>
        </Panel>

        <Panel help={helpText.conflicts} title="同步冲突">
          <div className="steward-table-list">
            {(sync?.conflicts ?? []).map((conflict) => (
              <SyncConflictRow
                busy={busy !== null}
                conflict={conflict}
                key={conflict.id}
                onResolve={(id) => runAction('处理冲突', () => resolveStewardSyncConflict(id, 'manual review accepted'))}
              />
            ))}
            {(sync?.conflicts ?? []).length === 0 ? <EmptyState text="暂无冲突" /> : null}
          </div>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel
          actions={
            <>
              <div aria-label="自主运行模式" className="steward-segmented" role="group">
                <button
                  className={autonomy?.settings.mode === 'suggest_only' ? 'is-active' : ''}
                  disabled={busy !== null || !autonomy}
                  onClick={() =>
                    runAction('切换为仅建议模式', () => updateStewardAutonomySettings({ mode: 'suggest_only' }))
                  }
                  title="只生成候选，不在后台自动执行"
                  type="button"
                >
                  仅建议
                </button>
                <button
                  className={autonomy?.settings.mode === 'controlled' ? 'is-active' : ''}
                  disabled={busy !== null || !autonomy}
                  onClick={() =>
                    runAction('切换为受控执行模式', () => updateStewardAutonomySettings({ mode: 'controlled' }))
                  }
                  title="只自动执行已设为低风险自动的启用规则"
                  type="button"
                >
                  受控执行
                </button>
              </div>
              <button
                className="steward-button steward-button-secondary"
                disabled={busy !== null}
                onClick={() =>
                  runAction(autonomy?.settings.paused ? '恢复自主能力' : '暂停自主能力', () =>
                    updateStewardAutonomySettings({ paused: !(autonomy?.settings.paused ?? false) }),
                  )
                }
                type="button"
              >
                {autonomy?.settings.paused ? '恢复' : '暂停'}
              </button>
              <button
                className="steward-button"
                disabled={busy !== null || autonomy?.settings.paused}
                onClick={() => runAction('自主扫描', () => runStewardAutonomyCycle(12))}
                type="button"
              >
                扫描
              </button>
            </>
          }
          help={helpText.autonomy}
          title="受控自主"
        >
          {autonomy ? (
            <div className="steward-table-list">
              <article className="steward-list-item">
                <div>
                  <strong>{autonomy.settings.paused ? '自主能力已暂停' : '自主能力可生成候选'}</strong>
                  <p>
                    {statusText(autonomy.settings.mode)} · 最高自动权限 {autonomy.settings.max_auto_permission}
                  </p>
                  <p>{advisorStatusText(autonomy.advisor)}</p>
                  <small>
                    执行动作 {autonomy.actions.length} · 规则 {autonomy.rules.length} · 候选{' '}
                    {autonomy.proposals.length} · 审批 {autonomy.approvals.length}
                  </small>
                </div>
              </article>
              <div className="steward-compact-list">
                {autonomy.runs.slice(0, 4).map((run) => (
                  <AutonomousRunRow key={run.id} run={run} />
                ))}
                {autonomy.runs.length === 0 ? <EmptyState text="暂无自主运行记录" /> : null}
              </div>
            </div>
          ) : (
            <EmptyState text="自主能力状态未加载" />
          )}
        </Panel>

        <Panel help={helpText.autonomyRules} title="自主规则">
          <div className="steward-table-list">
            {(autonomy?.rules ?? []).map((rule) => (
              <AutonomyRuleRow
                busy={busy !== null}
                key={rule.id}
                onUpdate={(id, payload) => runAction('更新自主规则', () => updateStewardAutonomyRule(id, payload))}
                rule={rule}
              />
            ))}
            {(autonomy?.rules ?? []).length === 0 ? <EmptyState text="暂无自主规则" /> : null}
          </div>
        </Panel>

        <Panel
          actions={
            <button
              className="steward-button steward-button-secondary"
              disabled={busy !== null || candidateProposalCount === 0}
              onClick={() =>
                runAction('批量清理候选', () =>
                  dismissStewardAutonomyProposals({
                    status: 'candidate',
                    limit: 100,
                    reason: 'workspace candidate cleanup',
                  }),
                )
              }
              type="button"
            >
              清理候选
            </button>
          }
          help={helpText.autonomyProposals}
          title={`自主候选 ${candidateProposalCount > 0 ? `(${candidateProposalCount})` : ''}`}
        >
          <div className="steward-table-list">
            {(autonomy?.proposals ?? []).map((proposal) => (
              <AutonomyProposalRow
                busy={busy !== null}
                key={proposal.id}
                onAction={runAction}
                proposal={proposal}
              />
            ))}
            {(autonomy?.proposals ?? []).length === 0 ? <EmptyState text="暂无自主候选" /> : null}
          </div>
        </Panel>
      </section>

      <section className="steward-list-layout">
        <Panel help={helpText.approvals} title="审批队列">
          <div className="steward-table-list">
            {(autonomy?.approvals ?? []).map((approval) => (
              <ApprovalRow
                approval={approval}
                busy={busy !== null}
                key={approval.id}
                onApprove={(id) => runAction('批准审批', () => approveStewardApprovalRequest(id, 'approved in steward workspace'))}
                onReject={(id) => runAction('拒绝审批', () => rejectStewardApprovalRequest(id, 'rejected in steward workspace'))}
              />
            ))}
            {(autonomy?.approvals ?? []).length === 0 ? <EmptyState text="暂无审批请求" /> : null}
          </div>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel help={helpText.collectors} title="采集器">
          <div className="steward-collector-list">
            {collectors.map((collector) => (
              <label className="steward-switch-row" key={collector.id}>
                <input
                  checked={collector.enabled}
                  disabled={busy !== null}
                  onChange={(event) =>
                    runAction('更新采集器', () =>
                      updateStewardCollector(collector.name, { enabled: event.currentTarget.checked }),
                    )
                  }
                  type="checkbox"
                />
                <span>
                  <InfoTerm help={collectorHelp[collector.name] ?? collector.scope_summary} label={collector.name} />
                  <small>{collector.scope_summary}</small>
                </span>
              </label>
            ))}
          </div>
        </Panel>

        <Panel help={helpText.search} title="统一搜索">
          <form className="steward-form" onSubmit={runSearch}>
            <input
              onChange={(event) => setSearchQuery(event.target.value)}
              placeholder="搜索标题、摘要、来源"
              value={searchQuery}
            />
            <select onChange={(event) => setSearchType(event.target.value)} value={searchType}>
              <option value="">全部实体</option>
              <option value="event">事件</option>
              <option value="timeline_segment">时间线</option>
              <option value="task">任务</option>
              <option value="intent">意图</option>
              <option value="memory">记忆</option>
              <option value="knowledge_item">知识</option>
            </select>
            <button className="steward-button" disabled={busy !== null} type="submit">
              搜索
            </button>
          </form>
          <div className="steward-compact-list">
            {searchResults.map((item) => (
              <article className="steward-compact-item" key={`${item.entity_type}-${item.id}`}>
                <strong>{item.title}</strong>
                <span>{entityText(item.entity_type)} · {statusText(item.status)} · {item.data_level}</span>
              </article>
            ))}
            {searchResults.length === 0 ? <EmptyState text="暂无搜索结果" /> : null}
          </div>
        </Panel>

        <Panel
          actions={
            <button className="steward-button steward-button-secondary" onClick={downloadExport} type="button">
              导出数据
            </button>
          }
          title="数据管理"
          help={helpText.dataManagement}
        >
          <form className="steward-form" onSubmit={submitTag}>
            <input
              onChange={(event) => setTagDraft((draft) => ({ ...draft, name: event.target.value }))}
              placeholder="新标签"
              value={tagDraft.name}
            />
            <select onChange={(event) => setTagDraft((draft) => ({ ...draft, type: event.target.value }))} value={tagDraft.type}>
              <option value="normal">普通</option>
              <option value="system">系统</option>
              <option value="sensitive">敏感</option>
              <option value="lifecycle">生命周期</option>
            </select>
            <button className="steward-button" disabled={busy !== null} type="submit">
              保存标签
            </button>
          </form>
          <div className="steward-tag-list">
            {tags.map((tag) => (
              <span className={`steward-tag steward-tag-${tag.type}`} key={tag.id}>
                {tag.name}
              </span>
            ))}
            {tags.length === 0 ? <EmptyState text="暂无标签" /> : null}
          </div>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel help={helpText.manualEvent} title="手动事件">
          <form className="steward-form" onSubmit={submitEvent}>
            <input
              onChange={(event) => setEventDraft((draft) => ({ ...draft, title: event.target.value }))}
              placeholder="事件标题"
              value={eventDraft.title}
            />
            <textarea
              onChange={(event) => setEventDraft((draft) => ({ ...draft, summary: event.target.value }))}
              placeholder="摘要"
              rows={3}
              value={eventDraft.summary}
            />
            <DataLevelSelect
              value={eventDraft.dataLevel}
              onChange={(value) => setEventDraft((draft) => ({ ...draft, dataLevel: value }))}
            />
            <button className="steward-button" disabled={busy !== null} type="submit">
              添加事件
            </button>
          </form>
        </Panel>

        <Panel help={helpText.manualTask} title="手动任务">
          <form className="steward-form" onSubmit={submitTask}>
            <input
              onChange={(event) => setTaskDraft((draft) => ({ ...draft, title: event.target.value }))}
              placeholder="任务标题"
              value={taskDraft.title}
            />
            <textarea
              onChange={(event) => setTaskDraft((draft) => ({ ...draft, description: event.target.value }))}
              placeholder="任务说明"
              rows={3}
              value={taskDraft.description}
            />
            <div className="steward-form-row">
              <select
                onChange={(event) =>
                  setTaskDraft((draft) => ({ ...draft, priority: event.target.value as TaskDraft['priority'] }))
                }
                value={taskDraft.priority}
              >
                <option value="low">低</option>
                <option value="normal">普通</option>
                <option value="high">高</option>
              </select>
              <input
                onChange={(event) => setTaskDraft((draft) => ({ ...draft, dueAt: event.target.value }))}
                type="datetime-local"
                value={taskDraft.dueAt}
              />
            </div>
            <DataLevelSelect
              value={taskDraft.dataLevel}
              onChange={(value) => setTaskDraft((draft) => ({ ...draft, dataLevel: value }))}
            />
            <button className="steward-button" disabled={busy !== null} type="submit">
              创建任务
            </button>
          </form>
        </Panel>

        <Panel help={helpText.candidateIntent} title="候选意图">
          <form className="steward-form" onSubmit={submitIntent}>
            <input
              onChange={(event) => setIntentDraft((draft) => ({ ...draft, title: event.target.value }))}
              placeholder="候选意图"
              value={intentDraft.title}
            />
            <textarea
              onChange={(event) => setIntentDraft((draft) => ({ ...draft, reason: event.target.value }))}
              placeholder="推断原因"
              rows={2}
              value={intentDraft.reason}
            />
            <input
              onChange={(event) => setIntentDraft((draft) => ({ ...draft, suggestedAction: event.target.value }))}
              placeholder="建议动作"
              value={intentDraft.suggestedAction}
            />
            <DataLevelSelect
              value={intentDraft.dataLevel}
              onChange={(value) => setIntentDraft((draft) => ({ ...draft, dataLevel: value }))}
            />
            <button className="steward-button" disabled={busy !== null} type="submit">
              加入候选池
            </button>
          </form>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel help={helpText.manualMemory} title="手动记忆">
          <form className="steward-form" onSubmit={submitMemory}>
            <input
              onChange={(event) => setMemoryDraft((draft) => ({ ...draft, title: event.target.value }))}
              placeholder="记忆标题"
              value={memoryDraft.title}
            />
            <input
              onChange={(event) => setMemoryDraft((draft) => ({ ...draft, scope: event.target.value }))}
              placeholder="适用范围，例如 global/project/device"
              value={memoryDraft.scope}
            />
            <textarea
              onChange={(event) => setMemoryDraft((draft) => ({ ...draft, content: event.target.value }))}
              placeholder="记忆内容"
              rows={3}
              value={memoryDraft.content}
            />
            <DataLevelSelect
              value={memoryDraft.dataLevel}
              onChange={(value) => setMemoryDraft((draft) => ({ ...draft, dataLevel: value }))}
            />
            <button className="steward-button" disabled={busy !== null} type="submit">
              写入记忆
            </button>
          </form>
        </Panel>

        <Panel help={helpText.knowledgeImport} title="知识导入">
          <form className="steward-form" onSubmit={submitKnowledge}>
            <input
              onChange={(event) => setKnowledgeDraft((draft) => ({ ...draft, title: event.target.value }))}
              placeholder="知识标题"
              value={knowledgeDraft.title}
            />
            <input
              onChange={(event) => setKnowledgeDraft((draft) => ({ ...draft, originalUri: event.target.value }))}
              placeholder="文件路径或 URL"
              value={knowledgeDraft.originalUri}
            />
            <textarea
              onChange={(event) => setKnowledgeDraft((draft) => ({ ...draft, summary: event.target.value }))}
              placeholder="摘要"
              rows={3}
              value={knowledgeDraft.summary}
            />
            <div className="steward-form-row">
              <select
                onChange={(event) => setKnowledgeDraft((draft) => ({ ...draft, type: event.target.value }))}
                value={knowledgeDraft.type}
              >
                <option value="note">笔记</option>
                <option value="document">文档</option>
                <option value="webpage">网页</option>
                <option value="code_snippet">代码片段</option>
                <option value="report">报告</option>
              </select>
              <label className="steward-inline-check">
                <input
                  checked={knowledgeDraft.allowIndex}
                  onChange={(event) => setKnowledgeDraft((draft) => ({ ...draft, allowIndex: event.target.checked }))}
                  type="checkbox"
                />
                <span>
                  检索
                  <HelpIcon text={helpText.indexing} />
                </span>
              </label>
            </div>
            <DataLevelSelect
              value={knowledgeDraft.dataLevel}
              onChange={(value) => setKnowledgeDraft((draft) => ({ ...draft, dataLevel: value }))}
            />
            <button className="steward-button" disabled={busy !== null} type="submit">
              导入知识
            </button>
          </form>
        </Panel>

        <Panel help={helpText.memoryCorrection} title="记忆纠正">
          {correctionDraft ? (
            <form className="steward-form" onSubmit={submitCorrection}>
              <input
                onChange={(event) => setCorrectionDraft((draft) => draft && { ...draft, title: event.target.value })}
                value={correctionDraft.title}
              />
              <textarea
                onChange={(event) => setCorrectionDraft((draft) => draft && { ...draft, content: event.target.value })}
                rows={3}
                value={correctionDraft.content}
              />
              <input
                onChange={(event) => setCorrectionDraft((draft) => draft && { ...draft, reason: event.target.value })}
                placeholder="纠正原因"
                value={correctionDraft.reason}
              />
              <button className="steward-button" disabled={busy !== null} type="submit">
                保存纠正
              </button>
            </form>
          ) : (
            <EmptyState text="选择一条记忆后可纠正" />
          )}
          <div className="steward-compact-list">
            {memoryVersions.map((version) => (
              <article className="steward-compact-item" key={version.id}>
                <strong>v{version.version} · {version.title}</strong>
                <span>{version.reason || '历史版本'} · {formatDate(version.created_at)}</span>
              </article>
            ))}
          </div>
        </Panel>
      </section>

      <section className="steward-list-layout">
        <Panel help={helpText.timeline} title="时间线">
          <div className="steward-table-list">
            {timelineSegments.map((segment) => (
              <article className="steward-list-item" key={segment.id}>
                <div>
                  <strong>{segment.title}</strong>
                  <p>{segment.summary || '无摘要'}</p>
                  <small>
                    {segment.event_count} 个事件 · {segment.data_level} · v{segment.version} · {formatDate(segment.start_at)}
                  </small>
                </div>
                <div className="steward-row-actions">
                  <button
                    className="steward-icon-button"
                    disabled={busy !== null}
                    onClick={() => showSources('timeline_segment', segment.id, segment.title)}
                    type="button"
                  >
                    来源
                  </button>
                  <button
                    className="steward-icon-button steward-danger"
                    disabled={busy !== null}
                    onClick={() => runAction('删除时间线', () => deleteStewardTimelineSegment(segment.id))}
                    type="button"
                  >
                    删除
                  </button>
                </div>
              </article>
            ))}
            {timelineSegments.length === 0 ? <EmptyState text="暂无时间线片段" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.events} title="最近事件">
          <div className="steward-table-list">
            {events.map((event) => (
              <EventRow busy={busy !== null} event={event} key={event.id} onAction={runAction} onSources={showSources} />
            ))}
            {events.length === 0 ? <EmptyState text="暂无事件" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.tasks} title="任务">
          <div className="steward-table-list">
            {tasks.map((task) => (
              <TaskRow busy={busy !== null} key={task.id} onAction={runAction} task={task} />
            ))}
            {tasks.length === 0 ? <EmptyState text="暂无任务" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.intents} title="意图候选池">
          <div className="steward-table-list">
            {intents.map((intent) => (
              <IntentRow busy={busy !== null} intent={intent} key={intent.id} onAction={runAction} onSources={showSources} />
            ))}
            {intents.length === 0 ? <EmptyState text="暂无候选意图" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.memoryLibrary} title="记忆库">
          <div className="steward-table-list">
            {memories.map((memory) => (
              <MemoryRow
                busy={busy !== null}
                key={memory.id}
                memory={memory}
                onAction={runAction}
                onSources={showSources}
                onVersions={showMemoryVersions}
              />
            ))}
            {memories.length === 0 ? <EmptyState text="暂无记忆" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.knowledgeLibrary} title="知识库">
          <div className="steward-table-list">
            {knowledgeItems.map((item) => (
              <KnowledgeRow busy={busy !== null} item={item} key={item.id} onAction={runAction} onSources={showSources} />
            ))}
            {knowledgeItems.length === 0 ? <EmptyState text="暂无知识条目" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.sourcePanel} title={`来源面板 · ${sourceTarget}`}>
          <div className="steward-table-list">
            {displayedSourceRefs.map((ref) => (
              <article className="steward-list-item steward-source-item" key={ref.id}>
                <div>
                  <strong>{entityText(ref.source_type)} · {ref.source_id || 'manual'}</strong>
                  <p>{ref.summary || ref.location || '无摘要'}</p>
                  <small>
                    可信度 {Math.round(ref.confidence * 100)}% · {ref.sensitive ? '敏感' : '非敏感'} · {formatDate(ref.created_at)}
                  </small>
                </div>
              </article>
            ))}
            {displayedSourceRefs.length === 0 ? <EmptyState text="暂无来源引用" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.audit} title="审计日志">
          <div className="steward-audit-list">
            {auditLogs.map((log) => (
              <AuditRow key={log.id} log={log} />
            ))}
            {auditLogs.length === 0 ? <EmptyState text="暂无审计日志" /> : null}
          </div>
        </Panel>
      </section>
    </div>
  )
}

function SyncChangeRow({ change }: { change: StewardSyncChange }) {
  return (
    <article className="steward-compact-item">
      <strong>
        #{change.sequence} · {entityText(change.entity_type)} · {statusText(change.operation)}
      </strong>
      <span>
        {statusText(change.sync_status)} · {change.origin_device_id} · {change.data_level} · v{change.version}
      </span>
    </article>
  )
}

function DeviceRow({
  device,
  busy,
  onRevoke,
  onSync,
  onVerify,
}: {
  device: StewardDevice
  busy: boolean
  onRevoke: (id: string) => Promise<void>
  onSync: (id: string) => Promise<void>
  onVerify: (id: string) => Promise<void>
}) {
  const isLocal = device.role === 'local'
  const canVerify = !isLocal && device.trust_status !== 'revoked' && Boolean(device.api_base_url && device.public_key)
  return (
    <article className="steward-list-item">
      <div>
        <strong>{device.device_name || device.id}</strong>
        <p>
          {device.platform} · {device.role} · {statusText(device.trust_status)}
        </p>
        <small>
          {device.permission_level} · {device.sync_enabled ? '同步开启' : '同步关闭'} · 最近{' '}
          {formatDate(device.last_seen_at)}
        </small>
        <small>
          {device.api_base_url || '未配置 peer API'} · 拉取序号 {device.last_sync_sequence ?? 0} · 发送序号{' '}
          {device.last_sent_sequence ?? 0} · 同步{' '}
          {formatDate(device.last_sync_at)}
        </small>
        {device.last_sync_error ? <small className="steward-error-text">{device.last_sync_error}</small> : null}
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy || isLocal || device.trust_status === 'revoked' || !device.api_base_url}
          onClick={() => onSync(device.id)}
          type="button"
        >
          同步
        </button>
        <button
          className="steward-icon-button"
          disabled={busy || !canVerify}
          onClick={() => onVerify(device.id)}
          type="button"
        >
          验证
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy || isLocal || device.trust_status === 'revoked'}
          onClick={() => onRevoke(device.id)}
          type="button"
        >
          撤销
        </button>
      </div>
    </article>
  )
}

function DevicePermissionRow({
  permission,
  device,
  capability,
  busy,
  onUpdate,
}: {
  permission: StewardDevicePermission
  device: StewardDevice | null
  capability: StewardDeviceCapability | null
  busy: boolean
  onUpdate: (
    deviceId: string,
    capability: string,
    payload: { policy?: string; max_permission_level?: string; scope_summary?: string },
  ) => Promise<void>
}) {
  const deviceName = device?.device_name || device?.id || permission.device_id
  const disabled = busy || device?.trust_status === 'revoked'
  const updatePayload = (patch: { policy?: string; max_permission_level?: string }) => ({
    policy: patch.policy ?? permission.policy,
    max_permission_level: patch.max_permission_level ?? permission.max_permission_level,
    scope_summary: permission.scope_summary,
  })
  return (
    <article className="steward-list-item">
      <div>
        <strong>{permission.capability}</strong>
        <p>{permission.scope_summary || capability?.description || '未设置权限范围说明'}</p>
        <small>
          {deviceName} · {capability?.target_type || '设备策略'} · 当前{statusText(permission.policy)} · 最高{' '}
          {permission.max_permission_level}
        </small>
      </div>
      <div className="steward-row-actions">
        <select
          className="steward-inline-select"
          disabled={disabled}
          onChange={(event) =>
            onUpdate(permission.device_id, permission.capability, updatePayload({ policy: event.currentTarget.value }))
          }
          value={permission.policy}
        >
          <option value="allow">允许</option>
          <option value="confirm">需确认</option>
          <option value="deny">拒绝</option>
        </select>
        <select
          className="steward-inline-select steward-inline-select-narrow"
          disabled={disabled}
          onChange={(event) =>
            onUpdate(
              permission.device_id,
              permission.capability,
              updatePayload({ max_permission_level: event.currentTarget.value }),
            )
          }
          value={permission.max_permission_level}
        >
          {['A0', 'A1', 'A2', 'A3', 'A4', 'A5', 'A6', 'A7', 'A8', 'A9'].map((level) => (
            <option key={level} value={level}>
              最高 {level}
            </option>
          ))}
        </select>
      </div>
    </article>
  )
}

function SyncConflictRow({
  conflict,
  busy,
  onResolve,
}: {
  conflict: StewardSyncConflict
  busy: boolean
  onResolve: (id: string) => Promise<void>
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>
          {entityText(conflict.entity_type)} · {statusText(conflict.status)}
        </strong>
        <p>{conflict.reason}</p>
        <small>
          {conflict.entity_id} · {formatDate(conflict.created_at)}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy || conflict.status === 'resolved'}
          onClick={() => onResolve(conflict.id)}
          type="button"
        >
          已人工处理
        </button>
      </div>
    </article>
  )
}

function AutonomyRuleRow({
  rule,
  busy,
  onUpdate,
}: {
  rule: StewardAutonomyRule
  busy: boolean
  onUpdate: (
    id: string,
    payload: { policy?: string; enabled?: boolean; max_permission_level?: string; scope_summary?: string },
  ) => Promise<void>
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{rule.name}</strong>
        <p>{rule.scope_summary}</p>
        <small>
          {rule.trigger_type} · {rule.risk_level} · {rule.max_permission_level}
        </small>
      </div>
      <div className="steward-row-actions">
        <label className="steward-inline-check">
          <input
            checked={rule.enabled}
            disabled={busy}
            onChange={(event) => onUpdate(rule.id, { enabled: event.currentTarget.checked })}
            type="checkbox"
          />
          <span>启用</span>
        </label>
        <select
          className="steward-inline-select"
          disabled={busy}
          onChange={(event) => onUpdate(rule.id, { policy: event.currentTarget.value })}
          value={rule.policy}
        >
          <option value="suggest">仅建议</option>
          <option value="confirm">需确认</option>
          <option value="auto">低风险自动</option>
          <option value="never">禁止</option>
        </select>
      </div>
    </article>
  )
}

function AutonomyProposalRow({
  proposal,
  busy,
  onAction,
}: {
  proposal: StewardAutonomyProposal
  busy: boolean
  onAction: (label: string, action: () => Promise<unknown>) => Promise<void>
}) {
  const executable = proposal.status === 'approved' || proposal.policy === 'auto'
  return (
    <article className="steward-list-item">
      <div>
        <strong>{proposal.title}</strong>
        <p>{proposal.trigger_reason || proposal.summary}</p>
        {proposal.score_reason ? <p>评分依据：{proposal.score_reason}</p> : null}
        <small>
          候选分 {Math.round(proposal.score * 100)}% · {statusText(proposal.status)} ·{' '}
          {statusText(proposal.policy)} · {proposal.action} · {proposal.risk_level} · {proposal.permission_level}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() => onAction('模拟自主候选', () => simulateStewardAutonomyProposal(proposal.id))}
          type="button"
        >
          模拟
        </button>
        <button
          className="steward-icon-button"
          disabled={busy || proposal.status !== 'candidate'}
          onClick={() => onAction('批准自主候选', () => approveStewardAutonomyProposal(proposal.id))}
          type="button"
        >
          批准
        </button>
        <button
          className="steward-icon-button"
          disabled={busy || !executable}
          onClick={() => onAction('执行自主候选', () => executeStewardAutonomyProposal(proposal.id))}
          type="button"
        >
          执行
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy || proposal.status === 'dismissed'}
          onClick={() => onAction('忽略自主候选', () => dismissStewardAutonomyProposal(proposal.id))}
          type="button"
        >
          忽略
        </button>
      </div>
    </article>
  )
}

function ApprovalRow({
  approval,
  busy,
  onApprove,
  onReject,
}: {
  approval: StewardApprovalRequest
  busy: boolean
  onApprove: (id: string) => Promise<void>
  onReject: (id: string) => Promise<void>
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{approval.requested_action}</strong>
        <p>{approval.plan_summary || approval.risk_summary}</p>
        <small>
          {statusText(approval.status)} · {formatDate(approval.created_at)}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy || approval.status !== 'pending'}
          onClick={() => onApprove(approval.id)}
          type="button"
        >
          批准
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy || approval.status !== 'pending'}
          onClick={() => onReject(approval.id)}
          type="button"
        >
          拒绝
        </button>
      </div>
    </article>
  )
}

function AutonomousRunRow({ run }: { run: StewardAutonomousRun }) {
  return (
    <article className="steward-compact-item">
      <strong>
        {statusText(run.mode)} · {statusText(run.status)}
      </strong>
      <span>{run.impact_summary || run.recovery_hint || run.trigger_reason}</span>
    </article>
  )
}

function EventRow({
  event,
  busy,
  onAction,
  onSources,
}: {
  event: StewardEvent
  busy: boolean
  onAction: (label: string, action: () => Promise<unknown>) => Promise<void>
  onSources: (entityType: string, id: string, title: string) => Promise<void>
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{event.title}</strong>
        <p>{event.summary || '无摘要'}</p>
        <small>
          {event.type} · {event.source} · {event.data_level} · v{event.version} · {formatDate(event.created_at)}
        </small>
      </div>
      <div className="steward-row-actions">
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('事件转任务', () => convertStewardEvent(event.id, 'task'))} type="button">
          转任务
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('事件转意图', () => convertStewardEvent(event.id, 'intent'))} type="button">
          转意图
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('事件转记忆', () => convertStewardEvent(event.id, 'memory'))} type="button">
          转记忆
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('事件转知识', () => convertStewardEvent(event.id, 'knowledge'))} type="button">
          转知识
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('事件入时间线', () => convertStewardEvent(event.id, 'timeline'))} type="button">
          入时间线
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onSources('event', event.id, event.title)} type="button">
          来源
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('隐藏事件', () => hideStewardEvent(event.id))} type="button">
          隐藏
        </button>
        <button className="steward-icon-button steward-danger" disabled={busy} onClick={() => onAction('删除事件', () => deleteStewardEvent(event.id))} type="button">
          删除
        </button>
      </div>
    </article>
  )
}

function TaskRow({
  task,
  busy,
  onAction,
}: {
  task: StewardTask
  busy: boolean
  onAction: (label: string, action: () => Promise<unknown>) => Promise<void>
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{task.title}</strong>
        <p>{task.description || '无说明'}</p>
        <small>
          {statusText(task.status)} · {priorityText(task.priority)} · {task.data_level} · v{task.version} · 截止{' '}
          {formatDate(task.due_at)}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy || task.status === 'done'}
          onClick={() => onAction('完成任务', () => completeStewardTask(task.id))}
          type="button"
        >
          完成
        </button>
        <button
          className="steward-icon-button"
          disabled={busy || task.status === 'canceled'}
          onClick={() => onAction('取消任务', () => cancelStewardTask(task.id))}
          type="button"
        >
          取消
        </button>
        <button className="steward-icon-button steward-danger" disabled={busy} onClick={() => onAction('删除任务', () => deleteStewardTask(task.id))} type="button">
          删除
        </button>
      </div>
    </article>
  )
}

function IntentRow({
  intent,
  busy,
  onAction,
  onSources,
}: {
  intent: StewardIntent
  busy: boolean
  onAction: (label: string, action: () => Promise<unknown>) => Promise<void>
  onSources: (entityType: string, id: string, title: string) => Promise<void>
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{intent.title}</strong>
        <p>{intent.reason || intent.summary || '无原因'}</p>
        <small>
          {statusText(intent.status)} · 可信度 {Math.round(intent.confidence * 100)}% · {intent.data_level} · v{intent.version}
        </small>
      </div>
      <div className="steward-row-actions">
        <button className="steward-icon-button" disabled={busy || intent.status === 'accepted'} onClick={() => onAction('接受意图', () => acceptStewardIntent(intent.id))} type="button">
          接受
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('忽略意图', () => dismissStewardIntent(intent.id))} type="button">
          忽略
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('静音意图', () => muteStewardIntent(intent.id))} type="button">
          静音
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onSources('intent', intent.id, intent.title)} type="button">
          来源
        </button>
        <button className="steward-icon-button steward-danger" disabled={busy} onClick={() => onAction('删除意图', () => deleteStewardIntent(intent.id))} type="button">
          删除
        </button>
      </div>
    </article>
  )
}

function MemoryRow({
  memory,
  busy,
  onAction,
  onSources,
  onVersions,
}: {
  memory: StewardMemory
  busy: boolean
  onAction: (label: string, action: () => Promise<unknown>) => Promise<void>
  onSources: (entityType: string, id: string, title: string) => Promise<void>
  onVersions: (memory: StewardMemory) => Promise<void>
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{memory.title}</strong>
        <p>{memory.summary || memory.content || '无内容'}</p>
        <small>
          {statusText(memory.status)} · {memory.scope} · {memory.data_level} · v{memory.version} ·{' '}
          {memory.user_confirmed ? '已确认' : '未确认'}
        </small>
      </div>
      <div className="steward-row-actions">
        <button className="steward-icon-button" disabled={busy} onClick={() => onVersions(memory)} type="button">
          纠正
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onSources('memory', memory.id, memory.title)} type="button">
          来源
        </button>
        <button className="steward-icon-button" disabled={busy} onClick={() => onAction('归档记忆', () => archiveStewardMemory(memory.id))} type="button">
          归档
        </button>
        <button className="steward-icon-button steward-danger" disabled={busy} onClick={() => onAction('删除记忆', () => deleteStewardMemory(memory.id))} type="button">
          删除
        </button>
      </div>
    </article>
  )
}

function KnowledgeRow({
  item,
  busy,
  onAction,
  onSources,
}: {
  item: StewardKnowledgeItem
  busy: boolean
  onAction: (label: string, action: () => Promise<unknown>) => Promise<void>
  onSources: (entityType: string, id: string, title: string) => Promise<void>
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{item.title}</strong>
        <p>{item.summary || item.original_uri || '无摘要'}</p>
        <small>
          {item.type} · {item.data_level} · v{item.version} · {item.allow_index ? '可检索' : '不进检索'}
        </small>
      </div>
      <div className="steward-row-actions">
        <button className="steward-icon-button" disabled={busy} onClick={() => onSources('knowledge_item', item.id, item.title)} type="button">
          来源
        </button>
        <button className="steward-icon-button steward-danger" disabled={busy} onClick={() => onAction('删除知识', () => deleteStewardKnowledgeItem(item.id))} type="button">
          删除
        </button>
      </div>
    </article>
  )
}

function AuditRow({ log }: { log: StewardAuditLog }) {
  return (
    <article className="steward-audit-item">
      <span>{log.action}</span>
      <strong>{entityText(log.target_type)}</strong>
      <small>
        {log.permission_level} · {log.data_level} · v{log.version} · {formatDate(log.occurred_at)}
      </small>
      <p>{log.after_summary || log.output_summary || log.input_summary}</p>
    </article>
  )
}

function DataLevelSelect({ value, onChange }: { value: string; onChange: (value: string) => void }) {
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

function Panel({
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

function Metric({ label, value }: { label: string; value: number }) {
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

function InfoTerm({ label, help }: { label: string; help: string }) {
  return (
    <strong className="steward-term-title">
      {label}
      <HelpIcon text={help} />
    </strong>
  )
}

function HelpIcon({ text }: { text?: string }) {
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

function EmptyState({ text }: { text: string }) {
  return <div className="steward-empty">{text}</div>
}
