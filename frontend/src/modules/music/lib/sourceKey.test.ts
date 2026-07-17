import { describe, expect, it } from 'vitest'
import type { MusicTrack } from '../types'
import { getTrackSourceKey } from './sourceKey'

const localTrack: MusicTrack = {
  id: 'track-1',
  source: 'local',
  title: 'Test',
  localHandleId: 'handle-1',
  addedAt: '2026-07-11T00:00:00.000Z',
}

describe('getTrackSourceKey', () => {
  it('stays stable when playback metadata changes', () => {
    expect(getTrackSourceKey({
      ...localTrack,
      duration: 12.5,
      audioQuality: {
        duration: 12.5,
        analyzedAt: '2026-07-11T00:01:00.000Z',
        analysisSource: 'metadata',
      },
    })).toBe(getTrackSourceKey(localTrack))
  })

  it('changes when the actual source changes', () => {
    expect(getTrackSourceKey({ ...localTrack, localHandleId: 'handle-2' })).not.toBe(getTrackSourceKey(localTrack))
  })
})
