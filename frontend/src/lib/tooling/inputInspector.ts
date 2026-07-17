import type {
  InspectIssue,
  InspectResult,
  InspectSuggestedAction,
  InspectSuggestedActionId,
} from '../../shared/data/types'
import { formatMongoJson, repairStandardJson, unescapeMongoJsonString } from '../mongodb-core'
import { parseShellStatement } from './jsonFormatter'

const actionCatalog: Record<InspectSuggestedActionId, InspectSuggestedAction> = {
  format: {
    id: 'format',
    label: '格式化',
    description: '把当前内容送入 JSON 或 MongoDB JSON 格式化工作区。',
  },
  repair: {
    id: 'repair',
    label: '修复 JSON',
    description: '显式把破损或宽松 JSON 修复为标准 JSON。',
  },
  unescape: {
    id: 'unescape',
    label: '还原字符串',
    description: '把转义后的 JSON 字符串还原成可读文本。',
  },
  diff: {
    id: 'diff',
    label: '进入 Diff',
    description: '把提取结果作为左侧输入，进入 MongoDB 结构对比。',
  },
  table: {
    id: 'table',
    label: '构建表格',
    description: '将对象或对象数组展平，查看字段稳定性。',
  },
  shell: {
    id: 'shell',
    label: 'Shell 检查',
    description: '识别集合、方法链、操作符和潜在查询风险。',
  },
  extract: {
    id: 'extract',
    label: '提取片段',
    description: '优先使用识别出的 JSON 片段继续处理。',
  },
}

function withActions(ids: InspectSuggestedActionId[]) {
  return Array.from(new Set(ids)).map((id) => actionCatalog[id])
}

function clampConfidence(value: number) {
  return Math.max(0, Math.min(1, value))
}

function findBalancedFragment(input: string, open: '{' | '[') {
  const close = open === '{' ? '}' : ']'
  const start = input.indexOf(open)
  if (start < 0) return null

  let depth = 0
  let inString = false
  let quote = ''
  for (let i = start; i < input.length; i += 1) {
    const ch = input[i]
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
    if (ch === open) depth += 1
    if (ch === close) depth -= 1
    if (depth === 0) return input.slice(start, i + 1)
  }

  return null
}

function extractJsonFragment(input: string) {
  const objectFragment = findBalancedFragment(input, '{')
  const arrayFragment = findBalancedFragment(input, '[')
  if (!objectFragment) return arrayFragment
  if (!arrayFragment) return objectFragment
  return objectFragment.length >= arrayFragment.length ? objectFragment : arrayFragment
}

function parseStrictJson(input: string) {
  try {
    return JSON.parse(input) as unknown
  } catch {
    return null
  }
}

function looksLikeNdjson(input: string) {
  const lines = input
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
  if (lines.length < 2) return false
  return lines.every((line) => Boolean(parseStrictJson(line)))
}

function normalizeNdjson(input: string) {
  const rows = input
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => parseStrictJson(line))
  return JSON.stringify(rows, null, 2)
}

function inspectJsonLike(text: string, issues: InspectIssue[] = []): InspectResult | null {
  const strict = parseStrictJson(text)
  if (strict != null) {
    const isString = typeof strict === 'string'
    const escaped = isString ? unescapeMongoJsonString(text) : null
    if (isString && escaped?.output) {
      return {
        kind: 'escaped-json-string',
        confidence: 0.96,
        extractedText: text,
        issues,
        suggestedActions: withActions(['unescape', 'format', 'diff']),
      }
    }

    return {
      kind: 'standard-json',
      confidence: 0.95,
      extractedText: text,
      issues,
      suggestedActions: withActions(['format', 'table', 'diff']),
    }
  }

  const mongoResult = formatMongoJson(text, false)
  if (mongoResult.ok) {
    return {
      kind: 'mongo-json',
      confidence: 0.9,
      extractedText: text,
      issues,
      suggestedActions: withActions(['format', 'table', 'diff', 'repair']),
    }
  }

  return null
}

function inspectCurl(input: string): InspectResult | null {
  if (!/^\s*curl\s+/i.test(input)) return null
  const dataFlag = /(?:--data(?:-raw|-binary)?|-d)\s+(["'])([\s\S]*?)\1/i.exec(input)
  const extractedText = dataFlag?.[2] ? dataFlag[2].replace(/\\"/g, '"') : input
  const issues: InspectIssue[] = dataFlag
    ? [{ level: 'info', message: '已从 curl 参数中提取请求体。' }]
    : [{ level: 'warn', message: '未找到 -d/--data 请求体，保留原始 curl 文本。' }]

  return {
    kind: 'curl',
    confidence: dataFlag ? 0.86 : 0.68,
    extractedText,
    issues,
    suggestedActions: withActions(dataFlag ? ['extract', 'format', 'table'] : ['extract']),
  }
}

export function inspectInput(input: string): InspectResult {
  const trimmed = input.trim()
  if (!trimmed) {
    return {
      kind: 'unknown',
      confidence: 0,
      extractedText: '',
      issues: [{ level: 'info', message: '等待粘贴需要诊断的数据。' }],
      suggestedActions: [],
    }
  }

  const curl = inspectCurl(trimmed)
  if (curl) return curl

  if (looksLikeNdjson(trimmed)) {
    return {
      kind: 'ndjson',
      confidence: 0.93,
      extractedText: normalizeNdjson(trimmed),
      issues: [{ level: 'info', message: '已把 NDJSON 规整为 JSON 数组，便于继续表格化或可视化。' }],
      suggestedActions: withActions(['table', 'format', 'diff']),
    }
  }

  if (parseShellStatement(trimmed)) {
    return {
      kind: 'mongo-shell',
      confidence: 0.92,
      extractedText: trimmed,
      issues: [],
      suggestedActions: withActions(['shell', 'extract']),
    }
  }

  const jsonLike = inspectJsonLike(trimmed)
  if (jsonLike) return jsonLike

  const repair = repairStandardJson(trimmed)
  if (repair.ok) {
    return {
      kind: 'unknown',
      confidence: 0.72,
      extractedText: trimmed,
      issues: [{ level: 'info', message: '当前内容无法稳定识别为 MongoDB JSON，但可修复为标准 JSON。' }],
      suggestedActions: withActions(['repair', 'extract']),
    }
  }

  const fragment = extractJsonFragment(trimmed)
  if (fragment) {
    const fragmentResult = inspectJsonLike(fragment, [{ level: 'info', message: '已从日志或混合文本中提取 JSON 片段。' }])
    if (fragmentResult) {
      return {
        ...fragmentResult,
        kind: 'log-json-fragment',
        confidence: clampConfidence(fragmentResult.confidence - 0.12),
        suggestedActions: withActions(['extract', ...fragmentResult.suggestedActions.map((item) => item.id), 'repair']),
      }
    }
  }

  return {
    kind: 'unknown',
    confidence: 0.2,
    extractedText: trimmed,
    issues: [{ level: 'warn', message: '暂未识别出稳定的数据结构，可先手动提取 JSON 或 Shell 片段。' }],
    suggestedActions: withActions(['extract', 'repair']),
  }
}
