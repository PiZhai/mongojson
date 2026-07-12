import {
  createContext,
  type PointerEvent,
  type PropsWithChildren,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import './styles.css'
import { Link, useLocation } from 'react-router-dom'
import type { MusicLibraryFolder, MusicLibraryState, MusicTrack, PlaybackMode } from './types'
import {
  deleteLocalDirectoryHandle,
  deleteLocalFileHandle,
  getLocalDirectoryHandle,
  getLocalFileHandle,
  loadMusicLibraryState,
  saveLocalDirectoryHandle,
  saveLocalFileHandle,
  saveMusicLibraryState,
  supportsPersistentLocalFiles,
  supportsPersistentMusicFolders,
  type LocalDirectoryHandle,
  type LocalFileHandle,
} from './lib/storage'
import {
  analyzeAudioFileQuality,
  analyzeRemoteAudioQuality,
  inferAudioQuality,
  mergeDurationIntoQuality,
  summarizeAudioQuality,
} from './lib/audioQuality'
import type { MusicAudioQuality } from './types'
import { musicModule } from './manifest'
import { getTrackSourceKey } from './lib/sourceKey'
import { fetchRemoteMusicPage, uploadMusicTrack } from './api'

type RemoteTrackPayload = {
  title: string
  artist?: string
  note?: string
  remoteUrl: string
}

type TrackEditPayload = {
  title: string
  artist?: string
  note?: string
  remoteUrl?: string
}

type FolderScanResult = {
  folderId: string
  folderName: string
  added: number
  scanned: number
  skipped: number
  lyricsMatched: number
}

type LrcLine = {
  time: number
  text: string
}

type LocalTrackOptions = {
  localHandleId?: string
  folderHandleId?: string
  relativePath?: string
  lyricHandleId?: string
  lyricFileName?: string
  lyricRelativePath?: string
  audioQuality?: MusicAudioQuality
}

type LyricUpdate = {
  trackId: string
  lyricHandleId: string
  lyricFileName: string
  lyricRelativePath: string
}

type FolderTrackCollectionResult = {
  tracks: MusicTrack[]
  scanned: number
  skipped: number
  lyricsMatched: number
  lyricUpdates: LyricUpdate[]
}

type MusicPlayerContextValue = {
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
  addRemoteTrack: (payload: RemoteTrackPayload) => string
  addLocalFileHandles: (handles: LocalFileHandle[]) => Promise<void>
  addLocalFiles: (files: File[]) => Promise<void>
  uploadLocalTrack: (id: string) => Promise<void>
  loadMoreRemoteTracks: () => Promise<void>
  scanLocalDirectory: (handle: LocalDirectoryHandle) => Promise<FolderScanResult>
  rescanLocalFolder: (folderId: string) => Promise<FolderScanResult>
  removeLocalFolder: (folderId: string) => void
  updateTrack: (id: string, payload: TrackEditPayload) => void
  removeTrack: (id: string) => void
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
  openQueue: () => void
  closeQueue: () => void
  isQueueOpen: boolean
  persistentLocalFilesSupported: boolean
  persistentMusicFoldersSupported: boolean
}

const MusicPlayerContext = createContext<MusicPlayerContextValue | null>(null)

const FLOATING_PLAYER_POSITION_KEY = 'personal-tooling-music-floating-position'
const FLOATING_PLAYER_WIDTH = 220
const FLOATING_PLAYER_HEIGHT = 82
const FLOATING_PLAYER_MARGIN = 16
const SUPPORTED_AUDIO_EXTENSIONS = /\.(mp3|flac|wav|ogg|m4a|aac|opus|webm)$/i
const SUPPORTED_LYRIC_EXTENSION = /\.lrc$/i

type FloatingPlayerPosition = {
  x: number
  y: number
}

function createMusicId(prefix: string) {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return `${prefix}-${crypto.randomUUID()}`
  }

  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function isSupportedAudioFile(fileName: string, mimeType?: string) {
  return Boolean(mimeType?.startsWith('audio/')) || SUPPORTED_AUDIO_EXTENSIONS.test(fileName)
}

function isSupportedLyricFile(fileName: string) {
  return SUPPORTED_LYRIC_EXTENSION.test(fileName)
}

function getPathWithoutExtension(path: string) {
  return path.replace(/\\/g, '/').replace(/\.[^/.]+$/, '').toLowerCase()
}

function createLocalTrackFromFile(file: File, id: string, options: LocalTrackOptions = {}): MusicTrack {
  return {
    id,
    source: 'local',
    title: file.name.replace(/\.[^.]+$/, '') || file.name,
    localHandleId: options.localHandleId,
    folderHandleId: options.folderHandleId,
    relativePath: options.relativePath,
    lyricHandleId: options.lyricHandleId,
    lyricFileName: options.lyricFileName,
    lyricRelativePath: options.lyricRelativePath,
    fileName: file.name,
    mimeType: file.type || undefined,
    duration: options.audioQuality?.duration,
    audioQuality: options.audioQuality,
    addedAt: new Date().toISOString(),
  }
}

function getFolderTrackKey(folderId: string, relativePath: string) {
  return `${folderId}::${relativePath.toLowerCase()}`
}

function getLyricMatchKey(path: string) {
  return getPathWithoutExtension(path)
}

function parseLrcTimestamp(value: string) {
  const match = value.match(/^(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?$/)
  if (!match) {
    return null
  }

  const minutes = Number(match[1])
  const seconds = Number(match[2])
  const fraction = match[3] ?? '0'
  if (!Number.isFinite(minutes) || !Number.isFinite(seconds)) {
    return null
  }

  return minutes * 60 + seconds + Number(fraction.padEnd(3, '0').slice(0, 3)) / 1000
}

function parseLrcText(value: string): LrcLine[] {
  const lines: LrcLine[] = []

  for (const rawLine of value.replace(/^\uFEFF/, '').split(/\r?\n/)) {
    const matches = Array.from(rawLine.matchAll(/\[(\d{1,3}:\d{2}(?:[.:]\d{1,3})?)\]/g))
    if (matches.length === 0) {
      continue
    }

    const text = rawLine.replace(/\[[^\]]+\]/g, '').trim()
    if (!text) {
      continue
    }

    for (const match of matches) {
      const time = parseLrcTimestamp(match[1])
      if (time !== null) {
        lines.push({ time, text })
      }
    }
  }

  return lines.sort((a, b) => a.time - b.time)
}

function countEncodingArtifacts(value: string) {
  const replacementCount = (value.match(/\uFFFD/g) ?? []).length
  const mojibakeCount = (value.match(/锟斤拷|ï»¿|â€|â€™|Ã./g) ?? []).length
  return replacementCount * 4 + mojibakeCount * 3
}

function decodeLyricBuffer(buffer: ArrayBuffer) {
  const bytes = new Uint8Array(buffer)

  if (bytes.length >= 3 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) {
    return new TextDecoder('utf-8').decode(bytes.subarray(3))
  }

  if (bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xfe) {
    return new TextDecoder('utf-16le').decode(bytes.subarray(2))
  }

  if (bytes.length >= 2 && bytes[0] === 0xfe && bytes[1] === 0xff) {
    return new TextDecoder('utf-16be').decode(bytes.subarray(2))
  }

  const candidates: string[] = []
  try {
    candidates.push(new TextDecoder('utf-8', { fatal: true }).decode(bytes))
  } catch {
    // GBK/GB18030 lyrics often fail strict UTF-8 decoding.
  }

  for (const encoding of ['gb18030', 'gbk', 'utf-8']) {
    try {
      const decoded = new TextDecoder(encoding).decode(bytes)
      if (!candidates.includes(decoded)) {
        candidates.push(decoded)
      }
    } catch {
      // Some browsers may not expose every legacy label.
    }
  }

  return candidates
    .filter(Boolean)
    .sort((left, right) => countEncodingArtifacts(left) - countEncodingArtifacts(right))[0] ?? ''
}

async function readLyricFileText(file: File) {
  return decodeLyricBuffer(await file.arrayBuffer())
}

function formatPlayerTime(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return '0:00'
  }

  const minutes = Math.floor(value / 60)
  const seconds = Math.floor(value % 60)
  return `${minutes}:${seconds.toString().padStart(2, '0')}`
}

function PlayerIcon({
  name,
}: {
  name:
    | 'previous'
    | 'play'
    | 'pause'
    | 'next'
    | 'music-page'
    | 'queue'
    | 'close'
    | 'up'
    | 'down'
    | 'trash'
    | 'order'
    | 'repeat-all'
    | 'repeat-one'
    | 'shuffle'
    | 'volume'
}) {
  if (name === 'previous') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M6 5v14" />
        <path d="M18 6l-9 6 9 6z" />
      </svg>
    )
  }

  if (name === 'play') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M8 5l11 7-11 7z" />
      </svg>
    )
  }

  if (name === 'pause') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M8 5v14" />
        <path d="M16 5v14" />
      </svg>
    )
  }

  if (name === 'music-page') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M9 18V6l9-2v12" />
        <circle cx="6.5" cy="18" r="2.5" />
        <circle cx="15.5" cy="16" r="2.5" />
      </svg>
    )
  }

  if (name === 'queue') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M5 7h10" />
        <path d="M5 12h8" />
        <path d="M5 17h6" />
        <path d="M17 15v-4l3 2z" />
      </svg>
    )
  }

  if (name === 'order') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M5 7h11" />
        <path d="M5 12h9" />
        <path d="M5 17h7" />
        <path d="M17 15l2 2 2-2" />
      </svg>
    )
  }

  if (name === 'repeat-all') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M17 3l4 4-4 4" />
        <path d="M3 11V9a2 2 0 0 1 2-2h16" />
        <path d="M7 21l-4-4 4-4" />
        <path d="M21 13v2a2 2 0 0 1-2 2H3" />
      </svg>
    )
  }

  if (name === 'repeat-one') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M17 3l4 4-4 4" />
        <path d="M3 11V9a2 2 0 0 1 2-2h16" />
        <path d="M7 21l-4-4 4-4" />
        <path d="M21 13v2a2 2 0 0 1-2 2H3" />
        <path d="M12 10v5" />
      </svg>
    )
  }

  if (name === 'shuffle') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M4 7h3c2.5 0 4 10 6.5 10H20" />
        <path d="M17 14l3 3-3 3" />
        <path d="M4 17h3c1.2 0 2.1-1.2 3-2.8" />
        <path d="M14 7h6" />
        <path d="M17 4l3 3-3 3" />
      </svg>
    )
  }

  if (name === 'volume') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M4 10v4h4l5 4V6l-5 4z" />
        <path d="M16 9.5a4 4 0 0 1 0 5" />
        <path d="M18.5 7a7 7 0 0 1 0 10" />
      </svg>
    )
  }

  if (name === 'close') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M7 7l10 10" />
        <path d="M17 7L7 17" />
      </svg>
    )
  }

  if (name === 'up') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M12 19V5" />
        <path d="M6 11l6-6 6 6" />
      </svg>
    )
  }

  if (name === 'down') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M12 5v14" />
        <path d="M18 13l-6 6-6-6" />
      </svg>
    )
  }

  if (name === 'trash') {
    return (
      <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
        <path d="M5 7h14" />
        <path d="M9 7V5h6v2" />
        <path d="M8 10v8" />
        <path d="M12 10v8" />
        <path d="M16 10v8" />
      </svg>
    )
  }

  return (
    <svg aria-hidden="true" className="music-control-icon" viewBox="0 0 24 24">
      <path d="M6 6l9 6-9 6z" />
      <path d="M18 5v14" />
    </svg>
  )
}

function clampFloatingPosition(position: FloatingPlayerPosition): FloatingPlayerPosition {
  if (typeof window === 'undefined') {
    return position
  }

  const maxX = Math.max(FLOATING_PLAYER_MARGIN, window.innerWidth - FLOATING_PLAYER_WIDTH - FLOATING_PLAYER_MARGIN)
  const maxY = Math.max(FLOATING_PLAYER_MARGIN, window.innerHeight - FLOATING_PLAYER_HEIGHT - FLOATING_PLAYER_MARGIN)

  return {
    x: Math.min(Math.max(position.x, FLOATING_PLAYER_MARGIN), maxX),
    y: Math.min(Math.max(position.y, FLOATING_PLAYER_MARGIN), maxY),
  }
}

function getDefaultFloatingPosition(): FloatingPlayerPosition {
  if (typeof window === 'undefined') {
    return { x: 0, y: 0 }
  }

  return {
    x: Math.max(FLOATING_PLAYER_MARGIN, window.innerWidth - FLOATING_PLAYER_WIDTH - 24),
    y: Math.max(FLOATING_PLAYER_MARGIN, window.innerHeight - FLOATING_PLAYER_HEIGHT - 24),
  }
}

function loadFloatingPosition() {
  if (typeof window === 'undefined') {
    return getDefaultFloatingPosition()
  }

  try {
    const raw = window.localStorage.getItem(FLOATING_PLAYER_POSITION_KEY)
    return raw ? clampFloatingPosition(JSON.parse(raw) as FloatingPlayerPosition) : getDefaultFloatingPosition()
  } catch {
    return getDefaultFloatingPosition()
  }
}

function getNextTrackId(library: MusicLibraryState) {
  if (!library.currentTrackId || library.queue.length === 0) {
    return library.queue[0]
  }

  if (library.mode === 'shuffle') {
    const candidates = library.queue.length > 1 ? library.queue.filter((id) => id !== library.currentTrackId) : library.queue
    return candidates[Math.floor(Math.random() * candidates.length)]
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

function getPreviousTrackId(library: MusicLibraryState) {
  if (!library.currentTrackId || library.queue.length === 0) {
    return library.queue[0]
  }

  const currentIndex = library.queue.indexOf(library.currentTrackId)
  if (currentIndex > 0) {
    return library.queue[currentIndex - 1]
  }

  return library.mode === 'repeat-all' ? library.queue[library.queue.length - 1] : library.currentTrackId
}

function getModeLabel(mode: PlaybackMode) {
  if (mode === 'repeat-all') {
    return '列表循环'
  }

  if (mode === 'repeat-one') {
    return '单曲循环'
  }

  if (mode === 'shuffle') {
    return '随机播放'
  }

  return '顺序播放'
}

function getModeIcon(mode: PlaybackMode): Parameters<typeof PlayerIcon>[0]['name'] {
  if (mode === 'repeat-all') {
    return 'repeat-all'
  }

  if (mode === 'repeat-one') {
    return 'repeat-one'
  }

  if (mode === 'shuffle') {
    return 'shuffle'
  }

  return 'order'
}

function getNextMode(mode: PlaybackMode): PlaybackMode {
  if (mode === 'order') {
    return 'repeat-all'
  }

  if (mode === 'repeat-all') {
    return 'repeat-one'
  }

  if (mode === 'repeat-one') {
    return 'shuffle'
  }

  return 'order'
}

async function readLocalHandleFile(handle: LocalFileHandle) {
  if (handle.queryPermission && (await handle.queryPermission({ mode: 'read' })) !== 'granted') {
    if (!handle.requestPermission || (await handle.requestPermission({ mode: 'read' })) !== 'granted') {
      throw new Error('本地文件读取授权已失效，请重新选择文件。')
    }
  }

  return handle.getFile()
}

async function ensureDirectoryReadPermission(handle: LocalDirectoryHandle) {
  if (handle.queryPermission && (await handle.queryPermission({ mode: 'read' })) !== 'granted') {
    if (!handle.requestPermission || (await handle.requestPermission({ mode: 'read' })) !== 'granted') {
      throw new Error('本地文件夹读取授权已失效，请重新选择文件夹。')
    }
  }
}

export function MusicPlayerProvider({ children }: PropsWithChildren) {
  const [library, setLibrary] = useState<MusicLibraryState>(() => loadMusicLibraryState())
  const [remoteTracks, setRemoteTracks] = useState<MusicTrack[]>([])
  const [remoteCursor, setRemoteCursor] = useState<string | undefined>()
  const [remoteTracksLoading, setRemoteTracksLoading] = useState(false)
  const [remoteTracksInitialized, setRemoteTracksInitialized] = useState(false)
  const [uploadingTrackIds, setUploadingTrackIds] = useState<ReadonlySet<string>>(() => new Set())
  const [audioSrc, setAudioSrc] = useState<string | null>(null)
  const [isPlaying, setIsPlaying] = useState(false)
  const [isQueueOpen, setIsQueueOpen] = useState(false)
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [lyrics, setLyrics] = useState<LrcLine[]>([])
  const [lyricStatusMessage, setLyricStatusMessage] = useState<string | null>(null)
  const [statusMessage, setStatusMessage] = useState<string | null>(null)
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const objectUrlRef = useRef<string | null>(null)
  const pendingPlayRef = useRef(false)
  const sessionFilesRef = useRef(new Map<string, File>())
  const sessionLyricsRef = useRef(new Map<string, string>())
  const remoteInitialLoadRef = useRef(false)
  const remoteLoadingRef = useRef(false)

  const tracks = useMemo(() => {
    const remoteIds = new Set(remoteTracks.map((track) => track.id))
    return [...remoteTracks, ...library.tracks.filter((track) => !remoteIds.has(track.id))]
  }, [library.tracks, remoteTracks])

  const currentTrack = useMemo(
    () => tracks.find((track) => track.id === library.currentTrackId) ?? null,
    [library.currentTrackId, tracks],
  )
  const currentTrackRef = useRef<MusicTrack | null>(currentTrack)
  const currentTrackSourceKey = getTrackSourceKey(currentTrack)

  useEffect(() => {
    currentTrackRef.current = currentTrack
  }, [currentTrack])

  const currentLyricIndex = useMemo(() => {
    if (lyrics.length === 0) {
      return -1
    }

    for (let index = lyrics.length - 1; index >= 0; index -= 1) {
      if (currentTime + 0.2 >= lyrics[index].time) {
        return index
      }
    }

    return -1
  }, [currentTime, lyrics])

  const currentLyricLine = currentLyricIndex >= 0 ? lyrics[currentLyricIndex] : null

  useEffect(() => {
    saveMusicLibraryState(library)
  }, [library])

  const loadMoreRemoteTracks = useCallback(async () => {
    if (remoteLoadingRef.current || (remoteTracksInitialized && !remoteCursor)) {
      return
    }
    remoteLoadingRef.current = true
    setRemoteTracksLoading(true)
    try {
      const page = await fetchRemoteMusicPage(remoteCursor)
      setRemoteTracks((value) => {
        const known = new Set(value.map((track) => track.id))
        return [...value, ...page.tracks.filter((track) => !known.has(track.id))]
      })
      setRemoteCursor(page.nextCursor)
      setRemoteTracksInitialized(true)
    } catch (error) {
      setStatusMessage(error instanceof Error ? `远程曲库加载失败：${error.message}` : '远程曲库加载失败。')
    } finally {
      remoteLoadingRef.current = false
      setRemoteTracksLoading(false)
    }
  }, [remoteCursor, remoteTracksInitialized])

  useEffect(() => {
    if (remoteInitialLoadRef.current) {
      return
    }
    remoteInitialLoadRef.current = true
    void loadMoreRemoteTracks()
  }, [loadMoreRemoteTracks])

  useEffect(() => {
    const audio = audioRef.current
    if (audio) {
      audio.volume = library.volume
    }
  }, [library.volume])

  useEffect(() => {
    let cancelled = false
    const track = currentTrackRef.current

    async function resolveSource(nextTrack: MusicTrack) {
      if (objectUrlRef.current) {
        const audio = audioRef.current
        if (audio?.src === objectUrlRef.current) {
          audio.pause()
          audio.removeAttribute('src')
          audio.load()
        }
        URL.revokeObjectURL(objectUrlRef.current)
        objectUrlRef.current = null
      }

      setCurrentTime(0)
      setDuration(nextTrack.duration ?? 0)
      setAudioSrc(null)

      if (nextTrack.source === 'remote') {
        if (!nextTrack.remoteUrl) {
          setStatusMessage('这首云端音乐缺少可播放 URL。')
          pendingPlayRef.current = false
          setIsPlaying(false)
          return
        }

        setStatusMessage(null)
        setAudioSrc(nextTrack.remoteUrl)
        return
      }

      try {
        let file = sessionFilesRef.current.get(nextTrack.id)
        if (!file && nextTrack.localHandleId) {
          const handle = await getLocalFileHandle(nextTrack.localHandleId)
          if (handle) {
            file = await readLocalHandleFile(handle)
          }
        }

        if (!file) {
          setStatusMessage('本地音乐需要重新选择文件后才能播放。')
          pendingPlayRef.current = false
          setIsPlaying(false)
          return
        }

        const objectUrl = URL.createObjectURL(file)
        if (!cancelled) {
          objectUrlRef.current = objectUrl
          setStatusMessage(null)
          setAudioSrc(objectUrl)
        } else {
          URL.revokeObjectURL(objectUrl)
        }
      } catch (error) {
        setStatusMessage(error instanceof Error ? error.message : '无法读取本地音乐文件。')
        pendingPlayRef.current = false
        setIsPlaying(false)
      }
    }

    if (!track) {
      pendingPlayRef.current = false
      setAudioSrc(null)
      setCurrentTime(0)
      setDuration(0)
      setIsPlaying(false)
      return undefined
    }

    void resolveSource(track)

    return () => {
      cancelled = true
    }
  }, [currentTrackSourceKey])

  useEffect(() => {
    let cancelled = false

    async function resolveLyrics(track: MusicTrack) {
      setLyrics([])
      setLyricStatusMessage(null)

      try {
        let lyricText = sessionLyricsRef.current.get(track.id)
        if ((!lyricText || countEncodingArtifacts(lyricText) > 0) && track.lyricHandleId) {
          const handle = await getLocalFileHandle(track.lyricHandleId)
          if (handle) {
            const file = await readLocalHandleFile(handle)
            lyricText = await readLyricFileText(file)
            sessionLyricsRef.current.set(track.id, lyricText)
          }
        }

        if (!lyricText) {
          return
        }

        const parsedLyrics = parseLrcText(lyricText)
        if (cancelled) {
          return
        }

        setLyrics(parsedLyrics)
        setLyricStatusMessage(parsedLyrics.length > 0 ? null : '歌词文件没有可识别的时间轴。')
      } catch (error) {
        if (!cancelled) {
          setLyrics([])
          setLyricStatusMessage(error instanceof Error ? error.message : '无法读取歌词文件。')
        }
      }
    }

    if (!currentTrack) {
      setLyrics([])
      setLyricStatusMessage(null)
      return undefined
    }

    void resolveLyrics(currentTrack)

    return () => {
      cancelled = true
    }
  }, [currentTrack])

  const requestAudioPlay = useCallback(() => {
    const audio = audioRef.current
    if (!audio || !audioSrc) {
      pendingPlayRef.current = true
      return
    }

    pendingPlayRef.current = false
    void audio
      .play()
      .then(() => {
        setIsPlaying(true)
        setStatusMessage(null)
      })
      .catch(() => {
        pendingPlayRef.current = false
        setIsPlaying(false)
        setStatusMessage('播放启动失败，请再次点击播放。')
      })
  }, [audioSrc])

  useEffect(() => {
    if (audioSrc && pendingPlayRef.current) {
      requestAudioPlay()
    }
  }, [audioSrc, requestAudioPlay])

  useEffect(() => {
    const audio = audioRef.current
    if (audio && !isPlaying) {
      audio.pause()
    }
  }, [isPlaying])

  useEffect(
    () => () => {
      const audio = audioRef.current
      if (audio) {
        audio.pause()
        audio.removeAttribute('src')
        audio.load()
      }
      if (objectUrlRef.current) {
        URL.revokeObjectURL(objectUrlRef.current)
        objectUrlRef.current = null
      }
    },
    [],
  )

  const setTrackAudioQuality = useCallback((trackId: string, audioQuality: MusicAudioQuality) => {
    setLibrary((value) => ({
      ...value,
      tracks: value.tracks.map((track) =>
        track.id === trackId
          ? {
              ...track,
              audioQuality,
              duration: audioQuality.duration ?? track.duration,
            }
          : track,
      ),
    }))
  }, [])

  const analyzeRemoteTrackAudioQuality = useCallback(
    (trackId: string, remoteUrl: string) => {
      void analyzeRemoteAudioQuality(remoteUrl).then((audioQuality) => {
        setTrackAudioQuality(trackId, audioQuality)
      })
    },
    [setTrackAudioQuality],
  )

  const addRemoteTrack = useCallback((payload: RemoteTrackPayload) => {
    const id = createMusicId('remote')
    const track: MusicTrack = {
      id,
      source: 'remote',
      title: payload.title,
      artist: payload.artist || undefined,
      note: payload.note || undefined,
      remoteUrl: payload.remoteUrl,
      audioQuality: inferAudioQuality({ fileName: payload.remoteUrl }),
      addedAt: new Date().toISOString(),
    }

    setLibrary((value) => ({
      ...value,
      tracks: [track, ...value.tracks],
      queue: [track.id, ...value.queue],
      currentTrackId: value.currentTrackId ?? track.id,
    }))
    setStatusMessage('已添加云端音乐。')
    analyzeRemoteTrackAudioQuality(id, payload.remoteUrl)
    return id
  }, [analyzeRemoteTrackAudioQuality])

  const addLocalFileHandles = useCallback(async (handles: LocalFileHandle[]) => {
    const tracks: MusicTrack[] = []
    const lyricHandles = new Map<string, LocalFileHandle>()
    let lyricsMatched = 0

    for (const handle of handles) {
      if (isSupportedLyricFile(handle.name)) {
        lyricHandles.set(getLyricMatchKey(handle.name), handle)
      }
    }

    for (const handle of handles) {
      if (isSupportedLyricFile(handle.name)) {
        continue
      }

      const file = await readLocalHandleFile(handle)
      if (!isSupportedAudioFile(file.name, file.type)) {
        continue
      }

      const trackId = createMusicId('local')
      const handleId = createMusicId('handle')
      const lyricHandle = lyricHandles.get(getLyricMatchKey(handle.name))
      let lyricHandleId: string | undefined

      if (lyricHandle) {
        lyricHandleId = createMusicId('lyric')
        await saveLocalFileHandle(lyricHandleId, lyricHandle)
        sessionLyricsRef.current.set(trackId, await readLyricFileText(await readLocalHandleFile(lyricHandle)))
        lyricsMatched += 1
      }

      await saveLocalFileHandle(handleId, handle)
      const audioQuality = await analyzeAudioFileQuality(file)
      sessionFilesRef.current.set(trackId, file)
      tracks.push(
        createLocalTrackFromFile(file, trackId, {
          localHandleId: handleId,
          lyricHandleId,
          lyricFileName: lyricHandle?.name,
          audioQuality,
        }),
      )
    }

    if (tracks.length === 0) {
      setStatusMessage('没有找到可识别的本地音乐文件。')
      return
    }

    setLibrary((value) => ({
      ...value,
      tracks: [...tracks, ...value.tracks],
      queue: [...tracks.map((track) => track.id), ...value.queue],
      currentTrackId: value.currentTrackId ?? tracks[0]?.id,
    }))
    setStatusMessage(`已添加 ${tracks.length} 个本地音乐文件${lyricsMatched > 0 ? `，自动匹配 ${lyricsMatched} 个歌词` : ''}。`)
  }, [])

  const addLocalFiles = useCallback(async (files: File[]) => {
    const lyricFiles = new Map<string, File>()
    let lyricsMatched = 0

    for (const file of files) {
      if (isSupportedLyricFile(file.name)) {
        lyricFiles.set(getLyricMatchKey(file.name), file)
      }
    }

    const audioFiles = files.filter((file) => isSupportedAudioFile(file.name, file.type))
    const tracks = await Promise.all(audioFiles.map(async (file) => {
      const trackId = createMusicId('local')
      const lyricFile = lyricFiles.get(getLyricMatchKey(file.name))
      const audioQuality = await analyzeAudioFileQuality(file)
      sessionFilesRef.current.set(trackId, file)
      if (lyricFile) {
        sessionLyricsRef.current.set(trackId, await readLyricFileText(lyricFile))
        lyricsMatched += 1
      }
      return createLocalTrackFromFile(file, trackId, {
        lyricFileName: lyricFile?.name,
        audioQuality,
      })
    }))

    if (tracks.length === 0) {
      setStatusMessage('没有找到可识别的本地音乐文件。')
      return
    }

    setLibrary((value) => ({
      ...value,
      tracks: [...tracks, ...value.tracks],
      queue: [...tracks.map((track) => track.id), ...value.queue],
      currentTrackId: value.currentTrackId ?? tracks[0]?.id,
    }))
    setStatusMessage(
      `已添加 ${tracks.length} 个本地音乐文件${lyricsMatched > 0 ? `，自动匹配 ${lyricsMatched} 个歌词` : ''}；当前浏览器不支持刷新后自动恢复。`,
    )
  }, [])

  const uploadLocalTrack = useCallback(async (id: string) => {
    const track = library.tracks.find((item) => item.id === id)
    if (!track || track.source !== 'local') {
      throw new Error('只能上传本地歌曲。')
    }

    setUploadingTrackIds((value) => new Set(value).add(id))
    try {
      let file = sessionFilesRef.current.get(id)
      if (!file && track.localHandleId) {
        const handle = await getLocalFileHandle(track.localHandleId)
        if (handle) {
          file = await readLocalHandleFile(handle)
        }
      }
      if (!file) {
        throw new Error('本地文件授权已失效，请重新选择该歌曲后再上传。')
      }

      const uploaded = await uploadMusicTrack(file, track)
      setRemoteTracks((value) => [uploaded, ...value.filter((item) => item.id !== uploaded.id)])
      setStatusMessage(`《${track.title}》已上传到远程曲库。`)
    } catch (error) {
      const message = error instanceof Error ? error.message : '歌曲上传失败。'
      setStatusMessage(message)
      throw error
    } finally {
      setUploadingTrackIds((value) => {
        const next = new Set(value)
        next.delete(id)
        return next
      })
    }
  }, [library.tracks])

  const collectDirectoryTracks = useCallback(
    async (
      folderId: string,
      directoryHandle: LocalDirectoryHandle,
      existingTracksByKey: Map<string, MusicTrack>,
    ): Promise<FolderTrackCollectionResult> => {
      const tracks: MusicTrack[] = []
      const audioEntries: Array<{ entry: LocalFileHandle; relativePath: string }> = []
      const lyricEntries = new Map<string, { entry: LocalFileHandle; relativePath: string }>()
      const lyricUpdates: LyricUpdate[] = []
      let scanned = 0
      let skipped = 0
      let lyricsMatched = 0

      async function walk(handle: LocalDirectoryHandle, basePath: string) {
        for await (const [entryName, entry] of handle.entries()) {
          const relativePath = basePath ? `${basePath}/${entryName}` : entryName

          if (entry.kind === 'directory') {
            await walk(entry, relativePath)
            continue
          }

          if (isSupportedLyricFile(entry.name)) {
            lyricEntries.set(getLyricMatchKey(relativePath), { entry, relativePath })
            continue
          }

          if (!isSupportedAudioFile(entry.name)) {
            continue
          }

          audioEntries.push({ entry, relativePath })
        }
      }

      await ensureDirectoryReadPermission(directoryHandle)
      await walk(directoryHandle, '')

      for (const { entry, relativePath } of audioEntries) {
        scanned += 1

        const trackKey = getFolderTrackKey(folderId, relativePath)
        const lyricEntry = lyricEntries.get(getLyricMatchKey(relativePath))
        const existingTrack = existingTracksByKey.get(trackKey)

        if (existingTrack) {
          skipped += 1
          if (lyricEntry && !existingTrack.lyricHandleId) {
            const lyricHandleId = createMusicId('lyric')
            await saveLocalFileHandle(lyricHandleId, lyricEntry.entry)
            lyricUpdates.push({
              trackId: existingTrack.id,
              lyricHandleId,
              lyricFileName: lyricEntry.entry.name,
              lyricRelativePath: lyricEntry.relativePath,
            })
            lyricsMatched += 1
          }
          continue
        }

        const file = await readLocalHandleFile(entry)
        const trackId = createMusicId('local')
        const handleId = createMusicId('handle')
        let lyricHandleId: string | undefined

        if (lyricEntry) {
          lyricHandleId = createMusicId('lyric')
          await saveLocalFileHandle(lyricHandleId, lyricEntry.entry)
          sessionLyricsRef.current.set(trackId, await readLyricFileText(await readLocalHandleFile(lyricEntry.entry)))
          lyricsMatched += 1
        }

        await saveLocalFileHandle(handleId, entry)
        const audioQuality = await analyzeAudioFileQuality(file)
        sessionFilesRef.current.set(trackId, file)
        const track = createLocalTrackFromFile(file, trackId, {
          localHandleId: handleId,
          folderHandleId: folderId,
          relativePath,
          lyricHandleId,
          lyricFileName: lyricEntry?.entry.name,
          lyricRelativePath: lyricEntry?.relativePath,
          audioQuality,
        })
        tracks.push(track)
        existingTracksByKey.set(trackKey, track)
      }

      return { tracks, scanned, skipped, lyricsMatched, lyricUpdates }
    },
    [],
  )

  const scanLocalDirectory = useCallback(
    async (handle: LocalDirectoryHandle) => {
      await ensureDirectoryReadPermission(handle)

      let folderId = createMusicId('folder')
      let folderName = handle.name
      let isExistingFolder = false

      if (handle.isSameEntry) {
        for (const folder of library.folders) {
          const savedHandle = await getLocalDirectoryHandle(folder.id).catch(() => undefined)
          if (savedHandle && (await handle.isSameEntry(savedHandle))) {
            folderId = folder.id
            folderName = folder.name
            isExistingFolder = true
            break
          }
        }
      }

      await saveLocalDirectoryHandle(folderId, handle)

      const existingTracksByKey = new Map(
        library.tracks
          .filter((track) => track.folderHandleId === folderId && track.relativePath)
          .map((track) => [getFolderTrackKey(folderId, track.relativePath as string), track]),
      )
      const result = await collectDirectoryTracks(folderId, handle, existingTracksByKey)
      const scannedAt = new Date().toISOString()

      setLibrary((value) => {
        const lyricUpdatesById = new Map(result.lyricUpdates.map((update) => [update.trackId, update]))
        const nextTrackCount =
          value.tracks.filter((track) => track.folderHandleId === folderId).length + result.tracks.length
        const nextFolder: MusicLibraryFolder = {
          id: folderId,
          name: folderName,
          addedAt: value.folders.find((folder) => folder.id === folderId)?.addedAt ?? scannedAt,
          lastScannedAt: scannedAt,
          trackCount: nextTrackCount,
        }
        const folders = isExistingFolder
          ? value.folders.map((folder) => (folder.id === folderId ? nextFolder : folder))
          : [nextFolder, ...value.folders]

        return {
          ...value,
          folders,
          tracks: [
            ...result.tracks,
            ...value.tracks.map((track) => {
              const lyricUpdate = lyricUpdatesById.get(track.id)
              return lyricUpdate
                ? {
                    ...track,
                    lyricHandleId: lyricUpdate.lyricHandleId,
                    lyricFileName: lyricUpdate.lyricFileName,
                    lyricRelativePath: lyricUpdate.lyricRelativePath,
                  }
                : track
            }),
          ],
          queue: [...result.tracks.map((track) => track.id), ...value.queue],
          currentTrackId: value.currentTrackId ?? result.tracks[0]?.id,
        }
      })

      setStatusMessage(
        result.tracks.length > 0
          ? `已扫描 ${folderName}，新增 ${result.tracks.length} 首音乐${result.lyricsMatched > 0 ? `，匹配 ${result.lyricsMatched} 个歌词` : ''}。`
          : `已扫描 ${folderName}，没有发现新的音乐文件${result.lyricsMatched > 0 ? `，补全 ${result.lyricsMatched} 个歌词` : ''}。`,
      )

      return {
        folderId,
        folderName,
        added: result.tracks.length,
        scanned: result.scanned,
        skipped: result.skipped,
        lyricsMatched: result.lyricsMatched,
      }
    },
    [collectDirectoryTracks, library.folders, library.tracks],
  )

  const rescanLocalFolder = useCallback(
    async (folderId: string) => {
      const folder = library.folders.find((item) => item.id === folderId)
      if (!folder) {
        throw new Error('没有找到这个音乐文件夹。')
      }

      const handle = await getLocalDirectoryHandle(folderId)
      if (!handle) {
        throw new Error('这个文件夹需要重新选择后才能扫描。')
      }

      await ensureDirectoryReadPermission(handle)
      const existingTracksByKey = new Map(
        library.tracks
          .filter((track) => track.folderHandleId === folderId && track.relativePath)
          .map((track) => [getFolderTrackKey(folderId, track.relativePath as string), track]),
      )
      const result = await collectDirectoryTracks(folderId, handle, existingTracksByKey)
      const scannedAt = new Date().toISOString()

      setLibrary((value) => {
        const lyricUpdatesById = new Map(result.lyricUpdates.map((update) => [update.trackId, update]))
        const nextTrackCount =
          value.tracks.filter((track) => track.folderHandleId === folderId).length + result.tracks.length

        return {
          ...value,
          folders: value.folders.map((item) =>
            item.id === folderId
              ? {
                  ...item,
                  lastScannedAt: scannedAt,
                  trackCount: nextTrackCount,
                }
              : item,
          ),
          tracks: [
            ...result.tracks,
            ...value.tracks.map((track) => {
              const lyricUpdate = lyricUpdatesById.get(track.id)
              return lyricUpdate
                ? {
                    ...track,
                    lyricHandleId: lyricUpdate.lyricHandleId,
                    lyricFileName: lyricUpdate.lyricFileName,
                    lyricRelativePath: lyricUpdate.lyricRelativePath,
                  }
                : track
            }),
          ],
          queue: [...result.tracks.map((track) => track.id), ...value.queue],
          currentTrackId: value.currentTrackId ?? result.tracks[0]?.id,
        }
      })

      setStatusMessage(
        result.tracks.length > 0
          ? `已重新扫描 ${folder.name}，新增 ${result.tracks.length} 首音乐${result.lyricsMatched > 0 ? `，匹配 ${result.lyricsMatched} 个歌词` : ''}。`
          : `已重新扫描 ${folder.name}，没有发现新的音乐文件${result.lyricsMatched > 0 ? `，补全 ${result.lyricsMatched} 个歌词` : ''}。`,
      )

      return {
        folderId,
        folderName: folder.name,
        added: result.tracks.length,
        scanned: result.scanned,
        skipped: result.skipped,
        lyricsMatched: result.lyricsMatched,
      }
    },
    [collectDirectoryTracks, library.folders, library.tracks],
  )

  const removeLocalFolder = useCallback((folderId: string) => {
    setLibrary((value) => ({
      ...value,
      folders: value.folders.filter((folder) => folder.id !== folderId),
    }))
    void deleteLocalDirectoryHandle(folderId).catch(() => undefined)
    setStatusMessage('已停止跟踪这个音乐文件夹，已扫描歌曲仍保留在曲库。')
  }, [])

  const updateTrack = useCallback((id: string, payload: TrackEditPayload) => {
    setLibrary((value) => ({
      ...value,
      tracks: value.tracks.map((track) =>
        track.id === id
          ? {
              ...track,
              title: payload.title,
              artist: payload.artist || undefined,
              note: payload.note || undefined,
              remoteUrl: track.source === 'remote' ? payload.remoteUrl : track.remoteUrl,
            }
          : track,
      ),
    }))
    setStatusMessage('已更新音乐信息。')
    if (payload.remoteUrl) {
      analyzeRemoteTrackAudioQuality(id, payload.remoteUrl)
    }
  }, [analyzeRemoteTrackAudioQuality])

  const removeTrack = useCallback((id: string) => {
    setLibrary((value) => {
      const removedTrack = value.tracks.find((track) => track.id === id)
      const tracks = value.tracks.filter((track) => track.id !== id)
      const queue = value.queue.filter((trackId) => trackId !== id)
      const currentTrackId = value.currentTrackId === id ? queue[0] : value.currentTrackId

      if (removedTrack?.localHandleId) {
        void deleteLocalFileHandle(removedTrack.localHandleId).catch(() => undefined)
      }
      if (removedTrack?.lyricHandleId) {
        void deleteLocalFileHandle(removedTrack.lyricHandleId).catch(() => undefined)
      }
      sessionFilesRef.current.delete(id)
      sessionLyricsRef.current.delete(id)

      return {
        ...value,
        folders: removedTrack?.folderHandleId
          ? value.folders.map((folder) =>
              folder.id === removedTrack.folderHandleId
                ? { ...folder, trackCount: Math.max((folder.trackCount ?? 1) - 1, 0) }
                : folder,
            )
          : value.folders,
        tracks,
        queue,
        currentTrackId,
      }
    })
    setStatusMessage('已删除音乐。')
  }, [])

  const playTrack = useCallback(
    (id: string) => {
      pendingPlayRef.current = true
      setLibrary((value) => ({
        ...value,
        queue: value.queue.includes(id) ? value.queue : [id, ...value.queue],
        currentTrackId: id,
      }))

      if (library.currentTrackId === id) {
        requestAudioPlay()
      }
    },
    [library.currentTrackId, requestAudioPlay],
  )

  const enqueueTrack = useCallback((id: string) => {
    setLibrary((value) => ({
      ...value,
      queue: value.queue.includes(id) ? value.queue : [...value.queue, id],
    }))
    setStatusMessage('已加入播放队列。')
  }, [])

  const removeFromQueue = useCallback((id: string) => {
    setLibrary((value) => {
      const queue = value.queue.filter((trackId) => trackId !== id)
      const currentTrackId = value.currentTrackId === id ? queue[0] : value.currentTrackId
      return {
        ...value,
        queue,
        currentTrackId,
      }
    })
    setStatusMessage('已从播放队列移除。')
  }, [])

  const clearQueue = useCallback(() => {
    setLibrary((value) => ({
      ...value,
      queue: value.currentTrackId ? [value.currentTrackId] : [],
    }))
    setStatusMessage('已清空待播队列。')
  }, [])

  const moveQueueItem = useCallback((id: string, direction: 'up' | 'down') => {
    setLibrary((value) => {
      const index = value.queue.indexOf(id)
      if (index < 0) {
        return value
      }

      const nextIndex = direction === 'up' ? index - 1 : index + 1
      if (nextIndex < 0 || nextIndex >= value.queue.length) {
        return value
      }

      const queue = [...value.queue]
      const [item] = queue.splice(index, 1)
      queue.splice(nextIndex, 0, item)
      return { ...value, queue }
    })
  }, [])

  const playNext = useCallback(() => {
    const nextTrackId = getNextTrackId(library)
    if (!nextTrackId) {
      pendingPlayRef.current = false
      setIsPlaying(false)
      return
    }

    pendingPlayRef.current = true
    if (nextTrackId === library.currentTrackId) {
      if (audioRef.current) {
        audioRef.current.currentTime = 0
      }
      requestAudioPlay()
      return
    }

    setLibrary((value) => ({ ...value, currentTrackId: nextTrackId }))
  }, [library, requestAudioPlay])

  const playPrevious = useCallback(() => {
    const previousTrackId = getPreviousTrackId(library)
    if (!previousTrackId) {
      return
    }

    pendingPlayRef.current = true
    setLibrary((value) => ({ ...value, currentTrackId: previousTrackId }))
    if (previousTrackId === library.currentTrackId) {
      requestAudioPlay()
    }
  }, [library, requestAudioPlay])

  const togglePlay = useCallback(() => {
    if (isPlaying) {
      pendingPlayRef.current = false
      audioRef.current?.pause()
      setIsPlaying(false)
      return
    }

    if (!library.currentTrackId && library.queue[0]) {
      pendingPlayRef.current = true
      setLibrary((value) => ({ ...value, currentTrackId: value.queue[0] }))
      return
    }

    if (!library.currentTrackId && library.tracks[0]) {
      pendingPlayRef.current = true
      setLibrary((value) => ({ ...value, currentTrackId: value.tracks[0].id, queue: value.queue.length ? value.queue : value.tracks.map((track) => track.id) }))
      return
    }

    pendingPlayRef.current = true
    requestAudioPlay()
  }, [isPlaying, library.currentTrackId, library.queue, library.tracks, requestAudioPlay])

  const seek = useCallback((time: number) => {
    const audio = audioRef.current
    if (!audio || !Number.isFinite(time)) {
      return
    }

    audio.currentTime = Math.min(Math.max(time, 0), Number.isFinite(audio.duration) ? audio.duration : time)
    setCurrentTime(audio.currentTime)
  }, [])

  const setVolume = useCallback((volume: number) => {
    setLibrary((value) => ({
      ...value,
      volume: Math.min(1, Math.max(0, volume)),
    }))
  }, [])

  const setMode = useCallback((mode: PlaybackMode) => {
    setLibrary((value) => ({ ...value, mode }))
  }, [])

  const handleEnded = () => {
    if (library.mode === 'repeat-one' && audioRef.current) {
      audioRef.current.currentTime = 0
      pendingPlayRef.current = true
      requestAudioPlay()
      return
    }

    const nextTrackId = getNextTrackId(library)
    if (nextTrackId) {
      pendingPlayRef.current = true
      if (nextTrackId === library.currentTrackId) {
        if (audioRef.current) {
          audioRef.current.currentTime = 0
        }
        requestAudioPlay()
        return
      }

      setLibrary((value) => ({ ...value, currentTrackId: nextTrackId }))
    } else {
      pendingPlayRef.current = false
      setIsPlaying(false)
    }
  }

  const handleLoadedMetadata = () => {
    const audio = audioRef.current
    if (!audio) {
      return
    }

    const nextDuration = Number.isFinite(audio.duration) ? audio.duration : 0
    setDuration(nextDuration)

    if (currentTrack && nextDuration > 0) {
      setLibrary((value) => ({
        ...value,
        tracks: value.tracks.map((track) =>
          track.id === currentTrack.id
            ? {
                ...track,
                duration: nextDuration,
                audioQuality: mergeDurationIntoQuality(track.audioQuality, nextDuration),
              }
            : track,
        ),
      }))
    }
  }

  const handleAudioError = () => {
    pendingPlayRef.current = false
    setIsPlaying(false)
    setStatusMessage('音频加载失败，请检查 URL、CORS、Range 请求或本地文件权限。')
  }

  const value = useMemo<MusicPlayerContextValue>(
    () => ({
      tracks,
      folders: library.folders,
      queue: library.queue,
      currentTrack,
      currentTrackId: library.currentTrackId,
      currentTime,
      duration,
      lyrics,
      currentLyricLine,
      currentLyricIndex,
      lyricStatusMessage,
      isPlaying,
      volume: library.volume,
      mode: library.mode,
      statusMessage,
      remoteTracksLoading,
      remoteTracksHasMore: !remoteTracksInitialized || Boolean(remoteCursor),
      uploadingTrackIds,
      addRemoteTrack,
      addLocalFileHandles,
      addLocalFiles,
      uploadLocalTrack,
      loadMoreRemoteTracks,
      scanLocalDirectory,
      rescanLocalFolder,
      removeLocalFolder,
      updateTrack,
      removeTrack,
      playTrack,
      enqueueTrack,
      removeFromQueue,
      clearQueue,
      moveQueueItem,
      togglePlay,
      playNext,
      playPrevious,
      seek,
      setVolume,
      setMode,
      openQueue: () => setIsQueueOpen(true),
      closeQueue: () => setIsQueueOpen(false),
      isQueueOpen,
      persistentLocalFilesSupported: supportsPersistentLocalFiles(),
      persistentMusicFoldersSupported: supportsPersistentMusicFolders(),
    }),
    [
      addLocalFileHandles,
      addLocalFiles,
      addRemoteTrack,
      clearQueue,
      currentTime,
      currentLyricIndex,
      currentLyricLine,
      currentTrack,
      duration,
      enqueueTrack,
      isPlaying,
      isQueueOpen,
      library.currentTrackId,
      library.folders,
      library.mode,
      library.queue,
      library.volume,
      lyricStatusMessage,
      lyrics,
      loadMoreRemoteTracks,
      moveQueueItem,
      playNext,
      playPrevious,
      playTrack,
      removeLocalFolder,
      removeFromQueue,
      removeTrack,
      rescanLocalFolder,
      scanLocalDirectory,
      seek,
      setMode,
      setVolume,
      statusMessage,
      remoteCursor,
      remoteTracksInitialized,
      remoteTracksLoading,
      tracks,
      uploadLocalTrack,
      uploadingTrackIds,
      togglePlay,
      updateTrack,
    ],
  )

  return (
    <MusicPlayerContext.Provider value={value}>
      {children}
      <audio
        onEnded={handleEnded}
        onError={handleAudioError}
        onLoadedMetadata={handleLoadedMetadata}
        onPause={() => setIsPlaying(false)}
        onPlay={() => setIsPlaying(true)}
        onTimeUpdate={(event) => setCurrentTime(event.currentTarget.currentTime)}
        ref={audioRef}
        src={audioSrc ?? undefined}
      />
    </MusicPlayerContext.Provider>
  )
}

export function useMusicPlayer() {
  const context = useContext(MusicPlayerContext)
  if (!context) {
    throw new Error('useMusicPlayer must be used inside MusicPlayerProvider.')
  }

  return context
}

export function MusicMiniPlayer() {
  const location = useLocation()
  const {
    currentTrack,
    currentTime,
    duration,
    isPlaying,
    mode,
    openQueue,
    playNext,
    playPrevious,
    seek,
    setMode,
    setVolume,
    statusMessage,
    togglePlay,
    tracks,
    volume,
  } = useMusicPlayer()

  const isMusicPage = location.pathname === musicModule.route.path

  if (!currentTrack && tracks.length === 0) {
    return null
  }

  const modeLabel = getModeLabel(mode)
  const nextMode = getNextMode(mode)
  const modeIcon = getModeIcon(mode)

  if (!isMusicPage) {
    return <MusicFloatingPlayer />
  }

  return (
    <section className="music-mini-player" aria-label="音乐播放器">
      <div className="music-mini-surface">
        <div className="music-now-playing">
          <span className="music-equalizer" aria-hidden="true">
            <span />
            <span />
            <span />
          </span>
          <div className="music-now-playing-copy">
            <strong>{currentTrack?.title ?? '未选择音乐'}</strong>
            <span>{currentTrack?.artist || currentTrack?.fileName || currentTrack?.remoteUrl || '打开音乐播放器添加曲目'}</span>
            {currentTrack?.audioQuality ? <span>{summarizeAudioQuality(currentTrack.audioQuality)}</span> : null}
            {statusMessage ? <span className="music-player-inline-message">{statusMessage}</span> : null}
          </div>
        </div>

        <div className="music-playback-cluster">
          <div className="music-transport">
            <button className="music-icon-button" onClick={playPrevious} type="button" aria-label="上一首" title="上一首">
              <PlayerIcon name="previous" />
            </button>
            <button className="music-play-button" onClick={togglePlay} type="button" aria-label={isPlaying ? '暂停' : '播放'} title={isPlaying ? '暂停' : '播放'}>
              <PlayerIcon name={isPlaying ? 'pause' : 'play'} />
            </button>
            <button className="music-icon-button" onClick={playNext} type="button" aria-label="下一首" title="下一首">
              <PlayerIcon name="next" />
            </button>
          </div>

          <div className="music-progress">
            <span className="music-progress-time">{formatPlayerTime(currentTime)}</span>
            <input
              aria-label="播放进度"
              max={duration || 0}
              min={0}
              onChange={(event) => seek(Number(event.target.value))}
              step={1}
              type="range"
              value={duration ? Math.min(currentTime, duration) : 0}
            />
            <span className="music-progress-time">{formatPlayerTime(duration)}</span>
          </div>
        </div>

        <div className="music-mini-actions">
          <button
            aria-label={`播放模式：${modeLabel}，点击切换`}
            className="music-mini-action-button"
            onClick={() => setMode(nextMode)}
            title={`播放模式：${modeLabel}`}
            type="button"
          >
            <PlayerIcon name={modeIcon} />
          </button>
          <button aria-label="打开播放队列" className="music-mini-action-button" onClick={openQueue} title="播放队列" type="button">
            <PlayerIcon name="queue" />
          </button>
          <label className="music-volume-control" title="音量">
            <span className="sr-only">音量</span>
            <PlayerIcon name="volume" />
            <input
              aria-label="音量"
              className="music-volume"
              max={1}
              min={0}
              onChange={(event) => setVolume(Number(event.target.value))}
              step={0.01}
              type="range"
              value={volume}
            />
          </label>
          {isMusicPage ? (
            <span aria-label="当前在音乐页" className="music-current-page-chip" title="当前在音乐页">
              <PlayerIcon name="music-page" />
            </span>
          ) : (
            <Link aria-label="打开音乐页" className="music-mini-action-button" title="打开音乐页" to={musicModule.route.path}>
              <PlayerIcon name="music-page" />
            </Link>
          )}
        </div>
      </div>
    </section>
  )
}

export function MusicQueueDrawer() {
  const {
    clearQueue,
    closeQueue,
    currentTrackId,
    isQueueOpen,
    moveQueueItem,
    playTrack,
    queue,
    removeFromQueue,
    tracks,
  } = useMusicPlayer()
  const queueTracks = queue
    .map((id) => tracks.find((track) => track.id === id))
    .filter((track): track is MusicTrack => Boolean(track))

  if (!isQueueOpen) {
    return null
  }

  return (
    <aside aria-label="播放队列" className="music-queue-drawer">
      <div className="music-queue-panel">
        <div className="music-queue-header">
          <div>
            <p className="music-queue-eyebrow">Queue</p>
            <h2>播放队列</h2>
          </div>
          <div className="music-queue-header-actions">
            <button className="music-queue-text-button" onClick={clearQueue} type="button">
              清空
            </button>
            <button className="music-queue-icon-button" onClick={closeQueue} type="button" aria-label="关闭队列">
              <PlayerIcon name="close" />
            </button>
          </div>
        </div>

        {queueTracks.length === 0 ? (
          <div className="music-queue-empty">播放队列为空。</div>
        ) : (
          <div className="music-queue-list">
            {queueTracks.map((track, index) => (
              <article
                className={`music-queue-item${track.id === currentTrackId ? ' music-queue-item-active' : ''}`}
                key={track.id}
              >
                <button className="music-queue-play-target" onClick={() => playTrack(track.id)} type="button">
                  <span className="music-queue-index">{track.id === currentTrackId ? '播放中' : String(index + 1)}</span>
                  <span className="music-queue-copy">
                    <strong>{track.title}</strong>
                    <span>{track.artist || track.fileName || track.remoteUrl || '未填写来源'}</span>
                  </span>
                </button>
                <div className="music-queue-item-actions">
                  <button
                    aria-label="上移"
                    className="music-queue-icon-button"
                    disabled={index === 0}
                    onClick={() => moveQueueItem(track.id, 'up')}
                    type="button"
                  >
                    <PlayerIcon name="up" />
                  </button>
                  <button
                    aria-label="下移"
                    className="music-queue-icon-button"
                    disabled={index === queueTracks.length - 1}
                    onClick={() => moveQueueItem(track.id, 'down')}
                    type="button"
                  >
                    <PlayerIcon name="down" />
                  </button>
                  <button
                    aria-label="从队列移除"
                    className="music-queue-icon-button music-queue-danger-button"
                    onClick={() => removeFromQueue(track.id)}
                    type="button"
                  >
                    <PlayerIcon name="trash" />
                  </button>
                </div>
              </article>
            ))}
          </div>
        )}
      </div>
    </aside>
  )
}

function MusicFloatingPlayer() {
  const { currentTrack, isPlaying, playNext, playPrevious, statusMessage, togglePlay } = useMusicPlayer()
  const [position, setPosition] = useState(loadFloatingPosition)
  const dragRef = useRef<{ offsetX: number; offsetY: number } | null>(null)

  useEffect(() => {
    const handleResize = () => {
      setPosition((value) => {
        const nextPosition = clampFloatingPosition(value)
        window.localStorage.setItem(FLOATING_PLAYER_POSITION_KEY, JSON.stringify(nextPosition))
        return nextPosition
      })
    }

    window.addEventListener('resize', handleResize)
    return () => window.removeEventListener('resize', handleResize)
  }, [])

  const updatePosition = useCallback((nextPosition: FloatingPlayerPosition) => {
    const clampedPosition = clampFloatingPosition(nextPosition)
    setPosition(clampedPosition)
    window.localStorage.setItem(FLOATING_PLAYER_POSITION_KEY, JSON.stringify(clampedPosition))
  }, [])

  const handlePointerDown = (event: PointerEvent<HTMLElement>) => {
    event.currentTarget.setPointerCapture(event.pointerId)
    dragRef.current = {
      offsetX: event.clientX - position.x,
      offsetY: event.clientY - position.y,
    }
  }

  const handlePointerMove = (event: PointerEvent<HTMLElement>) => {
    if (!dragRef.current) {
      return
    }

    updatePosition({
      x: event.clientX - dragRef.current.offsetX,
      y: event.clientY - dragRef.current.offsetY,
    })
  }

  const handlePointerUp = (event: PointerEvent<HTMLElement>) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
    dragRef.current = null
  }

  return (
    <section
      aria-label="浮动音乐播放器"
      className="music-floating-player"
      style={{ left: position.x, top: position.y }}
    >
      <Link aria-label="打开音乐页" className="music-floating-page-link" title="打开音乐页" to={musicModule.route.path}>
        <PlayerIcon name="music-page" />
      </Link>

      <div
        className="music-floating-drag"
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
      >
        <span className="music-floating-grip" aria-hidden="true" />
        <div className="music-floating-copy">
          <strong>{currentTrack?.title ?? '音乐播放器'}</strong>
          <span>{statusMessage || (currentTrack?.audioQuality ? summarizeAudioQuality(currentTrack.audioQuality) : currentTrack?.artist || currentTrack?.fileName || '拖动浮标调整位置')}</span>
        </div>
      </div>

      <div className="music-floating-controls">
        <button className="music-floating-control-button" onClick={playPrevious} type="button" aria-label="上一首" title="上一首">
          <PlayerIcon name="previous" />
        </button>
        <button
          className="music-floating-control-button music-floating-control-primary"
          onClick={togglePlay}
          type="button"
          aria-label={isPlaying ? '暂停' : '播放'}
          title={isPlaying ? '暂停' : '播放'}
        >
          <PlayerIcon name={isPlaying ? 'pause' : 'play'} />
        </button>
        <button className="music-floating-control-button" onClick={playNext} type="button" aria-label="下一首" title="下一首">
          <PlayerIcon name="next" />
        </button>
      </div>
    </section>
  )
}
