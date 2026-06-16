export type ToolStatusKind = 'idle' | 'success' | 'warning' | 'error'

export type ToolStatus = {
  kind: ToolStatusKind
  message: string
}

export type JsonNode =
  | { type: 'object'; entries: Array<{ key: string; value: JsonNode }> }
  | { type: 'array'; items: JsonNode[] }
  | { type: 'string'; value: string }
  | { type: 'number'; value: string }
  | { type: 'literal'; value: string }
  | { type: 'mongo'; func: string; args: string | null }

export type JsonFormatResult = {
  formatted: string
  ast: JsonNode
  lineCount: number
  charCount: number
  maxDepth: number
}

export type JsonFormatError = {
  error: string
  position?: number
}

export type JsonFormatResponse = JsonFormatResult | JsonFormatError

export type TableSchemaColumn = {
  path: string
  dominantType: string
  isMixed: boolean
  typeCounts: Record<string, number>
  nullCount: number
  totalCount: number
  nullRatio: number
}

export type TableValidation = {
  level: 'ok' | 'warn' | 'err'
  msg: string
}

export type TableData = {
  schema: TableSchemaColumn[]
  rows: Array<Array<JsonNode | null>>
  validation: TableValidation[]
  docCount: number
}

export type ShellValidation = {
  level: 'ok' | 'warn' | 'err'
  msg: string
}

export type DiffSummary = {
  leftOnly: string[]
  rightOnly: string[]
  changed: string[]
}

export type ChartSeriesRow = Record<string, string | number | null>

export type JobStatus = 'pending' | 'running' | 'success' | 'failed' | 'expired'

export type JobSummary = {
  id: string
  tool_type: string
  status: JobStatus
  input_file_id?: string | null
  output_file_id?: string | null
  params?: Record<string, unknown>
  error_message?: string | null
  created_at: string
  finished_at?: string | null
  expires_at?: string | null
}

export type FileSummary = {
  id: string
  original_name: string
  stored_name: string
  mime_type: string
  size_bytes: number
  category: string
  expires_at?: string | null
  created_at: string
}

export type PresetRecord = {
  id: string
  tool_type: string
  name: string
  payload: Record<string, unknown>
  created_at: string
  updated_at: string
}
