export type EnvironmentName = 'demo' | 'test' | 'stag' | 'prod'

export type Environment = {
  environment: EnvironmentName
  database_name: string
  configured: boolean
  updated_at?: string
}

export type FieldMapping = {
  document_path: string
  query_field: string
}

export type QueryRule = {
  id?: string
  name: string
  collection: string
  field_mappings: FieldMapping[]
  created_at?: string
  updated_at?: string
}

export type Diagnostic = {
  code: string
  message: string
  severity: 'error' | 'warning' | 'info'
  source: string
  offset?: number
  line?: number
  column?: number
}

export type ParsedOperation = {
  id: string
  type: string
  collection: string
  queryable: boolean
  description: string
  source: string
  contextSource?: string
  range: {
    start: number
    end: number
    startLine: number
    startColumn: number
    endLine: number
    endColumn: number
  }
  arguments: unknown[]
  unresolvedPaths: string[]
  diagnostics: Diagnostic[]
  bulkOrdered?: boolean
  children?: Array<{
    id: string
    index: number
    type: string
    queryable: boolean
    diagnostics: Diagnostic[]
  }>
}

export type ParseResult = {
  operations: ParsedOperation[]
  diagnostics: Diagnostic[]
}

export type ScriptRecord = {
  id?: string
  title: string
  source: string
  origin_path?: string
  operations?: ParsedOperation[]
  operation_descriptions?: Record<string, string>
  updated_at?: string
}

export type RepositoryFile = {
  path: string
  size: number
  modified_at: string
}

export type RepositoryProject = {
  name: string
  task_folders: string[]
}

export type RepositoryTaskLocation = {
  project: string
  task_folder: string
  file_count: number
}

export type RepositoryTaskSummary = {
  key: string
  locations: RepositoryTaskLocation[]
  file_count: number
}

export type RepositoryStatement = {
  id: string
  index: number
  source: string
  operation: ParsedOperation
  project: string
  task_folder: string
  file_path: string
}

export type RepositoryTaskFile = {
  path: string
  project: string
  task_folder: string
  size: number
  modified_at: string
  statements: RepositoryStatement[]
  diagnostics: Diagnostic[]
}

export type RepositoryTask = {
  key: string
  locations: RepositoryTaskLocation[]
  files: RepositoryTaskFile[]
}

export type FieldDifference = {
  path: string
  script?: unknown
  database?: unknown
  kind: 'changed' | 'script_only' | 'database_only'
}

export type OperationResult = {
  operation_id: string
  environment: EnvironmentName
  status: string
  message?: string
  match_count: number
  truncated: boolean
  documents?: Array<{
    before?: unknown
    after?: unknown
    differences?: FieldDifference[]
    modified_paths?: string[]
    uncertain_paths?: string[]
  }>
  diagnostics?: Diagnostic[]
}

export type Review = {
  id: string
  script_id?: string
  status: 'queued' | 'running' | 'completed' | 'failed'
  parse: ParseResult
  results: OperationResult[]
  error?: string
  created_at: string
  finished_at?: string
}
