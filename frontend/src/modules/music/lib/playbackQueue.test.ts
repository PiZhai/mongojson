import { describe, expect, it } from 'vitest'
import type { MusicLibraryState, PlaybackMode } from '../types'
import { getNextTrackId, getPreviousTrackId } from './playbackQueue'

function createLibrary(mode: PlaybackMode, currentTrackId = 'b'): MusicLibraryState {
  return {
    tracks: [],
    folders: [],
    queue: ['a', 'b', 'c'],
    favoriteTrackIds: [],
    recentTrackIds: [],
    playlists: [],
    currentTrackId,
    volume: 1,
    mode,
  }
}

describe('playback queue navigation', () => {
  it('moves forward and stops at the end in order mode', () => {
    expect(getNextTrackId(createLibrary('order'))).toBe('c')
    expect(getNextTrackId(createLibrary('order', 'c'))).toBeUndefined()
  })

  it('wraps in repeat-all mode', () => {
    expect(getNextTrackId(createLibrary('repeat-all', 'c'))).toBe('a')
    expect(getPreviousTrackId(createLibrary('repeat-all', 'a'))).toBe('c')
  })

  it('never selects the current track when shuffling a multi-track queue', () => {
    expect(getNextTrackId(createLibrary('shuffle'), () => 0)).toBe('a')
    expect(getNextTrackId(createLibrary('shuffle'), () => 0.99)).toBe('c')
  })

  it('falls back safely when the current track is outside the queue', () => {
    expect(getNextTrackId(createLibrary('order', 'missing'))).toBe('a')
    expect(getPreviousTrackId(createLibrary('order', 'missing'))).toBe('missing')
  })
})
