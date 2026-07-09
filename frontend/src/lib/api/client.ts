import type {
  FileSummary,
  JobSummary,
  MemoFloatingCardRecord,
  MemoRecord,
  PresetRecord,
  StewardAgentStatus,
  StewardApprovalRequest,
  StewardAuditLog,
  StewardAutonomousRun,
  StewardAutonomyBulkDismissResult,
  StewardAutonomyOverview,
  StewardAutonomyProposal,
  StewardAutonomyRule,
  StewardAutonomySettings,
  StewardCollectorConfig,
  StewardDataTag,
  StewardDevice,
  StewardDevicePermission,
  StewardDeviceSyncResult,
  StewardDeviceTrustVerification,
  StewardEvent,
  StewardIntent,
  StewardKnowledgeItem,
  StewardMemory,
  StewardMemoryVersion,
  StewardOverview,
  StewardSearchResult,
  StewardSourceRef,
  StewardSyncChange,
  StewardSyncConflict,
  StewardSyncStatus,
  StewardTask,
  StewardTimelineSegment,
} from '../../types/tooling'

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

export async function approveStewardApprovalRequest(id: string, decisionReason = '') {
  return request<{ approval: StewardApprovalRequest }>(`${API_BASE}/steward/autonomy/approvals/${id}/approve`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ decision_reason: decisionReason }),
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
