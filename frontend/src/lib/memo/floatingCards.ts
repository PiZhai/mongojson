import type { MemoFloatingCardRecord } from '../../types/tooling'

export const FLOATING_CARD_STORAGE_KEY = 'mongojson.memoDocs.floatingCard'
export const FLOATING_CARDS_STORAGE_KEY = 'mongojson.memoDocs.floatingCards'
export const FLOATING_CARDS_V2_STORAGE_KEY = 'mongojson.memoDocs.floatingCards.v2'
export const DEFAULT_FLOATING_CARD_COLOR = '#fff7d6'

const HEX_COLOR_PATTERN = /^#[0-9a-f]{6}$/i

export type FloatingCard = {
  color: string
  content: string
  createdAt: string
  id: string
  updatedAt: string
}

type FloatingCardStorage = Pick<Storage, 'getItem' | 'removeItem' | 'setItem'>

export function normalizeFloatingCardColor(value: unknown) {
  if (typeof value === 'string' && HEX_COLOR_PATTERN.test(value)) {
    return value.toLowerCase()
  }
  return DEFAULT_FLOATING_CARD_COLOR
}

export function createFloatingCard(content = '', color = DEFAULT_FLOATING_CARD_COLOR, now = new Date()): FloatingCard {
  const timestamp = now.toISOString()
  return {
    color: normalizeFloatingCardColor(color),
    content,
    createdAt: timestamp,
    id: typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`,
    updatedAt: timestamp,
  }
}

export function deserializeFloatingCards(value: unknown) {
  if (!Array.isArray(value)) {
    return []
  }
  return value
    .filter((card) => typeof card?.id === 'string' && typeof card?.content === 'string')
    .map((card) => {
      const createdAt = typeof card.created_at === 'string'
        ? card.created_at
        : typeof card.createdAt === 'string'
          ? card.createdAt
          : new Date().toISOString()
      const updatedAt = typeof card.updated_at === 'string'
        ? card.updated_at
        : typeof card.updatedAt === 'string'
          ? card.updatedAt
          : createdAt

      return {
        color: normalizeFloatingCardColor(card.color),
        content: card.content,
        createdAt,
        id: card.id,
        updatedAt,
      }
    })
}

export function serializeFloatingCards(cards: FloatingCard[]): MemoFloatingCardRecord[] {
  return cards.map((card) => ({
    color: normalizeFloatingCardColor(card.color),
    content: card.content,
    created_at: card.createdAt,
    id: card.id,
    updated_at: card.updatedAt,
  }))
}

export function loadFloatingCardsFromStorage(storage: FloatingCardStorage | null = getFloatingCardStorage(), fallbackToDefault = true) {
  if (!storage) {
    return fallbackToDefault ? [createFloatingCard()] : []
  }

  const storedCards =
    storage.getItem(FLOATING_CARDS_V2_STORAGE_KEY) ??
    storage.getItem(FLOATING_CARDS_STORAGE_KEY) ??
    storage.getItem(FLOATING_CARD_STORAGE_KEY)

  storage.removeItem(FLOATING_CARD_STORAGE_KEY)
  storage.removeItem(FLOATING_CARDS_STORAGE_KEY)

  if (!storedCards) {
    return fallbackToDefault ? [createFloatingCard()] : []
  }

  try {
    const parsed = JSON.parse(storedCards) as unknown
    if (Array.isArray(parsed)) {
      return deserializeFloatingCards(parsed)
    }
    if (typeof parsed === 'string') {
      return [createFloatingCard(parsed)]
    }
  } catch {
    if (storedCards.trim().length > 0) {
      return [createFloatingCard(storedCards)]
    }
  }

  storage.removeItem(FLOATING_CARDS_V2_STORAGE_KEY)
  return fallbackToDefault ? [createFloatingCard()] : []
}

export function saveFloatingCardsToStorage(cards: FloatingCard[], storage: FloatingCardStorage | null = getFloatingCardStorage()) {
  storage?.setItem(FLOATING_CARDS_V2_STORAGE_KEY, JSON.stringify(cards))
}

export function hasMeaningfulFloatingCards(cards: FloatingCard[]) {
  return cards.length > 1 || cards.some((card) => card.content.trim().length > 0 || normalizeFloatingCardColor(card.color) !== DEFAULT_FLOATING_CARD_COLOR)
}

function getFloatingCardStorage(): FloatingCardStorage | null {
  return typeof window === 'undefined' ? null : window.localStorage
}
