import { API_BASE, apiRequest } from '../../platform/http/client'
import type { PresetRecord } from './types'

export async function getPresets(toolType?: string) {
  const query = toolType ? `?tool_type=${encodeURIComponent(toolType)}` : ''
  return apiRequest<{ presets: PresetRecord[] }>(`${API_BASE}/presets${query}`)
}

export async function savePreset(payload: {
  tool_type: string
  name: string
  payload: Record<string, unknown>
}) {
  return apiRequest<{ preset: PresetRecord }>(`${API_BASE}/presets`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}
