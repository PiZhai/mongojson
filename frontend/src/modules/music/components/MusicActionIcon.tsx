export type MusicActionIconName =
  | 'play'
  | 'pause'
  | 'queue'
  | 'edit'
  | 'trash'
  | 'link'
  | 'folder'
  | 'close'
  | 'music'
  | 'clock'
  | 'plus'
  | 'refresh'
  | 'upload'
  | 'search'
  | 'more'
  | 'chevron-down'
  | 'heart'

export function MusicActionIcon({ name }: { name: MusicActionIconName }) {
  const paths: Partial<Record<MusicActionIconName, ReactNode>> = {
    play: <path d="M8 5l11 7-11 7z" />,
    pause: <><path d="M9 6v12" /><path d="M15 6v12" /></>,
    queue: <><path d="M5 7h10" /><path d="M5 12h8" /><path d="M5 17h6" /><path d="M17 15v-4l3 2z" /></>,
    edit: <><path d="M5 19h4l10-10-4-4L5 15z" /><path d="M13 7l4 4" /></>,
    trash: <><path d="M5 7h14" /><path d="M9 7V5h6v2" /><path d="M8 10v8" /><path d="M12 10v8" /><path d="M16 10v8" /></>,
    folder: <path d="M4 7h6l2 2h8v9H4z" />,
    close: <><path d="M7 7l10 10" /><path d="M17 7L7 17" /></>,
    music: <><path d="M9 18V6l9-2v12" /><circle cx="6.5" cy="18" r="2.5" /><circle cx="15.5" cy="16" r="2.5" /></>,
    clock: <><circle cx="12" cy="12" r="8" /><path d="M12 8v5l3 2" /></>,
    plus: <><path d="M12 5v14" /><path d="M5 12h14" /></>,
    refresh: <><path d="M20 8a7 7 0 0 0-12-3l-2 2" /><path d="M6 4v3h3" /><path d="M4 16a7 7 0 0 0 12 3l2-2" /><path d="M18 20v-3h-3" /></>,
    upload: <><path d="M12 16V5" /><path d="M8 9l4-4 4 4" /><path d="M5 15v4h14v-4" /></>,
    search: <><circle cx="11" cy="11" r="6.5" /><path d="M16 16l4 4" /></>,
    more: <><circle cx="6" cy="12" r="1" /><circle cx="12" cy="12" r="1" /><circle cx="18" cy="12" r="1" /></>,
    'chevron-down': <path d="M6 9l6 6 6-6" />,
    heart: <path d="M20.8 5.8a5.4 5.4 0 0 0-7.7 0L12 6.9l-1.1-1.1a5.4 5.4 0 1 0-7.7 7.7L12 22l8.8-8.5a5.4 5.4 0 0 0 0-7.7z" />,
    link: <><path d="M10 13a5 5 0 0 0 7 0l1-1a5 5 0 0 0-7-7l-1 1" /><path d="M14 11a5 5 0 0 0-7 0l-1 1a5 5 0 0 0 7 7l1-1" /></>,
  }

  return (
    <svg aria-hidden="true" className="music-action-icon" viewBox="0 0 24 24">
      {paths[name]}
    </svg>
  )
}
import type { ReactNode } from 'react'
