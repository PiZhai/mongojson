import type { MusicTrack } from '../types'

export type MusicLibraryFilter = 'all' | 'remote' | 'local' | 'url'

export function getMusicTrackCategory(track: MusicTrack): Exclude<MusicLibraryFilter, 'all'> {
  if (track.source === 'local') return 'local'
  return track.remoteId ? 'remote' : 'url'
}

export function filterMusicTracks(tracks: MusicTrack[], filter: MusicLibraryFilter) {
  return filter === 'all' ? tracks : tracks.filter((track) => getMusicTrackCategory(track) === filter)
}

export function countMusicTrackCategories(tracks: MusicTrack[]) {
  return tracks.reduce(
    (counts, track) => {
      counts[getMusicTrackCategory(track)] += 1
      counts.all += 1
      return counts
    },
    { all: 0, remote: 0, local: 0, url: 0 },
  )
}
