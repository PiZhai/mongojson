import { describe, expect, it } from 'vitest'
import { getBlockOrder, getMemoOutline, getMemoStats, normalizeBlockDocument } from './document'

const blocks = [
  { id: 'h1', type: 'heading', props: { level: 1 }, content: [{ type: 'text', text: 'Overview' }] },
  {
    id: 'list', type: 'bulletListItem', content: 'Item', children: [
      { id: 'h2', type: 'heading', props: { level: 2 }, content: [{ type: 'text', text: 'Details' }] },
      { id: 'image', type: 'image', props: { url: '/image.png' } },
    ],
  },
]

describe('memo block document helpers', () => {
  it('extracts stable block order and nested outline entries', () => {
    expect(getBlockOrder(blocks)).toEqual(['h1', 'list', 'h2', 'image'])
    expect(getMemoOutline(blocks)).toEqual([
      { id: 'h1', label: 'Overview', level: 1 },
      { id: 'h2', label: 'Details', level: 2 },
    ])
  })

  it('computes stats from canonical blocks and markdown projection', () => {
    expect(getMemoStats('# Overview\nhello world', blocks)).toEqual({ chars: 19, images: 1, blocks: 4 })
  })

  it('normalizes invalid document roots to an empty document', () => {
    expect(normalizeBlockDocument({})).toEqual([])
  })
})
