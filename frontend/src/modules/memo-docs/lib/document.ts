export type MemoOutlineItem = {
  id: string
  label: string
  level: number
}

type BlockLike = {
  id?: unknown
  type?: unknown
  props?: { level?: unknown }
  content?: unknown
  children?: unknown
}

export function normalizeBlockDocument(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

export function getBlockOrder(blocks: unknown[]) {
  const result: string[] = []
  walkBlocks(blocks, (block) => {
    if (typeof block.id === 'string' && block.id) result.push(block.id)
  })
  return result
}

export function getMemoOutline(blocks: unknown[]): MemoOutlineItem[] {
  const result: MemoOutlineItem[] = []
  walkBlocks(blocks, (block) => {
    if (block.type !== 'heading' || typeof block.id !== 'string') return
    const label = inlineContentToText(block.content).trim()
    if (!label) return
    const level = typeof block.props?.level === 'number' ? block.props.level : 1
    result.push({ id: block.id, label, level })
  })
  return result
}

export function getMemoStats(markdown: string, blocks: unknown[]) {
  const blockOrder = getBlockOrder(blocks)
  return {
    chars: Array.from(markdown.replace(/\s+/g, '')).length,
    images: countBlocksByType(blocks, new Set(['image'])),
    blocks: blockOrder.length,
  }
}

function countBlocksByType(blocks: unknown[], types: Set<string>) {
  let count = 0
  walkBlocks(blocks, (block) => {
    if (typeof block.type === 'string' && types.has(block.type)) count += 1
  })
  return count
}

function walkBlocks(blocks: unknown[], visit: (block: BlockLike) => void) {
  for (const value of blocks) {
    if (!value || typeof value !== 'object') continue
    const block = value as BlockLike
    visit(block)
    if (Array.isArray(block.children)) walkBlocks(block.children, visit)
  }
}

function inlineContentToText(content: unknown): string {
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  return content.map((item) => {
    if (typeof item === 'string') return item
    if (!item || typeof item !== 'object') return ''
    const value = item as { text?: unknown; content?: unknown }
    if (typeof value.text === 'string') return value.text
    return inlineContentToText(value.content)
  }).join('')
}
