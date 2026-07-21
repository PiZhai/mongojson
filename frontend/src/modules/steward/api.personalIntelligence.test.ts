import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  decideStewardAgentEpisode,
  getStewardProfile,
  getStewardProfileHistory,
  regenerateStewardReport,
  runStewardActivityBatches,
  updateStewardIntelligenceSettings,
} from './api'

function successfulResponse(payload: unknown) {
  return {
    ok: true,
    json: async () => payload,
  } as Response
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('personal intelligence management API', () => {
  it('surfaces the backend error message without leaking the JSON envelope', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      text: async () => JSON.stringify({ error: 'Agent 状态刚刚发生变化，请重试控制操作' }),
    } as Response)
    vi.stubGlobal('fetch', fetchMock)

    await expect(decideStewardAgentEpisode('episode-1', 'pause')).rejects.toThrow(
      'Agent 状态刚刚发生变化，请重试控制操作',
    )
  })

  it('loads each declared profile projection explicitly and merges them for the dialog', async () => {
    const fetchMock = vi.fn().mockImplementation(async (input: RequestInfo) => {
      const view = new URL(String(input), 'http://localhost').searchParams.get('view')
      return successfulResponse({ view, profile: view ? { id: `${view}-snapshot`, horizon: view } : null })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await getStewardProfile()

    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      '/api/steward/profile?view=recent',
      '/api/steward/profile?view=stable',
      '/api/steward/profile?view=explicit',
      '/api/steward/profile?view=merged',
    ])
    expect(result.profile.merged?.id).toBe('merged-snapshot')
  })

  it('queries the persisted profile history endpoint with explicit filters', async () => {
    const fetchMock = vi.fn().mockResolvedValue(successfulResponse({ facts: [] }))
    vi.stubGlobal('fetch', fetchMock)

    await getStewardProfileHistory({ horizon: 'explicit', status: 'active', key: 'preferred editor', limit: 25 })

    const [input, init] = fetchMock.mock.calls[0] as [RequestInfo, RequestInit | undefined]
    const url = new URL(String(input), 'http://localhost')
    expect(url.pathname).toBe('/api/steward/personal-intelligence/profile/history')
    expect(Object.fromEntries(url.searchParams)).toEqual({
      limit: '25',
      horizon: 'explicit',
      status: 'active',
      key: 'preferred editor',
    })
    expect(init).toBeUndefined()
  })

  it('uses durable backend operations for pause, immediate consolidation, and report regeneration', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(successfulResponse({ settings: { enabled: false, revision: 8 } }))
      .mockResolvedValueOnce(successfulResponse({ batches: [{ id: 'batch-1' }] }))
      .mockResolvedValueOnce(successfulResponse({ regeneration: { created: true, job: { id: 'job-1' } } }))
    vi.stubGlobal('fetch', fetchMock)

    await updateStewardIntelligenceSettings({ expected_revision: 7, enabled: false })
    await runStewardActivityBatches()
    await regenerateStewardReport('report/one', 'include the latest activity')

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/steward/intelligence-settings', expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ expected_revision: 7, enabled: false }),
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/steward/activity/batches/run', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({}),
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/steward/personal-intelligence/reports/report%2Fone/regenerate', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ reason: 'include the latest activity' }),
    }))
  })
})
