import { isCollationValid, isFilterValid, isHintValid, isProjectValid, isSortValid } from 'mongodb-query-parser'

import { errorMessage } from './diagnostics'
import type { MongoDiagnostic, MongoQueryPartKind } from './types'

function validateByKind(kind: MongoQueryPartKind, input: string) {
  if (kind === 'filter') return isFilterValid(input)
  if (kind === 'project') return isProjectValid(input)
  if (kind === 'sort') return isSortValid(input)
  if (kind === 'collation') return isCollationValid(input)
  return isHintValid(input)
}

export function validateMongoQueryPart(kind: MongoQueryPartKind, input: string): MongoDiagnostic[] {
  if (!input.trim()) {
    return [
      {
        code: `${kind}-empty`,
        message: `${kind} 参数为空。`,
        severity: 'warning',
        source: 'query-validator',
      },
    ]
  }

  try {
    const result = validateByKind(kind, input)
    if (result === false || result == null) {
      return [
        {
          code: `${kind}-invalid`,
          message: `${kind} 参数无法通过 MongoDB 查询解析器校验。`,
          severity: 'warning',
          source: 'query-validator',
        },
      ]
    }
    return []
  } catch (error) {
    return [
      {
        code: `${kind}-parse-error`,
        message: errorMessage(error),
        severity: 'error',
        source: 'query-validator',
      },
    ]
  }
}
