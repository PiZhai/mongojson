import type { CSSProperties } from 'react'
import type { MusicTrack } from '../types'
import { getMusicArtworkPalette } from '../lib/artwork'
import { MusicActionIcon } from './MusicActionIcon'

function getMusicArtworkStyle(track: MusicTrack | null) {
  const { hue, alternateHue } = getMusicArtworkPalette(track?.title, track?.artist)
  return {
    '--music-art-hue': `${hue}`,
    '--music-art-hue-alt': `${alternateHue}`,
  } as CSSProperties
}

export function MusicArtwork({
  className = '',
  track,
}: {
  className?: string
  track: MusicTrack | null
}) {
  const label = track ? `《${track.title}》封面` : '默认音乐封面'
  return (
    <span className={`music-artwork-visual${className ? ` ${className}` : ''}`} style={getMusicArtworkStyle(track)}>
      {track?.artworkUrl ? <img alt={label} decoding="async" src={track.artworkUrl} /> : null}
      <span className="music-artwork-fallback" aria-hidden={track?.artworkUrl ? 'true' : undefined}>
        <MusicActionIcon name="music" />
      </span>
    </span>
  )
}
