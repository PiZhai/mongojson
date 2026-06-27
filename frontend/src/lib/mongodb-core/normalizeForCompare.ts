import type { FormatMeta, JsonNode } from '../../types/tooling'
import { collectFieldPaths, computeMaxDepth, formatAst, sortAstNode } from '../tooling/jsonFormatter/ast'
import { formatMongoJson } from './formatMongoJson'

function buildKeyLineMap(formattedText: string, ast: JsonNode | null) {
  const keyLineMap: Record<string, number> = {}
  if (!ast) return keyLineMap
  const orderedPaths: Array<{ path: string; key: string }> = []
  collectFieldPaths(ast, '', orderedPaths)
  const lines = formattedText.split('\n')
  let cursor = 0

  for (const item of orderedPaths) {
    const escapedKey = item.key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const pattern = new RegExp(`"${escapedKey}"\\s*:`)
    for (let lineIndex = cursor; lineIndex < lines.length; lineIndex += 1) {
      if (pattern.test(lines[lineIndex])) {
        keyLineMap[item.path] = lineIndex + 1
        cursor = lineIndex + 1
        break
      }
    }
  }

  return keyLineMap
}

export function normalizeMongoForCompare(input: string): FormatMeta {
  if (!input.trim()) {
    return {
      text: '',
      error: null,
      lineCount: 0,
      charCount: 0,
      maxDepth: 0,
      ast: null,
      keyLineMap: {},
    }
  }

  const result = formatMongoJson(input, false)
  if (!result.ok || !result.ast) {
    return {
      text: input,
      error: result.diagnostics[0]?.message ?? '输入解析失败',
      lineCount: input.split('\n').length,
      charCount: input.length,
      ast: null,
      keyLineMap: {},
    }
  }

  const sortedAst = sortAstNode(result.ast)
  const text = formatAst(sortedAst, 0, 2)
  return {
    text,
    error: null,
    lineCount: text.split('\n').length,
    charCount: text.length,
    maxDepth: computeMaxDepth(sortedAst),
    ast: sortedAst,
    keyLineMap: buildKeyLineMap(text, sortedAst),
  }
}
