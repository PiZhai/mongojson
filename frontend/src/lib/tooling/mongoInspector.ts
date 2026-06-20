import type { MongoQueryRisk, PipelineInspectionResult, PipelineStageSummary, ShellMethod } from '../../types/tooling'
import { parseShellStatement } from './jsonFormatter'

const stageDescriptions: Record<string, string> = {
  $match: '过滤输入文档，通常决定后续处理的数据范围。',
  $project: '重塑输出字段，适合收敛返回结构。',
  $group: '按 key 聚合文档，可能显著减少或重排数据。',
  $lookup: '关联其他集合，可能带来 IO 和数据膨胀。',
  $unwind: '展开数组字段，可能放大文档数量。',
  $sort: '对结果排序，字段缺索引时容易变慢。',
  $limit: '限制输出条数，适合保护调试查询。',
  $skip: '跳过指定条数，深分页时可能变慢。',
}

function isEmptyObjectArg(method: ShellMethod) {
  const firstArg = method.argsRaw[0]?.text.replace(/\s+/g, '')
  return !firstArg || firstArg === '{}'
}

function hasMethod(methods: ShellMethod[], name: string) {
  return methods.some((method) => method.name === name)
}

function operatorExists(input: string, operator: string) {
  return new RegExp(`\\${operator}\\b`).test(input)
}

export function inspectMongoQuery(input: string): PipelineInspectionResult | null {
  const parsed = parseShellStatement(input)
  if (!parsed) return null

  const risks: MongoQueryRisk[] = []
  parsed.methods.forEach((method) => {
    if ((method.name === 'deleteMany' || method.name === 'updateMany') && isEmptyObjectArg(method)) {
      risks.push({
        level: 'danger',
        code: 'wide-write',
        method: method.name,
        message: `${method.name} 缺少有效 filter，可能影响整个集合。`,
      })
    }

    if (method.name === 'find') {
      if (!hasMethod(parsed.methods, 'limit')) {
        risks.push({ level: 'warn', code: 'find-without-limit', method: method.name, message: 'find 未搭配 limit，调试查询可能返回过多数据。' })
      }
      if (method.argsRaw.length < 2) {
        risks.push({ level: 'info', code: 'missing-projection', method: method.name, message: 'find 未提供 projection，可考虑只返回排查所需字段。' })
      }
    }

    if (method.name === 'sort' && !hasMethod(parsed.methods, 'hint')) {
      risks.push({ level: 'warn', code: 'sort-without-hint', method: method.name, message: 'sort 未显式 hint，建议确认排序字段有索引支撑。' })
    }
  })

  if (operatorExists(input, '$nin')) {
    risks.push({ level: 'warn', code: 'nin-scan-risk', message: '$nin 通常难以有效利用索引，注意扫描范围。' })
  }
  if (operatorExists(input, '$where')) {
    risks.push({ level: 'danger', code: 'where-js-risk', message: '$where 会执行 JavaScript 表达式，风险和性能成本都较高。' })
  }
  if (operatorExists(input, '$regex') || /\/.+\/[a-z]*/.test(input)) {
    risks.push({ level: 'warn', code: 'regex-query-risk', message: '正则查询需要确认前缀和索引，否则容易退化为扫描。' })
  }

  const aggregate = parsed.methods.find((method) => method.name === 'aggregate')
  const stages = aggregate ? inspectPipelineStages(aggregate.argsRaw[0]?.text ?? '') : []
  stages.forEach((stage) => {
    stage.risks.forEach((message) => risks.push({ level: message.includes('膨胀') || message.includes('阻塞') ? 'warn' : 'info', code: `stage-${stage.operator}`, method: 'aggregate', message }))
  })

  return {
    collection: parsed.collection,
    methodChain: parsed.methods.map((method) => method.name),
    risks,
    stages,
  }
}

export function inspectPipelineStages(raw: string): PipelineStageSummary[] {
  const stageRegex = /\{\s*(\$[a-zA-Z][a-zA-Z0-9]*)\s*:/g
  const matches = Array.from(raw.matchAll(stageRegex)).filter((match) => {
    const before = raw.slice(0, match.index)
    let braceDepth = 0
    let bracketDepth = 0
    let inString = false
    let quote = ''
    for (let i = 0; i < before.length; i += 1) {
      const ch = before[i]
      if (inString) {
        if (ch === '\\') i += 1
        else if (ch === quote) inString = false
        continue
      }
      if (ch === '"' || ch === "'") {
        inString = true
        quote = ch
        continue
      }
      if (ch === '{') braceDepth += 1
      if (ch === '}') braceDepth -= 1
      if (ch === '[') bracketDepth += 1
      if (ch === ']') bracketDepth -= 1
    }
    return bracketDepth >= 1 && braceDepth === 0
  })
  return matches.map((match, matchIndex) => {
    const operator = match[1]
    const start = match.index
    const end = matches[matchIndex + 1]?.index ?? raw.length
    const chunk = raw.slice(start, end).trim().replace(/,\s*$/, '')
    const fieldHints = Array.from(chunk.matchAll(/["']?([a-zA-Z_][a-zA-Z0-9_.]*)["']?\s*:/g))
      .map((item) => item[1])
      .filter((item) => !item.startsWith('$'))
      .slice(0, 6)
    const risks: string[] = []
    if (operator === '$lookup') risks.push('$lookup 会关联外部集合，注意索引和输出数组大小。')
    if (operator === '$unwind') risks.push('$unwind 可能造成文档数量膨胀。')
    if (operator === '$sort') risks.push('$sort 在大数据集上可能阻塞，建议确认排序字段索引。')
    if (operator === '$group') risks.push('$group 会重排数据流，注意内存占用。')

    return {
      index: matchIndex + 1,
      operator,
      title: `${matchIndex + 1}. ${operator}`,
      description: stageDescriptions[operator] ?? '自定义或较少见的 aggregation stage，建议结合 MongoDB 文档确认行为。',
      fieldHints: Array.from(new Set(fieldHints)),
      risks,
      raw: chunk,
    }
  })
}
