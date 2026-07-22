import { Drawer } from '@base-ui/react/drawer'
import { Slider } from '@base-ui/react/slider'
import { Tooltip } from '@base-ui/react/tooltip'
import { type PointerEvent, type ReactElement, useCallback, useEffect, useRef, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { summarizeAudioQuality } from '../lib/audioQuality'
import { musicModule } from '../manifest'
import type { MusicTrack, PlaybackMode } from '../types'
import { useMusicPlayer } from '../MusicPlayerContext'
import { MusicArtwork } from './MusicArtwork'

const FLOATING_PLAYER_POSITION_KEY = 'personal-tooling-music-floating-position'
const FLOATING_PLAYER_WIDTH = 300
const FLOATING_PLAYER_HEIGHT = 82
const FLOATING_PLAYER_MARGIN = 16

type FloatingPlayerPosition = { x: number; y: number }
type PlayerWorkspace = 'tools' | 'documents' | 'steward' | 'entertainment'

function getPlayerWorkspace(pathname: string): PlayerWorkspace {
  const routeNamespace = pathname.split('/').filter(Boolean)[0]
  if (routeNamespace === 'documents') return 'documents'
  if (routeNamespace === 'entertainment') return 'entertainment'
  if (routeNamespace === 'tools') return 'tools'
  return 'steward'
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

function PlayerTooltip({ children, label }: { children: ReactElement; label: string }) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger render={children} />
      <Tooltip.Portal>
        <Tooltip.Positioner sideOffset={8}>
          <Tooltip.Popup className="music-player-tooltip">{label}</Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  )
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
    volume,
  } = useMusicPlayer()

  const isMusicPage = location.pathname === musicModule.route.path

  if (!isMusicPage && !isPlaying) {
    return null
  }

  const modeLabel = getModeLabel(mode)
  const nextMode = getNextMode(mode)
  const modeIcon = getModeIcon(mode)

  if (!isMusicPage) {
    return <MusicFloatingPlayer workspace={getPlayerWorkspace(location.pathname)} />
  }

  return (
    <section className="music-mini-player music-mini-player-entertainment" aria-label="音乐播放器">
      <div className="music-mini-surface">
        <div className="music-now-playing">
          <MusicArtwork className="music-player-artwork" track={currentTrack} />
          <div className="music-now-playing-copy">
            <strong>{currentTrack?.title ?? '未选择音乐'}</strong>
            <span>{currentTrack?.artist || currentTrack?.fileName || currentTrack?.remoteUrl || '打开音乐播放器添加曲目'}</span>
            {currentTrack?.audioQuality ? <span>{summarizeAudioQuality(currentTrack.audioQuality)}</span> : null}
            {statusMessage ? <span className="music-player-inline-message">{statusMessage}</span> : null}
          </div>
        </div>

        <div className="music-playback-cluster">
          <div className="music-transport">
            <PlayerTooltip label="上一首"><button className="music-icon-button" onClick={playPrevious} type="button" aria-label="上一首"><PlayerIcon name="previous" /></button></PlayerTooltip>
            <PlayerTooltip label={isPlaying ? '暂停' : '播放'}><button className="music-play-button" onClick={togglePlay} type="button" aria-label={isPlaying ? '暂停' : '播放'}><PlayerIcon name={isPlaying ? 'pause' : 'play'} /></button></PlayerTooltip>
            <PlayerTooltip label="下一首"><button className="music-icon-button" onClick={playNext} type="button" aria-label="下一首"><PlayerIcon name="next" /></button></PlayerTooltip>
          </div>

          <div className="music-progress">
            <span className="music-progress-time">{formatPlayerTime(currentTime)}</span>
            <Slider.Root
              className="music-base-slider music-progress-slider"
              disabled={!currentTrack}
              max={duration || 1}
              min={0}
              onValueChange={(value) => seek(value)}
              step={1}
              thumbAlignment="edge"
              value={duration ? Math.min(currentTime, duration) : 0}
            >
              <Slider.Control className="music-base-slider-control">
                <Slider.Track className="music-base-slider-track">
                  <Slider.Indicator className="music-base-slider-indicator" />
                  <Slider.Thumb aria-label="播放进度" aria-valuetext={`${formatPlayerTime(currentTime)} / ${formatPlayerTime(duration)}`} className="music-base-slider-thumb" />
                </Slider.Track>
              </Slider.Control>
            </Slider.Root>
            <span className="music-progress-time">{formatPlayerTime(duration)}</span>
          </div>
        </div>

        <div className="music-mini-actions">
          <PlayerTooltip label={`播放模式：${modeLabel}`}><button aria-label={`播放模式：${modeLabel}，点击切换`} className="music-mini-action-button" onClick={() => setMode(nextMode)} type="button"><PlayerIcon name={modeIcon} /></button></PlayerTooltip>
          <PlayerTooltip label="播放队列"><button aria-label="打开播放队列" className="music-mini-action-button" onClick={openQueue} type="button"><PlayerIcon name="queue" /></button></PlayerTooltip>
          <label className="music-volume-control" title="音量">
            <span className="sr-only">音量</span>
            <PlayerIcon name="volume" />
            <Slider.Root
              className="music-base-slider music-volume"
              max={1}
              min={0}
              onValueChange={(value) => setVolume(value)}
              step={0.01}
              thumbAlignment="edge"
              value={volume}
            >
              <Slider.Control className="music-base-slider-control">
                <Slider.Track className="music-base-slider-track">
                  <Slider.Indicator className="music-base-slider-indicator" />
                  <Slider.Thumb aria-label="音量" className="music-base-slider-thumb" />
                </Slider.Track>
              </Slider.Control>
            </Slider.Root>
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
  const location = useLocation()
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
  const [draggedTrackId, setDraggedTrackId] = useState<string | null>(null)
  const queueTracks = queue
    .map((id) => tracks.find((track) => track.id === id))
    .filter((track): track is MusicTrack => Boolean(track))
  const isEntertainmentPage = location.pathname.startsWith('/entertainment/')

  useEffect(() => {
    if (!isEntertainmentPage && isQueueOpen) closeQueue()
  }, [closeQueue, isEntertainmentPage, isQueueOpen])

  if (!isEntertainmentPage) {
    return null
  }

  return (
    <Drawer.Root onOpenChange={(open) => { if (!open) closeQueue() }} open={isQueueOpen} swipeDirection="right">
      <Drawer.Portal>
        <Drawer.Backdrop className="music-queue-backdrop" />
        <Drawer.Viewport className="music-queue-viewport">
          <Drawer.Popup className="music-queue-drawer music-queue-drawer-entertainment">
            <Drawer.Content className="music-queue-panel">
              <div className="music-queue-header">
                <div>
                  <p className="music-queue-eyebrow">Queue</p>
                  <Drawer.Title>播放队列</Drawer.Title>
                </div>
                <div className="music-queue-header-actions">
                  <button className="music-queue-text-button" onClick={clearQueue} type="button">清空</button>
                  <Drawer.Close className="music-queue-icon-button" aria-label="关闭队列"><PlayerIcon name="close" /></Drawer.Close>
                </div>
              </div>

              {queueTracks.length === 0 ? <div className="music-queue-empty">播放队列为空。</div> : (
                <div className="music-queue-list">
                  {queueTracks.map((track, index) => (
                    <article
                      className={`music-queue-item${track.id === currentTrackId ? ' music-queue-item-active' : ''}${track.id === draggedTrackId ? ' is-dragging' : ''}`}
                      draggable key={track.id} onDragEnd={() => setDraggedTrackId(null)} onDragOver={(event) => event.preventDefault()} onDragStart={() => setDraggedTrackId(track.id)}
                      onDrop={() => {
                        if (!draggedTrackId || draggedTrackId === track.id) return
                        const sourceIndex = queueTracks.findIndex((item) => item.id === draggedTrackId)
                        const targetIndex = queueTracks.findIndex((item) => item.id === track.id)
                        const direction = sourceIndex < targetIndex ? 'down' : 'up'
                        for (let moveIndex = 0; moveIndex < Math.abs(targetIndex - sourceIndex); moveIndex += 1) moveQueueItem(draggedTrackId, direction)
                        setDraggedTrackId(null)
                      }}
                    >
                      <button className="music-queue-play-target" onClick={() => playTrack(track.id)} type="button">
                        <span className="music-queue-index">{track.id === currentTrackId ? '播放中' : String(index + 1)}</span>
                        <span className="music-queue-copy"><strong>{track.title}</strong><span>{track.artist || track.fileName || track.remoteUrl || '未填写来源'}</span></span>
                      </button>
                      <div className="music-queue-item-actions">
                        <button aria-label="上移" className="music-queue-icon-button" disabled={index === 0} onClick={() => moveQueueItem(track.id, 'up')} type="button"><PlayerIcon name="up" /></button>
                        <button aria-label="下移" className="music-queue-icon-button" disabled={index === queueTracks.length - 1} onClick={() => moveQueueItem(track.id, 'down')} type="button"><PlayerIcon name="down" /></button>
                        <button aria-label="从队列移除" className="music-queue-icon-button music-queue-danger-button" onClick={() => removeFromQueue(track.id)} type="button"><PlayerIcon name="trash" /></button>
                      </div>
                    </article>
                  ))}
                </div>
              )}
            </Drawer.Content>
          </Drawer.Popup>
        </Drawer.Viewport>
      </Drawer.Portal>
    </Drawer.Root>
  )
}

function MusicFloatingPlayer({ workspace }: { workspace: PlayerWorkspace }) {
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
      data-workspace={workspace}
      style={{ left: position.x, top: position.y }}
    >
      <Link aria-label="打开音乐页" className="music-floating-page-link" title="打开音乐页" to={musicModule.route.path}>
        <MusicArtwork className="music-floating-artwork" track={currentTrack} />
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
