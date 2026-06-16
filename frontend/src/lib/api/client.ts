import type { FileSummary, JobSummary, PresetRecord } from '../../types/tooling'

const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '/api'

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
