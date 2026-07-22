export type MusicTrackSource = 'remote' | 'local'

export type PlaybackMode = 'order' | 'repeat-all' | 'repeat-one' | 'shuffle'

export type MusicArtworkRef =
  | { kind: 'remote'; url: string; mimeType: string }
  | { kind: 'local'; storageKey: string; mimeType: string }

export type MusicAudioQuality = {
  container?: string
  codec?: string
  bitrate?: number
  sampleRate?: number
  bitsPerSample?: number
  numberOfChannels?: number
  lossless?: boolean
  duration?: number
  fileSize?: number
  analyzedAt: string
  analysisSource: 'metadata' | 'inferred'
  error?: string
}

export type MusicTrack = {
  id: string
  remoteId?: string
  fileAvailable?: boolean
  recordIssue?: string
  source: MusicTrackSource
  title: string
  artist?: string
  note?: string
  remoteUrl?: string
  localHandleId?: string
  folderHandleId?: string
  relativePath?: string
  lyricHandleId?: string
  lyricFileName?: string
  lyricUrl?: string
  lyricRelativePath?: string
  fileName?: string
  mimeType?: string
  duration?: number
  audioQuality?: MusicAudioQuality
  artwork?: MusicArtworkRef
  /** Resolved at runtime. Never persisted to localStorage. */
  artworkUrl?: string
  addedAt: string
}

export type MusicLibraryFolder = {
  id: string
  name: string
  addedAt: string
  lastScannedAt?: string
  trackCount?: number
}

export type MusicPlaylist = {
  id: string
  name: string
  trackIds: string[]
  createdAt: string
}

export type MusicLibraryState = {
  tracks: MusicTrack[]
  folders: MusicLibraryFolder[]
  queue: string[]
  favoriteTrackIds: string[]
  recentTrackIds: string[]
  playlists: MusicPlaylist[]
  currentTrackId?: string
  volume: number
  mode: PlaybackMode
}
