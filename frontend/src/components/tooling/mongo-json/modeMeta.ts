import type { ToolStatus } from '../../../types/tooling'
import type { MongoMode } from './types'

export const mongoModes: Array<[MongoMode, string]> = [
  ['format', '格式化'],
  ['diff', '对比'],
  ['table', '表格'],
  ['shell', 'Shell'],
  ['repair', '修复'],
  ['escape', '转义'],
  ['unescape', '还原'],
]

export const modeLabels: Record<MongoMode, string> = {
  format: '格式化',
  diff: '对比',
  table: '表格',
  shell: 'Shell',
  repair: '修复',
  escape: '转义',
  unescape: '还原',
}

export const modeDescriptions: Record<MongoMode, string> = {
  format: '格式化 MongoDB Extended JSON，保留 ObjectId、ISODate 等类型。',
  diff: '聚焦字段新增、缺失和值变化，并支持路径定位。',
  table: '展平对象结构，查看字段主类型、缺失率和逐行预览。',
  shell: '提取集合、方法链和操作符，辅助排查 Shell 语句。',
  repair: '显式修复为标准 JSON，不影响 MongoDB 类型保留格式化。',
  escape: '将 JSON 转为可嵌入代码或文本的字符串。',
  unescape: '把转义字符串还原成可读 JSON。',
}

export function isMongoMode(value: string | null): value is MongoMode {
  return value === 'format' || value === 'diff' || value === 'table' || value === 'shell' || value === 'repair' || value === 'escape' || value === 'unescape'
}

export function formatParseMessage(message: string, source: string, position?: number) {
  if (position == null || position < 0) {
    return `${source}${message}`
  }

  const prefix = source ? `${source}` : ''
  return `${prefix}${message}（位置 ${position + 1}）`
}

export function mapHintTone(kind: ToolStatus['kind']): 'ok' | 'warn' | 'error' {
  if (kind === 'error') return 'error'
  if (kind === 'warning') return 'warn'
  return 'ok'
}
