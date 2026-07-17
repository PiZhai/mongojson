import { describe, expect, it } from 'vitest'
import { entityText, formatDate, isSensitiveLevel, priorityText, statusText } from './model'

describe('steward presentation helpers', () => {
  it('translates known statuses and preserves unknown values', () => {
    expect(statusText('running')).toBe('运行中')
    expect(statusText('controlled')).toBe('受控执行模式')
    expect(statusText('future_status')).toBe('future_status')
  })

  it('translates entity and priority labels', () => {
    expect(entityText('knowledge_item')).toBe('知识')
    expect(entityText('custom_entity')).toBe('custom_entity')
    expect(priorityText('high')).toBe('高')
  })

  it('classifies only D4 through D6 as sensitive', () => {
    expect(isSensitiveLevel('D3')).toBe(false)
    expect(isSensitiveLevel('D4')).toBe(true)
    expect(isSensitiveLevel('D6')).toBe(true)
  })

  it('uses a stable fallback for missing dates', () => {
    expect(formatDate()).toBe('未记录')
    expect(formatDate(null)).toBe('未记录')
  })
})
