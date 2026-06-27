import { describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_FLOATING_CARD_COLOR,
  FLOATING_CARDS_V2_STORAGE_KEY,
  createFloatingCard,
  deserializeFloatingCards,
  hasMeaningfulFloatingCards,
  loadFloatingCardsFromStorage,
  serializeFloatingCards,
} from './floatingCards'

function createStorage(seed: Record<string, string> = {}) {
  const state = new Map(Object.entries(seed))
  return {
    getItem: vi.fn((key: string) => state.get(key) ?? null),
    removeItem: vi.fn((key: string) => {
      state.delete(key)
    }),
    setItem: vi.fn((key: string, value: string) => {
      state.set(key, value)
    }),
  }
}

describe('floating cards persistence helpers', () => {
  it('serializes and deserializes backend wire shape', () => {
    const cards = deserializeFloatingCards([
      {
        id: 'card-1',
        content: 'Remember this',
        color: '#EAF6FF',
        created_at: '2026-06-28T01:02:03.000Z',
        updated_at: '2026-06-28T04:05:06.000Z',
      },
    ])

    expect(cards).toEqual([
      {
        id: 'card-1',
        content: 'Remember this',
        color: '#eaf6ff',
        createdAt: '2026-06-28T01:02:03.000Z',
        updatedAt: '2026-06-28T04:05:06.000Z',
      },
    ])
    expect(serializeFloatingCards(cards)).toEqual([
      {
        id: 'card-1',
        content: 'Remember this',
        color: '#eaf6ff',
        created_at: '2026-06-28T01:02:03.000Z',
        updated_at: '2026-06-28T04:05:06.000Z',
      },
    ])
  })

  it('loads old local cards and fills missing color and updated timestamp', () => {
    const storage = createStorage({
      [FLOATING_CARDS_V2_STORAGE_KEY]: JSON.stringify([
        { id: 'card-1', content: 'old note', createdAt: '2026-06-28T01:02:03.000Z' },
      ]),
    })

    const cards = loadFloatingCardsFromStorage(storage)

    expect(cards).toHaveLength(1)
    expect(cards[0]).toMatchObject({
      id: 'card-1',
      content: 'old note',
      color: DEFAULT_FLOATING_CARD_COLOR,
      createdAt: '2026-06-28T01:02:03.000Z',
      updatedAt: '2026-06-28T01:02:03.000Z',
    })
  })

  it('keeps a stored empty card list empty', () => {
    const storage = createStorage({
      [FLOATING_CARDS_V2_STORAGE_KEY]: '[]',
    })

    expect(loadFloatingCardsFromStorage(storage)).toEqual([])
  })

  it('detects meaningful local drafts without treating a single blank default card as meaningful', () => {
    const blank = createFloatingCard('', DEFAULT_FLOATING_CARD_COLOR, new Date('2026-06-28T01:02:03.000Z'))
    const colored = createFloatingCard('', '#eaf6ff', new Date('2026-06-28T01:02:03.000Z'))

    expect(hasMeaningfulFloatingCards([blank])).toBe(false)
    expect(hasMeaningfulFloatingCards([colored])).toBe(true)
    expect(hasMeaningfulFloatingCards([{ ...blank, content: 'note' }])).toBe(true)
    expect(hasMeaningfulFloatingCards([blank, createFloatingCard('', DEFAULT_FLOATING_CARD_COLOR)])).toBe(true)
  })
})
