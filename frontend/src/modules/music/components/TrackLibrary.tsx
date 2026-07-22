import { Menu } from '@base-ui/react/menu'
import type { MusicPlaylist, MusicTrack } from '../types'
import { MusicActionIcon } from './MusicActionIcon'
import { MusicArtwork } from './MusicArtwork'

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
        <h3>没有找到音乐</h3>
        <p>添加本地音乐、扫描文件夹，或调整当前搜索和筛选条件。</p>
      </div>
    )
  }

  return (
    <ul className="music-track-list" aria-label="音乐曲库">
      {tracks.map((track, index) => {
        const isCurrent = currentTrackId === track.id
        const isPlayingTrack = isPlaying && isCurrent
        const isPlayable = track.fileAvailable !== false
        const favorite = favoriteTrackIds.has(track.id)
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
              <MusicArtwork className="music-track-art" track={track} />
              <span className="music-track-primary">
                <strong>{track.title}</strong>
                <span>{track.artist || track.note || '未知歌手'}</span>
              </span>
            </button>

            <span className="music-track-source">{track.remoteId ? '云端' : track.source === 'local' ? '本地' : 'URL'}</span>
            <span className="music-track-quality">{describeAudioQuality(track)}</span>
            <span className="music-track-duration">{formatDuration(track.duration)}</span>
            <span className="music-track-menu">
              <button className={`music-icon-action${favorite ? ' is-favorite' : ''}`} onClick={() => onToggleFavorite(track)} type="button" aria-label={`${favorite ? '取消收藏' : '收藏'} ${track.title}`} aria-pressed={favorite} title={favorite ? '取消收藏' : '收藏'}>
                <MusicActionIcon name="heart" />
              </button>
              <Menu.Root>
                <Menu.Trigger className="music-icon-action" aria-label={`更多操作 ${track.title}`} title="更多操作">
                  <MusicActionIcon name="more" />
                </Menu.Trigger>
                <Menu.Portal>
                  <Menu.Positioner className="music-menu-positioner" sideOffset={8}>
                    <Menu.Popup className="music-menu-popup">
                      <Menu.Item className="music-menu-item" onClick={() => onEnqueue(track)}>加入播放队列</Menu.Item>
                      {track.source === 'local' ? (
                        <Menu.Item className="music-menu-item" disabled={uploadingTrackIds.has(track.id)} onClick={() => onUpload(track)}>
                          {uploadingTrackIds.has(track.id) ? '正在上传…' : '上传到云端'}
                        </Menu.Item>
                      ) : null}
                      {!track.remoteId ? <Menu.Item className="music-menu-item" onClick={() => onEdit(track)}>编辑音乐信息</Menu.Item> : null}
                      {playlists.length > 0 ? <Menu.Separator className="music-menu-separator" /> : null}
                      {playlists.map((playlist) => (
                        <Menu.Item className="music-menu-item" key={playlist.id} onClick={() => onAddToPlaylist(track, playlist.id)}>
                          加入「{playlist.name}」
                        </Menu.Item>
                      ))}
                      <Menu.Separator className="music-menu-separator" />
                      <Menu.Item
                        className="music-menu-item is-danger"
                        disabled={deletingTrackId === track.id}
                        onClick={() => onDelete(track)}
                      >
                        {deletingTrackId === track.id ? '正在删除…' : `删除 · ${describeSource(track)}`}
                      </Menu.Item>
                    </Menu.Popup>
                  </Menu.Positioner>
                </Menu.Portal>
              </Menu.Root>
            </span>
          </li>
        )
      })}
    </ul>
  )
}
