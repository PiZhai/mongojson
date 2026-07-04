import type { MusicLibraryState } from '../../types/tooling'

export const MUSIC_LIBRARY_STORAGE_KEY = 'personal-tooling-music-library-v1'

const MUSIC_FILES_DB = 'personal-tooling-music-files-v1'
const MUSIC_FILES_STORE = 'file-handles'

export type LocalFileHandle = {
  kind: 'file'
  name: string
  getFile: () => Promise<File>
  queryPermission?: (descriptor?: { mode: 'read' }) => Promise<PermissionState>
  requestPermission?: (descriptor?: { mode: 'read' }) => Promise<PermissionState>
}

export type LocalDirectoryHandle = {
  kind: 'directory'
  name: string
  entries: () => AsyncIterableIterator<[string, LocalFileHandle | LocalDirectoryHandle]>
  isSameEntry?: (other: LocalDirectoryHandle) => Promise<boolean>
  queryPermission?: (descriptor?: { mode: 'read' }) => Promise<PermissionState>
  requestPermission?: (descriptor?: { mode: 'read' }) => Promise<PermissionState>
}

export type WindowWithFilePicker = Window & {
  showOpenFilePicker?: (options?: {
    multiple?: boolean
    types?: Array<{
      description: string
      accept: Record<string, string[]>
    }>
    excludeAcceptAllOption?: boolean
  }) => Promise<LocalFileHandle[]>
  showDirectoryPicker?: (options?: {
    id?: string
    mode?: 'read' | 'readwrite'
    startIn?: string
  }) => Promise<LocalDirectoryHandle>
}

export const defaultMusicLibraryState: MusicLibraryState = {
  tracks: [],
  folders: [],
  queue: [],
  volume: 0.82,
  mode: 'order',
}

function normalizeVolume(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? Math.min(1, Math.max(0, value)) : 0.82
}

export function loadMusicLibraryState(): MusicLibraryState {
  if (typeof window === 'undefined') {
    return defaultMusicLibraryState
  }

  try {
    const raw = window.localStorage.getItem(MUSIC_LIBRARY_STORAGE_KEY)
    if (!raw) {
      return defaultMusicLibraryState
    }

    const parsed = JSON.parse(raw) as Partial<MusicLibraryState>
    const tracks = Array.isArray(parsed.tracks) ? parsed.tracks : []
    const folders = Array.isArray(parsed.folders) ? parsed.folders : []
    const trackIds = new Set(tracks.map((track) => track.id))
    const queue = Array.isArray(parsed.queue) ? parsed.queue.filter((id) => trackIds.has(id)) : tracks.map((track) => track.id)
    const currentTrackId = parsed.currentTrackId && trackIds.has(parsed.currentTrackId) ? parsed.currentTrackId : undefined
    const mode =
      parsed.mode === 'repeat-one' || parsed.mode === 'repeat-all' || parsed.mode === 'shuffle' || parsed.mode === 'order'
        ? parsed.mode
        : 'order'

    return {
      tracks,
      folders,
      queue,
      currentTrackId,
      volume: normalizeVolume(parsed.volume),
      mode,
    }
  } catch {
    return defaultMusicLibraryState
  }
}

export function saveMusicLibraryState(state: MusicLibraryState) {
  if (typeof window === 'undefined') {
    return
  }

  window.localStorage.setItem(MUSIC_LIBRARY_STORAGE_KEY, JSON.stringify(state))
}

function openMusicFilesDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(MUSIC_FILES_DB, 1)

    request.onupgradeneeded = () => {
      request.result.createObjectStore(MUSIC_FILES_STORE)
    }
    request.onerror = () => reject(request.error)
    request.onsuccess = () => resolve(request.result)
  })
}

function withStore<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  if (typeof indexedDB === 'undefined') {
    return Promise.reject(new Error('当前浏览器不支持 IndexedDB。'))
  }

  return openMusicFilesDb().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const transaction = db.transaction(MUSIC_FILES_STORE, mode)
        const request = action(transaction.objectStore(MUSIC_FILES_STORE))

        request.onerror = () => reject(request.error)
        request.onsuccess = () => resolve(request.result)
        transaction.oncomplete = () => db.close()
        transaction.onerror = () => {
          db.close()
          reject(transaction.error)
        }
      }),
  )
}

export function saveLocalFileHandle(id: string, handle: LocalFileHandle) {
  return withStore('readwrite', (store) => store.put(handle, id))
}

export function getLocalFileHandle(id: string) {
  return withStore<LocalFileHandle | undefined>('readonly', (store) => store.get(id))
}

export function deleteLocalFileHandle(id: string) {
  return withStore('readwrite', (store) => store.delete(id))
}

export function saveLocalDirectoryHandle(id: string, handle: LocalDirectoryHandle) {
  return withStore('readwrite', (store) => store.put(handle, id))
}

export function getLocalDirectoryHandle(id: string) {
  return withStore<LocalDirectoryHandle | undefined>('readonly', (store) => store.get(id))
}

export function deleteLocalDirectoryHandle(id: string) {
  return withStore('readwrite', (store) => store.delete(id))
}

export function supportsPersistentLocalFiles() {
  return typeof window !== 'undefined' && typeof (window as WindowWithFilePicker).showOpenFilePicker === 'function'
}

export function supportsPersistentMusicFolders() {
  return typeof window !== 'undefined' && typeof (window as WindowWithFilePicker).showDirectoryPicker === 'function'
}
