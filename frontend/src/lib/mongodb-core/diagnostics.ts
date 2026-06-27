import type { MongoDiagnostic } from './types'

export function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

export function errorOffset(error: unknown) {
  const candidate = error as { position?: unknown; pos?: unknown; index?: unknown; loc?: { start?: { index?: unknown } } }
  const value = candidate.position ?? candidate.pos ?? candidate.index ?? candidate.loc?.start?.index
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

export function parserDiagnostic(error: unknown, code = 'mongo-parse-error'): MongoDiagnostic {
  return {
    code,
    message: errorMessage(error),
    severity: 'error',
    source: 'parser',
    offset: errorOffset(error),
  }
}

export function warningDiagnostic(code: string, message: string): MongoDiagnostic {
  return {
    code,
    message,
    severity: 'warning',
    source: 'legacy-fallback',
  }
}
