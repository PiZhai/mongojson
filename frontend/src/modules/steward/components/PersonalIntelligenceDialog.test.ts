import { describe, expect, it } from 'vitest'
import {
  backgroundStateLabel,
  findPersonalIntelligenceLoop,
  slotLabel,
} from './personalIntelligencePresentation'

describe('personal intelligence presentation', () => {
  it('keeps degraded and unhealthy states explicit', () => {
    expect(backgroundStateLabel('healthy')).toBe('后台工作正常')
    expect(backgroundStateLabel('degraded')).toBe('后台部分降级')
    expect(backgroundStateLabel('unhealthy')).toBe('后台未正常运行')
    expect(backgroundStateLabel('disabled')).toBe('持续智能已关闭')
  })

  it('renders persisted half-hour slots, including the midnight boundary', () => {
    expect(slotLabel(19)).toBe('09:30–10:00')
    expect(slotLabel(47)).toBe('23:30–00:00')
  })

  it('uses the R5.3 continuous intelligence loop before the legacy proactive loop', () => {
    const base = {
      enabled: true,
      running: true,
      interval: '1m',
      consecutive_failures: 0,
      updated_at: '2026-07-20T00:00:00Z',
    }
    const selected = findPersonalIntelligenceLoop([
      { ...base, name: 'proactive' },
      { ...base, name: 'continuous-intelligence' },
    ])

    expect(selected?.name).toBe('continuous-intelligence')
  })

  it('keeps the legacy proactive loop as a compatibility fallback', () => {
    const selected = findPersonalIntelligenceLoop([{
      name: 'proactive',
      enabled: true,
      running: true,
      interval: '1m',
      consecutive_failures: 0,
      updated_at: '2026-07-20T00:00:00Z',
    }])

    expect(selected?.name).toBe('proactive')
  })
})
