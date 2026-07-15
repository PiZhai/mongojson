import type { StewardAutonomyAdvisorStatus } from '../../../types/tooling'

export const dataLevels = [
  ['D0', 'D0 临时/低敏'],
  ['D1', 'D1 公开资料'],
  ['D2', 'D2 本地元数据'],
  ['D3', 'D3 用户内容'],
  ['D4', 'D4 敏感内容'],
  ['D5', 'D5 凭据'],
  ['D6', 'D6 高风险个人数据'],
] as const

export const helpText: Record<string, string> = {
  localSteward: '本机上的私人管家进程，负责本地数据、受控同步、对话、模型队列和自主候选；高权限动作必须通过独立策略和结构化执行器。',
  events: '事件是最小事实单元，用来记录你手动输入、导入或采集到的一次信息。',
  timeline: '时间线把多个事件聚合成一个时间片段，方便回看某段时间发生了什么。',
  tasks: '任务是你确认需要处理的事项，可以手动创建，也可以由事件或意图转化。',
  intents: '意图是系统或你自己记录的候选想法，不会直接执行，需要确认后才能转成任务。',
  memories: '记忆保存长期事实、偏好、决策和项目上下文，需要来源或用户确认。',
  knowledge: '知识保存资料、笔记、网页或文件摘要，不等同于关于你的长期事实。',
  sources: '来源引用记录一个任务、记忆或知识从哪里来，用于追溯和纠错。',
  audit: '审计日志记录关键数据变更、导出、删除、权限和工具动作。',
  collectors: '采集器是数据入口。启用采集器后，数据仍需通过等级与来源策略；两者缺一不可。',
  search: '统一搜索会在事件、时间线、任务、意图、记忆和知识中查找标题与摘要。',
  dataManagement: '数据管理用于导出、标签和敏感标记，不会默认导出高敏内容。',
  sync: '三端同步在 S3 采用本地优先变更队列、设备身份、权限和冲突显式处理；当前不做静默覆盖。',
  devices: '设备列表展示本地和已登记的对端设备，撤销后该设备不能继续参与同步。',
  syncChanges: '同步变更是可传输的最小数据包，记录实体、版本、来源设备和应用状态。',
  conflicts: '冲突需要人工处理；系统不会用远端版本静默覆盖本地数据。',
  autonomy: '自主能力默认只允许低风险执行。A4-A9 可逐动作授权，但还必须满足全局上限、规则、模拟、回滚和结构化执行器门禁。',
  autonomyRules: '规则定义候选任务的触发、策略和最高权限，可设置建议、确认、自动或永不执行。',
  autonomyProposals: '候选建议展示触发原因、影响范围和策略，执行前可模拟或审批。',
  approvals: '审批队列用于需确认或被阻断的动作；批准不会绕过当前权限策略、全局上限或执行器能力。',
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

export const metricHelp: Record<string, string> = {
  事件: helpText.events,
  时间线: helpText.timeline,
  任务: helpText.tasks,
  意图: helpText.intents,
  记忆: helpText.memories,
  知识: helpText.knowledge,
  来源: helpText.sources,
  审计: helpText.audit,
}

export const collectorHelp: Record<string, string> = {
  'manual-input': helpText.manualInput,
  'browser-link': helpText.browserLink,
  'clipboard-summary': helpText.clipboardSummary,
  'system-status': helpText.systemStatus,
  'watched-directory': helpText.watchedDirectory,
}

export function formatDate(value?: string | null) {
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

export function statusText(status: string) {
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

export function advisorStatusText(advisor?: StewardAutonomyAdvisorStatus | null) {
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

export function priorityText(priority: string) {
  const map: Record<string, string> = {
    low: '低',
    normal: '普通',
    high: '高',
  }
  return map[priority] ?? priority
}

export function entityText(entityType: string) {
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

export function isSensitiveLevel(level: string) {
  return ['D4', 'D5', 'D6'].includes(level)
}
