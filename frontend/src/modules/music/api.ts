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
  lyric_file_name?: string
  lyric_mime_type?: string
  artwork_available: boolean
  artwork_mime_type?: string
  file_available: boolean
  record_issue?: string
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
    fileAvailable: track.file_available,
    recordIssue: track.record_issue,
    source: 'remote',
    title: track.title,
    artist: track.artist,
    note: track.note,
    remoteUrl: resolveApiUrl(`/music/tracks/${track.id}/content`).toString(),
    fileName: track.original_name,
    mimeType: track.mime_type,
    duration: track.duration,
    audioQuality: track.audio_quality?.analyzedAt ? track.audio_quality : undefined,
    lyricFileName: track.lyric_file_name,
    lyricUrl: track.lyric_file_name ? resolveApiUrl(`/music/tracks/${track.id}/lyrics`).toString() : undefined,
    artwork: track.artwork_available && track.artwork_mime_type
      ? {
          kind: 'remote',
          url: resolveApiUrl(`/music/tracks/${track.id}/artwork`).toString(),
          mimeType: track.artwork_mime_type,
        }
      : undefined,
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

export async function uploadMusicTrack(file: File, track: MusicTrack, lyric?: File, artwork?: File) {
  const body = new FormData()
  body.set('file', file)
  if (lyric) body.set('lyric', lyric)
  if (artwork) body.set('artwork', artwork)
  body.set('title', track.title)
  if (track.artist) body.set('artist', track.artist)
  if (track.note) body.set('note', track.note)
  if (track.duration) body.set('duration', String(track.duration))
  if (track.audioQuality) body.set('audio_quality', JSON.stringify(track.audioQuality))

  const response = await apiRequest<{ track: RemoteMusicTrack; duplicate: boolean }>(resolveApiUrl('/music/tracks').toString(), {
    method: 'POST',
    body,
  })
  return { track: toMusicTrack(response.track), duplicate: response.duplicate }
}

export async function deleteRemoteMusicTrack(id: string) {
  const response = await fetch(resolveApiUrl(`/music/tracks/${id}`).toString(), { method: 'DELETE' })
  if (!response.ok) {
    const message = await response.text()
    throw new Error(message || `Request failed: ${response.status}`)
  }
}
