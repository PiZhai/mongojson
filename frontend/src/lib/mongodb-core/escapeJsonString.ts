import { formatMongoJson } from './formatMongoJson'

export function escapeMongoJsonString(rawInput: string): { output?: string; error?: string } {
  let trimmed = rawInput.trim()
  if (trimmed.startsWith('"') && trimmed.endsWith('"')) {
    try {
      const inner = JSON.parse(trimmed)
      if (typeof inner === 'string') trimmed = inner
    } catch {
      // Keep the original input and let the parser report the failure.
    }
  }

  const result = formatMongoJson(trimmed, true)
  if (!result.ok) return { error: result.diagnostics[0]?.message ?? '无法解析为有效 JSON' }

  const jsonText = result.extendedJson ?? result.text
  try {
    const parsed = JSON.parse(jsonText)
    return { output: JSON.stringify(JSON.stringify(parsed)) }
  } catch {
    return { error: '无法转换为可转义的标准 JSON' }
  }
}

export function unescapeMongoJsonString(rawInput: string): { output?: string; error?: string } {
  const trimmed = rawInput.trim()
  let inner: string | undefined

  try {
    const parsed = JSON.parse(trimmed)
    if (typeof parsed === 'string') inner = parsed
  } catch {
    inner = undefined
  }

  if (!inner && ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'")))) {
    inner = trimmed.slice(1, -1)
  }

  if (!inner) return { error: '输入不是转义后的 JSON 字符串' }

  const result = formatMongoJson(inner, false)
  if (!result.ok) return { error: '转义内容无法解析为有效 JSON' }
  return { output: result.text }
}
