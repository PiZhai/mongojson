import type { MusicPlaylist, MusicTrack } from '../types'
import { MusicActionIcon } from './MusicActionIcon'

type TrackLibraryProps = {
  currentTrackId?: string
  deletingTrackId: string | null
  isPlaying: boolean
  onDelete: (track: MusicTrack) => void
  onEdit: (track: MusicTrack) => void
  onEnqueue: (track: MusicTrack) => void
  onPlay: (track: MusicTrack) => void
  onUpload: (track: MusicTrack) => void
  tracks: MusicTrack[]
  uploadingTrackIds: ReadonlySet<string>
  favoriteTrackIds: ReadonlySet<string>
  playlists: MusicPlaylist[]
  describeAudioQuality: (track: MusicTrack) => string
  describeSource: (track: MusicTrack) => string
  formatDuration: (value?: number) => string
  onAddToPlaylist: (track: MusicTrack, playlistId: string) => void
  onToggleFavorite: (track: MusicTrack) => void
}

function TrackArtwork({ track, playing }: { track: MusicTrack; playing: boolean }) {
  return (
    <span className={`music-track-art music-source-${track.source}`} aria-hidden="true">
      <MusicActionIcon name={playing ? 'pause' : track.remoteId || !track.remoteId && track.source === 'remote' ? 'link' : 'folder'} />
    </span>
  )
}

export function TrackLibrary({
  currentTrackId,
  deletingTrackId,
  describeAudioQuality,
  describeSource,
  formatDuration,
  favoriteTrackIds,
  isPlaying,
  onDelete,
  onAddToPlaylist,
  onEdit,
  onEnqueue,
  onPlay,
  onToggleFavorite,
  onUpload,
  tracks,
  playlists,
  uploadingTrackIds,
}: TrackLibraryProps) {
  if (tracks.length === 0) {
    return (
      <div className="music-library-empty">
        <span aria-hidden="true"><MusicActionIcon name="music" /></span>
        <h3>曲库还是空的</h3>
        <p>添加本地音乐、扫描文件夹，或粘贴一个音频 URL 开始播放。</p>
      </div>
    )
  }

  return (
    <ul className="music-track-list" aria-label="音乐曲库">
      {tracks.map((track, index) => {
        const isCurrent = currentTrackId === track.id
        const isPlayingTrack = isPlaying && isCurrent
        const isPlayable = track.fileAvailable !== false
        return (
          <li className={`music-track-row${isCurrent ? ' is-current' : ''}`} key={track.id}>
            <button
              className="music-track-play-target"
              disabled={!isPlayable}
              onClick={() => onPlay(track)}
              type="button"
              aria-label={`${isPlayingTrack ? '暂停' : '播放'} ${track.title}`}
            >
              <span className="music-track-number">{isPlayingTrack ? <span className="music-row-equalizer"><i /><i /><i /></span> : index + 1}</span>
              <TrackArtwork playing={isPlayingTrack} track={track} />
              <span className="music-track-primary">
                <strong>{track.title}</strong>
                <span>{track.artist || track.note || '未知歌手'}</span>
              </span>
            </button>

            <span className="music-track-source">{track.remoteId ? '云端' : track.source === 'local' ? '本地' : 'URL'}</span>
            <span className="music-track-quality">{describeAudioQuality(track)}</span>
            <span className="music-track-duration">{formatDuration(track.duration)}</span>
            <span className="music-track-menu">
              <button className={`music-icon-action${favoriteTrackIds.has(track.id) ? ' is-favorite' : ''}`} onClick={() => onToggleFavorite(track)} type="button" aria-label={`${favoriteTrackIds.has(track.id) ? '取消收藏' : '收藏'} ${track.title}`} aria-pressed={favoriteTrackIds.has(track.id)} title={favoriteTrackIds.has(track.id) ? '取消收藏' : '收藏'}>
                <MusicActionIcon name="heart" />
              </button>
              <button className="music-icon-action" onClick={() => onEnqueue(track)} type="button" aria-label={`加入队列 ${track.title}`} title="加入队列">
                <MusicActionIcon name="queue" />
              </button>
              {playlists.length > 0 ? (
                <label className="music-playlist-picker">
                  <span className="sr-only">将 {track.title} 添加到歌单</span>
                  <select aria-label={`将 ${track.title} 添加到歌单`} defaultValue="" onChange={(event) => {
                    if (event.target.value) onAddToPlaylist(track, event.target.value)
                    event.target.value = ''
                  }}>
                    <option value="">歌单</option>
                    {playlists.map((playlist) => <option key={playlist.id} value={playlist.id}>{playlist.name}</option>)}
                  </select>
                </label>
              ) : null}
              {track.source === 'local' ? (
                <button className="music-icon-action" disabled={uploadingTrackIds.has(track.id)} onClick={() => onUpload(track)} type="button" aria-label={`上传 ${track.title}`} title={uploadingTrackIds.has(track.id) ? '上传中' : '上传到云端'}>
                  <MusicActionIcon name="upload" />
                </button>
              ) : null}
              {!track.remoteId ? (
                <button className="music-icon-action" onClick={() => onEdit(track)} type="button" aria-label={`编辑 ${track.title}`} title="编辑">
                  <MusicActionIcon name="edit" />
                </button>
              ) : null}
              <button className="music-icon-action music-icon-action-danger" disabled={deletingTrackId === track.id} onClick={() => onDelete(track)} type="button" aria-label={`删除 ${track.title}`} title={deletingTrackId === track.id ? '删除中' : describeSource(track)}>
                <MusicActionIcon name="trash" />
              </button>
            </span>
          </li>
        )
      })}
    </ul>
  )
}
