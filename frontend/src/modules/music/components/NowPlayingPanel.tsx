import { ScrollArea } from '@base-ui/react/scroll-area'
import { Tabs } from '@base-ui/react/tabs'
import { useState } from 'react'
import type { LrcLine } from '../MusicPlayerContext'
import type { MusicTrack } from '../types'
import { summarizeAudioQuality } from '../lib/audioQuality'
import { MusicActionIcon } from './MusicActionIcon'
import { MusicArtwork } from './MusicArtwork'

type NowPlayingPanelProps = {
  currentLyricIndex: number
  currentTrack: MusicTrack | null
  currentTime: number
  duration: number
  lyricStatusMessage: string | null
  lyrics: LrcLine[]
  onOpenQueue: () => void
}

function formatTime(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0:00'
  return `${Math.floor(value / 60)}:${Math.floor(value % 60).toString().padStart(2, '0')}`
}

export function NowPlayingPanel({
  currentLyricIndex,
  currentTrack,
  currentTime,
  duration,
  lyricStatusMessage,
  lyrics,
  onOpenQueue,
}: NowPlayingPanelProps) {
  const [tab, setTab] = useState<'lyrics' | 'details'>('lyrics')
  const progress = duration > 0 ? Math.min(100, Math.max(0, (currentTime / duration) * 100)) : 0

  return (
    <aside className="music-now-panel" aria-label="正在播放">
      <div className="music-now-artwork-wrap">
        <MusicArtwork className="music-now-artwork" track={currentTrack} />
        <span className="music-now-artwork-aura" aria-hidden="true" />
      </div>

      <div className="music-now-heading">
        <div className="music-now-copy">
          <p className="music-overline">Now playing</p>
          <h2>{currentTrack?.title ?? '等待你的第一首歌'}</h2>
          <p>{currentTrack?.artist || currentTrack?.fileName || '从曲库中选择一首歌曲'}</p>
        </div>
        <button className="music-icon-action" onClick={onOpenQueue} type="button" aria-label="打开播放队列" title="播放队列">
          <MusicActionIcon name="queue" />
        </button>
      </div>

      <div className="music-now-progress" aria-label={`播放进度 ${formatTime(currentTime)} / ${formatTime(duration)}`}>
        <span style={{ width: `${progress}%` }} />
      </div>
      <div className="music-now-time"><span>{formatTime(currentTime)}</span><span>{formatTime(duration)}</span></div>

      {currentTrack?.audioQuality ? <p className="music-quality-line">{summarizeAudioQuality(currentTrack.audioQuality)}</p> : null}

      <Tabs.Root className="music-now-tabs-root" onValueChange={(value) => setTab(value as 'lyrics' | 'details')} value={tab}>
        <Tabs.List className="music-now-tabs" aria-label="播放详情">
          <Tabs.Tab value="lyrics">歌词</Tabs.Tab>
          <Tabs.Tab value="details">详情</Tabs.Tab>
          <Tabs.Indicator className="music-now-tabs-indicator" />
        </Tabs.List>

        <Tabs.Panel className="music-now-tab-panel" value="lyrics">
          {!currentTrack ? (
            <div className="music-now-empty">播放音乐后，这里会显示同步歌词。</div>
          ) : lyricStatusMessage ? (
            <div className="music-now-empty">{lyricStatusMessage}</div>
          ) : lyrics.length === 0 ? (
            <div className="music-now-empty">
              <strong>还没有同步歌词</strong>
              <span>{currentTrack.lyricFileName ? '当前歌词没有可识别的时间轴。' : '重新导入同名 .lrc 文件即可补充。'}</span>
            </div>
          ) : (
            <ScrollArea.Root className="music-lyric-scroll">
              <ScrollArea.Viewport className="music-lyric-viewport">
                <ScrollArea.Content className="music-now-lyrics" aria-live="polite">
                  {lyrics.map((line, index) => (
                    <p className={index === currentLyricIndex ? 'is-active' : ''} key={`${line.time}-${index}`}>{line.text}</p>
                  ))}
                </ScrollArea.Content>
              </ScrollArea.Viewport>
              <ScrollArea.Scrollbar className="music-scrollbar"><ScrollArea.Thumb className="music-scrollbar-thumb" /></ScrollArea.Scrollbar>
            </ScrollArea.Root>
          )}
        </Tabs.Panel>

        <Tabs.Panel className="music-now-tab-panel" value="details">
          <dl className="music-track-details">
            <div><dt>来源</dt><dd>{currentTrack?.remoteId ? '云端曲库' : currentTrack?.source === 'local' ? '本地文件' : 'URL'}</dd></div>
            <div><dt>音频文件</dt><dd>{currentTrack?.fileName || currentTrack?.remoteUrl || '暂无'}</dd></div>
            <div><dt>歌词</dt><dd>{currentTrack?.lyricFileName || '未匹配'}</dd></div>
            <div><dt>封面</dt><dd>{currentTrack?.artworkUrl ? '音频内嵌封面' : '生成封面'}</dd></div>
            <div><dt>备注</dt><dd>{currentTrack?.note || '暂无'}</dd></div>
          </dl>
        </Tabs.Panel>
      </Tabs.Root>
    </aside>
  )
}
