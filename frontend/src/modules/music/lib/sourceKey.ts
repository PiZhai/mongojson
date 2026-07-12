import type { MusicTrack } from '../types'

export function getTrackSourceKey(track: MusicTrack | null) {
  if (!track) return ''
  return [track.id, track.source, track.remoteUrl ?? '', track.localHandleId ?? ''].join('|')
}
