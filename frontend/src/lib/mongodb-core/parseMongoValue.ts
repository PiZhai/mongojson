import parseShellBson, { ParseMode } from '@mongodb-js/shell-bson-parser'
import { EJSON } from 'bson'

import { parserDiagnostic } from './diagnostics'
import type { MongoDiagnostic } from './types'

export type ParsedMongoValue =
  | {
      ok: true
      value: unknown
      parser: 'shell-bson' | 'ejson'
      diagnostics: MongoDiagnostic[]
    }
  | {
      ok: false
      diagnostics: MongoDiagnostic[]
    }

export function parseMongoValue(input: string): ParsedMongoValue {
  try {
    JSON.parse(input)
    return {
      ok: true,
      value: EJSON.parse(input, { relaxed: false }),
      parser: 'ejson',
      diagnostics: [],
    }
  } catch {
    // Not strict JSON; continue with MongoDB shell-compatible syntax.
  }

  try {
    return {
      ok: true,
      value: parseShellBson(input, { mode: ParseMode.Loose }),
      parser: 'shell-bson',
      diagnostics: [],
    }
  } catch (shellError) {
    return {
      ok: false,
      diagnostics: [parserDiagnostic(shellError)],
    }
  }
}

export function stringifyCanonicalExtendedJson(value: unknown, compact = false) {
  return EJSON.stringify(value, null, compact ? 0 : 2, { relaxed: false })
}
