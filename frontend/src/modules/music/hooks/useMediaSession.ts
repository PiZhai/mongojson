import { useEffect } from 'react'
import type { MusicTrack } from '../types'
import { getGeneratedMusicArtworkUrl } from '../lib/artwork'

type MediaSessionControls = {
  currentTime: number
  duration: number
  isPlaying: boolean
  onNext: () => void
  onPause: () => void
  onPlay: () => void
  onPrevious: () => void
  onSeek: (time: number) => void
  track: MusicTrack | null
}

export function useMediaSession({
  currentTime,
  duration,
  isPlaying,
  onNext,
  onPause,
  onPlay,
  onPrevious,
  onSeek,
  track,
}: MediaSessionControls) {
  useEffect(() => {
    if (!('mediaSession' in navigator)) return

    navigator.mediaSession.metadata = track
      ? new MediaMetadata({
          title: track.title,
          artist: track.artist || '未知歌手',
          album: track.note || (track.remoteId ? '云端曲库' : track.source === 'local' ? '本地音乐' : 'URL 音乐'),
          artwork: track.artworkUrl && track.artwork
            ? [{ src: track.artworkUrl, type: track.artwork.mimeType }]
            : [{ src: getGeneratedMusicArtworkUrl(track.title, track.artist), type: 'image/svg+xml', sizes: '512x512' }],
        })
      : null
  }, [track])

  useEffect(() => {
    if (!('mediaSession' in navigator)) return

    navigator.mediaSession.playbackState = isPlaying ? 'playing' : 'paused'
    const handlers: Array<[MediaSessionAction, MediaSessionActionHandler | null]> = [
      ['play', onPlay],
      ['pause', onPause],
      ['previoustrack', onPrevious],
      ['nexttrack', onNext],
      ['seekbackward', (details) => onSeek(Math.max(0, currentTime - (details.seekOffset || 10)))],
      ['seekforward', (details) => onSeek(Math.min(duration || currentTime + 10, currentTime + (details.seekOffset || 10)))],
      ['seekto', (details) => details.seekTime === undefined ? undefined : onSeek(details.seekTime)],
    ]

    for (const [action, handler] of handlers) {
      try {
        navigator.mediaSession.setActionHandler(action, handler)
      } catch {
        // Browsers expose different subsets of Media Session actions.
      }
    }

    return () => {
      for (const [action] of handlers) {
        try {
          navigator.mediaSession.setActionHandler(action, null)
        } catch {
          // Ignore unsupported actions during cleanup as well.
        }
      }
    }
  }, [currentTime, duration, isPlaying, onNext, onPause, onPlay, onPrevious, onSeek])

  useEffect(() => {
    if (!('mediaSession' in navigator) || duration <= 0 || !Number.isFinite(duration)) return
    try {
      navigator.mediaSession.setPositionState({
        duration,
        playbackRate: 1,
        position: Math.min(Math.max(currentTime, 0), duration),
      })
    } catch {
      // Position state is optional and may reject incomplete metadata.
    }
  }, [currentTime, duration])
}
