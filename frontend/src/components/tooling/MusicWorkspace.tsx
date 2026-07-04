import { useMemo, useRef, useState } from 'react'
import type { MusicTrack, PlaybackMode, ToolStatus } from '../../types/tooling'
import { type WindowWithFilePicker } from '../../lib/music/storage'
import { StatusBanner } from '../common/StatusBanner'
import { useMusicPlayer } from '../music/MusicPlayerProvider'

type MusicFilter = 'all' | 'remote' | 'local'

const emptyForm = {
  title: '',
  artist: '',
  remoteUrl: '',
  note: '',
}

function ActionIcon({
  name,
}: {
  name: 'play' | 'queue' | 'edit' | 'trash' | 'link' | 'folder' | 'close' | 'music' | 'clock' | 'plus' | 'refresh'
}) {
  if (name === 'play') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M8 5l11 7-11 7z" />
      </svg>
    )
  }

  if (name === 'queue') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M5 7h10" />
        <path d="M5 12h8" />
        <path d="M5 17h6" />
        <path d="M17 15v-4l3 2z" />
      </svg>
    )
  }

  if (name === 'edit') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M5 19h4l10-10-4-4L5 15z" />
        <path d="M13 7l4 4" />
      </svg>
    )
  }

  if (name === 'trash') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M5 7h14" />
        <path d="M9 7V5h6v2" />
        <path d="M8 10v8" />
        <path d="M12 10v8" />
        <path d="M16 10v8" />
      </svg>
    )
  }

  if (name === 'folder') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M4 7h6l2 2h8v9H4z" />
      </svg>
    )
  }

  if (name === 'close') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M7 7l10 10" />
        <path d="M17 7L7 17" />
      </svg>
    )
  }

  if (name === 'music') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M9 18V6l9-2v12" />
        <circle cx="6.5" cy="18" r="2.5" />
        <circle cx="15.5" cy="16" r="2.5" />
      </svg>
    )
  }

  if (name === 'clock') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <circle cx="12" cy="12" r="8" />
        <path d="M12 8v5l3 2" />
      </svg>
    )
  }

  if (name === 'plus') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M12 5v14" />
        <path d="M5 12h14" />
      </svg>
    )
  }

  if (name === 'refresh') {
    return (
      <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
        <path d="M20 8a7 7 0 0 0-12-3l-2 2" />
        <path d="M6 4v3h3" />
        <path d="M4 16a7 7 0 0 0 12 3l2-2" />
        <path d="M18 20v-3h-3" />
      </svg>
    )
  }

  return (
    <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
      <path d="M10 13a5 5 0 0 0 7 0l1-1a5 5 0 0 0-7-7l-1 1" />
      <path d="M14 11a5 5 0 0 0-7 0l-1 1a5 5 0 0 0 7 7l1-1" />
    </svg>
  )
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
    if (track.relativePath) {
      return `文件夹扫描 · ${track.relativePath}`
    }

    return track.localHandleId ? '本地文件 · 刷新后可恢复' : '本地文件 · 当前会话'
  }

  try {
    return new URL(track.remoteUrl ?? '').hostname
  } catch {
    return '云端 URL'
  }
}

function describeTrackLyric(track: MusicTrack) {
  if (!track.lyricFileName) {
    return '未匹配歌词'
  }

  return track.lyricRelativePath ? `歌词 · ${track.lyricRelativePath}` : `歌词 · ${track.lyricFileName}`
}

function describeMode(mode: PlaybackMode) {
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

function formatScanTime(value?: string) {
  if (!value) {
    return '未扫描'
  }

  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
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
    currentLyricIndex,
    currentLyricLine,
    currentTrack,
    currentTrackId,
    enqueueTrack,
    folders,
    isPlaying,
    lyricStatusMessage,
    lyrics,
    mode,
    openQueue,
    persistentLocalFilesSupported,
    persistentMusicFoldersSupported,
    playTrack,
    queue,
    removeLocalFolder,
    removeTrack,
    rescanLocalFolder,
    scanLocalDirectory,
    statusMessage,
    tracks,
    updateTrack,
  } = useMusicPlayer()
  const [form, setForm] = useState(emptyForm)
  const [editingTrackId, setEditingTrackId] = useState<string | null>(null)
  const [filter, setFilter] = useState<MusicFilter>('all')
  const [folderScanInProgress, setFolderScanInProgress] = useState(false)
  const [rescanningFolderId, setRescanningFolderId] = useState<string | null>(null)
  const [sourceDrawerOpen, setSourceDrawerOpen] = useState(false)
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

  const lyricPreview = useMemo(() => {
    if (lyrics.length === 0) {
      return []
    }

    const activeIndex = currentLyricIndex >= 0 ? currentLyricIndex : 0
    const start = Math.max(0, activeIndex - 3)
    const end = Math.min(lyrics.length, activeIndex + 4)
    return lyrics.slice(start, end).map((line, offset) => ({
      line,
      index: start + offset,
    }))
  }, [currentLyricIndex, lyrics])

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
    setSourceDrawerOpen(false)
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
    setSourceDrawerOpen(true)
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
                'audio/*': ['.mp3', '.flac', '.wav', '.ogg', '.m4a', '.aac', '.opus', '.webm'],
                'text/plain': ['.lrc'],
              },
            },
          ],
        })
        if (handles.length === 0) {
          return
        }

        await addLocalFileHandles(handles)
        setStatus({ kind: 'success', message: '已处理选择的本地音乐和歌词文件。' })
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

  const chooseMusicFolder = async () => {
    const picker = (window as WindowWithFilePicker).showDirectoryPicker
    if (!picker || !persistentMusicFoldersSupported) {
      setStatus({ kind: 'warning', message: '当前浏览器不支持选择文件夹；请使用 Chromium / Edge / Chrome。' })
      return
    }

    setFolderScanInProgress(true)
    try {
      const handle = await picker({
        id: 'personal-tooling-music-folders',
        mode: 'read',
      })
      const result = await scanLocalDirectory(handle)
      setStatus({
        kind: result.added > 0 ? 'success' : 'warning',
        message:
          result.added > 0
            ? `已扫描 ${result.folderName}，新增 ${result.added} 首音乐${result.lyricsMatched > 0 ? `，匹配 ${result.lyricsMatched} 个歌词` : ''}。`
            : `已扫描 ${result.folderName}，没有发现新的音乐文件${result.lyricsMatched > 0 ? `，补全 ${result.lyricsMatched} 个歌词` : ''}。`,
      })
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') {
        return
      }
      setStatus({ kind: 'error', message: error instanceof Error ? error.message : '无法扫描本地音乐文件夹。' })
    } finally {
      setFolderScanInProgress(false)
    }
  }

  const rescanFolder = async (folderId: string) => {
    setRescanningFolderId(folderId)
    try {
      const result = await rescanLocalFolder(folderId)
      setStatus({
        kind: result.added > 0 ? 'success' : 'idle',
        message:
          result.added > 0
            ? `已重新扫描 ${result.folderName}，新增 ${result.added} 首音乐${result.lyricsMatched > 0 ? `，匹配 ${result.lyricsMatched} 个歌词` : ''}。`
            : `已重新扫描 ${result.folderName}，没有新的音乐文件${result.lyricsMatched > 0 ? `，补全 ${result.lyricsMatched} 个歌词` : ''}。`,
      })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? error.message : '无法重新扫描这个文件夹。' })
    } finally {
      setRescanningFolderId(null)
    }
  }

  const handleFallbackFiles = async (files: FileList | null) => {
    const selectedFiles = Array.from(files ?? []).filter(
      (file) => file.type.startsWith('audio/') || /\.(mp3|flac|wav|ogg|m4a|aac|opus|webm|lrc)$/i.test(file.name),
    )
    if (selectedFiles.length === 0) {
      setStatus({ kind: 'warning', message: '没有选择可识别的音频或歌词文件。' })
      return
    }

    try {
      await addLocalFiles(selectedFiles)
      setStatus({ kind: 'warning', message: '已处理本地音乐和歌词；当前浏览器刷新后需要重新选择文件。' })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? error.message : '无法读取本地文件。' })
    } finally {
      if (fallbackFileInputRef.current) {
        fallbackFileInputRef.current.value = ''
      }
    }
  }

  return (
    <div className="page-shell music-page-shell">
      <div className="music-workbench">
        <aside className="music-sidebar" aria-label="音乐工作台侧栏">
          <section className="music-sidebar-card music-source-card">
            <div className="music-section-heading">
              <div>
                <p className="music-section-eyebrow">Source</p>
                <h2>音乐来源</h2>
              </div>
              <button
                aria-expanded={sourceDrawerOpen}
                className="music-icon-action"
                onClick={() => setSourceDrawerOpen((value) => !value)}
                type="button"
                aria-label={sourceDrawerOpen ? '收起来源抽屉' : '展开来源抽屉'}
              >
                <ActionIcon name={sourceDrawerOpen ? 'close' : 'plus'} />
              </button>
            </div>

            <div className="music-source-actions">
              <button className="button button-primary music-wide-action" onClick={() => setSourceDrawerOpen(true)} type="button">
                <ActionIcon name="link" />
                添加 URL
              </button>
              <button className="button music-wide-action" onClick={chooseLocalFiles} type="button">
                <ActionIcon name="folder" />
                本地文件
              </button>
              <button className="button music-wide-action" disabled={folderScanInProgress} onClick={chooseMusicFolder} type="button">
                <ActionIcon name="folder" />
                {folderScanInProgress ? '扫描中' : '扫描文件夹'}
              </button>
            </div>

            {sourceDrawerOpen ? (
              <div className="music-source-drawer">
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
                  <label className="field-label">
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
                <div className="music-source-submit-row">
                  <button className="button button-primary" onClick={submitRemoteTrack} type="button">
                    {editingTrack ? '保存信息' : '添加 URL'}
                  </button>
                  {editingTrack ? (
                    <button className="button button-ghost" onClick={resetForm} type="button">
                      取消编辑
                    </button>
                  ) : null}
                </div>
              </div>
            ) : null}

            <input
              accept="audio/*,.mp3,.flac,.wav,.ogg,.m4a,.aac,.opus,.webm,.lrc"
              hidden
              multiple
              onChange={(event) => void handleFallbackFiles(event.target.files)}
              ref={fallbackFileInputRef}
              type="file"
            />
            <StatusBanner
              right={
                persistentLocalFilesSupported
                  ? persistentMusicFoldersSupported
                    ? '文件/文件夹可恢复'
                    : '文件可恢复'
                  : '仅当前会话'
              }
              status={statusMessage ? { kind: 'warning', message: statusMessage } : status}
            />
            <div className="music-folder-panel">
              <div className="music-folder-panel-header">
                <span>已指定文件夹</span>
                <strong>{folders.length}</strong>
              </div>
              {folders.length === 0 ? (
                <p className="music-folder-empty">选择文件夹后会自动递归扫描音频文件，并匹配同名 .lrc 歌词。</p>
              ) : (
                <div className="music-folder-list">
                  {folders.map((folder) => (
                    <article className="music-folder-item" key={folder.id}>
                      <div className="music-folder-copy">
                        <strong>{folder.name}</strong>
                        <span>
                          {folder.trackCount ?? 0} 首 · {formatScanTime(folder.lastScannedAt)}
                        </span>
                      </div>
                      <div className="music-folder-actions">
                        <button
                          aria-label={`重新扫描 ${folder.name}`}
                          className="music-icon-action"
                          disabled={rescanningFolderId === folder.id}
                          onClick={() => void rescanFolder(folder.id)}
                          title="重新扫描"
                          type="button"
                        >
                          <ActionIcon name="refresh" />
                        </button>
                        <button
                          aria-label={`停止跟踪 ${folder.name}`}
                          className="music-icon-action music-icon-action-danger"
                          onClick={() => removeLocalFolder(folder.id)}
                          title="停止跟踪"
                          type="button"
                        >
                          <ActionIcon name="trash" />
                        </button>
                      </div>
                    </article>
                  ))}
                </div>
              )}
            </div>
          </section>

          <section className="music-sidebar-card">
            <div className="music-section-heading">
              <div>
                <p className="music-section-eyebrow">Filter</p>
                <h2>曲库筛选</h2>
              </div>
            </div>
            <div className="music-filter-grid">
              {[
                { id: 'all', label: '全部', count: sourceCounts.all },
                { id: 'remote', label: '云端', count: sourceCounts.remote },
                { id: 'local', label: '本地', count: sourceCounts.local },
              ].map((item) => (
                <button
                  className={`music-filter-card${filter === item.id ? ' music-filter-card-active' : ''}`}
                  key={item.id}
                  onClick={() => setFilter(item.id as MusicFilter)}
                  type="button"
                >
                  <span>{item.label}</span>
                  <strong>{item.count}</strong>
                </button>
              ))}
            </div>
          </section>

          <section className="music-sidebar-card music-current-summary">
            <div className="music-section-heading">
              <div>
                <p className="music-section-eyebrow">Now</p>
                <h2>当前播放</h2>
              </div>
            </div>
            <div className="music-current-plate">
              <span className="music-current-art" aria-hidden="true">
                <ActionIcon name="music" />
              </span>
              <div className="music-current-copy">
                <strong>{currentTrack?.title ?? '未选择音乐'}</strong>
                <span>{currentTrack?.artist || currentTrack?.fileName || currentTrack?.remoteUrl || '从曲库选择一首歌开始播放'}</span>
              </div>
            </div>
            <div className="music-sidebar-stats">
              <span>{isPlaying ? '播放中' : '已暂停'}</span>
              <span>{describeMode(mode)}</span>
              <button className="music-inline-link" onClick={openQueue} type="button">
                队列 {queue.length}
              </button>
            </div>
          </section>

        </aside>

        <div className="music-main-column">
          <section className="music-lyrics-stage">
            <div className="music-lyrics-stage-header">
              <div>
                <p className="music-section-eyebrow">Lyrics</p>
                <h2>同步歌词</h2>
              </div>
              <span className="music-lyrics-stage-meta">
                {currentTrack?.lyricFileName ?? '未匹配 .lrc'}
              </span>
            </div>
            {!currentTrack ? (
              <div className="music-lyrics-empty music-lyrics-empty-large">选择一首音乐后显示歌词。</div>
            ) : lyricStatusMessage ? (
              <div className="music-lyrics-empty music-lyrics-empty-large">{lyricStatusMessage}</div>
            ) : lyrics.length === 0 ? (
              <div className="music-lyrics-empty music-lyrics-empty-large">
                {currentTrack.lyricFileName ? '歌词文件暂无可识别时间轴。' : '未匹配同名 .lrc 歌词。'}
              </div>
            ) : (
              <div className="music-lyrics-panel" aria-live="polite">
                <div className="music-lyrics-lines">
                  {lyricPreview.map(({ line, index }) => (
                    <p
                      className={`music-lyric-line${index === currentLyricIndex ? ' music-lyric-line-active' : ''}`}
                      key={`${line.time}-${index}`}
                    >
                      {line.text}
                    </p>
                  ))}
                </div>
                <span className="music-lyrics-meta">
                  {currentLyricLine ? '当前歌词' : '等待歌词开始'} · {currentTrack.title}
                </span>
              </div>
            )}
          </section>

          <section className="music-library-panel">
          <div className="music-library-header">
            <div>
              <p className="music-section-eyebrow">Library</p>
              <h2>音乐列表</h2>
            </div>
            <div className="music-library-summary">
              <span>共 {sourceCounts.all} 首</span>
              <span>{currentTrack ? `当前：${currentTrack.title}` : '未播放'}</span>
            </div>
          </div>

          {visibleTracks.length === 0 ? (
            <div className="inline-empty-state">
              <p className="inline-empty-state-title">暂无音乐</p>
              <p className="inline-empty-state-text">添加云端 URL 或选择本地文件后会显示在这里。</p>
            </div>
          ) : (
            <div className="music-media-grid">
              {visibleTracks.map((track) => {
                const isActive = currentTrackId === track.id
                return (
                  <article className={`music-track-card${isActive ? ' music-track-card-active' : ''}`} key={track.id}>
                    <div className="music-track-card-main">
                      <span className={`music-track-art music-source-${track.source}`} aria-hidden="true">
                        <ActionIcon name={isActive ? 'play' : track.source === 'remote' ? 'link' : 'folder'} />
                      </span>
                      <div className="music-track-copy">
                        <div className="music-track-title-row">
                          <h3>{track.title}</h3>
                          {isActive ? <span className="music-playing-pill">播放中</span> : null}
                        </div>
                        <p>{track.artist || track.note || describeTrackSource(track)}</p>
                      </div>
                    </div>

                    <div className="music-track-card-meta">
                      <span className={`music-source-pill music-source-${track.source}`}>
                        {track.source === 'remote' ? '云端' : '本地'}
                      </span>
                      <span className="music-meta-chip">
                        <ActionIcon name="clock" />
                        {formatTrackDuration(track.duration)}
                      </span>
                      <span className="music-track-source-text">{describeTrackSource(track)}</span>
                      {track.lyricFileName ? <span className="music-track-source-text">{describeTrackLyric(track)}</span> : null}
                    </div>

                    <div className="music-track-actions">
                      <button
                        aria-label={`播放 ${track.title}`}
                        className="music-icon-action music-icon-action-primary"
                        onClick={() => playTrack(track.id)}
                        type="button"
                      >
                        <ActionIcon name="play" />
                      </button>
                      <button
                        aria-label={`加入队列 ${track.title}`}
                        className="music-icon-action"
                        onClick={() => enqueueTrack(track.id)}
                        type="button"
                      >
                        <ActionIcon name="queue" />
                      </button>
                      <button
                        aria-label={`编辑 ${track.title}`}
                        className="music-icon-action"
                        onClick={() => startEditTrack(track)}
                        type="button"
                      >
                        <ActionIcon name="edit" />
                      </button>
                      <button
                        aria-label={`删除 ${track.title}`}
                        className="music-icon-action music-icon-action-danger"
                        onClick={() => removeTrack(track.id)}
                        type="button"
                      >
                        <ActionIcon name="trash" />
                      </button>
                    </div>
                  </article>
                )
              })}
            </div>
          )}
          </section>
        </div>
      </div>
    </div>
  )
}
