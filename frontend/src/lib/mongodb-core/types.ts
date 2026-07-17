import type { JsonNode } from '../../shared/data/types'

export type MongoInputMode = 'mongo-json' | 'mongo-shell' | 'standard-json' | 'repair-json'

export type MongoDiagnosticSeverity = 'info' | 'warning' | 'error'

export type MongoDiagnosticSource = 'parser' | 'formatter' | 'repair' | 'query-validator' | 'risk-rule' | 'legacy-fallback'

export type MongoDiagnostic = {
  code: string
  message: string
  severity: MongoDiagnosticSeverity
  source: MongoDiagnosticSource
  offset?: number
  line?: number
  column?: number
  path?: string
}

export type MongoFormatResult =
  | {
      ok: true
      text: string
      ast?: JsonNode
      diagnostics: MongoDiagnostic[]
      extendedJson?: string
      stats?: {
        chars: number
        lines: number
        maxDepth?: number
      }
    }
  | {
      ok: false
      text?: string
      diagnostics: MongoDiagnostic[]
    }

export type MongoShellSummary = {
  collection: string | null
  methods: Array<{
    name: string
    nameOffset?: number
    args: Array<{ text: string; offset?: number }>
  }>
  operators: Array<{ name: string; offset?: number }>
}

export type MongoQueryPartKind = 'filter' | 'project' | 'sort' | 'collation' | 'hint'
