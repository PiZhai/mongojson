import { describe, expect, it } from 'vitest'
import { isConversationNearBottom } from './conversationScroll'

describe('conversation scroll position', () => {
  it('treats the bottom threshold as following the latest message', () => {
    expect(isConversationNearBottom({ scrollHeight: 1000, scrollTop: 428, clientHeight: 500 })).toBe(true)
    expect(isConversationNearBottom({ scrollHeight: 1000, scrollTop: 427, clientHeight: 500 })).toBe(false)
  })

  it('keeps short conversations attached to the bottom', () => {
    expect(isConversationNearBottom({ scrollHeight: 400, scrollTop: 0, clientHeight: 500 })).toBe(true)
  })
})
