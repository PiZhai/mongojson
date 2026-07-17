import { describe, expect, it } from 'vitest'
import type { MusicTrack } from '../types'
import { countMusicTrackCategories, filterMusicTracks, getMusicTrackCategory } from './catalog'

const tracks: MusicTrack[] = [
  { id: 'server:1', remoteId: '1', source: 'remote', title: 'Remote', addedAt: '2026-07-12T00:00:00Z' },
  { id: 'local:1', source: 'local', title: 'Local', addedAt: '2026-07-12T00:00:00Z' },
  { id: 'url:1', source: 'remote', title: 'URL', remoteUrl: 'https://example.com/a.mp3', addedAt: '2026-07-12T00:00:00Z' },
]

describe('music catalog categories', () => {
  it('distinguishes server, local, and URL tracks', () => {
    expect(tracks.map(getMusicTrackCategory)).toEqual(['remote', 'local', 'url'])
    expect(countMusicTrackCategories(tracks)).toEqual({ all: 3, remote: 1, local: 1, url: 1 })
  })

  it('filters each source without mixing URL and server tracks', () => {
    expect(filterMusicTracks(tracks, 'remote').map((track) => track.id)).toEqual(['server:1'])
    expect(filterMusicTracks(tracks, 'local').map((track) => track.id)).toEqual(['local:1'])
    expect(filterMusicTracks(tracks, 'url').map((track) => track.id)).toEqual(['url:1'])
  })
})
