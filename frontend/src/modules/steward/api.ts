import type {
  FileSummary,
  JobSummary,
  MemoFloatingCardRecord,
  MemoRecord,
  PresetRecord,
  StewardAgentStatus,
  StewardActivitySession,
  StewardApprovalRequest,
  StewardSignedApprovalProof,
  StewardAuditLog,
  StewardAutonomousRun,
  StewardAutonomyBulkDismissResult,
  StewardAutonomyOverview,
  StewardAutonomyProposal,
  StewardAutonomyRule,
  StewardAutonomySettings,
  StewardCollectorConfig,
  StewardDataPolicy,
  StewardPermissionPolicy,
  StewardModelDispatch,
  StewardProactiveRun,
  StewardModelSettings,
  StewardToolDefinition,
  StewardCatalogTool,
  StewardToolHostStatus,
  StewardConversation,
  StewardConversationExecution,
  StewardConversationMessage,
  StewardConversationSuggestion,
  StewardDataTag,
  StewardDevice,
  StewardDevicePermission,
  StewardDeviceSyncResult,
  StewardDeviceTrustVerification,
  StewardEvent,
  StewardIntent,
  StewardInsight,
  StewardKnowledgeItem,
  StewardMemory,
  StewardMemoryVersion,
  StewardEntity,
  StewardHabit,
  StewardLifecycleEvaluation,
  StewardLifecycleStatus,
  StewardObservation,
  StewardPurgeResult,
  StewardRelation,
  StewardRetentionPolicy,
  StewardOverview,
  StewardSearchResult,
  StewardSourceRef,
  StewardSyncChange,
  StewardSyncConflict,
  StewardSyncStatus,
  StewardTask,
  StewardTimelineSegment,
  StewardAgentRun,
  StewardAgentRunSummary,
  StewardEvidenceArtifact,
  StewardRunEvent,
  StewardRuntimeExecutionControl,
  StewardRuntimePlannerStatus,
} from './types'

const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '/api'

function resolveApiUrl(path: string) {
  return new URL(`${API_BASE}${path}`, window.location.origin)
}

async function request<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const response = await fetch(input, init)
  if (!response.ok) {
    const message = await response.text()
    throw new Error(message || `Request failed: ${response.status}`)
  }
  return (await response.json()) as T
}

export async function uploadFile(file: File) {
  const formData = new FormData()
  formData.append('file', file)
  return request<{ file: FileSummary }>(`${API_BASE}/files`, {
    method: 'POST',
    body: formData,
  })
}

export async function createJob(payload: {
  tool_type: string
  input_file_id?: string
  params?: Record<string, unknown>
}) {
  return request<{ job: JobSummary }>(`${API_BASE}/jobs`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function getJob(id: string) {
  return request<{ job: JobSummary }>(`${API_BASE}/jobs/${id}`)
}

export async function getPresets(toolType?: string) {
  const query = toolType ? `?tool_type=${encodeURIComponent(toolType)}` : ''
  return request<{ presets: PresetRecord[] }>(`${API_BASE}/presets${query}`)
}

export async function savePreset(payload: {
  tool_type: string
  name: string
  payload: Record<string, unknown>
}) {
  return request<{ preset: PresetRecord }>(`${API_BASE}/presets`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export function getFileDownloadUrl(id: string) {
  return `${API_BASE}/files/${id}/download`
}

export function getWatchRoomWebSocketUrl(roomId: string, clientId: string) {
  const url = resolveApiUrl(`/watch/rooms/${encodeURIComponent(roomId)}/ws`)
  url.searchParams.set('client_id', clientId)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

export async function getMemo(slug = 'inbox') {
  const query = slug ? `?slug=${encodeURIComponent(slug)}` : ''
  return request<{ memo: MemoRecord }>(`${API_BASE}/memo${query}`)
}

export async function saveMemo(payload: {
  slug?: string
  title: string
  content_html: string
  content_text: string
  floating_cards?: MemoFloatingCardRecord[]
}) {
  return request<{ memo: MemoRecord }>(`${API_BASE}/memo`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function getStewardOverview() {
  return request<{ overview: StewardOverview }>(`${API_BASE}/steward/overview`)
}

export async function startStewardAgent() {
  return request<{ agent: StewardAgentStatus }>(`${API_BASE}/steward/agent/start`, {
    method: 'POST',
  })
}

export async function stopStewardAgent() {
  return request<{ agent: StewardAgentStatus }>(`${API_BASE}/steward/agent/stop`, {
    method: 'POST',
  })
}

export async function updateStewardCollector(
  name: string,
  payload: {
    enabled?: boolean
    scope_summary?: string
    settings?: Record<string, unknown>
  },
) {
  return request<{ collector: StewardCollectorConfig }>(`${API_BASE}/steward/collectors/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function getStewardDataPolicies() {
  return request<{ data_policies: StewardDataPolicy[] }>(`${API_BASE}/steward/automation/data-policies`)
}

export async function updateStewardDataPolicy(payload: Partial<StewardDataPolicy> & {
  data_level: string
  source_pattern: string
}) {
  return request<{ data_policy: StewardDataPolicy }>(`${API_BASE}/steward/automation/data-policies`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardPermissionPolicies() {
  return request<{ permission_policies: StewardPermissionPolicy[] }>(`${API_BASE}/steward/automation/permission-policies`)
}

export async function updateStewardPermissionPolicy(payload: Partial<StewardPermissionPolicy> & {
  permission_level: string
  action_pattern: string
}) {
  return request<{ permission_policy: StewardPermissionPolicy }>(`${API_BASE}/steward/automation/permission-policies`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardModelDispatches(limit = 60) {
  return request<{ model_dispatches: StewardModelDispatch[] }>(`${API_BASE}/steward/automation/model-dispatches?limit=${limit}`)
}

export async function runStewardModelDispatches(limit = 20) {
  return request<{ model_dispatches: StewardModelDispatch[] }>(`${API_BASE}/steward/automation/model-dispatches/run?limit=${limit}`, {
    method: 'POST',
  })
}

export async function getStewardProactiveRuns(limit = 50) {
  return request<{ runs: StewardProactiveRun[] }>(`${API_BASE}/steward/proactive/runs?limit=${limit}`)
}

export async function runStewardProactiveCycle(payload: { force?: boolean; cadence?: 'daily' | 'weekly' | 'all' } = {}) {
  return request<{ runs: StewardProactiveRun[] }>(`${API_BASE}/steward/proactive/run`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardToolDefinitions() {
  return request<{ tools: StewardToolDefinition[] }>(`${API_BASE}/steward/automation/tools`)
}

export async function updateStewardToolDefinition(payload: Omit<StewardToolDefinition, 'id' | 'created_at' | 'updated_at'>) {
  return request<{ tool: StewardToolDefinition }>(`${API_BASE}/steward/automation/tools`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardObservations(limit = 100) {
  return request<{ observations: StewardObservation[] }>(`${API_BASE}/steward/activity/observations?limit=${limit}`)
}

export async function createStewardObservation(payload: Record<string, unknown>) {
  return request<{ observation: StewardObservation }>(`${API_BASE}/steward/activity/observations`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardActivitySessions(limit = 100) {
  return request<{ sessions: StewardActivitySession[] }>(`${API_BASE}/steward/activity/sessions?limit=${limit}`)
}

export async function getStewardEntities(limit = 100) {
  return request<{ entities: StewardEntity[] }>(`${API_BASE}/steward/entities?limit=${limit}`)
}

export async function getStewardEntityRelations(id: string, limit = 100) {
  return request<{ relations: StewardRelation[] }>(`${API_BASE}/steward/entities/${encodeURIComponent(id)}/relations?limit=${limit}`)
}

export async function getStewardHabits(limit = 100) {
  return request<{ habits: StewardHabit[] }>(`${API_BASE}/steward/habits?limit=${limit}`)
}

export async function updateStewardHabit(id: string, payload: Record<string, unknown>) {
  return request<{ habit: StewardHabit }>(`${API_BASE}/steward/habits/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardInsights(limit = 100) {
  return request<{ insights: StewardInsight[] }>(`${API_BASE}/steward/insights?limit=${limit}`)
}

export async function updateStewardInsight(id: string, payload: Record<string, unknown>) {
  return request<{ insight: StewardInsight }>(`${API_BASE}/steward/insights/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardLifecycleStatus() {
  return request<{ lifecycle: StewardLifecycleStatus }>(`${API_BASE}/steward/lifecycle/status`)
}

export async function evaluateStewardLifecycle(limit = 1000) {
  return request<{ evaluation: StewardLifecycleEvaluation }>(`${API_BASE}/steward/lifecycle/evaluate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ limit }),
  })
}

export async function purgeStewardLifecycle(evaluationId: string) {
  return request<{ purge: StewardPurgeResult }>(`${API_BASE}/steward/lifecycle/purge`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ evaluation_id: evaluationId, execute: true }),
  })
}

export async function getStewardRetentionPolicies() {
  return request<{ retention_policies: StewardRetentionPolicy[] }>(`${API_BASE}/steward/retention-policies`)
}

export async function updateStewardRetentionPolicy(id: string, payload: Record<string, unknown>) {
  return request<{ retention_policy: StewardRetentionPolicy }>(`${API_BASE}/steward/retention-policies/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardConversations(limit = 30, archived = false) {
  return request<{ conversations: StewardConversation[] }>(
    `${API_BASE}/steward/conversations?limit=${limit}&archived=${archived}`,
  )
}

export async function getStewardTools(query = '') {
  const params = new URLSearchParams()
  if (query.trim()) params.set('query', query.trim())
  const suffix = params.size ? `?${params.toString()}` : ''
  return request<{ tools: StewardCatalogTool[]; hosts: StewardToolHostStatus[] }>(`${API_BASE}/steward/tools${suffix}`)
}

export async function getStewardTool(name: string) {
  return request<{ tool: StewardCatalogTool }>(`${API_BASE}/steward/tools/${encodeURIComponent(name)}`)
}

export async function decideStewardTool(name: string, decision: 'enable' | 'disable' | 'test' | 'rollback' | 'delete', version?: string) {
  return request<{ tool: StewardCatalogTool }>(`${API_BASE}/steward/tools/${encodeURIComponent(name)}/decision`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ decision, version }),
  })
}

export async function createStewardConversation(payload: { title?: string; data_level?: string }) {
  return request<{ conversation: StewardConversation }>(`${API_BASE}/steward/conversations`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function getStewardConversationMessages(id: string, limit = 100) {
  return request<{ messages: StewardConversationMessage[] }>(
    `${API_BASE}/steward/conversations/${encodeURIComponent(id)}/messages?limit=${limit}`,
  )
}

export async function sendStewardConversationMessage(
  id: string,
  payload: { content: string; data_level?: string; context_limit?: number },
) {
  return request<{ conversation: StewardConversation; message: StewardConversationMessage }>(
    `${API_BASE}/steward/conversations/${encodeURIComponent(id)}/messages`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    },
  )
}

export async function decideStewardConversationSuggestion(id: string, decision: 'accepted' | 'dismissed') {
  return request<{ suggestion: StewardConversationSuggestion }>(
    `${API_BASE}/steward/conversation-suggestions/${encodeURIComponent(id)}/decision`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision }),
    },
  )
}

export async function decideStewardConversationExecution(
  id: string,
  decision: 'confirm' | 'pause' | 'cancel',
  reason = '',
  approvalProof?: StewardSignedApprovalProof,
) {
  return request<{ execution: StewardConversationExecution }>(
    `${API_BASE}/steward/conversation-executions/${encodeURIComponent(id)}/decision`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision, reason, approval_proof: approvalProof }),
    },
  )
}

export async function updateStewardConversation(id: string, payload: { archived: boolean }) {
  return request<{ conversation: StewardConversation }>(`${API_BASE}/steward/conversations/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function decideStewardAgentEpisode(
  id: string,
  decision: 'pause' | 'resume' | 'cancel' | 'switch_device',
  targetDeviceID = '',
) {
  return request<{ episode: import('./types').StewardAgentEpisode }>(
    `${API_BASE}/steward/agent-episodes/${encodeURIComponent(id)}/decision`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision, target_device_id: targetDeviceID }),
    },
  )
}

export async function createStewardEvent(payload: {
  type?: string
  title: string
  summary?: string
  source?: string
  data_level?: string
  permission_level?: string
  user_confirmed?: boolean
}) {
  return request<{ event: StewardEvent }>(`${API_BASE}/steward/events`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function hideStewardEvent(id: string) {
  return request<{ event: StewardEvent }>(`${API_BASE}/steward/events/${id}/hide`, {
    method: 'PATCH',
  })
}

export async function deleteStewardEvent(id: string) {
  return request<{ status: string }>(`${API_BASE}/steward/events/${id}`, {
    method: 'DELETE',
  })
}

export async function convertStewardEvent(id: string, targetType: string) {
  return request<Record<string, unknown>>(`${API_BASE}/steward/events/${id}/convert`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ target_type: targetType }),
  })
}

export async function getStewardTimelineSegments(limit = 50) {
  return request<{ timeline_segments: StewardTimelineSegment[] }>(`${API_BASE}/steward/timeline-segments?limit=${limit}`)
}

export async function deleteStewardTimelineSegment(id: string) {
  return request<{ status: string }>(`${API_BASE}/steward/timeline-segments/${id}`, {
    method: 'DELETE',
  })
}

export async function createStewardTask(payload: {
  type?: string
  title: string
  description?: string
  priority?: string
  due_at?: string | null
  source?: string
  data_level?: string
  permission_level?: string
  risk_level?: string
  user_confirmed?: boolean
}) {
  return request<{ task: StewardTask }>(`${API_BASE}/steward/tasks`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function updateStewardTask(
  id: string,
  payload: {
    title?: string
    description?: string
    status?: string
    priority?: string
    due_at?: string | null
  },
) {
  return request<{ task: StewardTask }>(`${API_BASE}/steward/tasks/${id}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function completeStewardTask(id: string) {
  return request<{ task: StewardTask }>(`${API_BASE}/steward/tasks/${id}/complete`, {
    method: 'POST',
  })
}

export async function cancelStewardTask(id: string) {
  return request<{ task: StewardTask }>(`${API_BASE}/steward/tasks/${id}/cancel`, {
    method: 'POST',
  })
}

export async function deleteStewardTask(id: string) {
  return request<{ status: string }>(`${API_BASE}/steward/tasks/${id}`, {
    method: 'DELETE',
  })
}

export async function createStewardIntent(payload: {
  type?: string
  title: string
  summary?: string
  reason?: string
  suggested_action?: string
  risk_level?: string
  source?: string
  data_level?: string
  permission_level?: string
  confidence?: number
  user_confirmed?: boolean
}) {
  return request<{ intent: StewardIntent }>(`${API_BASE}/steward/intents`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function acceptStewardIntent(id: string) {
  return request<{ task: StewardTask }>(`${API_BASE}/steward/intents/${id}/accept`, {
    method: 'POST',
  })
}

export async function dismissStewardIntent(id: string) {
  return request<{ intent: StewardIntent }>(`${API_BASE}/steward/intents/${id}/dismiss`, {
    method: 'POST',
  })
}

export async function muteStewardIntent(id: string) {
  return request<{ intent: StewardIntent }>(`${API_BASE}/steward/intents/${id}/mute`, {
    method: 'POST',
  })
}

export async function deleteStewardIntent(id: string) {
  return request<{ status: string }>(`${API_BASE}/steward/intents/${id}`, {
    method: 'DELETE',
  })
}

export async function createStewardMemory(payload: {
  type?: string
  title: string
  summary?: string
  content?: string
  scope?: string
  source?: string
  data_level?: string
  permission_level?: string
  confidence?: number
  user_confirmed?: boolean
}) {
  return request<{ memory: StewardMemory }>(`${API_BASE}/steward/memories`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function correctStewardMemory(
  id: string,
  payload: {
    title?: string
    summary?: string
    content?: string
    reason?: string
  },
) {
  return request<{ memory: StewardMemory }>(`${API_BASE}/steward/memories/${id}/correct`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function archiveStewardMemory(id: string) {
  return request<{ memory: StewardMemory }>(`${API_BASE}/steward/memories/${id}/archive`, {
    method: 'POST',
  })
}

export async function deleteStewardMemory(id: string) {
  return request<{ status: string }>(`${API_BASE}/steward/memories/${id}`, {
    method: 'DELETE',
  })
}

export async function getStewardMemoryVersions(id: string) {
  return request<{ versions: StewardMemoryVersion[] }>(`${API_BASE}/steward/memories/${id}/versions`)
}

export async function createStewardKnowledgeItem(payload: {
  type?: string
  title: string
  summary?: string
  source?: string
  original_uri?: string
  import_method?: string
  data_level?: string
  permission_level?: string
  allow_index?: boolean
  user_confirmed?: boolean
}) {
  return request<{ knowledge_item: StewardKnowledgeItem }>(`${API_BASE}/steward/knowledge-items`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function deleteStewardKnowledgeItem(id: string) {
  return request<{ status: string }>(`${API_BASE}/steward/knowledge-items/${id}`, {
    method: 'DELETE',
  })
}

export async function getStewardSourceRefs(targetType?: string, targetId?: string) {
  const params = new URLSearchParams()
  if (targetType) {
    params.set('target_type', targetType)
  }
  if (targetId) {
    params.set('target_id', targetId)
  }
  const query = params.toString() ? `?${params}` : ''
  return request<{ source_refs: StewardSourceRef[] }>(`${API_BASE}/steward/source-refs${query}`)
}

export async function createStewardTag(payload: {
  name: string
  type?: string
  color?: string
  description?: string
}) {
  return request<{ tag: StewardDataTag }>(`${API_BASE}/steward/tags`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function assignStewardTag(payload: {
  entity_type: string
  entity_id: string
  tag_id: string
  source?: string
  confidence?: number
}) {
  return request<{ status: string }>(`${API_BASE}/steward/tags/assign`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function searchStewardData(params: {
  q?: string
  entity_type?: string
  status?: string
  data_level?: string
  limit?: number
}) {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') {
      query.set(key, String(value))
    }
  })
  return request<{ results: StewardSearchResult[] }>(`${API_BASE}/steward/search?${query}`)
}

export async function getStewardAuditLogs(limit = 50) {
  return request<{ audit_logs: StewardAuditLog[] }>(`${API_BASE}/steward/audit-logs?limit=${limit}`)
}

export async function exportStewardData(includeSensitive = false) {
  return request<{ export: StewardOverview }>(
    `${API_BASE}/steward/export?include_sensitive=${includeSensitive ? 'true' : 'false'}`,
  )
}

export async function getStewardSyncStatus() {
  return request<{ sync: StewardSyncStatus }>(`${API_BASE}/steward/sync/status`)
}

export async function getStewardSyncChanges(sinceSequence = 0, limit = 50) {
  const query = new URLSearchParams({ since_sequence: String(sinceSequence), limit: String(limit) })
  return request<{ changes: StewardSyncChange[] }>(`${API_BASE}/steward/sync/changes?${query}`)
}

export async function createStewardSyncChange(payload: {
  entity_type: string
  entity_id: string
  operation: string
  origin_device_id?: string
  version?: number
  data_level?: string
  payload?: Record<string, unknown>
}) {
  return request<{ change: StewardSyncChange }>(`${API_BASE}/steward/sync/changes`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function importStewardSyncChanges(payload: {
  device?: Partial<StewardDevice>
  changes: Array<{
    id?: string
    entity_type: string
    entity_id: string
    operation: string
    origin_device_id?: string
    version?: number
    data_level?: string
    payload?: Record<string, unknown>
  }>
}) {
  return request<{
    result: {
      imported: number
      applied: number
      conflicts: StewardSyncConflict[]
      changes: StewardSyncChange[]
    }
  }>(`${API_BASE}/steward/sync/changes/import`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function getStewardSyncConflicts(status = '', limit = 50) {
  const query = new URLSearchParams({ limit: String(limit) })
  if (status) {
    query.set('status', status)
  }
  return request<{ conflicts: StewardSyncConflict[] }>(`${API_BASE}/steward/sync/conflicts?${query}`)
}

export async function resolveStewardSyncConflict(id: string, resolution: string) {
  return request<{ conflict: StewardSyncConflict }>(`${API_BASE}/steward/sync/conflicts/${id}/resolve`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ resolution }),
  })
}

export async function getStewardDevices() {
  return request<{ devices: StewardDevice[] }>(`${API_BASE}/steward/devices`)
}

export async function registerStewardDevice(payload: Partial<StewardDevice>) {
  return request<{ device: StewardDevice }>(`${API_BASE}/steward/devices`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function revokeStewardDevice(id: string) {
  return request<{ device: StewardDevice }>(`${API_BASE}/steward/devices/${id}/revoke`, {
    method: 'POST',
  })
}

export async function syncStewardDevice(id: string) {
  return request<{ sync: StewardDeviceSyncResult }>(`${API_BASE}/steward/devices/${encodeURIComponent(id)}/sync`, {
    method: 'POST',
  })
}

export async function verifyStewardDeviceTrust(id: string) {
  return request<{ verification: StewardDeviceTrustVerification }>(
    `${API_BASE}/steward/devices/${encodeURIComponent(id)}/verify`,
    {
      method: 'POST',
    },
  )
}

export async function getStewardDevicePermissions(deviceId: string) {
  return request<{ permissions: StewardDevicePermission[] }>(
    `${API_BASE}/steward/devices/${encodeURIComponent(deviceId)}/permissions`,
  )
}

export async function updateStewardDevicePermission(
  deviceId: string,
  capability: string,
  payload: {
    policy?: string
    max_permission_level?: string
    scope_summary?: string
  },
) {
  return request<{ permission: StewardDevicePermission }>(
    `${API_BASE}/steward/devices/${encodeURIComponent(deviceId)}/permissions/${encodeURIComponent(capability)}`,
    {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(payload),
    },
  )
}

export async function getStewardAutonomy() {
  return request<{ autonomy: StewardAutonomyOverview }>(`${API_BASE}/steward/autonomy`)
}

export async function updateStewardAutonomySettings(payload: {
  paused?: boolean
  mode?: string
  max_auto_permission?: string
}) {
  return request<{ settings: StewardAutonomySettings }>(`${API_BASE}/steward/autonomy/settings`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function runStewardAutonomyCycle(limit = 12) {
  return request<{ autonomy: StewardAutonomyOverview }>(`${API_BASE}/steward/autonomy/run?limit=${limit}`, {
    method: 'POST',
  })
}

export async function updateStewardAutonomyRule(
  id: string,
  payload: {
    policy?: string
    enabled?: boolean
    max_permission_level?: string
    scope_summary?: string
  },
) {
  return request<{ rule: StewardAutonomyRule }>(`${API_BASE}/steward/autonomy/rules/${id}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function createStewardAutonomyProposal(payload: {
  rule_id?: string | null
  source_entity_type?: string
  source_entity_id?: string | null
  action?: string
  title: string
  summary?: string
  trigger_reason?: string
  suggested_action?: string
  risk_level?: string
  permission_level?: string
  data_level?: string
  policy?: string
  impact_summary?: string
}) {
  return request<{ proposal: StewardAutonomyProposal }>(`${API_BASE}/steward/autonomy/proposals`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function approveStewardAutonomyProposal(id: string) {
  return request<{ proposal: StewardAutonomyProposal }>(`${API_BASE}/steward/autonomy/proposals/${id}/approve`, {
    method: 'POST',
  })
}

export async function dismissStewardAutonomyProposal(id: string) {
  return request<{ proposal: StewardAutonomyProposal }>(`${API_BASE}/steward/autonomy/proposals/${id}/dismiss`, {
    method: 'POST',
  })
}

export async function dismissStewardAutonomyProposals(payload: {
  status?: string
  limit?: number
  reason?: string
}) {
  return request<{ result: StewardAutonomyBulkDismissResult }>(`${API_BASE}/steward/autonomy/proposals/bulk-dismiss`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })
}

export async function simulateStewardAutonomyProposal(id: string) {
  return request<{ run: StewardAutonomousRun }>(`${API_BASE}/steward/autonomy/proposals/${id}/simulate`, {
    method: 'POST',
  })
}

export async function executeStewardAutonomyProposal(id: string) {
  return request<{ run: StewardAutonomousRun }>(`${API_BASE}/steward/autonomy/proposals/${id}/execute`, {
    method: 'POST',
  })
}

export async function retryStewardAutonomyProposal(id: string) {
  return request<{ run: StewardAutonomousRun }>(`${API_BASE}/steward/autonomy/proposals/${id}/retry`, {
    method: 'POST',
  })
}

export async function approveStewardApprovalRequest(id: string, decisionReason = '', approvalProof?: StewardSignedApprovalProof) {
  return request<{ approval: StewardApprovalRequest }>(`${API_BASE}/steward/autonomy/approvals/${id}/approve`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ decision_reason: decisionReason, approval_proof: approvalProof }),
  })
}

export async function rejectStewardApprovalRequest(id: string, decisionReason = '') {
  return request<{ approval: StewardApprovalRequest }>(`${API_BASE}/steward/autonomy/approvals/${id}/reject`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ decision_reason: decisionReason }),
  })
}

export async function getStewardModelSettings() {
  return request<{ settings: StewardModelSettings }>(`${API_BASE}/steward/model-settings`)
}

export async function updateStewardModelSettings(payload: {
  provider?: string
  base_url?: string
  model?: string
  api_key?: string
  allow_no_api_key?: boolean
  max_data_level?: string
  timeout_seconds?: number
  agent_max_rounds?: number
  agent_max_tool_calls?: number
  agent_max_duration_seconds?: number
  agent_no_progress_limit?: number
  agent_progress_detail?: string
}) {
  return request<{ settings: StewardModelSettings }>(`${API_BASE}/steward/model-settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export type StewardModelProbe = {
  ok: boolean
  error?: string
  duration_ms: number
  protocol_checked: boolean
  tool_count: number
  response_mode?: 'text' | 'tool_call' | string
  failure?: {
    code: string
    title: string
    message: string
    suggestions: string[]
    retryable: boolean
    technical_summary?: string
  }
  status: StewardModelSettings['advisor']
}

export function stewardModelProbeError(probe: StewardModelProbe) {
  const failure = probe.failure
  const suggestions = failure?.suggestions?.map((item) => `- ${item}`).join('\n')
  return [
    failure?.title || '模型连接检查失败',
    failure?.message,
    suggestions ? `处理建议：\n${suggestions}` : '',
    failure?.code ? `错误代码：${failure.code}` : '',
    !failure && probe.error ? probe.error : '',
  ].filter(Boolean).join('\n')
}

export async function probeStewardModelConnection() {
  return request<{ probe: StewardModelProbe }>(`${API_BASE}/steward/autonomy/advisor/probe`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ data_level: 'D0' }),
  })
}

export async function getStewardRuntimePlanner() {
  return request<{ planner: StewardRuntimePlannerStatus }>(`${API_BASE}/steward/runtime/planner`)
}

export async function getStewardExecutionControl() {
  return request<{ control: StewardRuntimeExecutionControl }>(`${API_BASE}/steward/execution/control`)
}

export async function setStewardExecutionStopped(stopped: boolean, reason: string) {
  return request<{ control: StewardRuntimeExecutionControl }>(
    `${API_BASE}/steward/execution/control/${stopped ? 'stop' : 'resume'}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reason, changed_by: 'local-user' }),
    },
  )
}

export const getStewardRuntimeControl = getStewardExecutionControl
export const setStewardRuntimePaused = setStewardExecutionStopped

export async function getStewardAgentRuns(limit = 40, status = '') {
  const params = new URLSearchParams({ limit: String(limit) })
  if (status) params.set('status', status)
  return request<{ runs: StewardAgentRunSummary[] }>(`${API_BASE}/steward/runs?${params.toString()}`)
}

export async function getStewardAgentRun(id: string) {
  return request<{ run: StewardAgentRun }>(`${API_BASE}/steward/runs/${encodeURIComponent(id)}`)
}

export async function getStewardAgentRunEvidence(runId: string, evidenceId: string) {
  return request<{ evidence: StewardEvidenceArtifact }>(
    `${API_BASE}/steward/runs/${encodeURIComponent(runId)}/evidence/${encodeURIComponent(evidenceId)}`,
  )
}

export async function planStewardAgentRun(payload: {
  instruction: string
  data_level: string
  permission_ceiling: string
  auto_start: boolean
}) {
  return request<{ run: StewardAgentRun }>(`${API_BASE}/steward/runs/plan`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function transitionStewardAgentRun(id: string, action: 'start' | 'cancel' | 'resume') {
  return request<{ run: StewardAgentRun }>(
    `${API_BASE}/steward/runs/${encodeURIComponent(id)}/${action}`,
    { method: 'POST' },
  )
}

export async function approveStewardAgentRun(id: string, planHash: string, reason: string, approvalProof?: StewardSignedApprovalProof) {
  return request<{ run: StewardAgentRun }>(
    `${API_BASE}/steward/runs/${encodeURIComponent(id)}/approve`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        plan_hash: planHash,
        granted_by: 'local-user',
        scope: 'run',
        reason,
        approval_proof: approvalProof,
      }),
    },
  )
}

export async function streamStewardAgentRunEvents(
  id: string,
  after: number,
  onEvent: (event: StewardRunEvent) => void,
  signal: AbortSignal,
  onOpen?: () => void,
) {
  const url = new URL(`${API_BASE}/steward/runs/${encodeURIComponent(id)}/events`, window.location.origin)
  url.searchParams.set('after', String(after))
  const response = await fetch(url, { headers: { Accept: 'text/event-stream' }, signal })
  if (!response.ok || !response.body) {
    throw new Error((await response.text()) || `Event stream failed: ${response.status}`)
  }
  onOpen?.()
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  while (true) {
    const { value, done } = await reader.read()
    buffer += decoder.decode(value, { stream: !done })
    const blocks = buffer.split(/\r?\n\r?\n/)
    buffer = blocks.pop() ?? ''
    for (const block of blocks) {
      const data = block
        .split(/\r?\n/)
        .filter((line) => line.startsWith('data:'))
        .map((line) => line.slice(5).trimStart())
        .join('\n')
      if (!data) continue
      const parsed = JSON.parse(data) as StewardRunEvent | { error: string }
      if ('error' in parsed) throw new Error(parsed.error)
      onEvent(parsed)
    }
    if (done) break
  }
}
