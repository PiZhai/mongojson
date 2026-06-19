import { formatJson } from './format'

export function escapeJsonString(rawInput: string): { output?: string; error?: string } {
  let trimmed = rawInput.trim()
  if (trimmed.startsWith('"') && trimmed.endsWith('"')) {
    try {
      const inner = JSON.parse(trimmed)
      if (typeof inner === 'string') trimmed = inner
    } catch {
      // noop
    }
  }

  let jsonText = trimmed
  try {
    JSON.parse(trimmed)
  } catch {
    const result = formatJson(trimmed, false)
    if ('error' in result) return { error: '无法解析为有效 JSON' }
    jsonText = result.formatted
  }

  try {
    const parsed = JSON.parse(jsonText)
    const compact = JSON.stringify(parsed)
    return { output: JSON.stringify(compact) }
  } catch {
    return { error: '无法解析为有效 JSON' }
  }
}

export function unescapeJsonString(rawInput: string): { output?: string; error?: string } {
  const trimmed = rawInput.trim()
  let inner: string | undefined

  try {
    const parsed = JSON.parse(trimmed)
    if (typeof parsed === 'string') inner = parsed
  } catch {
    inner = undefined
  }

  if (!inner && ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'")))) {
    try {
      const parsed = JSON.parse(trimmed.slice(1, -1))
      inner = typeof parsed === 'string' ? parsed : trimmed.slice(1, -1)
    } catch {
      inner = trimmed.slice(1, -1)
    }
  }

  if (!inner) return { error: '输入不是转义后的 JSON 字符串' }

  const result = formatJson(inner, false)
  if ('error' in result) return { error: '转义内容无法解析为有效 JSON' }
  return { output: result.formatted }
}
