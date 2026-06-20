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

export type FormatMeta = {
  text: string
  error: string | null
  lineCount: number
  charCount: number
  maxDepth?: number
  ast: JsonNode | null
  keyLineMap: Record<string, number>
}

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

export type ShellArg = {
  text: string
  start: number
  end: number
}

export type ShellMethod = {
  name: string
  nameStart: number
  nameEnd: number
  openParen: number
  closeParen: number
  argsRaw: ShellArg[]
}

export type ShellStatement = {
  collection: string
  collectionStart: number
  collectionEnd: number
  methods: ShellMethod[]
  operators: Array<{ name: string; pos: number }>
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

export type InspectInputKind =
  | 'standard-json'
  | 'mongo-json'
  | 'escaped-json-string'
  | 'mongo-shell'
  | 'curl'
  | 'log-json-fragment'
  | 'ndjson'
  | 'unknown'

export type InspectSuggestedActionId = 'format' | 'unescape' | 'diff' | 'table' | 'shell' | 'extract'

export type InspectSuggestedAction = {
  id: InspectSuggestedActionId
  label: string
  description: string
  targetPath?: string
}

export type InspectIssue = {
  level: 'info' | 'warn' | 'error'
  message: string
}

export type InspectResult = {
  kind: InspectInputKind
  confidence: number
  extractedText: string
  issues: InspectIssue[]
  suggestedActions: InspectSuggestedAction[]
}

export type SemanticDiffOptions = {
  ignorePaths?: string[]
  arrayMatchKey?: string
}

export type SemanticDiffChange = {
  path: string
  leftType?: string
  rightType?: string
  leftValue?: string
  rightValue?: string
  message: string
}

export type JsonPatchOperation =
  | { op: 'add'; path: string; value: unknown }
  | { op: 'remove'; path: string }
  | { op: 'replace'; path: string; value: unknown }

export type SemanticDiffResult = {
  added: SemanticDiffChange[]
  removed: SemanticDiffChange[]
  typeChanged: SemanticDiffChange[]
  valueChanged: SemanticDiffChange[]
  patch: JsonPatchOperation[]
}

export type SchemaProfileField = {
  path: string
  dominantType: string
  optional: boolean
  nullRatio: number
  presenceRatio: number
  isMixed: boolean
  typeCounts: Record<string, number>
  examples: string[]
  risks: string[]
}

export type SchemaProfile = {
  docCount: number
  fieldCount: number
  nullableFieldCount: number
  mixedFieldCount: number
  riskFieldCount: number
  fields: SchemaProfileField[]
}

export type GeneratedSchemaTarget = 'typescript' | 'zod' | 'go'

export type GeneratedSchema = {
  target: GeneratedSchemaTarget
  code: string
}

export type MongoQueryRisk = {
  level: 'info' | 'warn' | 'danger'
  code: string
  message: string
  method?: string
}

export type PipelineStageSummary = {
  index: number
  operator: string
  title: string
  description: string
  fieldHints: string[]
  risks: string[]
  raw: string
}

export type PipelineInspectionResult = {
  collection: string
  methodChain: string[]
  risks: MongoQueryRisk[]
  stages: PipelineStageSummary[]
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
