import type { JsonFormatResponse } from '../../../types/tooling'
import { computeMaxDepth, formatAst, formatAstCompact } from './ast'
import { parse, tokenize } from './parser'

export function formatJson(input: string, compact = false): JsonFormatResponse {
  if (!input.trim()) {
    return { error: '输入为空' }
  }

  try {
    const tokens = tokenize(input)
    const ast = parse(tokens)
    const formatted = compact ? formatAstCompact(ast) : formatAst(ast, 0, 2)
    return {
      formatted,
      ast,
      lineCount: formatted.split('\n').length,
      charCount: formatted.length,
      maxDepth: computeMaxDepth(ast),
    }
  } catch (error) {
    const detail = error as Error & { position?: number }
    return {
      error: detail.message,
      position: detail.position,
    }
  }
}
