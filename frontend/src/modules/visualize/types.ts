export type ChartSeriesRow = Record<string, string | number | null>

export type PresetRecord = {
  id: string
  tool_type: string
  name: string
  payload: Record<string, unknown>
  created_at: string
  updated_at: string
}
