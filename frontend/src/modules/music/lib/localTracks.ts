import type { MusicArtworkRef, MusicAudioQuality, MusicTrack } from '../types'

export type LocalTrackOptions = {
  localHandleId?: string
  folderHandleId?: string
  relativePath?: string
  lyricHandleId?: string
  lyricFileName?: string
  lyricRelativePath?: string
  audioQuality?: MusicAudioQuality
  artwork?: MusicArtworkRef
}

const SUPPORTED_AUDIO_EXTENSIONS = /\.(mp3|flac|wav|ogg|m4a|aac|opus|webm)$/i
const SUPPORTED_LYRIC_EXTENSION = /\.lrc$/i

export function createMusicId(prefix: string) {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return `${prefix}-${crypto.randomUUID()}`
  }

  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export function isSupportedAudioFile(fileName: string, mimeType?: string) {
  return Boolean(mimeType?.startsWith('audio/')) || SUPPORTED_AUDIO_EXTENSIONS.test(fileName)
}

export function isSupportedLyricFile(fileName: string) {
  return SUPPORTED_LYRIC_EXTENSION.test(fileName)
}

function getPathWithoutExtension(path: string) {
  return path.replace(/\\/g, '/').replace(/\.[^/.]+$/, '').toLowerCase()
}

export function createLocalTrackFromFile(file: File, id: string, options: LocalTrackOptions = {}): MusicTrack {
  return {
    id,
    source: 'local',
    title: file.name.replace(/\.[^.]+$/, '') || file.name,
    localHandleId: options.localHandleId,
    folderHandleId: options.folderHandleId,
    relativePath: options.relativePath,
    lyricHandleId: options.lyricHandleId,
    lyricFileName: options.lyricFileName,
    lyricRelativePath: options.lyricRelativePath,
    fileName: file.name,
    mimeType: file.type || undefined,
    duration: options.audioQuality?.duration,
    audioQuality: options.audioQuality,
    artwork: options.artwork,
    addedAt: new Date().toISOString(),
  }
}

export function getFolderTrackKey(folderId: string, relativePath: string) {
  return `${folderId}::${relativePath.toLowerCase()}`
}

export function getLyricMatchKey(path: string) {
  return getPathWithoutExtension(path)
}
