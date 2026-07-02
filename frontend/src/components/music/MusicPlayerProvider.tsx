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
import { Link, useLocation } from 'react-router-dom'
import type { MusicLibraryState, MusicTrack, PlaybackMode } from '../../types/tooling'
import {
  deleteLocalFileHandle,
  getLocalFileHandle,
  loadMusicLibraryState,
  saveLocalFileHandle,
  saveMusicLibraryState,
  supportsPersistentLocalFiles,
  type LocalFileHandle,
} from '../../lib/music/storage'

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

type MusicPlayerContextValue = {
  tracks: MusicTrack[]
  queue: string[]
  currentTrack: MusicTrack | null
  currentTrackId?: string
  currentTime: number
  duration: number
  isPlaying: boolean
  volume: number
  mode: PlaybackMode
  statusMessage: string | null
  addRemoteTrack: (payload: RemoteTrackPayload) => string
  addLocalFileHandles: (handles: LocalFileHandle[]) => Promise<void>
  addLocalFiles: (files: File[]) => void
  updateTrack: (id: string, payload: TrackEditPayload) => void
  removeTrack: (id: string) => void
  playTrack: (id: string) => void
  enqueueTrack: (id: string) => void
  togglePlay: () => void
  playNext: () => void
  playPrevious: () => void
  seek: (time: number) => void
  setVolume: (volume: number) => void
  setMode: (mode: PlaybackMode) => void
  persistentLocalFilesSupported: boolean
}

const MusicPlayerContext = createContext<MusicPlayerContextValue | null>(null)

const FLOATING_PLAYER_POSITION_KEY = 'personal-tooling-music-floating-position'
const FLOATING_PLAYER_WIDTH = 220
const FLOATING_PLAYER_HEIGHT = 82
const FLOATING_PLAYER_MARGIN = 16

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

function createLocalTrackFromFile(file: File, id: string, localHandleId?: string): MusicTrack {
  return {
    id,
    source: 'local',
    title: file.name.replace(/\.[^.]+$/, '') || file.name,
    localHandleId,
    fileName: file.name,
    mimeType: file.type || undefined,
    addedAt: new Date().toISOString(),
  }
}

function formatPlayerTime(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return '0:00'
  }

  const minutes = Math.floor(value / 60)
  const seconds = Math.floor(value % 60)
  return `${minutes}:${seconds.toString().padStart(2, '0')}`
}

function PlayerIcon({ name }: { name: 'previous' | 'play' | 'pause' | 'next' | 'music-page' }) {
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

async function readLocalHandleFile(handle: LocalFileHandle) {
  if (handle.queryPermission && (await handle.queryPermission({ mode: 'read' })) !== 'granted') {
    if (!handle.requestPermission || (await handle.requestPermission({ mode: 'read' })) !== 'granted') {
      throw new Error('本地文件读取授权已失效，请重新选择文件。')
    }
  }

  return handle.getFile()
}

export function MusicPlayerProvider({ children }: PropsWithChildren) {
  const [library, setLibrary] = useState<MusicLibraryState>(() => loadMusicLibraryState())
  const [audioSrc, setAudioSrc] = useState<string | null>(null)
  const [isPlaying, setIsPlaying] = useState(false)
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [statusMessage, setStatusMessage] = useState<string | null>(null)
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const objectUrlRef = useRef<string | null>(null)
  const sessionFilesRef = useRef(new Map<string, File>())

  const currentTrack = useMemo(
    () => library.tracks.find((track) => track.id === library.currentTrackId) ?? null,
    [library.currentTrackId, library.tracks],
  )

  useEffect(() => {
    saveMusicLibraryState(library)
  }, [library])

  useEffect(() => {
    const audio = audioRef.current
    if (audio) {
      audio.volume = library.volume
    }
  }, [library.volume])

  useEffect(() => {
    let cancelled = false

    async function resolveSource(track: MusicTrack) {
      if (objectUrlRef.current) {
        URL.revokeObjectURL(objectUrlRef.current)
        objectUrlRef.current = null
      }

      setCurrentTime(0)
      setDuration(track.duration ?? 0)
      setAudioSrc(null)

      if (track.source === 'remote') {
        if (!track.remoteUrl) {
          setStatusMessage('这首云端音乐缺少可播放 URL。')
          setIsPlaying(false)
          return
        }

        setStatusMessage(null)
        setAudioSrc(track.remoteUrl)
        return
      }

      try {
        let file = sessionFilesRef.current.get(track.id)
        if (!file && track.localHandleId) {
          const handle = await getLocalFileHandle(track.localHandleId)
          if (handle) {
            file = await readLocalHandleFile(handle)
          }
        }

        if (!file) {
          setStatusMessage('本地音乐需要重新选择文件后才能播放。')
          setIsPlaying(false)
          return
        }

        const objectUrl = URL.createObjectURL(file)
        objectUrlRef.current = objectUrl
        if (!cancelled) {
          setStatusMessage(null)
          setAudioSrc(objectUrl)
        } else {
          URL.revokeObjectURL(objectUrl)
        }
      } catch (error) {
        setStatusMessage(error instanceof Error ? error.message : '无法读取本地音乐文件。')
        setIsPlaying(false)
      }
    }

    if (!currentTrack) {
      setAudioSrc(null)
      setCurrentTime(0)
      setDuration(0)
      setIsPlaying(false)
      return undefined
    }

    void resolveSource(currentTrack)

    return () => {
      cancelled = true
    }
  }, [currentTrack])

  useEffect(() => {
    const audio = audioRef.current
    if (!audio || !audioSrc) {
      return
    }

    if (isPlaying) {
      void audio.play().catch(() => {
        setIsPlaying(false)
        setStatusMessage('浏览器阻止了自动播放，请手动点击播放。')
      })
    } else {
      audio.pause()
    }
  }, [audioSrc, isPlaying])

  useEffect(
    () => () => {
      if (objectUrlRef.current) {
        URL.revokeObjectURL(objectUrlRef.current)
      }
    },
    [],
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
      addedAt: new Date().toISOString(),
    }

    setLibrary((value) => ({
      ...value,
      tracks: [track, ...value.tracks],
      queue: [track.id, ...value.queue],
      currentTrackId: value.currentTrackId ?? track.id,
    }))
    setStatusMessage('已添加云端音乐。')
    return id
  }, [])

  const addLocalFileHandles = useCallback(async (handles: LocalFileHandle[]) => {
    const tracks: MusicTrack[] = []
    for (const handle of handles) {
      const file = await readLocalHandleFile(handle)
      const trackId = createMusicId('local')
      const handleId = createMusicId('handle')
      await saveLocalFileHandle(handleId, handle)
      sessionFilesRef.current.set(trackId, file)
      tracks.push(createLocalTrackFromFile(file, trackId, handleId))
    }

    setLibrary((value) => ({
      ...value,
      tracks: [...tracks, ...value.tracks],
      queue: [...tracks.map((track) => track.id), ...value.queue],
      currentTrackId: value.currentTrackId ?? tracks[0]?.id,
    }))
    setStatusMessage(`已添加 ${tracks.length} 个本地音乐文件。`)
  }, [])

  const addLocalFiles = useCallback((files: File[]) => {
    const tracks = files.map((file) => {
      const trackId = createMusicId('local')
      sessionFilesRef.current.set(trackId, file)
      return createLocalTrackFromFile(file, trackId)
    })

    setLibrary((value) => ({
      ...value,
      tracks: [...tracks, ...value.tracks],
      queue: [...tracks.map((track) => track.id), ...value.queue],
      currentTrackId: value.currentTrackId ?? tracks[0]?.id,
    }))
    setStatusMessage(`已添加 ${tracks.length} 个本地音乐文件；当前浏览器不支持刷新后自动恢复。`)
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
  }, [])

  const removeTrack = useCallback((id: string) => {
    setLibrary((value) => {
      const removedTrack = value.tracks.find((track) => track.id === id)
      const tracks = value.tracks.filter((track) => track.id !== id)
      const queue = value.queue.filter((trackId) => trackId !== id)
      const currentTrackId = value.currentTrackId === id ? queue[0] : value.currentTrackId

      if (removedTrack?.localHandleId) {
        void deleteLocalFileHandle(removedTrack.localHandleId).catch(() => undefined)
      }
      sessionFilesRef.current.delete(id)

      return {
        ...value,
        tracks,
        queue,
        currentTrackId,
      }
    })
    setStatusMessage('已删除音乐。')
  }, [])

  const playTrack = useCallback((id: string) => {
    setLibrary((value) => ({
      ...value,
      queue: value.queue.includes(id) ? value.queue : [id, ...value.queue],
      currentTrackId: id,
    }))
    setIsPlaying(true)
  }, [])

  const enqueueTrack = useCallback((id: string) => {
    setLibrary((value) => ({
      ...value,
      queue: value.queue.includes(id) ? value.queue : [...value.queue, id],
    }))
    setStatusMessage('已加入播放队列。')
  }, [])

  const playNext = useCallback(() => {
    const nextTrackId = getNextTrackId(library)
    if (!nextTrackId) {
      setIsPlaying(false)
      return
    }

    setLibrary((value) => ({ ...value, currentTrackId: nextTrackId }))
    setIsPlaying(true)
  }, [library])

  const playPrevious = useCallback(() => {
    setLibrary((value) => {
      const previousTrackId = getPreviousTrackId(value)
      return previousTrackId ? { ...value, currentTrackId: previousTrackId } : value
    })
    setIsPlaying(true)
  }, [])

  const togglePlay = useCallback(() => {
    if (!library.currentTrackId && library.queue[0]) {
      setLibrary((value) => ({ ...value, currentTrackId: value.queue[0] }))
      setIsPlaying(true)
      return
    }

    if (!library.currentTrackId && library.tracks[0]) {
      setLibrary((value) => ({ ...value, currentTrackId: value.tracks[0].id, queue: value.queue.length ? value.queue : value.tracks.map((track) => track.id) }))
      setIsPlaying(true)
      return
    }

    setIsPlaying((value) => !value)
  }, [library.currentTrackId, library.queue, library.tracks])

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
      void audioRef.current.play()
      return
    }

    const nextTrackId = getNextTrackId(library)
    if (nextTrackId) {
      setLibrary((value) => ({ ...value, currentTrackId: nextTrackId }))
      setIsPlaying(true)
    } else {
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

    if (currentTrack && nextDuration > 0 && currentTrack.duration !== nextDuration) {
      setLibrary((value) => ({
        ...value,
        tracks: value.tracks.map((track) => (track.id === currentTrack.id ? { ...track, duration: nextDuration } : track)),
      }))
    }
  }

  const handleAudioError = () => {
    setIsPlaying(false)
    setStatusMessage('音频加载失败，请检查 URL、CORS、Range 请求或本地文件权限。')
  }

  const value = useMemo<MusicPlayerContextValue>(
    () => ({
      tracks: library.tracks,
      queue: library.queue,
      currentTrack,
      currentTrackId: library.currentTrackId,
      currentTime,
      duration,
      isPlaying,
      volume: library.volume,
      mode: library.mode,
      statusMessage,
      addRemoteTrack,
      addLocalFileHandles,
      addLocalFiles,
      updateTrack,
      removeTrack,
      playTrack,
      enqueueTrack,
      togglePlay,
      playNext,
      playPrevious,
      seek,
      setVolume,
      setMode,
      persistentLocalFilesSupported: supportsPersistentLocalFiles(),
    }),
    [
      addLocalFileHandles,
      addLocalFiles,
      addRemoteTrack,
      currentTime,
      currentTrack,
      duration,
      enqueueTrack,
      isPlaying,
      library.currentTrackId,
      library.mode,
      library.queue,
      library.tracks,
      library.volume,
      playNext,
      playPrevious,
      playTrack,
      removeTrack,
      seek,
      setMode,
      setVolume,
      statusMessage,
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

  if (!currentTrack && tracks.length === 0) {
    return null
  }

  const modeLabel = mode === 'order' ? '顺序' : mode === 'repeat-one' ? '单曲' : '循环'
  const nextMode: PlaybackMode = mode === 'order' ? 'repeat-all' : mode === 'repeat-all' ? 'repeat-one' : 'order'
  const isMusicPage = location.pathname === '/tools/music'

  if (!isMusicPage) {
    return <MusicFloatingPlayer />
  }

  return (
    <section className="music-mini-player" aria-label="音乐播放器">
      <div className="music-now-playing">
        <span className="music-equalizer" aria-hidden="true">
          <span />
          <span />
          <span />
        </span>
        <div className="music-now-playing-copy">
          <strong>{currentTrack?.title ?? '未选择音乐'}</strong>
          <span>{currentTrack?.artist || currentTrack?.fileName || currentTrack?.remoteUrl || '打开音乐播放器添加曲目'}</span>
        </div>
      </div>

      <div className="music-transport">
        <button className="music-icon-button" onClick={playPrevious} type="button" aria-label="上一首">
          <span aria-hidden="true">‹‹</span>
        </button>
        <button className="music-play-button" onClick={togglePlay} type="button">
          {isPlaying ? '暂停' : '播放'}
        </button>
        <button className="music-icon-button" onClick={playNext} type="button" aria-label="下一首">
          <span aria-hidden="true">››</span>
        </button>
      </div>

      <div className="music-progress">
        <span>{formatPlayerTime(currentTime)}</span>
        <input
          aria-label="播放进度"
          max={duration || 0}
          min={0}
          onChange={(event) => seek(Number(event.target.value))}
          step={1}
          type="range"
          value={duration ? Math.min(currentTime, duration) : 0}
        />
        <span>{formatPlayerTime(duration)}</span>
      </div>

      <div className="music-mini-actions">
        <button className="music-mode-button" onClick={() => setMode(nextMode)} type="button">
          {modeLabel}
        </button>
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
        {isMusicPage ? (
          <span className="music-current-page-chip">当前页</span>
        ) : (
          <Link className="button button-ghost button-sm" to="/tools/music">
            音乐页
          </Link>
        )}
      </div>

      {statusMessage ? <div className="music-player-message">{statusMessage}</div> : null}
    </section>
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
      <Link aria-label="打开音乐页" className="music-floating-page-link" title="打开音乐页" to="/tools/music">
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
          <span>{statusMessage || currentTrack?.artist || currentTrack?.fileName || '拖动浮标调整位置'}</span>
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
