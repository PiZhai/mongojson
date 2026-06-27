import { formatShellStatement, parseShellStatement, validateShellStatement } from '../tooling/jsonFormatter'
import type { ShellValidation } from '../../types/tooling'
import { validateMongoQueryPart } from './validateQuery'
import type { MongoFormatResult, MongoQueryPartKind, MongoShellSummary } from './types'

const queryPartByMethod: Record<string, MongoQueryPartKind> = {
  find: 'filter',
  findOne: 'filter',
  updateOne: 'filter',
  updateMany: 'filter',
  replaceOne: 'filter',
  deleteOne: 'filter',
  deleteMany: 'filter',
  remove: 'filter',
  sort: 'sort',
  project: 'project',
  projection: 'project',
  hint: 'hint',
  collation: 'collation',
}

export function summarizeMongoShell(input: string): MongoShellSummary {
  const parsed = parseShellStatement(input)
  if (!parsed) {
    return {
      collection: null,
      methods: [],
      operators: [],
    }
  }

  return {
    collection: parsed.collection,
    methods: parsed.methods.map((method) => ({
      name: method.name,
      nameOffset: method.nameStart,
      args: method.argsRaw.map((arg) => ({ text: arg.text, offset: arg.start })),
    })),
    operators: parsed.operators.map((operator) => ({ name: operator.name, offset: operator.pos })),
  }
}

export function validateMongoShell(input: string): ShellValidation[] {
  const base = validateShellStatement(input)
  const parsed = parseShellStatement(input)
  if (!parsed) return base

  const queryChecks = parsed.methods.flatMap((method) => {
    const kind = queryPartByMethod[method.name]
    const arg = method.argsRaw[0]
    if (!kind || !arg) return []
    return validateMongoQueryPart(kind, arg.text).map((diagnostic): ShellValidation => ({
      level: diagnostic.severity === 'error' ? 'err' : 'warn',
      msg: `.${method.name}() ${diagnostic.message}`,
    }))
  })

  return [...base, ...queryChecks]
}

export function formatMongoShell(input: string): MongoFormatResult {
  const parsed = parseShellStatement(input)
  const validations = validateMongoShell(input)
  if (!parsed) {
    return {
      ok: false,
      text: '未识别为 MongoDB Shell 语句。',
      diagnostics: [
        {
          code: 'shell-parse-error',
          message: '未识别为 MongoDB Shell 语句（期望 db.xxx）。',
          severity: 'error',
          source: 'parser',
        },
      ],
    }
  }

  const formatted = formatShellStatement(input) ?? input
  return {
    ok: true,
    text: formatted,
    diagnostics: validations
      .filter((item) => item.level !== 'ok')
      .map((item) => ({
        code: item.level === 'err' ? 'shell-validation-error' : 'shell-validation-warning',
        message: item.msg,
        severity: item.level === 'err' ? 'error' : 'warning',
        source: 'query-validator',
      })),
    stats: {
      chars: formatted.length,
      lines: formatted.split('\n').length,
    },
  }
}
