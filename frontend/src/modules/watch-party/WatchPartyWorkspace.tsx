import { Dialog } from '@base-ui/react/dialog'
import { Drawer } from '@base-ui/react/drawer'
import { Menu } from '@base-ui/react/menu'
import { Tabs } from '@base-ui/react/tabs'
import { Tooltip } from '@base-ui/react/tooltip'
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactElement } from 'react'
import './styles.css'
import { getWatchRoomWebSocketUrl } from './api'
import { createManagementWebSocket } from '../../platform/auth/managementSession'
import type { ToolStatus } from '../../shared/ui/toolStatus'
import { StatusBanner } from '../../components/common/StatusBanner'
import './midnight.css'

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

function isWatchPlayerControlTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) {
    return false
  }

  return Boolean(target.closest('.watch-video-controls, .watch-rate-menu'))
}

export function WatchPartyWorkspace() {
  const clientId = useMemo(() => createClientId(), [])
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
  const [volumePanelOpen, setVolumePanelOpen] = useState(false)
  const [contextDrawerOpen, setContextDrawerOpen] = useState(false)
  const [roomDialogOpen, setRoomDialogOpen] = useState(false)
  const [roomSessionEnabled, setRoomSessionEnabled] = useState(true)
  const [connectionGeneration, setConnectionGeneration] = useState(0)
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const videoFrameRef = useRef<HTMLDivElement | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const suppressLocalEventsRef = useRef(false)
  const lastVersionRef = useRef(0)
  const localVideoRef = useRef<LocalVideo | null>(null)
  const isPlayingRef = useRef(false)
  const controlsHideTimerRef = useRef<number | null>(null)
  const controlsInteractionRef = useRef(false)
  const playerFocusFromKeyboardRef = useRef(false)
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
    if (!roomSessionEnabled) {
      socketRef.current?.close()
      socketRef.current = null
      setPeerCount(0)
      setSocketStatus({ kind: 'idle', message: '已离开同步房间。' })
      return
    }
    if (!roomPattern.test(activeRoom)) {
      setSocketStatus({ kind: 'error', message: '房间号只能包含字母、数字、下划线或短横线。' })
      return
    }

    const socket = createManagementWebSocket(getWatchRoomWebSocketUrl(activeRoom, clientId))
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
  }, [activeRoom, applyRemoteState, clientId, connectionGeneration, roomSessionEnabled, sendControl])

  const joinRoom = () => {
    const nextRoom = roomInput.trim()
    if (!roomPattern.test(nextRoom)) {
      setSocketStatus({ kind: 'error', message: '房间号只能包含字母、数字、下划线或短横线。' })
      return
    }
    setRoomSessionEnabled(true)
    setActiveRoom(nextRoom)
    setRoomDialogOpen(false)
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
      if (event.key === 'Tab') {
        playerFocusFromKeyboardRef.current = true
        videoFrameRef.current?.setAttribute('data-focus-origin', 'keyboard')
        return
      }

      const isSpaceKey = event.code === 'Space' || event.key === ' ' || event.key === 'Spacebar'
      const normalizedKey = event.key.toLowerCase()
      const isPlayerShortcut = event.key === 'ArrowLeft' || event.key === 'ArrowRight' || isSpaceKey || normalizedKey === 'f' || normalizedKey === 'm'
      const pointerFocusedPlayerControl =
        isWatchPlayerControlTarget(event.target) && !playerFocusFromKeyboardRef.current
      const preserveFocusedControlSpace =
        isSpaceKey &&
        isSpaceShortcutTarget(event.target) &&
        !pointerFocusedPlayerControl
      const preserveEditableShortcut =
        isEditableShortcutTarget(event.target) &&
        !(isSpaceKey && pointerFocusedPlayerControl)

      if (
        !isPlayerShortcut ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey ||
        preserveEditableShortcut ||
        preserveFocusedControlSpace
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

    const handlePointerDown = (event: PointerEvent) => {
      playerFocusFromKeyboardRef.current = false
      videoFrameRef.current?.setAttribute('data-focus-origin', 'pointer')
      const pointerTarget = event.target

      window.requestAnimationFrame(() => {
        const activeElement = document.activeElement
        if (
          activeElement instanceof HTMLElement &&
          isWatchPlayerControlTarget(activeElement) &&
          pointerTarget instanceof Node &&
          !activeElement.contains(pointerTarget)
        ) {
          activeElement.blur()
        }
      })
    }

    document.addEventListener('keydown', handleKeyDown, { capture: true })
    document.addEventListener('keyup', handleKeyUp, { capture: true })
    document.addEventListener('pointerdown', handlePointerDown, { capture: true })
    window.addEventListener('blur', handleWindowBlur)

    return () => {
      document.removeEventListener('keydown', handleKeyDown, { capture: true })
      document.removeEventListener('keyup', handleKeyUp, { capture: true })
      document.removeEventListener('pointerdown', handlePointerDown, { capture: true })
      window.removeEventListener('blur', handleWindowBlur)
      finishRightArrowPress(false)
      rightArrowPressedRef.current = false
      rightArrowHoldActiveRef.current = false
    }
  }, [changePlaybackRate, seekBy, showControls, toggleFullscreen, toggleMute, togglePlayback])

  const mediaMatchesRoom = !remoteState?.media_id || !localVideo || remoteState.media_id === localVideo.mediaId

  const contextPanel = (
    <aside className="watch-context-panel" aria-label="房间与同步信息">
      <Tabs.Root className="watch-context-tabs" defaultValue="room">
        <Tabs.List className="watch-context-tab-list" aria-label="一起看状态">
          <Tabs.Tab value="room">房间</Tabs.Tab>
          <Tabs.Tab value="sync">同步</Tabs.Tab>
          <Tabs.Indicator className="watch-context-tab-indicator" />
        </Tabs.List>
        <Tabs.Panel className="watch-context-tab-panel" value="room">
          <div className="watch-room-identity">
            <span>当前房间</span>
            <strong>{roomSessionEnabled ? activeRoom : '未加入'}</strong>
            <button disabled={!roomSessionEnabled} onClick={() => void navigator.clipboard?.writeText(activeRoom)} type="button">复制房间号</button>
          </div>
          <StatusBanner right={`${peerCount} 个连接`} status={socketStatus} />
          <dl className="watch-context-facts">
            <div><dt>连接状态</dt><dd>{socketStatus.kind === 'success' ? '已连接' : socketStatus.kind === 'error' ? '连接失败' : socketStatus.kind === 'warning' ? '已断开' : '等待连接'}</dd></div>
            <div><dt>房间媒体</dt><dd>{remoteState?.media_name || '暂无'}</dd></div>
            <div><dt>本地媒体</dt><dd>{localVideo?.name || '未选择'}</dd></div>
            <div><dt>文件匹配</dt><dd className={mediaMatchesRoom ? 'is-success' : 'is-warning'}>{mediaMatchesRoom ? '匹配' : '需要重新选择'}</dd></div>
          </dl>
          <div className="watch-context-actions">
            <button className="button button-primary" onClick={() => setRoomDialogOpen(true)} type="button">切换房间</button>
            {roomSessionEnabled ? <button className="button button-ghost" onClick={() => setRoomSessionEnabled(false)} type="button">离开房间</button> : <button className="button button-ghost" onClick={() => setRoomSessionEnabled(true)} type="button">重新加入</button>}
            {(socketStatus.kind === 'error' || socketStatus.kind === 'warning') && roomSessionEnabled ? <button className="button button-ghost" onClick={() => setConnectionGeneration((value) => value + 1)} type="button">重试连接</button> : null}
          </div>
        </Tabs.Panel>
        <Tabs.Panel className="watch-context-tab-panel" value="sync">
          <div className="watch-sync-orbit" aria-hidden="true"><span /><i /></div>
          <dl className="watch-context-facts watch-sync-facts">
            <div><dt>远端状态</dt><dd>{remoteState ? (remoteState.paused ? '暂停' : '播放') : '等待'}</dd></div>
            <div><dt>远端进度</dt><dd>{formatClock(remoteState?.position ?? 0)}</dd></div>
            <div><dt>播放倍速</dt><dd>{remoteState?.playback_rate ?? playbackRate}x</dd></div>
            <div><dt>状态版本</dt><dd>v{remoteState?.version ?? 0}</dd></div>
            <div><dt>最近同步</dt><dd>{remoteState?.server_time ? new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(remoteState.server_time)) : '尚未同步'}</dd></div>
          </dl>
          <p className="watch-sync-note">同步只校准播放状态。视频文件始终由每位参与者在本机选择，不会上传到服务器。</p>
        </Tabs.Panel>
      </Tabs.Root>
    </aside>
  )

  return (
    <div className="page-shell watch-party-shell" data-layout-region="watch-workspace">
      <section className="watch-page-intro">
        <div><p>Shared screening room</p><h1>把同一刻留在屏幕上</h1><span>视频留在本机，只同步播放、暂停、进度和倍速。</span></div>
        <div className="watch-page-actions"><button className="button" onClick={() => setContextDrawerOpen(true)} type="button">房间状态</button><button className="button button-primary" onClick={() => setRoomDialogOpen(true)} type="button">进入房间</button></div>
      </section>
      <div className="watch-party-grid" data-layout-region="watch-grid">
        <section className="watch-stage" data-layout-region="watch-stage">
          <div className={`watch-video-frame${controlsVisible ? '' : ' watch-video-frame-idle'}`} onPointerDown={showControls} onPointerMove={showControls} ref={videoFrameRef}>
            <div className="watch-stage-status"><span className={socketStatus.kind === 'success' ? 'is-online' : ''} /><strong>{roomSessionEnabled ? activeRoom : '未加入房间'}</strong><small>{peerCount} 个连接</small></div>
            {!mediaMatchesRoom ? <div className="watch-mismatch-banner" role="alert"><strong>本地文件与房间不同</strong><span>房间正在播放：{remoteState?.media_name || '未知视频'}</span><label>选择对应视频<input accept="video/*,.mp4,.webm,.mkv,.mov,.m4v" onChange={(event) => chooseVideo(event.target.files)} type="file" /></label></div> : null}
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
                      onPointerCancel={(event) => event.currentTarget.blur()}
                      onPointerUp={(event) => event.currentTarget.blur()}
                      step={0.1}
                      type="range"
                      value={duration ? Math.min(position, duration) : 0}
                    />
                  </div>
                  <div className="watch-video-control-row">
                    <div className="watch-video-control-group watch-video-transport">
                      <WatchTooltip label={isPlaying ? '暂停' : '播放'}><button aria-label={isPlaying ? '暂停' : '播放'} className="watch-video-icon-button watch-video-control-primary" onClick={togglePlayback} type="button"><WatchControlIcon name={isPlaying ? 'pause' : 'play'} /></button></WatchTooltip>
                      <WatchTooltip label="快退 10 秒"><button aria-label="快退 10 秒" className="watch-video-icon-button watch-video-skip-button" onClick={() => seekBy(-10)} type="button"><WatchControlIcon name="rewind" /><span>10</span></button></WatchTooltip>
                      <WatchTooltip label="快进 10 秒"><button aria-label="快进 10 秒" className="watch-video-icon-button watch-video-skip-button" onClick={() => seekBy(10)} type="button"><WatchControlIcon name="forward" /><span>10</span></button></WatchTooltip>
                    </div>
                    <span className="watch-video-time">
                      {formatClock(position)} / {formatClock(duration)}
                    </span>
                    <div className="watch-video-control-group watch-video-options">
                      <Menu.Root>
                        <Menu.Trigger aria-label={`播放倍速，当前 ${playbackRate}x`} className="watch-video-text-button"><span>倍速</span><strong>{playbackRate}x</strong></Menu.Trigger>
                        <Menu.Portal><Menu.Positioner className="watch-menu-positioner" sideOffset={8}><Menu.Popup className="watch-rate-menu">{[...playbackRates].reverse().map((rate) => <Menu.Item className={playbackRate === rate ? 'watch-rate-menu-item is-active' : 'watch-rate-menu-item'} key={rate} onClick={() => changePlaybackRate(rate)}>{rate}x</Menu.Item>)}</Menu.Popup></Menu.Positioner></Menu.Portal>
                      </Menu.Root>
                      <div
                        className={`watch-video-volume-control${volumePanelOpen ? ' is-open' : ''}`}
                        onBlurCapture={(event) => {
                          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                            setVolumePanelOpen(false)
                          }
                        }}
                      >
                        {volumePanelOpen ? (
                          <div aria-label="音量设置" className="watch-video-volume-panel" id="watch-video-volume-panel">
                            <div className="watch-video-volume-panel-header">
                              <button aria-label={muted ? '取消静音' : '静音'} onClick={toggleMute} type="button"><WatchControlIcon name={muted || volume === 0 ? 'muted' : 'volume'} /></button>
                              <strong>{Math.round((muted ? 0 : volume) * 100)}</strong>
                            </div>
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
                        ) : null}
                        <WatchTooltip label={volumePanelOpen ? '收起音量' : '调节音量'}><button aria-controls="watch-video-volume-panel" aria-expanded={volumePanelOpen} aria-label={volumePanelOpen ? '收起音量设置' : '打开音量设置'} className="watch-video-icon-button" onClick={() => setVolumePanelOpen((open) => !open)} type="button"><WatchControlIcon name={muted || volume === 0 ? 'muted' : 'volume'} /></button></WatchTooltip>
                      </div>
                      <WatchTooltip label={isFullscreen ? '退出全屏' : '全屏'}><button aria-label={isFullscreen ? '退出全屏' : '全屏'} className="watch-video-icon-button" onClick={toggleFullscreen} type="button"><WatchControlIcon name={isFullscreen ? 'fullscreenExit' : 'fullscreen'} /></button></WatchTooltip>
                    </div>
                  </div>
                </div>
              </>
            ) : (
              <div className="watch-empty-video">
                <span className="watch-empty-mark" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M4 6h12v12H4z" /><path d="m16 10 4-2v8l-4-2z" /><path d="m9 10 4 2-4 2z" /></svg></span>
                <strong>选择今晚要一起看的视频</strong>
                <p>文件只在本机读取；加入同一房间后，播放状态会自动同步。</p>
                <div><label className="button button-primary">选择视频<input accept="video/*,.mp4,.webm,.mkv,.mov,.m4v" onChange={(event) => chooseVideo(event.target.files)} type="file" /></label><button className="button button-ghost" onClick={() => setRoomDialogOpen(true)} type="button">切换房间</button></div>
              </div>
            )}
          </div>
          <div className="watch-stage-footer">
            <div>
              <strong>{localVideo?.name || '尚未选择视频'}</strong>
              <span>{videoStatus.message}</span>
            </div>
            <label className="watch-change-file">{localVideo ? '更换视频' : '选择视频'}<input accept="video/*,.mp4,.webm,.mkv,.mov,.m4v" onChange={(event) => chooseVideo(event.target.files)} type="file" /></label>
          </div>
        </section>
        <div className="watch-context-desktop" data-layout-region="watch-rail">{contextPanel}</div>
      </div>

      <Drawer.Root onOpenChange={setContextDrawerOpen} open={contextDrawerOpen} swipeDirection="right"><Drawer.Portal><Drawer.Backdrop className="watch-overlay-backdrop" /><Drawer.Viewport className="watch-drawer-viewport"><Drawer.Popup className="watch-drawer-popup"><Drawer.Content><div className="watch-drawer-header"><Drawer.Title>房间状态</Drawer.Title><Drawer.Close aria-label="关闭房间状态">关闭</Drawer.Close></div>{contextPanel}</Drawer.Content></Drawer.Popup></Drawer.Viewport></Drawer.Portal></Drawer.Root>

      <Dialog.Root onOpenChange={setRoomDialogOpen} open={roomDialogOpen}><Dialog.Portal><Dialog.Backdrop className="watch-overlay-backdrop" /><Dialog.Popup className="watch-room-dialog"><header><div><p>Screening room</p><Dialog.Title>进入同步房间</Dialog.Title></div><Dialog.Close aria-label="关闭房间设置">关闭</Dialog.Close></header><Dialog.Description>使用相同房间号的浏览器会共享播放、暂停、进度与倍速。</Dialog.Description><label>房间号<input autoFocus className="field-input" onChange={(event) => setRoomInput(event.target.value)} type="text" value={roomInput} /></label><p className="watch-room-helper">仅支持字母、数字、下划线和短横线，最长 64 个字符。</p><div className="watch-room-dialog-actions"><Dialog.Close className="button button-ghost">取消</Dialog.Close><button className="button button-primary" onClick={joinRoom} type="button">进入房间</button></div></Dialog.Popup></Dialog.Portal></Dialog.Root>
    </div>
  )
}

function WatchTooltip({ children, label }: { children: ReactElement; label: string }) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger render={children} />
      <Tooltip.Portal><Tooltip.Positioner sideOffset={8}><Tooltip.Popup className="watch-player-tooltip">{label}</Tooltip.Popup></Tooltip.Positioner></Tooltip.Portal>
    </Tooltip.Root>
  )
}
