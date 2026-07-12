import { apiRequest, resolveApiUrl } from '../../platform/http/client'
import type { MusicAudioQuality, MusicTrack } from './types'

type RemoteMusicTrack = {
  id: string
  title: string
  artist?: string
  note?: string
  original_name: string
  mime_type: string
  size_bytes: number
  duration?: number
  audio_quality?: MusicAudioQuality
  created_at: string
}

type RemoteMusicPage = {
  tracks: RemoteMusicTrack[]
  next_cursor?: string
}

function toMusicTrack(track: RemoteMusicTrack): MusicTrack {
  return {
    id: `server:${track.id}`,
    remoteId: track.id,
    source: 'remote',
    title: track.title,
    artist: track.artist,
    note: track.note,
    remoteUrl: resolveApiUrl(`/music/tracks/${track.id}/content`).toString(),
    fileName: track.original_name,
    mimeType: track.mime_type,
    duration: track.duration,
    audioQuality: track.audio_quality?.analyzedAt ? track.audio_quality : undefined,
    addedAt: track.created_at,
  }
}

export async function fetchRemoteMusicPage(cursor?: string, limit = 20) {
  const url = resolveApiUrl('/music/tracks')
  url.searchParams.set('limit', String(limit))
  if (cursor) {
    url.searchParams.set('cursor', cursor)
  }
  const page = await apiRequest<RemoteMusicPage>(url.toString())
  return {
    tracks: page.tracks.map(toMusicTrack),
    nextCursor: page.next_cursor,
  }
}

export async function uploadMusicTrack(file: File, track: MusicTrack) {
  const body = new FormData()
  body.set('file', file)
  body.set('title', track.title)
  if (track.artist) body.set('artist', track.artist)
  if (track.note) body.set('note', track.note)
  if (track.duration) body.set('duration', String(track.duration))
  if (track.audioQuality) body.set('audio_quality', JSON.stringify(track.audioQuality))

  const response = await apiRequest<{ track: RemoteMusicTrack }>(resolveApiUrl('/music/tracks').toString(), {
    method: 'POST',
    body,
  })
  return toMusicTrack(response.track)
}
