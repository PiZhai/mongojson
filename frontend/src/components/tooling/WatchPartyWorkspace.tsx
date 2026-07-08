import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
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
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const suppressLocalEventsRef = useRef(false)
  const lastVersionRef = useRef(0)
  const localVideoRef = useRef<LocalVideo | null>(null)

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

  const togglePlayback = () => {
    const video = videoRef.current
    if (!video || !localVideo) {
      return
    }
    if (video.paused) {
      void video.play()
    } else {
      video.pause()
    }
  }

  const seekBy = (delta: number) => {
    const video = videoRef.current
    if (!video || !Number.isFinite(video.duration)) {
      return
    }
    video.currentTime = Math.min(video.duration, Math.max(0, video.currentTime + delta))
    sendControl()
  }

  const changePlaybackRate = (value: number) => {
    const video = videoRef.current
    const nextRate = clampPlaybackRate(value)
    setPlaybackRate(nextRate)
    if (video) {
      video.playbackRate = nextRate
      sendControl()
    }
  }

  const mediaMatchesRoom = !remoteState?.media_id || !localVideo || remoteState.media_id === localVideo.mediaId

  return (
    <div className="page-shell watch-party-shell">
      <div className="watch-party-grid">
        <section className="watch-stage">
          <div className="watch-video-frame">
            {localVideo ? (
              <video
                className="watch-video"
                controls
                onLoadedMetadata={(event) => {
                  const target = event.currentTarget
                  setDuration(target.duration)
                  target.playbackRate = playbackRate
                  sendControl()
                }}
                onPause={() => {
                  setIsPlaying(false)
                  sendControl()
                }}
                onPlay={() => {
                  setIsPlaying(true)
                  sendControl()
                }}
                onRateChange={(event) => {
                  setPlaybackRate(event.currentTarget.playbackRate)
                  sendControl()
                }}
                onSeeked={sendControl}
                onTimeUpdate={(event) => setPosition(event.currentTarget.currentTime)}
                ref={videoRef}
                src={localVideo.objectUrl}
              />
            ) : (
              <div className="watch-empty-video">
                <span>VIDEO</span>
              </div>
            )}
          </div>

          <div className="watch-control-bar">
            <button className="watch-icon-button watch-primary-action" disabled={!localVideo} onClick={togglePlayback} type="button">
              {isPlaying ? '暂停' : '播放'}
            </button>
            <button className="watch-icon-button" disabled={!localVideo} onClick={() => seekBy(-10)} type="button">
              -10s
            </button>
            <button className="watch-icon-button" disabled={!localVideo} onClick={() => seekBy(10)} type="button">
              +10s
            </button>
            <label className="watch-rate-control">
              <span>倍速</span>
              <select onChange={(event) => changePlaybackRate(Number(event.target.value))} value={playbackRate}>
                {[0.5, 0.75, 1, 1.25, 1.5, 2].map((rate) => (
                  <option key={rate} value={rate}>
                    {rate}x
                  </option>
                ))}
              </select>
            </label>
            <div className="watch-time-readout">
              {formatClock(position)} / {formatClock(duration)}
            </div>
          </div>
        </section>

        <aside className="watch-side-panel">
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
