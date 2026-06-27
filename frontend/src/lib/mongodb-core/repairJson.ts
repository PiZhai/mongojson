import { JSONRepairError, jsonrepair } from 'jsonrepair'

import type { MongoFormatResult } from './types'

export function repairStandardJson(input: string): MongoFormatResult {
  if (!input.trim()) {
    return {
      ok: false,
      diagnostics: [
        {
          code: 'empty-input',
          message: '输入为空',
          severity: 'error',
          source: 'repair',
        },
      ],
    }
  }

  try {
    const repaired = jsonrepair(input)
    const parsed = JSON.parse(repaired)
    const text = JSON.stringify(parsed, null, 2)
    return {
      ok: true,
      text,
      diagnostics: [
        {
          code: 'json-repaired',
          message: '已修复为标准 JSON。MongoDB shell 类型会按 jsonrepair 规则转换为普通 JSON 值。',
          severity: 'info',
          source: 'repair',
        },
      ],
      stats: {
        chars: text.length,
        lines: text.split('\n').length,
      },
    }
  } catch (error) {
    return {
      ok: false,
      diagnostics: [
        {
          code: 'json-repair-error',
          message: error instanceof Error ? error.message : String(error),
          severity: 'error',
          source: 'repair',
          offset: error instanceof JSONRepairError ? error.position : undefined,
        },
      ],
    }
  }
}
