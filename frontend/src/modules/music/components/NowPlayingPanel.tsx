import { useState, type CSSProperties } from 'react'
import type { LrcLine } from '../MusicPlayerContext'
import type { MusicTrack } from '../types'
import { summarizeAudioQuality } from '../lib/audioQuality'
import { MusicActionIcon } from './MusicActionIcon'

type NowPlayingPanelProps = {
  currentLyricIndex: number
  currentTrack: MusicTrack | null
  lyricStatusMessage: string | null
  lyrics: LrcLine[]
  onOpenQueue: () => void
}

function artworkStyle(track: MusicTrack | null) {
  const seed = track?.title ?? 'music'
  let hash = 0
  for (const character of seed) hash = character.charCodeAt(0) + ((hash << 5) - hash)
  const hue = Math.abs(hash) % 360
  return {
    '--music-art-hue': `${hue}`,
    '--music-art-hue-alt': `${(hue + 54) % 360}`,
  } as CSSProperties
}

export function NowPlayingPanel({
  currentLyricIndex,
  currentTrack,
  lyricStatusMessage,
  lyrics,
  onOpenQueue,
}: NowPlayingPanelProps) {
  const [tab, setTab] = useState<'lyrics' | 'details'>('lyrics')
  const activeIndex = currentLyricIndex >= 0 ? currentLyricIndex : 0
  const lyricWindow = lyrics.slice(Math.max(0, activeIndex - 3), Math.min(lyrics.length, activeIndex + 5))
  const lyricStart = Math.max(0, activeIndex - 3)

  return (
    <aside className="music-now-panel" aria-label="正在播放">
      <div className="music-artwork" style={artworkStyle(currentTrack)}>
        <div className="music-artwork-glow" />
        <span className="music-artwork-mark" aria-hidden="true">
          <MusicActionIcon name="music" />
        </span>
      </div>

      <div className="music-now-heading">
        <div className="music-now-copy">
          <p className="music-overline">正在播放</p>
          <h2>{currentTrack?.title ?? '还没有播放音乐'}</h2>
          <p>{currentTrack?.artist || currentTrack?.fileName || '从曲库中选择一首歌曲'}</p>
        </div>
        <button className="music-icon-action" onClick={onOpenQueue} type="button" aria-label="打开播放队列" title="播放队列">
          <MusicActionIcon name="queue" />
        </button>
      </div>

      {currentTrack?.audioQuality ? (
        <p className="music-quality-line">{summarizeAudioQuality(currentTrack.audioQuality)}</p>
      ) : null}

      <div className="music-now-tabs" role="tablist" aria-label="播放详情">
        <button aria-selected={tab === 'lyrics'} onClick={() => setTab('lyrics')} role="tab" type="button">歌词</button>
        <button aria-selected={tab === 'details'} onClick={() => setTab('details')} role="tab" type="button">详情</button>
      </div>

      <div className="music-now-tab-panel" role="tabpanel">
        {tab === 'lyrics' ? (
          !currentTrack ? (
            <div className="music-now-empty">选择歌曲后，这里会显示同步歌词。</div>
          ) : lyricStatusMessage ? (
            <div className="music-now-empty">{lyricStatusMessage}</div>
          ) : lyrics.length === 0 ? (
            <div className="music-now-empty">{currentTrack.lyricFileName ? '歌词文件没有可识别的时间轴。' : '未匹配同名 .lrc 歌词。'}</div>
          ) : (
            <div className="music-now-lyrics" aria-live="polite">
              {lyricWindow.map((line, index) => {
                const absoluteIndex = lyricStart + index
                return <p className={absoluteIndex === currentLyricIndex ? 'is-active' : ''} key={`${line.time}-${absoluteIndex}`}>{line.text}</p>
              })}
            </div>
          )
        ) : (
          <dl className="music-track-details">
            <div><dt>来源</dt><dd>{currentTrack?.remoteId ? '云端曲库' : currentTrack?.source === 'local' ? '本地文件' : 'URL'}</dd></div>
            <div><dt>音频文件</dt><dd>{currentTrack?.fileName || currentTrack?.remoteUrl || '暂无'}</dd></div>
            <div><dt>歌词</dt><dd>{currentTrack?.lyricFileName || '未匹配'}</dd></div>
            <div><dt>备注</dt><dd>{currentTrack?.note || '暂无'}</dd></div>
          </dl>
        )}
      </div>
    </aside>
  )
}
