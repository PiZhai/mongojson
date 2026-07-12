import type { JsonNode, TableData, TableValidation } from '../../../shared/data/types'
import { astValueToDisplay, astValueToType, compareFieldKeys } from './ast'

function flattenAst(node: JsonNode | null | undefined, prefix: string, result: Record<string, JsonNode | null>) {
  if (!node) {
    result[prefix] = null
    return
  }

  if (node.type === 'object') {
    if (node.entries.length === 0) {
      result[prefix] = node
      return
    }
    for (const entry of node.entries) {
      flattenAst(entry.value, prefix ? `${prefix}.${entry.key}` : entry.key, result)
    }
    return
  }

  if (node.type === 'array') {
    result[prefix] = node
    node.items.slice(0, 10).forEach((item, index) => {
      flattenAst(item, `${prefix}[${index}]`, result)
    })
    return
  }

  result[prefix] = node
}

function buildValidation(schema: TableData['schema'], docCount: number): TableValidation[] {
  const checks: TableValidation[] = []
  const fullyNull = schema.filter((item) => item.nullCount === item.totalCount)
  if (fullyNull.length > 0) {
    checks.push({
      level: 'warn',
      msg: `${fullyNull.length} 个字段全为 null: ${fullyNull
        .slice(0, 3)
        .map((item) => item.path)
        .join(', ')}${fullyNull.length > 3 ? '…' : ''}`,
    })
  }

  const mixed = schema.filter((item) => item.isMixed)
  if (mixed.length > 0) {
    checks.push({
      level: 'warn',
      msg: `${mixed.length} 个字段类型不一致: ${mixed
        .slice(0, 3)
        .map((item) => `${item.path}[${Object.keys(item.typeCounts).join('/')}]`)
        .join(', ')}${mixed.length > 3 ? '…' : ''}`,
    })
  }

  const highNull = schema.filter((item) => item.nullRatio > 0.5 && item.nullRatio < 1)
  if (highNull.length > 0) {
    checks.push({
      level: 'warn',
      msg: `${highNull.length} 个字段缺失率>50%: ${highNull
        .slice(0, 3)
        .map((item) => `${item.path} (${Math.round(item.nullRatio * 100)}%)`)
        .join(', ')}${highNull.length > 3 ? '…' : ''}`,
    })
  }

  let maxDepth = 0
  schema.forEach((item) => {
    const depth = (item.path.match(/\./g) ?? []).length
    if (depth > maxDepth) maxDepth = depth
  })
  checks.unshift({ level: 'ok', msg: `${docCount} 条文档, ${schema.length} 个字段, 最大嵌套深度 ${maxDepth}` })
  return checks
}

export function buildTableFromAst(ast: JsonNode | null): TableData | null {
  if (!ast) return null
  const docs: JsonNode[] =
    ast.type === 'array'
      ? ast.items.filter((item): item is Extract<JsonNode, { type: 'object' }> => item.type === 'object')
      : ast.type === 'object'
        ? [ast]
        : []

  if (docs.length === 0) return null

  const allPathsSet: Record<string, boolean> = {}
  const pathOrder: string[] = []
  const docMaps: Array<Record<string, JsonNode | null>> = []

  for (const doc of docs) {
    const flat: Record<string, JsonNode | null> = {}
    flattenAst(doc, '', flat)
    docMaps.push(flat)
    Object.keys(flat).forEach((key) => {
      if (!allPathsSet[key]) {
        allPathsSet[key] = true
        pathOrder.push(key)
      }
    })
  }

  pathOrder.sort((a, b) => {
    const depthA = (a.match(/\./g) ?? []).length
    const depthB = (b.match(/\./g) ?? []).length
    if (depthA !== depthB) return depthA - depthB
    return compareFieldKeys(a, b)
  })

  const schema = pathOrder.map((path) => {
    const typeCounts: Record<string, number> = {}
    let nullCount = 0
    for (const map of docMaps) {
      const node = map[path]
      if (node == null) {
        nullCount += 1
        continue
      }
      const type = astValueToType(node)
      typeCounts[type] = (typeCounts[type] ?? 0) + 1
    }

    let dominantType = 'null'
    let maxCount = 0
    Object.entries(typeCounts).forEach(([type, count]) => {
      if (count > maxCount) {
        dominantType = type
        maxCount = count
      }
    })

    return {
      path,
      dominantType,
      isMixed: Object.keys(typeCounts).length > 1,
      typeCounts,
      nullCount,
      totalCount: docMaps.length,
      nullRatio: nullCount / docMaps.length,
    }
  })

  const rows = docMaps.map((map) => schema.map((column) => (map[column.path] !== undefined ? map[column.path] : null)))

  return {
    schema,
    rows,
    validation: buildValidation(schema, docs.length),
    docCount: docs.length,
  }
}

export { astValueToDisplay, astValueToType }
