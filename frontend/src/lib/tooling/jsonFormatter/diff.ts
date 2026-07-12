import type { DiffSummary, FormatMeta, JsonNode } from '../../../shared/data/types'
import {
  collectFieldPaths,
  compareFieldKeys,
  computeMaxDepth,
  formatAst,
  formatAstCompact,
  joinFieldPath,
  sortAstNode,
} from './ast'
import { formatJson } from './format'

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

function compareFieldDiffs(nodeA: JsonNode | undefined, nodeB: JsonNode | undefined, basePath: string, result: DiffSummary) {
  if (!nodeA && !nodeB) return
  if (!nodeA) {
    if (basePath) result.rightOnly.push(basePath)
    return
  }
  if (!nodeB) {
    if (basePath) result.leftOnly.push(basePath)
    return
  }

  if (nodeA.type === 'object' && nodeB.type === 'object') {
    const mapA: Record<string, JsonNode> = {}
    const mapB: Record<string, JsonNode> = {}
    const keys = new Set<string>()
    nodeA.entries.forEach((entry) => {
      mapA[entry.key] = entry.value
      keys.add(entry.key)
    })
    nodeB.entries.forEach((entry) => {
      mapB[entry.key] = entry.value
      keys.add(entry.key)
    })

    Array.from(keys)
      .sort(compareFieldKeys)
      .forEach((key) => {
        const path = joinFieldPath(basePath, key)
        if (!(key in mapB)) {
          result.leftOnly.push(path)
        } else if (!(key in mapA)) {
          result.rightOnly.push(path)
        } else {
          compareFieldDiffs(mapA[key], mapB[key], path, result)
        }
      })
    return
  }

  if (nodeA.type === 'array' && nodeB.type === 'array') {
    const maxLength = Math.max(nodeA.items.length, nodeB.items.length)
    for (let index = 0; index < maxLength; index += 1) {
      compareFieldDiffs(nodeA.items[index], nodeB.items[index], joinFieldPath(basePath, index), result)
    }
    return
  }

  if (formatAstCompact(nodeA) !== formatAstCompact(nodeB) && basePath) {
    result.changed.push(basePath)
  }
}

export function getFieldDiffSummary(astA: JsonNode | null, astB: JsonNode | null): DiffSummary {
  const result: DiffSummary = { leftOnly: [], rightOnly: [], changed: [] }
  if (!astA || !astB) return result
  compareFieldDiffs(astA, astB, '', result)
  return result
}

export function normalizeForCompare(input: string): FormatMeta {
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

  const result = formatJson(input, false)
  if ('error' in result) {
    return {
      text: input,
      error: result.error,
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
