import { computeMaxDepth, formatAst, formatAstCompact } from '../tooling/jsonFormatter/ast'
import { formatJson } from '../tooling/jsonFormatter/format'
import { mongoValueToJsonNode } from './bsonToJsonNode'
import { errorOffset, parserDiagnostic, warningDiagnostic } from './diagnostics'
import { parseMongoValue, stringifyCanonicalExtendedJson } from './parseMongoValue'
import type { MongoFormatResult } from './types'

export function formatMongoJson(input: string, compact = false): MongoFormatResult {
  if (!input.trim()) {
    return {
      ok: false,
      diagnostics: [
        {
          code: 'empty-input',
          message: '输入为空',
          severity: 'error',
          source: 'parser',
        },
      ],
    }
  }

  const parsed = parseMongoValue(input)
  const legacy = formatJson(input, compact)

  if ('error' in legacy) {
    return {
      ok: false,
      text: legacy.error,
      diagnostics: [
        ...parsed.diagnostics,
        {
          code: 'legacy-format-error',
          message: legacy.error,
          severity: 'error',
          source: 'formatter',
          offset: errorOffset(legacy) ?? legacy.position,
        },
      ],
    }
  }

  if (parsed.ok) {
    const ast = mongoValueToJsonNode(parsed.value)
    const text = compact ? formatAstCompact(ast) : formatAst(ast, 0, 2)
    return {
      ok: true,
      text,
      ast,
      diagnostics: parsed.diagnostics,
      extendedJson: stringifyCanonicalExtendedJson(parsed.value, compact),
      stats: {
        chars: text.length,
        lines: text.split('\n').length,
        maxDepth: computeMaxDepth(ast),
      },
    }
  }

  return {
    ok: true,
    text: legacy.formatted,
    ast: legacy.ast,
    diagnostics: [warningDiagnostic('legacy-parser-fallback', '开源 MongoDB 解析器未接受该输入，已使用项目兼容解析器格式化。')],
    extendedJson: undefined,
    stats: {
      chars: legacy.charCount,
      lines: legacy.lineCount,
      maxDepth: legacy.maxDepth,
    },
  }
}

export function parseMongoJson(input: string): MongoFormatResult {
  const parsed = parseMongoValue(input)
  if (!parsed.ok) {
    const legacy = formatJson(input, false)
    if ('error' in legacy) {
      return {
        ok: false,
        diagnostics: [...parsed.diagnostics, parserDiagnostic(legacy, 'legacy-parse-error')],
      }
    }
    return {
      ok: true,
      text: legacy.formatted,
      ast: legacy.ast,
      diagnostics: [warningDiagnostic('legacy-parser-fallback', '已使用项目兼容解析器解析。')],
      stats: {
        chars: legacy.charCount,
        lines: legacy.lineCount,
        maxDepth: legacy.maxDepth,
      },
    }
  }

  return formatMongoJson(input, false)
}
