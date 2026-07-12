import { useEffect, useMemo, useRef, useState } from 'react'
import type { MusicTrack } from './types'
import type { ToolStatus } from '../../shared/ui/toolStatus'
import { type WindowWithFilePicker } from './lib/storage'
import { compactAudioQualityLabel } from './lib/audioQuality'
import { StatusBanner } from '../../components/common/StatusBanner'
import { useMusicPlayer } from './MusicPlayerContext'
import { countMusicTrackCategories, filterMusicTracks, type MusicLibraryFilter } from './lib/catalog'
import { MusicActionIcon } from './components/MusicActionIcon'
import { NowPlayingPanel } from './components/NowPlayingPanel'
import { TrackLibrary } from './components/TrackLibrary'

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
    if (track.relativePath) {
      return `文件夹扫描 · ${track.relativePath}`
    }

    return track.localHandleId ? '本地文件 · 刷新后可恢复' : '本地文件 · 当前会话'
  }

  if (!track.remoteId) {
    return '手工添加 URL'
  }

  try {
    return new URL(track.remoteUrl ?? '').hostname
  } catch {
    return '云端 URL'
  }
}

function describeTrackAudioQuality(track: MusicTrack) {
  return compactAudioQualityLabel(track.audioQuality) ?? '音质待识别'
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
    addTrackToPlaylist,
    addLocalFileHandles,
    addLocalFiles,
    addRemoteTrack,
    currentLyricIndex,
    currentTrack,
    currentTrackId,
    createPlaylist,
    deletePlaylist,
    enqueueTrack,
    folders,
    favoriteTrackIds,
    isPlaying,
    lyricStatusMessage,
    lyrics,
    openQueue,
    playlists,
    persistentLocalFilesSupported,
    persistentMusicFoldersSupported,
    playTrack,
    queue,
    recentTrackIds,
    removeLocalFolder,
    removeTrack,
    removeRemoteTrack,
    rescanLocalFolder,
    scanLocalDirectory,
    statusMessage,
    togglePlay,
    toggleFavorite,
    tracks,
    remoteTracksHasMore,
    remoteTracksLoading,
    loadMoreRemoteTracks,
    uploadLocalTrack,
    uploadingTrackIds,
    updateTrack,
  } = useMusicPlayer()
  const [form, setForm] = useState(emptyForm)
  const [editingTrackId, setEditingTrackId] = useState<string | null>(null)
  const [filter, setFilter] = useState<MusicLibraryFilter>('all')
  const [folderScanInProgress, setFolderScanInProgress] = useState(false)
  const [rescanningFolderId, setRescanningFolderId] = useState<string | null>(null)
  const [deletingTrackId, setDeletingTrackId] = useState<string | null>(null)
  const [sourceDrawerOpen, setSourceDrawerOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [sortBy, setSortBy] = useState<'recent' | 'title' | 'artist'>('recent')
  const [collectionId, setCollectionId] = useState('all')
  const [playlistDialogOpen, setPlaylistDialogOpen] = useState(false)
  const [playlistName, setPlaylistName] = useState('')
  const [status, setStatus] = useState<ToolStatus>({
    kind: 'idle',
    message: '添加云端音频 URL 或选择本地音乐文件。',
  })
  const fallbackFileInputRef = useRef<HTMLInputElement | null>(null)
  const remoteListSentinelRef = useRef<HTMLDivElement | null>(null)
  const addMusicButtonRef = useRef<HTMLButtonElement | null>(null)
  const sourceDrawerRef = useRef<HTMLElement | null>(null)
  const sourceTitleInputRef = useRef<HTMLInputElement | null>(null)
  const playlistButtonRef = useRef<HTMLButtonElement | null>(null)
  const playlistDialogRef = useRef<HTMLElement | null>(null)

  const closePlaylistDialog = () => {
    setPlaylistDialogOpen(false)
    window.requestAnimationFrame(() => playlistButtonRef.current?.focus())
  }

  const closeSourceDrawer = () => {
    setSourceDrawerOpen(false)
    window.requestAnimationFrame(() => addMusicButtonRef.current?.focus())
  }

  useEffect(() => {
    if (!sourceDrawerOpen) return
    sourceTitleInputRef.current?.focus()
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeSourceDrawer()
      if (event.key === 'Tab' && sourceDrawerRef.current) {
        const focusable = Array.from(sourceDrawerRef.current.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'))
        const first = focusable[0]
        const last = focusable.at(-1)
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault()
          last?.focus()
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault()
          first?.focus()
        }
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [sourceDrawerOpen])

  useEffect(() => {
    if (!playlistDialogOpen) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closePlaylistDialog()
        return
      }
      if (event.key === 'Tab' && playlistDialogRef.current) {
        const focusable = Array.from(playlistDialogRef.current.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'))
        const first = focusable[0]
        const last = focusable.at(-1)
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault()
          last?.focus()
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault()
          first?.focus()
        }
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [playlistDialogOpen])

  const editingTrack = useMemo(
    () => tracks.find((track) => track.id === editingTrackId) ?? null,
    [editingTrackId, tracks],
  )
  const favoriteTrackIdSet = useMemo(() => new Set(favoriteTrackIds), [favoriteTrackIds])

  const visibleTracks = useMemo(() => {
    const query = searchQuery.trim().toLocaleLowerCase()
    const sourceFiltered = filterMusicTracks(tracks, filter)
    const collectionFiltered = collectionId === 'favorites'
      ? sourceFiltered.filter((track) => favoriteTrackIds.includes(track.id))
      : collectionId === 'recent'
        ? sourceFiltered.filter((track) => recentTrackIds.includes(track.id))
        : collectionId.startsWith('playlist:')
          ? sourceFiltered.filter((track) => playlists.find((playlist) => `playlist:${playlist.id}` === collectionId)?.trackIds.includes(track.id))
          : sourceFiltered
    const filtered = collectionFiltered.filter((track) => {
      if (!query) return true
      return [track.title, track.artist, track.fileName, track.note]
        .filter(Boolean)
        .some((value) => value!.toLocaleLowerCase().includes(query))
    })

    return [...filtered].sort((left, right) => {
      if (collectionId === 'recent' && sortBy === 'recent') return recentTrackIds.indexOf(left.id) - recentTrackIds.indexOf(right.id)
      if (sortBy === 'title') return left.title.localeCompare(right.title, 'zh-CN')
      if (sortBy === 'artist') return (left.artist || '').localeCompare(right.artist || '', 'zh-CN')
      return new Date(right.addedAt).getTime() - new Date(left.addedAt).getTime()
    })
  }, [collectionId, favoriteTrackIds, filter, playlists, recentTrackIds, searchQuery, sortBy, tracks])

  const sourceCounts = useMemo(() => countMusicTrackCategories(tracks), [tracks])
  const playOrToggleTrack = (track: MusicTrack) => {
    if (currentTrackId === track.id) {
      togglePlay()
      return
    }
    playTrack(track.id)
  }

  useEffect(() => {
    const sentinel = remoteListSentinelRef.current
    if (!sentinel || !remoteTracksHasMore) {
      return
    }
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        void loadMoreRemoteTracks()
      }
    }, { rootMargin: '240px 0px' })
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [loadMoreRemoteTracks, remoteTracksHasMore])

  const uploadTrack = async (track: MusicTrack) => {
    try {
      const result = await uploadLocalTrack(track.id)
      setStatus({
        kind: result.duplicate ? 'warning' : 'success',
        message: result.duplicate ? `《${track.title}》已存在，未重复上传。` : `《${track.title}》已上传，远程版本已加入列表。`,
      })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? error.message : '歌曲上传失败。' })
    }
  }

  const deleteRemoteTrack = async (track: MusicTrack) => {
    if (!track.remoteId || !window.confirm(`确定从远程曲库删除《${track.title}》吗？`)) {
      return
    }
    setDeletingTrackId(track.id)
    try {
      await removeRemoteTrack(track.id)
      setStatus({ kind: 'success', message: `已从远程曲库删除《${track.title}》。` })
    } catch (error) {
      setStatus({ kind: 'error', message: error instanceof Error ? error.message : '远程歌曲删除失败。' })
    } finally {
      setDeletingTrackId(null)
    }
  }

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
    closeSourceDrawer()
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
    <div className="page-shell music-page-shell" data-layout-region="music-workspace">
      <section className="music-page-intro" aria-labelledby="music-library-title">
        <div>
          <p className="music-overline">Personal listening space</p>
          <h2 id="music-library-title">我的音乐</h2>
          <p>整理本地与云端音乐，在一个安静、持续的空间里播放。</p>
        </div>
        <div className="music-page-actions">
          <button className="button" onClick={chooseLocalFiles} type="button">
            <MusicActionIcon name="folder" />
            本地文件
          </button>
          <button className="button button-primary" onClick={() => setSourceDrawerOpen(true)} ref={addMusicButtonRef} type="button">
            <MusicActionIcon name="plus" />
            添加音乐
          </button>
        </div>
      </section>

      <div className="music-workbench">
        <section className="music-library-panel" aria-label="音乐曲库">
          <div className="music-library-toolbar">
            <label className="music-search-field">
              <span className="sr-only">搜索音乐</span>
              <MusicActionIcon name="search" />
              <input
                onChange={(event) => setSearchQuery(event.target.value)}
                placeholder="搜索歌曲、歌手或文件名"
                type="search"
                value={searchQuery}
              />
            </label>

            <div className="music-filter-tabs" aria-label="曲库来源筛选">
              {[
                { id: 'all', label: '全部', count: sourceCounts.all },
                { id: 'remote', label: '云端', count: sourceCounts.remote },
                { id: 'local', label: '本地', count: sourceCounts.local },
                { id: 'url', label: 'URL', count: sourceCounts.url },
              ].map((item) => (
                <button
                  aria-pressed={filter === item.id}
                  className={filter === item.id ? 'is-active' : ''}
                  key={item.id}
                  onClick={() => setFilter(item.id as MusicLibraryFilter)}
                  type="button"
                >
                  {item.label}<span>{item.count}</span>
                </button>
              ))}
            </div>

            <label className="music-sort-field">
              <span className="sr-only">排序方式</span>
              <select onChange={(event) => setSortBy(event.target.value as typeof sortBy)} value={sortBy}>
                <option value="recent">最近添加</option>
                <option value="title">按歌名</option>
                <option value="artist">按歌手</option>
              </select>
              <MusicActionIcon name="chevron-down" />
            </label>
          </div>

          <div className="music-collection-bar" aria-label="音乐集合">
            <button className={collectionId === 'all' ? 'is-active' : ''} onClick={() => setCollectionId('all')} type="button">全部歌曲</button>
            <button className={collectionId === 'favorites' ? 'is-active' : ''} onClick={() => setCollectionId('favorites')} type="button">我喜欢 <span>{favoriteTrackIds.length}</span></button>
            <button className={collectionId === 'recent' ? 'is-active' : ''} onClick={() => setCollectionId('recent')} type="button">最近播放</button>
            {playlists.map((playlist) => (
              <button className={collectionId === `playlist:${playlist.id}` ? 'is-active' : ''} key={playlist.id} onClick={() => setCollectionId(`playlist:${playlist.id}`)} type="button">{playlist.name} <span>{playlist.trackIds.length}</span></button>
            ))}
            <button className="music-new-playlist-button" onClick={() => setPlaylistDialogOpen(true)} ref={playlistButtonRef} type="button"><MusicActionIcon name="plus" /> 新建歌单</button>
          </div>

          <div className="music-library-heading">
            <div>
              <h3>歌曲</h3>
              <p>{visibleTracks.length === sourceCounts.all ? `共 ${sourceCounts.all} 首` : `显示 ${visibleTracks.length} / ${sourceCounts.all} 首`}</p>
            </div>
            <button className="music-text-action" onClick={openQueue} type="button">
              播放队列 · {queue.length}
            </button>
          </div>

          <div className="music-track-columns" aria-hidden="true">
            <span>歌曲</span><span>来源</span><span>音质</span><span>时长</span><span>操作</span>
          </div>

          <TrackLibrary
            currentTrackId={currentTrackId}
            deletingTrackId={deletingTrackId}
            describeAudioQuality={describeTrackAudioQuality}
            describeSource={describeTrackSource}
            formatDuration={formatTrackDuration}
            favoriteTrackIds={favoriteTrackIdSet}
            isPlaying={isPlaying}
            onDelete={(track) => {
              if (track.remoteId) void deleteRemoteTrack(track)
              else removeTrack(track.id)
            }}
            onAddToPlaylist={(track, playlistId) => addTrackToPlaylist(track.id, playlistId)}
            onEdit={startEditTrack}
            onEnqueue={(track) => enqueueTrack(track.id)}
            onPlay={playOrToggleTrack}
            onToggleFavorite={(track) => toggleFavorite(track.id)}
            onUpload={(track) => void uploadTrack(track)}
            tracks={visibleTracks}
            playlists={playlists}
            uploadingTrackIds={uploadingTrackIds}
          />

          <div className="music-remote-list-sentinel" ref={remoteListSentinelRef}>
            {remoteTracksLoading ? '正在加载云端歌曲…' : remoteTracksHasMore ? '继续下滑加载云端歌曲' : sourceCounts.remote > 0 ? '云端歌曲已全部加载' : ''}
          </div>
        </section>

        <NowPlayingPanel
          currentLyricIndex={currentLyricIndex}
          currentTrack={currentTrack}
          lyricStatusMessage={lyricStatusMessage}
          lyrics={lyrics}
          onOpenQueue={openQueue}
        />
      </div>

      <input
        accept="audio/*,.mp3,.flac,.wav,.ogg,.m4a,.aac,.opus,.webm,.lrc"
        hidden
        multiple
        onChange={(event) => void handleFallbackFiles(event.target.files)}
        ref={fallbackFileInputRef}
        type="file"
      />

      {sourceDrawerOpen ? (
        <div className="music-source-layer">
          <button className="music-source-scrim" onClick={closeSourceDrawer} type="button" aria-label="关闭添加音乐面板" />
          <aside aria-label="添加音乐" aria-modal="true" className="music-source-drawer" ref={sourceDrawerRef} role="dialog">
            <header className="music-source-drawer-header">
              <div>
                <p className="music-overline">Add to library</p>
                <h2>{editingTrack ? '编辑音乐信息' : '添加音乐'}</h2>
              </div>
              <button className="music-icon-action" onClick={closeSourceDrawer} type="button" aria-label="关闭">
                <MusicActionIcon name="close" />
              </button>
            </header>

            <div className="music-source-quick-actions">
              <button className="music-source-option" onClick={chooseLocalFiles} type="button">
                <span><MusicActionIcon name="music" /></span>
                <strong>选择本地文件</strong>
                <small>支持 MP3、FLAC、WAV、M4A、OGG 和 LRC</small>
              </button>
              <button className="music-source-option" disabled={folderScanInProgress} onClick={chooseMusicFolder} type="button">
                <span><MusicActionIcon name="folder" /></span>
                <strong>{folderScanInProgress ? '正在扫描…' : '扫描音乐文件夹'}</strong>
                <small>递归扫描音频，并自动匹配同名歌词</small>
              </button>
            </div>

            <div className="music-source-divider"><span>或者添加 URL</span></div>

            <div className="music-source-form">
              <label className="field-label">
                标题
                <input className="field-input" onChange={(event) => setForm((value) => ({ ...value, title: event.target.value }))} placeholder="歌曲标题" ref={sourceTitleInputRef} type="text" value={form.title} />
              </label>
              <label className="field-label">
                歌手
                <input className="field-input" onChange={(event) => setForm((value) => ({ ...value, artist: event.target.value }))} placeholder="可选" type="text" value={form.artist} />
              </label>
              {editingTrack?.source === 'local' ? null : (
                <label className="field-label">
                  音频 URL
                  <input className="field-input" onChange={(event) => setForm((value) => ({ ...value, remoteUrl: event.target.value }))} placeholder="https://example.com/song.mp3" type="url" value={form.remoteUrl} />
                </label>
              )}
              <label className="field-label">
                备注
                <input className="field-input" onChange={(event) => setForm((value) => ({ ...value, note: event.target.value }))} placeholder="可选" type="text" value={form.note} />
              </label>
              <div className="music-source-submit-row">
                <button className="button button-primary" onClick={submitRemoteTrack} type="button">{editingTrack ? '保存信息' : '添加到曲库'}</button>
                {editingTrack ? <button className="button button-ghost" onClick={resetForm} type="button">取消编辑</button> : null}
              </div>
            </div>

            <StatusBanner
              right={persistentLocalFilesSupported ? persistentMusicFoldersSupported ? '文件与文件夹可恢复' : '文件可恢复' : '仅当前会话'}
              status={statusMessage ? { kind: 'warning', message: statusMessage } : status}
            />

            <section className="music-folder-panel">
              <div className="music-folder-panel-header"><span>已跟踪文件夹</span><strong>{folders.length}</strong></div>
              {folders.length === 0 ? <p className="music-folder-empty">还没有跟踪文件夹。</p> : (
                <div className="music-folder-list">
                  {folders.map((folder) => (
                    <article className="music-folder-item" key={folder.id}>
                      <div className="music-folder-copy"><strong>{folder.name}</strong><span>{folder.trackCount ?? 0} 首 · {formatScanTime(folder.lastScannedAt)}</span></div>
                      <div className="music-folder-actions">
                        <button aria-label={`重新扫描 ${folder.name}`} className="music-icon-action" disabled={rescanningFolderId === folder.id} onClick={() => void rescanFolder(folder.id)} type="button"><MusicActionIcon name="refresh" /></button>
                        <button aria-label={`停止跟踪 ${folder.name}`} className="music-icon-action music-icon-action-danger" onClick={() => removeLocalFolder(folder.id)} type="button"><MusicActionIcon name="trash" /></button>
                      </div>
                    </article>
                  ))}
                </div>
              )}
            </section>
          </aside>
        </div>
      ) : null}

      {playlistDialogOpen ? (
        <div className="music-source-layer music-playlist-dialog-layer">
          <button className="music-source-scrim" onClick={closePlaylistDialog} type="button" aria-label="关闭歌单管理" />
          <section aria-label="管理歌单" aria-modal="true" className="music-playlist-dialog" ref={playlistDialogRef} role="dialog">
            <header className="music-source-drawer-header">
              <div><p className="music-overline">Playlists</p><h2>管理歌单</h2></div>
              <button className="music-icon-action" onClick={closePlaylistDialog} type="button" aria-label="关闭"><MusicActionIcon name="close" /></button>
            </header>
            <form className="music-playlist-create" onSubmit={(event) => {
              event.preventDefault()
              if (!playlistName.trim()) return
              const id = createPlaylist(playlistName)
              setCollectionId(`playlist:${id}`)
              setPlaylistName('')
              closePlaylistDialog()
            }}>
              <label className="field-label">歌单名称<input autoFocus className="field-input" onChange={(event) => setPlaylistName(event.target.value)} placeholder="例如：深夜循环" value={playlistName} /></label>
              <button className="button button-primary" disabled={!playlistName.trim()} type="submit">创建歌单</button>
            </form>
            {playlists.length > 0 ? (
              <div className="music-playlist-list">
                {playlists.map((playlist) => (
                  <div className="music-playlist-item" key={playlist.id}>
                    <button onClick={() => { setCollectionId(`playlist:${playlist.id}`); closePlaylistDialog() }} type="button"><strong>{playlist.name}</strong><span>{playlist.trackIds.length} 首</span></button>
                    <button className="music-icon-action music-icon-action-danger" onClick={() => { deletePlaylist(playlist.id); if (collectionId === `playlist:${playlist.id}`) setCollectionId('all') }} type="button" aria-label={`删除歌单 ${playlist.name}`}><MusicActionIcon name="trash" /></button>
                  </div>
                ))}
              </div>
            ) : null}
          </section>
        </div>
      ) : null}
    </div>
  )
}
