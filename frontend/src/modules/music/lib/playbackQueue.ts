import type { MusicLibraryState } from '../types'

export function getNextTrackId(library: MusicLibraryState, random = Math.random) {
  if (!library.currentTrackId || library.queue.length === 0) {
    return library.queue[0]
  }

  if (library.mode === 'shuffle') {
    const candidates = library.queue.length > 1
      ? library.queue.filter((id) => id !== library.currentTrackId)
      : library.queue
    return candidates[Math.floor(random() * candidates.length)]
  }

  const currentIndex = library.queue.indexOf(library.currentTrackId)
  if (currentIndex < 0) {
    return library.queue[0]
  }

  if (currentIndex < library.queue.length - 1) {
    return library.queue[currentIndex + 1]
  }

  return library.mode === 'repeat-all' ? library.queue[0] : undefined
}

export function getPreviousTrackId(library: MusicLibraryState) {
  if (!library.currentTrackId || library.queue.length === 0) {
    return library.queue[0]
  }

  const currentIndex = library.queue.indexOf(library.currentTrackId)
  if (currentIndex > 0) {
    return library.queue[currentIndex - 1]
  }

  return library.mode === 'repeat-all'
    ? library.queue[library.queue.length - 1]
    : library.currentTrackId
}
