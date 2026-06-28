export const MEMO_EDITOR_TAIL_LINES = 3

export function countTrailingNewlines(markdown: string) {
  const match = /\n*$/.exec(markdown)
  return match?.[0].length ?? 0
}

export function ensureTrailingNewlines(markdown: string, targetLine: number) {
  if (markdown.length === 0) {
    return targetLine <= 1 ? markdown : '\n'.repeat(Math.min(MEMO_EDITOR_TAIL_LINES, targetLine) - 1)
  }

  const normalizedTarget = Math.max(1, Math.min(MEMO_EDITOR_TAIL_LINES, Math.floor(targetLine)))
  const currentTrailingNewlines = countTrailingNewlines(markdown)
  if (currentTrailingNewlines >= normalizedTarget) {
    return markdown
  }

  return `${markdown}${'\n'.repeat(normalizedTarget - currentTrailingNewlines)}`
}

export function getTailClickLine(clientY: number, lastContentBottom: number, lineHeight: number) {
  if (clientY <= lastContentBottom || lineHeight <= 0) {
    return null
  }

  return Math.max(1, Math.min(MEMO_EDITOR_TAIL_LINES, Math.floor((clientY - lastContentBottom) / lineHeight) + 1))
}
