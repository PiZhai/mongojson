import { useEffect, useRef } from 'react'

type PlaybackShortcutOptions = {
  currentTime: number
  onSeek: (time: number) => void
  onSetVolume: (volume: number) => void
  onTogglePlay: () => void
  volume: number
}

function isEditableTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return false
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT', 'BUTTON', 'A'].includes(target.tagName)
}

export function usePlaybackShortcuts({ currentTime, onSeek, onSetVolume, onTogglePlay, volume }: PlaybackShortcutOptions) {
  const previousVolumeRef = useRef(volume || 0.82)

  useEffect(() => {
    if (volume > 0) previousVolumeRef.current = volume
  }, [volume])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.altKey || event.ctrlKey || event.metaKey || isEditableTarget(event.target)) return

      if (event.code === 'Space') {
        event.preventDefault()
        onTogglePlay()
      } else if (event.key === 'ArrowLeft') {
        event.preventDefault()
        onSeek(Math.max(0, currentTime - 5))
      } else if (event.key === 'ArrowRight') {
        event.preventDefault()
        onSeek(currentTime + 5)
      } else if (event.key.toLowerCase() === 'm') {
        event.preventDefault()
        onSetVolume(volume > 0 ? 0 : previousVolumeRef.current)
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [currentTime, onSeek, onSetVolume, onTogglePlay, volume])
}
