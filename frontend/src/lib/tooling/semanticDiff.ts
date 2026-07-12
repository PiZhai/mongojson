import type {
  JsonNode,
  JsonPatchOperation,
  SemanticDiffChange,
  SemanticDiffOptions,
  SemanticDiffResult,
} from '../../shared/data/types'
import { astNodeToDisplay, astValueToType, formatAstCompact } from './jsonFormatter'

function toPointer(path: string) {
  if (!path) return ''
  const parts = path
    .replace(/\[([^\]]+)]/g, '.$1')
    .split('.')
    .filter(Boolean)
    .map((part) => part.replace(/~/g, '~0').replace(/\//g, '~1'))
  return `/${parts.join('/')}`
}

function shouldIgnore(path: string, ignorePaths: string[]) {
  return ignorePaths.some((ignored) => path === ignored || path.startsWith(`${ignored}.`) || path.startsWith(`${ignored}[`))
}

function joinPath(basePath: string, segment: string | number) {
  if (typeof segment === 'number') return basePath ? `${basePath}[${segment}]` : `[${segment}]`
  return basePath ? `${basePath}.${segment}` : segment
}

function joinArrayMatchPath(basePath: string, key: string) {
  return basePath ? `${basePath}[${key}]` : `[${key}]`
}

function nodeToPatchValue(node: JsonNode): unknown {
  switch (node.type) {
    case 'object':
      return Object.fromEntries(node.entries.map((entry) => [entry.key, nodeToPatchValue(entry.value)]))
    case 'array':
      return node.items.map(nodeToPatchValue)
    case 'string':
      return node.value
    case 'number': {
      const value = Number(node.value)
      return Number.isFinite(value) ? value : node.value
    }
    case 'literal':
      if (node.value === 'true') return true
      if (node.value === 'false') return false
      if (node.value === 'null' || node.value === 'undefined') return null
      return node.value
    case 'mongo':
      return node.args != null ? `${node.func}(${node.args})` : node.func
  }
}

function changeFor(path: string, left: JsonNode | undefined, right: JsonNode | undefined, message: string): SemanticDiffChange {
  return {
    path,
    leftType: left ? astValueToType(left) : undefined,
    rightType: right ? astValueToType(right) : undefined,
    leftValue: left ? astNodeToDisplay(left) : undefined,
    rightValue: right ? astNodeToDisplay(right) : undefined,
    message,
  }
}

function mapObject(node: Extract<JsonNode, { type: 'object' }>) {
  return Object.fromEntries(node.entries.map((entry) => [entry.key, entry.value]))
}

function arrayKey(node: JsonNode, key: string) {
  if (node.type !== 'object') return null
  const field = node.entries.find((entry) => entry.key === key)
  return field ? astNodeToDisplay(field.value) : null
}

function compareNodes(
  left: JsonNode | undefined,
  right: JsonNode | undefined,
  path: string,
  options: Required<SemanticDiffOptions>,
  result: SemanticDiffResult,
) {
  if (shouldIgnore(path, options.ignorePaths)) return

  if (!left && right) {
    const change = changeFor(path, left, right, '右侧新增字段')
    result.added.push(change)
    result.patch.push({ op: 'add', path: toPointer(path), value: nodeToPatchValue(right) })
    return
  }
  if (left && !right) {
    const change = changeFor(path, left, right, '右侧缺少字段')
    result.removed.push(change)
    result.patch.push({ op: 'remove', path: toPointer(path) })
    return
  }
  if (!left || !right) return

  if (left.type !== right.type || astValueToType(left) !== astValueToType(right)) {
    const change = changeFor(path, left, right, '字段类型发生变化')
    result.typeChanged.push(change)
    result.patch.push({ op: 'replace', path: toPointer(path), value: nodeToPatchValue(right) })
    return
  }

  if (left.type === 'object' && right.type === 'object') {
    const leftMap = mapObject(left)
    const rightMap = mapObject(right)
    const keys = Array.from(new Set([...Object.keys(leftMap), ...Object.keys(rightMap)])).sort((a, b) => a.localeCompare(b, 'en', { numeric: true }))
    keys.forEach((key) => compareNodes(leftMap[key], rightMap[key], joinPath(path, key), options, result))
    return
  }

  if (left.type === 'array' && right.type === 'array') {
    if (options.arrayMatchKey) {
      const leftMap = new Map<string, JsonNode>()
      const rightMap = new Map<string, JsonNode>()
      left.items.forEach((item, index) => leftMap.set(arrayKey(item, options.arrayMatchKey) ?? `#${index}`, item))
      right.items.forEach((item, index) => rightMap.set(arrayKey(item, options.arrayMatchKey) ?? `#${index}`, item))
      Array.from(new Set([...leftMap.keys(), ...rightMap.keys()]))
        .sort((a, b) => a.localeCompare(b, 'en', { numeric: true }))
        .forEach((key) => compareNodes(leftMap.get(key), rightMap.get(key), joinArrayMatchPath(path, `${options.arrayMatchKey}=${key}`), options, result))
      return
    }

    const maxLength = Math.max(left.items.length, right.items.length)
    for (let index = 0; index < maxLength; index += 1) {
      compareNodes(left.items[index], right.items[index], joinPath(path, index), options, result)
    }
    return
  }

  if (formatAstCompact(left) !== formatAstCompact(right)) {
    const change = changeFor(path, left, right, '字段值发生变化')
    result.valueChanged.push(change)
    result.patch.push({ op: 'replace', path: toPointer(path), value: nodeToPatchValue(right) })
  }
}

export function getSemanticDiff(
  left: JsonNode | null,
  right: JsonNode | null,
  options: SemanticDiffOptions = {},
): SemanticDiffResult {
  const result: SemanticDiffResult = {
    added: [],
    removed: [],
    typeChanged: [],
    valueChanged: [],
    patch: [],
  }
  if (!left || !right) return result
  compareNodes(left, right, '', { ignorePaths: options.ignorePaths ?? [], arrayMatchKey: options.arrayMatchKey ?? '' }, result)
  return result
}

export function formatJsonPatch(patch: JsonPatchOperation[]) {
  return JSON.stringify(patch, null, 2)
}
