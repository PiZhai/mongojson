import type { JsonNode } from '../../../shared/data/types'

export function formatAst(node: JsonNode, indent = 0, indentSize = 2): string {
  const pad = ' '.repeat(indent)
  const inner = ' '.repeat(indent + indentSize)

  switch (node.type) {
    case 'object': {
      if (node.entries.length === 0) return `${pad}{}`
      const parts = node.entries.map((entry) => {
        const value = formatAst(entry.value, indent + indentSize, indentSize)
        return `${inner}"${entry.key}" : ${value.trimStart()}`
      })
      return `${pad}{\n${parts.join(',\n')}\n${pad}}`
    }
    case 'array': {
      if (node.items.length === 0) return `${pad}[]`
      const parts = node.items.map((item) => formatAst(item, indent + indentSize, indentSize))
      const allSimple =
        node.items.every(
          (item) =>
            item.type === 'string' ||
            item.type === 'number' ||
            item.type === 'literal' ||
            item.type === 'mongo' ||
            (item.type === 'object' && item.entries.length === 0) ||
            (item.type === 'array' && item.items.length === 0),
        )
      if (allSimple) {
        return `${pad}[ ${parts.map((part) => part.trim()).join(', ')} ]`
      }
      return `${pad}[\n${parts.join(',\n')}\n${pad}]`
    }
    case 'string':
      return `${pad}"${node.value}"`
    case 'number':
      return `${pad}${node.value}`
    case 'literal':
      return `${pad}${node.value}`
    case 'mongo':
      return node.args != null ? `${pad}${node.func}(${node.args})` : `${pad}${node.func}`
  }
}

export function formatAstCompact(node: JsonNode): string {
  switch (node.type) {
    case 'object':
      return `{${node.entries.map((entry) => `"${entry.key}":${formatAstCompact(entry.value)}`).join(',')}}`
    case 'array':
      return `[${node.items.map((item) => formatAstCompact(item)).join(',')}]`
    case 'string':
      return `"${node.value}"`
    case 'number':
      return node.value
    case 'literal':
      return node.value
    case 'mongo':
      return node.args != null ? `${node.func}(${node.args})` : node.func
  }
}

export function computeMaxDepth(node: JsonNode | null): number {
  if (!node) return 0
  switch (node.type) {
    case 'object':
      return node.entries.length === 0 ? 1 : 1 + Math.max(...node.entries.map((entry) => computeMaxDepth(entry.value)))
    case 'array':
      return node.items.length === 0 ? 1 : 1 + Math.max(...node.items.map((item) => computeMaxDepth(item)))
    default:
      return 0
  }
}

export function compareFieldKeys(a: string, b: string) {
  return a.localeCompare(b, 'en', {
    numeric: true,
    sensitivity: 'base',
  })
}

export function sortAstNode(node: JsonNode): JsonNode {
  switch (node.type) {
    case 'object':
      return {
        type: 'object',
        entries: node.entries
          .map((entry) => ({ key: entry.key, value: sortAstNode(entry.value) }))
          .sort((a, b) => compareFieldKeys(a.key, b.key)),
      }
    case 'array':
      return {
        type: 'array',
        items: node.items.map((item) => sortAstNode(item)),
      }
    case 'string':
      return { type: 'string', value: node.value }
    case 'number':
      return { type: 'number', value: node.value }
    case 'literal':
      return { type: 'literal', value: node.value }
    case 'mongo':
      return { type: 'mongo', func: node.func, args: node.args }
  }
}

export function joinFieldPath(basePath: string, segment: string | number) {
  if (typeof segment === 'number') {
    return basePath ? `${basePath}[${segment}]` : `[${segment}]`
  }
  return basePath ? `${basePath}.${segment}` : segment
}

export function collectFieldPaths(node: JsonNode | null, basePath: string, paths: Array<{ path: string; key: string }>) {
  if (!node) return

  if (node.type === 'object') {
    for (const entry of node.entries) {
      const entryPath = joinFieldPath(basePath, entry.key)
      paths.push({ path: entryPath, key: entry.key })
      collectFieldPaths(entry.value, entryPath, paths)
    }
    return
  }

  if (node.type === 'array') {
    node.items.forEach((item, index) => collectFieldPaths(item, joinFieldPath(basePath, index), paths))
  }
}

export function astValueToDisplay(node: JsonNode | null | undefined): string | null {
  if (!node) return null
  switch (node.type) {
    case 'string':
      return node.value
    case 'number':
      return node.value
    case 'literal':
      return node.value === 'null' || node.value === 'undefined' ? null : node.value
    case 'mongo':
      return node.args != null ? `${node.func}(${node.args})` : node.func
    case 'object':
      return node.entries.length === 0 ? '{}' : `{…${node.entries.length}}`
    case 'array':
      return node.items.length === 0 ? '[]' : `[…${node.items.length}]`
  }
}

export function astNodeToDisplay(node: JsonNode | null | undefined): string {
  return astValueToDisplay(node) ?? 'NULL'
}

export function astValueToType(node: JsonNode | null | undefined): string {
  if (!node) return 'null'
  switch (node.type) {
    case 'string':
      return 'string'
    case 'number':
      return 'number'
    case 'literal':
      if (node.value === 'true' || node.value === 'false') return 'bool'
      if (node.value === 'null' || node.value === 'undefined') return 'null'
      return 'string'
    case 'mongo':
      if (node.func === 'ObjectId') return 'oid'
      if (node.func === 'ISODate' || node.func === 'new Date' || node.func === 'Date') return 'date'
      return 'mongo'
    case 'object':
      return 'object'
    case 'array':
      return 'array'
  }
}
