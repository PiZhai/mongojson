import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { getWatchRoomWebSocketUrl } from '../../lib/api/client'
import type { ToolStatus } from '../../types/tooling'
import { StatusBanner } from '../common/StatusBanner'

type LocalVideo = {
  file: File
  mediaId: string
  name: string
  objectUrl: string
}

type PlaybackState = {
  room_id: string
  media_id: string
  media_name: string
  paused: boolean
  position: number
  playback_rate: number
  server_time: number
  version: number
}

type ServerMessage = {
  type: 'state' | 'peer_count' | 'error'
  state?: PlaybackState
  peer_count?: number
  actor_client_id?: string
  message?: string
}

type ControlMessage = {
  type: 'control'
  client_id: string
  media_id: string
  media_name: string
  paused: boolean
  position: number
  playback_rate: number
  base_version: number
}

const roomPattern = /^[A-Za-z0-9_-]{1,64}$/
const driftToleranceSeconds = 0.35
const keyboardSeekSeconds = 10
const keyboardBoostRate = 2
const keyboardHoldDelayMs = 350
const controlsHideDelayMs = 1000
const playbackRates = [0.5, 0.75, 1, 1.25, 1.5, 2]

type WatchControlIconName = 'play' | 'pause' | 'rewind' | 'forward' | 'volume' | 'muted' | 'fullscreen' | 'fullscreenExit'

function WatchControlIcon({ name }: { name: WatchControlIconName }) {
  switch (name) {
    case 'play':
      return (
        <svg aria-hidden="true" fill="currentColor" viewBox="0 0 24 24">
          <path d="M8 5.6v12.8c0 .8.9 1.3 1.6.8l9.1-6.4a1 1 0 0 0 0-1.6L9.6 4.8C8.9 4.3 8 4.8 8 5.6Z" />
        </svg>
      )
    case 'pause':
      return (
        <svg aria-hidden="true" fill="currentColor" viewBox="0 0 24 24">
          <path d="M7.5 5h2.7c.5 0 .8.3.8.8v12.4c0 .5-.3.8-.8.8H7.5c-.5 0-.8-.3-.8-.8V5.8c0-.5.3-.8.8-.8Zm6.3 0h2.7c.5 0 .8.3.8.8v12.4c0 .5-.3.8-.8.8h-2.7c-.5 0-.8-.3-.8-.8V5.8c0-.5.3-.8.8-.8Z" />
        </svg>
      )
    case 'rewind':
      return (
        <svg aria-hidden="true" fill="none" viewBox="0 0 24 24">
          <path d="M11 8 6 12l5 4V8Zm7 0-5 4 5 4V8Z" fill="currentColor" />
        </svg>
      )
    case 'forward':
      return (
        <svg aria-hidden="true" fill="none" viewBox="0 0 24 24">
          <path d="m13 8 5 4-5 4V8ZM6 8l5 4-5 4V8Z" fill="currentColor" />
        </svg>
      )
    case 'muted':
      return (
        <svg aria-hidden="true" fill="none" viewBox="0 0 24 24">
          <path d="M4 9.5v5h3.2l4.3 3.6V5.9L7.2 9.5H4Z" fill="currentColor" />
          <path d="m16 9 4 4m0-4-4 4" stroke="currentColor" strokeLinecap="round" strokeWidth="2" />
        </svg>
      )
    case 'fullscreen':
      return (
        <svg aria-hidden="true" fill="none" viewBox="0 0 24 24">
          <path d="M8 4H4v4m12-4h4v4M8 20H4v-4m16 0v4h-4" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" />
        </svg>
      )
    case 'fullscreenExit':
      return (
        <svg aria-hidden="true" fill="none" viewBox="0 0 24 24">
          <path d="M9 4v5H4m11-5v5h5M9 20v-5H4m11 5v-5h5" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" />
        </svg>
      )
    case 'volume':
    default:
      return (
        <svg aria-hidden="true" fill="none" viewBox="0 0 24 24">
          <path d="M4 9.5v5h3.2l4.3 3.6V5.9L7.2 9.5H4Z" fill="currentColor" />
          <path d="M15.5 8.5a5 5 0 0 1 0 7M18 6a8 8 0 0 1 0 12" stroke="currentColor" strokeLinecap="round" strokeWidth="2" />
        </svg>
      )
  }
}

function createClientId() {
  const key = 'watch-party-client-id'
  const existing = window.localStorage.getItem(key)
  if (existing) {
    return existing
  }
  const value =
    typeof window.crypto?.randomUUID === 'function'
      ? window.crypto.randomUUID()
      : `client-${Date.now()}-${Math.random().toString(16).slice(2)}`
  window.localStorage.setItem(key, value)
  return value
}

function getMediaId(file: File) {
  return `${file.name}:${file.size}`
}

function formatClock(value: number) {
  if (!Number.isFinite(value) || value < 0) {
    return '0:00'
  }
  const minutes = Math.floor(value / 60)
  const seconds = Math.floor(value % 60)
  return `${minutes}:${seconds.toString().padStart(2, '0')}`
}

function clampPlaybackRate(value: number) {
  return Math.min(4, Math.max(0.25, value))
}

function isEditableShortcutTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) {
    return false
  }

  if (target.isContentEditable || target.closest("[contenteditable='true']")) {
    return true
  }

  return ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
}

function isSpaceShortcutTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) {
    return false
  }

  return Boolean(target.closest("button, a, input, textarea, select, [role='button'], [contenteditable='true']"))
}

export function WatchPartyWorkspace() {
  const clientId = useMemo(createClientId, [])
  const [roomInput, setRoomInput] = useState('main-room')
  const [activeRoom, setActiveRoom] = useState('main-room')
  const [socketStatus, setSocketStatus] = useState<ToolStatus>({
    kind: 'idle',
    message: '正在连接同步房间。',
  })
  const [videoStatus, setVideoStatus] = useState<ToolStatus>({
    kind: 'idle',
    message: '选择本地视频后即可参与同步。',
  })
  const [localVideo, setLocalVideo] = useState<LocalVideo | null>(null)
  const [peerCount, setPeerCount] = useState(0)
  const [remoteState, setRemoteState] = useState<PlaybackState | null>(null)
  const [isPlaying, setIsPlaying] = useState(false)
  const [playbackRate, setPlaybackRate] = useState(1)
  const [position, setPosition] = useState(0)
  const [duration, setDuration] = useState(0)
  const [volume, setVolume] = useState(1)
  const [muted, setMuted] = useState(false)
  const [isFullscreen, setIsFullscreen] = useState(false)
  const [controlsVisible, setControlsVisible] = useState(true)
  const [speedMenuOpen, setSpeedMenuOpen] = useState(false)
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const videoFrameRef = useRef<HTMLDivElement | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const suppressLocalEventsRef = useRef(false)
  const lastVersionRef = useRef(0)
  const localVideoRef = useRef<LocalVideo | null>(null)
  const isPlayingRef = useRef(false)
  const controlsHideTimerRef = useRef<number | null>(null)
  const controlsInteractionRef = useRef(false)
  const rightArrowHoldTimerRef = useRef<number | null>(null)
  const rightArrowHoldActiveRef = useRef(false)
  const rightArrowPressedRef = useRef(false)
  const rightArrowHoldBaseRateRef = useRef(1)

  useEffect(() => {
    localVideoRef.current = localVideo
  }, [localVideo])

  useEffect(() => {
    return () => {
      if (localVideoRef.current) {
        URL.revokeObjectURL(localVideoRef.current.objectUrl)
      }
    }
  }, [])

  const clearControlsHideTimer = useCallback(() => {
    if (controlsHideTimerRef.current === null) {
      return
    }
    window.clearTimeout(controlsHideTimerRef.current)
    controlsHideTimerRef.current = null
  }, [])

  const scheduleControlsHide = useCallback(() => {
    clearControlsHideTimer()
    if (!isPlayingRef.current || controlsInteractionRef.current) {
      return
    }

    controlsHideTimerRef.current = window.setTimeout(() => {
      controlsHideTimerRef.current = null
      if (isPlayingRef.current && !controlsInteractionRef.current) {
        setControlsVisible(false)
      }
    }, controlsHideDelayMs)
  }, [clearControlsHideTimer])

  const showControls = useCallback(() => {
    setControlsVisible(true)
    scheduleControlsHide()
  }, [scheduleControlsHide])

  useEffect(() => {
    isPlayingRef.current = isPlaying
    setControlsVisible(true)
    if (isPlaying) {
      scheduleControlsHide()
    } else {
      clearControlsHideTimer()
    }
  }, [clearControlsHideTimer, isPlaying, scheduleControlsHide])

  useEffect(() => {
    return () => {
      clearControlsHideTimer()
    }
  }, [clearControlsHideTimer])

  useEffect(() => {
    const handleFullscreenChange = () => {
      setIsFullscreen(document.fullscreenElement === videoFrameRef.current)
    }

    document.addEventListener('fullscreenchange', handleFullscreenChange)
    return () => {
      document.removeEventListener('fullscreenchange', handleFullscreenChange)
    }
  }, [])

  const sendControl = useCallback(() => {
    const socket = socketRef.current
    const video = videoRef.current
    const selected = localVideoRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN || !video || !selected || suppressLocalEventsRef.current) {
      return
    }

    const payload: ControlMessage = {
      type: 'control',
      client_id: clientId,
      media_id: selected.mediaId,
      media_name: selected.name,
      paused: video.paused,
      position: Number.isFinite(video.currentTime) ? video.currentTime : 0,
      playback_rate: clampPlaybackRate(video.playbackRate || 1),
      base_version: lastVersionRef.current,
    }
    socket.send(JSON.stringify(payload))
  }, [clientId])

  const applyRemoteState = useCallback(
    (state: PlaybackState, actorClientId = '') => {
      if (state.version < lastVersionRef.current) {
        return
      }
      lastVersionRef.current = state.version
      setRemoteState(state)

      if (actorClientId === clientId) {
        return
      }

      const selected = localVideoRef.current
      const video = videoRef.current
      if (!video) {
        return
      }

      if (!selected) {
        if (state.media_name) {
          setVideoStatus({ kind: 'warning', message: `房间正在播放：${state.media_name}` })
        }
        return
      }

      if (state.media_id && selected.mediaId !== state.media_id) {
        setVideoStatus({ kind: 'error', message: `本地视频与房间不同：${state.media_name || state.media_id}` })
        return
      }

      const elapsed = state.paused ? 0 : ((Date.now() - state.server_time) / 1000) * state.playback_rate
      const targetTime = Math.max(0, state.position + elapsed)
      suppressLocalEventsRef.current = true
      video.playbackRate = clampPlaybackRate(state.playback_rate || 1)
      setPlaybackRate(video.playbackRate)

      if (Math.abs(video.currentTime - targetTime) > driftToleranceSeconds) {
        video.currentTime = targetTime
      }

      if (state.paused) {
        video.pause()
        window.setTimeout(() => {
          suppressLocalEventsRef.current = false
        }, 250)
      } else {
        video
          .play()
          .then(() => {
            setVideoStatus({ kind: 'success', message: '已同步远端播放状态。' })
          })
          .catch(() => {
            setVideoStatus({ kind: 'warning', message: '浏览器阻止自动播放，请点击播放后继续同步。' })
          })
          .finally(() => {
            window.setTimeout(() => {
              suppressLocalEventsRef.current = false
            }, 250)
          })
      }
    },
    [clientId],
  )

  useEffect(() => {
    if (!roomPattern.test(activeRoom)) {
      setSocketStatus({ kind: 'error', message: '房间号只能包含字母、数字、下划线或短横线。' })
      return
    }

    const socket = new WebSocket(getWatchRoomWebSocketUrl(activeRoom, clientId))
    socketRef.current = socket
    setSocketStatus({ kind: 'idle', message: `正在连接房间：${activeRoom}` })
    setPeerCount(0)
    lastVersionRef.current = 0

    socket.addEventListener('open', () => {
      setSocketStatus({ kind: 'success', message: `已连接房间：${activeRoom}` })
      window.setTimeout(() => sendControl(), 0)
    })
    socket.addEventListener('message', (event) => {
      try {
        const message = JSON.parse(String(event.data)) as ServerMessage
        if (message.type === 'state' && message.state) {
          applyRemoteState(message.state, message.actor_client_id)
        } else if (message.type === 'peer_count') {
          setPeerCount(message.peer_count ?? 0)
        } else if (message.type === 'error') {
          setSocketStatus({ kind: 'error', message: message.message ?? '同步服务返回错误。' })
        }
      } catch {
        setSocketStatus({ kind: 'error', message: '同步消息格式无效。' })
      }
    })
    socket.addEventListener('close', () => {
      if (socketRef.current === socket) {
        setSocketStatus({ kind: 'warning', message: '同步连接已断开。' })
      }
    })
    socket.addEventListener('error', () => {
      setSocketStatus({ kind: 'error', message: '无法连接同步服务。' })
    })

    return () => {
      socket.close()
      if (socketRef.current === socket) {
        socketRef.current = null
      }
    }
  }, [activeRoom, applyRemoteState, clientId, sendControl])

  const joinRoom = () => {
    const nextRoom = roomInput.trim()
    if (!roomPattern.test(nextRoom)) {
      setSocketStatus({ kind: 'error', message: '房间号只能包含字母、数字、下划线或短横线。' })
      return
    }
    setActiveRoom(nextRoom)
  }

  const chooseVideo = (files: FileList | null) => {
    const file = files?.[0]
    if (!file) {
      return
    }
    if (localVideoRef.current) {
      URL.revokeObjectURL(localVideoRef.current.objectUrl)
    }
    const nextVideo: LocalVideo = {
      file,
      mediaId: getMediaId(file),
      name: file.name,
      objectUrl: URL.createObjectURL(file),
    }
    setLocalVideo(nextVideo)
    setPosition(0)
    setDuration(0)
    setVideoStatus({ kind: 'success', message: `已选择：${file.name}` })
  }

  const togglePlayback = useCallback(() => {
    const video = videoRef.current
    if (!video || !localVideoRef.current) {
      return
    }
    if (video.paused) {
      void video.play()
    } else {
      video.pause()
    }
  }, [])

  const seekTo = useCallback((value: number) => {
    const video = videoRef.current
    if (!video || !Number.isFinite(video.duration)) {
      return
    }
    video.currentTime = Math.min(video.duration, Math.max(0, value))
    setPosition(video.currentTime)
    sendControl()
  }, [sendControl])

  const seekBy = useCallback((delta: number) => {
    const video = videoRef.current
    if (!video || !Number.isFinite(video.duration)) {
      return
    }
    seekTo(video.currentTime + delta)
  }, [seekTo])

  const changePlaybackRate = useCallback((value: number) => {
    const video = videoRef.current
    const nextRate = clampPlaybackRate(value)
    setPlaybackRate(nextRate)
    if (video) {
      video.playbackRate = nextRate
      sendControl()
    }
  }, [sendControl])

  const changeVolume = (value: number) => {
    const nextVolume = Math.min(1, Math.max(0, value))
    const video = videoRef.current
    setVolume(nextVolume)
    if (video) {
      video.volume = nextVolume
      video.muted = nextVolume === 0
      setMuted(video.muted)
    }
  }

  const toggleMute = useCallback(() => {
    const video = videoRef.current
    if (!video) {
      return
    }

    const nextMuted = !video.muted
    video.muted = nextMuted
    if (!nextMuted && video.volume === 0) {
      video.volume = volume || 1
      setVolume(video.volume)
    }
    setMuted(video.muted)
  }, [volume])

  const toggleFullscreen = useCallback(() => {
    const frame = videoFrameRef.current
    if (!frame) {
      return
    }

    if (document.fullscreenElement) {
      void document.exitFullscreen()
      return
    }

    void frame.requestFullscreen()
  }, [])

  useEffect(() => {
    const clearRightArrowHoldTimer = () => {
      if (rightArrowHoldTimerRef.current === null) {
        return false
      }
      window.clearTimeout(rightArrowHoldTimerRef.current)
      rightArrowHoldTimerRef.current = null
      return true
    }

    const finishRightArrowPress = (seekOnTap: boolean) => {
      const wasPressed = rightArrowPressedRef.current
      const wasWaitingForHold = clearRightArrowHoldTimer()
      rightArrowPressedRef.current = false
      if (!rightArrowHoldActiveRef.current) {
        if (seekOnTap && wasPressed && wasWaitingForHold) {
          seekBy(keyboardSeekSeconds)
        }
        return
      }

      rightArrowHoldActiveRef.current = false
      changePlaybackRate(rightArrowHoldBaseRateRef.current)
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      const isSpaceKey = event.code === 'Space' || event.key === ' ' || event.key === 'Spacebar'
      const normalizedKey = event.key.toLowerCase()
      const isPlayerShortcut = event.key === 'ArrowLeft' || event.key === 'ArrowRight' || isSpaceKey || normalizedKey === 'f' || normalizedKey === 'm'

      if (
        !isPlayerShortcut ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey ||
        isEditableShortcutTarget(event.target) ||
        (isSpaceKey && isSpaceShortcutTarget(event.target))
      ) {
        return
      }

      const video = videoRef.current
      if (!video || !localVideoRef.current) {
        return
      }

      showControls()
      event.preventDefault()
      event.stopPropagation()

      if (isSpaceKey) {
        if (!event.repeat) {
          togglePlayback()
        }
        return
      }

      if (normalizedKey === 'f') {
        if (!event.repeat) {
          toggleFullscreen()
        }
        return
      }

      if (normalizedKey === 'm') {
        if (!event.repeat) {
          toggleMute()
        }
        return
      }

      if (event.key === 'ArrowLeft') {
        if (!event.repeat) {
          seekBy(-keyboardSeekSeconds)
        }
        return
      }

      if (rightArrowPressedRef.current) {
        return
      }

      rightArrowPressedRef.current = true
      rightArrowHoldBaseRateRef.current = clampPlaybackRate(video.playbackRate || 1)
      rightArrowHoldTimerRef.current = window.setTimeout(() => {
        rightArrowHoldTimerRef.current = null
        if (!rightArrowPressedRef.current || !videoRef.current || !localVideoRef.current) {
          return
        }

        rightArrowHoldActiveRef.current = true
        changePlaybackRate(keyboardBoostRate)
      }, keyboardHoldDelayMs)
    }

    const handleKeyUp = (event: KeyboardEvent) => {
      if (event.key === 'ArrowRight') {
        if (rightArrowPressedRef.current) {
          event.preventDefault()
          event.stopPropagation()
        }
        finishRightArrowPress(true)
      }
    }

    const handleWindowBlur = () => {
      finishRightArrowPress(false)
    }

    document.addEventListener('keydown', handleKeyDown, { capture: true })
    document.addEventListener('keyup', handleKeyUp, { capture: true })
    window.addEventListener('blur', handleWindowBlur)

    return () => {
      document.removeEventListener('keydown', handleKeyDown, { capture: true })
      document.removeEventListener('keyup', handleKeyUp, { capture: true })
      window.removeEventListener('blur', handleWindowBlur)
      finishRightArrowPress(false)
      rightArrowPressedRef.current = false
      rightArrowHoldActiveRef.current = false
    }
  }, [changePlaybackRate, seekBy, showControls, toggleFullscreen, toggleMute, togglePlayback])

  const mediaMatchesRoom = !remoteState?.media_id || !localVideo || remoteState.media_id === localVideo.mediaId

  return (
    <div className="page-shell watch-party-shell layout-frame" data-layout-region="watch-workspace">
      <div className="watch-party-grid layout-min-grid" data-layout-region="watch-grid">
        <section className="watch-stage layout-cell" data-layout-region="watch-stage">
          <div className={`watch-video-frame${controlsVisible ? '' : ' watch-video-frame-idle'}`} onPointerDown={showControls} onPointerMove={showControls} ref={videoFrameRef}>
            {localVideo ? (
              <>
                <video
                  className="watch-video"
                  onClick={togglePlayback}
                  onLoadedMetadata={(event) => {
                    const target = event.currentTarget
                    setDuration(target.duration)
                    target.playbackRate = playbackRate
                    target.volume = volume
                    target.muted = muted
                    sendControl()
                  }}
                  onPause={() => {
                    isPlayingRef.current = false
                    setIsPlaying(false)
                    sendControl()
                  }}
                  onPlay={() => {
                    isPlayingRef.current = true
                    setIsPlaying(true)
                    scheduleControlsHide()
                    sendControl()
                  }}
                  onRateChange={(event) => {
                    setPlaybackRate(event.currentTarget.playbackRate)
                    sendControl()
                  }}
                  onSeeked={sendControl}
                  onTimeUpdate={(event) => setPosition(event.currentTarget.currentTime)}
                  onVolumeChange={(event) => {
                    setVolume(event.currentTarget.volume)
                    setMuted(event.currentTarget.muted)
                  }}
                  playsInline
                  ref={videoRef}
                  src={localVideo.objectUrl}
                />
                <div
                  className={`watch-video-controls${controlsVisible ? '' : ' watch-video-controls-hidden'}`}
                  aria-label="视频播放控制"
                  onBlurCapture={(event) => {
                    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                      controlsInteractionRef.current = false
                      scheduleControlsHide()
                    }
                  }}
                  onFocusCapture={() => {
                    controlsInteractionRef.current = true
                    clearControlsHideTimer()
                    setControlsVisible(true)
                  }}
                  onPointerEnter={() => {
                    controlsInteractionRef.current = true
                    clearControlsHideTimer()
                    setControlsVisible(true)
                  }}
                  onPointerLeave={() => {
                    controlsInteractionRef.current = false
                    scheduleControlsHide()
                  }}
                  role="group"
                  style={{ '--watch-video-progress': duration ? `${Math.min(100, Math.max(0, (position / duration) * 100))}%` : '0%' } as CSSProperties}
                >
                  <div className="watch-video-progress-track">
                    <input
                      aria-label="播放进度"
                      className="watch-video-progress"
                      disabled={!duration}
                      max={duration || 0}
                      min={0}
                      onChange={(event) => seekTo(Number(event.target.value))}
                      step={0.1}
                      type="range"
                      value={duration ? Math.min(position, duration) : 0}
                    />
                  </div>
                  <div className="watch-video-control-row">
                    <div className="watch-video-control-group watch-video-transport">
                      <button aria-label={isPlaying ? '暂停' : '播放'} className="watch-video-icon-button watch-video-control-primary" onClick={togglePlayback} type="button">
                        <WatchControlIcon name={isPlaying ? 'pause' : 'play'} />
                      </button>
                      <button aria-label="快退 10 秒" className="watch-video-icon-button watch-video-skip-button" onClick={() => seekBy(-10)} type="button">
                        <WatchControlIcon name="rewind" />
                        <span>10</span>
                      </button>
                      <button aria-label="快进 10 秒" className="watch-video-icon-button watch-video-skip-button" onClick={() => seekBy(10)} type="button">
                        <WatchControlIcon name="forward" />
                        <span>10</span>
                      </button>
                    </div>
                    <span className="watch-video-time">
                      {formatClock(position)} / {formatClock(duration)}
                    </span>
                    <div className="watch-video-control-group watch-video-options">
                      <div
                        className="watch-video-rate-control"
                        onBlurCapture={(event) => {
                          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                            setSpeedMenuOpen(false)
                          }
                        }}
                        onPointerEnter={() => setSpeedMenuOpen(true)}
                        onPointerLeave={() => setSpeedMenuOpen(false)}
                      >
                        <button
                          aria-expanded={speedMenuOpen}
                          aria-haspopup="menu"
                          aria-label={`播放倍速，当前 ${playbackRate}x`}
                          className="watch-video-text-button"
                          onClick={() => setSpeedMenuOpen((open) => !open)}
                          type="button"
                        >
                          <span>倍速</span>
                          <strong>{playbackRate}x</strong>
                        </button>
                        <div className={`watch-video-rate-menu${speedMenuOpen ? ' watch-video-rate-menu-open' : ''}`} role="menu">
                          {[...playbackRates].reverse().map((rate) => (
                            <button
                              aria-checked={playbackRate === rate}
                              className={playbackRate === rate ? 'watch-video-rate-option watch-video-rate-option-active' : 'watch-video-rate-option'}
                              key={rate}
                              onClick={() => {
                                changePlaybackRate(rate)
                                setSpeedMenuOpen(false)
                              }}
                              role="menuitemradio"
                              type="button"
                            >
                              {rate}x
                            </button>
                          ))}
                        </div>
                      </div>
                      <div className="watch-video-volume-control">
                        <div className="watch-video-volume-panel">
                          <strong>{Math.round((muted ? 0 : volume) * 100)}</strong>
                          <div className="watch-video-volume-slider-slot">
                            <input
                              aria-label="音量"
                              className="watch-video-volume watch-video-volume-vertical"
                              max={1}
                              min={0}
                              onChange={(event) => changeVolume(Number(event.target.value))}
                              step={0.01}
                              type="range"
                              value={muted ? 0 : volume}
                            />
                          </div>
                        </div>
                        <button aria-label={muted ? '取消静音' : '静音'} className="watch-video-icon-button" onClick={toggleMute} type="button">
                          <WatchControlIcon name={muted || volume === 0 ? 'muted' : 'volume'} />
                        </button>
                      </div>
                      <button aria-label={isFullscreen ? '退出全屏' : '全屏'} className="watch-video-icon-button" onClick={toggleFullscreen} type="button">
                        <WatchControlIcon name={isFullscreen ? 'fullscreenExit' : 'fullscreen'} />
                      </button>
                    </div>
                  </div>
                </div>
              </>
            ) : (
              <div className="watch-empty-video">
                <span>VIDEO</span>
              </div>
            )}
          </div>
        </section>

        <aside className="watch-side-panel layout-cell" data-layout-region="watch-rail">
          <section className="watch-panel">
            <div className="watch-panel-heading">
              <span>Room</span>
              <strong>{peerCount}</strong>
            </div>
            <div className="watch-room-row">
              <input
                className="field-input"
                onChange={(event) => setRoomInput(event.target.value)}
                type="text"
                value={roomInput}
              />
              <button className="button button-primary" onClick={joinRoom} type="button">
                加入
              </button>
            </div>
            <StatusBanner right={activeRoom} status={socketStatus} />
          </section>

          <section className="watch-panel">
            <div className="watch-panel-heading">
              <span>Local</span>
              <strong>{localVideo ? '已选择' : '空'}</strong>
            </div>
            <label className="watch-file-picker">
              <input accept="video/*,.mp4,.webm,.mkv,.mov,.m4v" onChange={(event) => chooseVideo(event.target.files)} type="file" />
              <span>选择视频</span>
            </label>
            <StatusBanner right={mediaMatchesRoom ? '匹配' : '不匹配'} status={videoStatus} />
            {localVideo ? (
              <div className="watch-file-meta">
                <strong>{localVideo.name}</strong>
                <span>{(localVideo.file.size / 1024 / 1024).toFixed(1)} MB</span>
              </div>
            ) : null}
          </section>

          <section className="watch-panel watch-state-panel">
            <div className="watch-panel-heading">
              <span>Sync</span>
              <strong>v{remoteState?.version ?? 0}</strong>
            </div>
            <div className="watch-state-list">
              <span>状态</span>
              <strong>{remoteState ? (remoteState.paused ? '暂停' : '播放') : '等待'}</strong>
              <span>进度</span>
              <strong>{formatClock(remoteState?.position ?? 0)}</strong>
              <span>倍速</span>
              <strong>{remoteState?.playback_rate ?? playbackRate}x</strong>
              <span>视频</span>
              <strong>{remoteState?.media_name || localVideo?.name || '未选择'}</strong>
            </div>
          </section>
        </aside>
      </div>
    </div>
  )
}
