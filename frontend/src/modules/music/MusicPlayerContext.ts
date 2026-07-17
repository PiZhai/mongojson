import { createContext, useContext } from 'react'
import type { LocalDirectoryHandle, LocalFileHandle } from './lib/storage'
import type { MusicLibraryFolder, MusicPlaylist, MusicTrack, PlaybackMode } from './types'

export type RemoteTrackPayload = {
  title: string
  artist?: string
  note?: string
  remoteUrl: string
}

export type TrackEditPayload = {
  title: string
  artist?: string
  note?: string
  remoteUrl?: string
}

export type FolderScanResult = {
  folderId: string
  folderName: string
  added: number
  scanned: number
  skipped: number
  lyricsMatched: number
}

export type LrcLine = {
  time: number
  text: string
}

export type MusicPlayerContextValue = {
  tracks: MusicTrack[]
  folders: MusicLibraryFolder[]
  queue: string[]
  currentTrack: MusicTrack | null
  currentTrackId?: string
  currentTime: number
  duration: number
  lyrics: LrcLine[]
  currentLyricLine: LrcLine | null
  currentLyricIndex: number
  lyricStatusMessage: string | null
  isPlaying: boolean
  volume: number
  mode: PlaybackMode
  statusMessage: string | null
  remoteTracksLoading: boolean
  remoteTracksHasMore: boolean
  uploadingTrackIds: ReadonlySet<string>
  favoriteTrackIds: string[]
  recentTrackIds: string[]
  playlists: MusicPlaylist[]
  addRemoteTrack: (payload: RemoteTrackPayload) => string
  addLocalFileHandles: (handles: LocalFileHandle[]) => Promise<void>
  addLocalFiles: (files: File[]) => Promise<void>
  uploadLocalTrack: (id: string) => Promise<{ duplicate: boolean }>
  loadMoreRemoteTracks: () => Promise<void>
  scanLocalDirectory: (handle: LocalDirectoryHandle) => Promise<FolderScanResult>
  rescanLocalFolder: (folderId: string) => Promise<FolderScanResult>
  removeLocalFolder: (folderId: string) => void
  updateTrack: (id: string, payload: TrackEditPayload) => void
  removeTrack: (id: string) => void
  removeRemoteTrack: (id: string) => Promise<void>
  playTrack: (id: string) => void
  enqueueTrack: (id: string) => void
  removeFromQueue: (id: string) => void
  clearQueue: () => void
  moveQueueItem: (id: string, direction: 'up' | 'down') => void
  togglePlay: () => void
  playNext: () => void
  playPrevious: () => void
  seek: (time: number) => void
  setVolume: (volume: number) => void
  setMode: (mode: PlaybackMode) => void
  toggleFavorite: (id: string) => void
  createPlaylist: (name: string) => string
  addTrackToPlaylist: (trackId: string, playlistId: string) => void
  deletePlaylist: (playlistId: string) => void
  openQueue: () => void
  closeQueue: () => void
  isQueueOpen: boolean
  persistentLocalFilesSupported: boolean
  persistentMusicFoldersSupported: boolean
}

export const MusicPlayerContext = createContext<MusicPlayerContextValue | null>(null)

export function useMusicPlayer() {
  const context = useContext(MusicPlayerContext)
  if (!context) throw new Error('useMusicPlayer must be used inside MusicPlayerProvider.')
  return context
}
