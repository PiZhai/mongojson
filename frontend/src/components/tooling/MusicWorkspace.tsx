import { useMemo, useRef, useState } from 'react'
import type { MusicTrack, ToolStatus } from '../../types/tooling'
import { type WindowWithFilePicker } from '../../lib/music/storage'
import { Panel } from '../common/Panel'
import { StatusBanner } from '../common/StatusBanner'
import { useMusicPlayer } from '../music/MusicPlayerProvider'

type MusicFilter = 'all' | 'remote' | 'local'

const emptyForm = {
  title: '',
  artist: '',
  remoteUrl: '',
  note: '',
}

function formatTrackDuration(value?: number) {
  if (!value || !Number.isFinite(value)) {
    return '未知'
  }

  const minutes = Math.floor(value / 60)
  const seconds = Math.floor(value % 60)
  return `${minutes}:${seconds.toString().padStart(2, '0')}`
}

function describeTrackSource(track: MusicTrack) {
  if (track.source === 'local') {
    return track.localHandleId ? '本地文件 · 可尝试恢复' : '本地文件 · 当前会话'
  }

  try {
    return new URL(track.remoteUrl ?? '').hostname
  } catch {
    return '云端 URL'
  }
}

function validateRemoteUrl(value: string) {
  try {
    const url = new URL(value)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

export function MusicWorkspace() {
  const {
    addLocalFileHandles,
    addLocalFiles,
    addRemoteTrack,
    currentTrackId,
    enqueueTrack,
    persistentLocalFilesSupported,
    playTrack,
    removeTrack,
    statusMessage,
    tracks,
    updateTrack,
  } = useMusicPlayer()
  const [form, setForm] = useState(emptyForm)
  const [editingTrackId, setEditingTrackId] = useState<string | null>(null)
  const [filter, setFilter] = useState<MusicFilter>('all')
  const [status, setStatus] = useState<ToolStatus>({
    kind: 'idle',
    message: '添加云端音频 URL 或选择本地音乐文件。',
  })
  const fallbackFileInputRef = useRef<HTMLInputElement | null>(null)

  const editingTrack = useMemo(
    () => tracks.find((track) => track.id === editingTrackId) ?? null,
    [editingTrackId, tracks],
  )

  const visibleTracks = useMemo(() => {
    if (filter === 'all') {
      return tracks
    }

    return tracks.filter((track) => track.source === filter)
  }, [filter, tracks])

  const sourceCounts = useMemo(
    () => ({
      all: tracks.length,
      remote: tracks.filter((track) => track.source === 'remote').length,
      local: tracks.filter((track) => track.source === 'local').length,
    }),
    [tracks],
  )

  const resetForm = () => {
    setForm(emptyForm)
    setEditingTrackId(null)
  }

  const submitRemoteTrack = () => {
    const title = form.title.trim()
    const artist = form.artist.trim()
    const note = form.note.trim()
    const remoteUrl = form.remoteUrl.trim()

    if (!title) {
      setStatus({ kind: 'error', message: '请输入音乐标题。' })
      return
    }

    if (!editingTrack || editingTrack.source === 'remote') {
      if (!validateRemoteUrl(remoteUrl)) {
        setStatus({ kind: 'error', message: '请输入 http 或 https 开头的可播放音频 URL。' })
        return
      }
    }

    if (editingTrack) {
      updateTrack(editingTrack.id, {
        title,
        artist,
        note,
        remoteUrl: editingTrack.source === 'remote' ? remoteUrl : undefined,
      })
      setStatus({ kind: 'success', message: '已更新音乐信息。' })
    } else {
      addRemoteTrack({ title, artist, note, remoteUrl })
      setStatus({ kind: 'success', message: '已添加云端音乐。' })
    }

    resetForm()
  }

  const startEditTrack = (track: MusicTrack) => {
    setEditingTrackId(track.id)
    setForm({
      title: track.title,
      artist: track.artist ?? '',
      remoteUrl: track.remoteUrl ?? '',
      note: track.note ?? '',
    })
    setStatus({ kind: 'idle', message: '正在编辑音乐信息。' })
  }

  const chooseLocalFiles = async () => {
    const picker = (window as WindowWithFilePicker).showOpenFilePicker
    if (picker) {
      try {
        const handles = await picker({
          multiple: true,
          types: [
            {
              description: '音频文件',
              accept: {
                'audio/*': ['.mp3', '.flac', '.wav', '.ogg', '.m4a', '.aac'],
              },
            },
          ],
        })
        if (handles.length === 0) {
          return
        }

        await addLocalFileHandles(handles)
        setStatus({ kind: 'success', message: `已添加 ${handles.length} 个本地音乐文件。` })
      } catch (error) {
        if (error instanceof DOMException && error.name === 'AbortError') {
          return
        }
        setStatus({ kind: 'error', message: error instanceof Error ? error.message : '无法读取本地音乐文件。' })
      }
      return
    }

    fallbackFileInputRef.current?.click()
  }

  const handleFallbackFiles = (files: FileList | null) => {
    const audioFiles = Array.from(files ?? []).filter((file) => file.type.startsWith('audio/') || /\.(mp3|flac|wav|ogg|m4a|aac)$/i.test(file.name))
    if (audioFiles.length === 0) {
      setStatus({ kind: 'warning', message: '没有选择可识别的音频文件。' })
      return
    }

    addLocalFiles(audioFiles)
    setStatus({ kind: 'warning', message: '已添加本地音乐；当前浏览器刷新后需要重新选择文件。' })
    if (fallbackFileInputRef.current) {
      fallbackFileInputRef.current.value = ''
    }
  }

  return (
    <div className="page-shell music-page-shell">
      <Panel
        actions={
          <>
            <button className="button button-primary" onClick={submitRemoteTrack} type="button">
              {editingTrack ? '保存信息' : '添加 URL'}
            </button>
            {editingTrack ? (
              <button className="button button-ghost" onClick={resetForm} type="button">
                取消编辑
              </button>
            ) : null}
            <button className="button" onClick={chooseLocalFiles} type="button">
              选择本地音乐
            </button>
          </>
        }
        eyebrow="Music"
        title={editingTrack ? '编辑音乐信息' : '音乐来源'}
      >
        <div className="music-form-grid">
          <label className="field-label">
            标题
            <input
              className="field-input"
              onChange={(event) => setForm((value) => ({ ...value, title: event.target.value }))}
              placeholder="例如：Demo Track"
              type="text"
              value={form.title}
            />
          </label>
          <label className="field-label">
            歌手
            <input
              className="field-input"
              onChange={(event) => setForm((value) => ({ ...value, artist: event.target.value }))}
              placeholder="可选"
              type="text"
              value={form.artist}
            />
          </label>
          {editingTrack?.source === 'local' ? null : (
            <label className="field-label music-url-field">
              音频 URL
              <input
                className="field-input"
                onChange={(event) => setForm((value) => ({ ...value, remoteUrl: event.target.value }))}
                placeholder="https://example.com/music/song.mp3"
                type="url"
                value={form.remoteUrl}
              />
            </label>
          )}
          <label className="field-label">
            备注
            <input
              className="field-input"
              onChange={(event) => setForm((value) => ({ ...value, note: event.target.value }))}
              placeholder="可选"
              type="text"
              value={form.note}
            />
          </label>
          <input
            accept="audio/*,.mp3,.flac,.wav,.ogg,.m4a,.aac"
            multiple
            onChange={(event) => handleFallbackFiles(event.target.files)}
            ref={fallbackFileInputRef}
            type="file"
            hidden
          />
        </div>
        <StatusBanner
          right={persistentLocalFilesSupported ? '本地文件支持刷新后恢复授权' : '本地文件仅当前会话可用'}
          status={statusMessage ? { kind: 'warning', message: statusMessage } : status}
        />
      </Panel>

      <Panel
        actions={
          <div className="mode-switch">
            {[
              { id: 'all', label: `全部 ${sourceCounts.all}` },
              { id: 'remote', label: `云端 ${sourceCounts.remote}` },
              { id: 'local', label: `本地 ${sourceCounts.local}` },
            ].map((item) => (
              <button
                className={`mode-switch-button${filter === item.id ? ' mode-switch-button-active' : ''}`}
                key={item.id}
                onClick={() => setFilter(item.id as MusicFilter)}
                type="button"
              >
                {item.label}
              </button>
            ))}
          </div>
        }
        eyebrow="Library"
        title="音乐列表"
      >
        {visibleTracks.length === 0 ? (
          <div className="inline-empty-state">
            <p className="inline-empty-state-title">暂无音乐</p>
            <p className="inline-empty-state-text">添加云端 URL 或选择本地文件后会显示在这里。</p>
          </div>
        ) : (
          <div className="music-track-list">
            {visibleTracks.map((track) => (
              <article className={`music-track-row${currentTrackId === track.id ? ' music-track-row-active' : ''}`} key={track.id}>
                <div className="music-track-main">
                  <span className={`music-source-pill music-source-${track.source}`}>
                    {track.source === 'remote' ? '云端' : '本地'}
                  </span>
                  <div className="music-track-copy">
                    <h3>{track.title}</h3>
                    <p>{track.artist || track.note || describeTrackSource(track)}</p>
                  </div>
                </div>
                <div className="music-track-meta">
                  <span>{describeTrackSource(track)}</span>
                  <span>{formatTrackDuration(track.duration)}</span>
                </div>
                <div className="music-track-actions">
                  <button className="button button-primary button-sm" onClick={() => playTrack(track.id)} type="button">
                    播放
                  </button>
                  <button className="button button-ghost button-sm" onClick={() => enqueueTrack(track.id)} type="button">
                    入队
                  </button>
                  <button className="button button-ghost button-sm" onClick={() => startEditTrack(track)} type="button">
                    编辑
                  </button>
                  <button className="button button-danger button-sm" onClick={() => removeTrack(track.id)} type="button">
                    删除
                  </button>
                </div>
              </article>
            ))}
          </div>
        )}
      </Panel>
    </div>
  )
}
