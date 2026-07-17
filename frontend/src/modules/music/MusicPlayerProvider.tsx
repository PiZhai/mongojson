import {
  type PropsWithChildren,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import './styles.css'
import type { MusicLibraryFolder, MusicLibraryState, MusicTrack, PlaybackMode } from './types'
import {
  deleteLocalDirectoryHandle,
  deleteLocalFileHandle,
  getLocalDirectoryHandle,
  getLocalFileHandle,
  loadMusicLibraryState,
  saveLocalDirectoryHandle,
  saveLocalFileHandle,
  saveMusicLibraryState,
  supportsPersistentLocalFiles,
  supportsPersistentMusicFolders,
  type LocalDirectoryHandle,
  type LocalFileHandle,
} from './lib/storage'
import {
  analyzeAudioFileQuality,
  analyzeRemoteAudioQuality,
  inferAudioQuality,
  mergeDurationIntoQuality,
} from './lib/audioQuality'
import type { MusicAudioQuality } from './types'
import { getTrackSourceKey } from './lib/sourceKey'
import { deleteRemoteMusicTrack, fetchRemoteMusicPage, uploadMusicTrack } from './api'
import { MusicPlayerContext, type LrcLine, type MusicPlayerContextValue, type RemoteTrackPayload, type TrackEditPayload } from './MusicPlayerContext'
import { useMediaSession } from './hooks/useMediaSession'
import { usePlaybackShortcuts } from './hooks/usePlaybackShortcuts'
import { countEncodingArtifacts, parseLrcText, readLyricFileText } from './lib/lyrics'
import { getNextTrackId, getPreviousTrackId } from './lib/playbackQueue'
import {
  createLocalTrackFromFile,
  createMusicId,
  getFolderTrackKey,
  getLyricMatchKey,
  isSupportedAudioFile,
  isSupportedLyricFile,
} from './lib/localTracks'

type LyricUpdate = {
  trackId: string
  lyricHandleId: string
  lyricFileName: string
  lyricRelativePath: string
}

type FolderTrackCollectionResult = {
  tracks: MusicTrack[]
  scanned: number
  skipped: number
  lyricsMatched: number
  lyricUpdates: LyricUpdate[]
}


async function readLocalHandleFile(handle: LocalFileHandle) {
  if (handle.queryPermission && (await handle.queryPermission({ mode: 'read' })) !== 'granted') {
    if (!handle.requestPermission || (await handle.requestPermission({ mode: 'read' })) !== 'granted') {
      throw new Error('本地文件读取授权已失效，请重新选择文件。')
    }
  }

  return handle.getFile()
}

async function ensureDirectoryReadPermission(handle: LocalDirectoryHandle) {
  if (handle.queryPermission && (await handle.queryPermission({ mode: 'read' })) !== 'granted') {
    if (!handle.requestPermission || (await handle.requestPermission({ mode: 'read' })) !== 'granted') {
      throw new Error('本地文件夹读取授权已失效，请重新选择文件夹。')
    }
  }
}

export function MusicPlayerProvider({ children }: PropsWithChildren) {
  const [library, setLibrary] = useState<MusicLibraryState>(() => loadMusicLibraryState())
  const [remoteTracks, setRemoteTracks] = useState<MusicTrack[]>([])
  const [remoteCursor, setRemoteCursor] = useState<string | undefined>()
  const [remoteTracksLoading, setRemoteTracksLoading] = useState(false)
  const [remoteTracksInitialized, setRemoteTracksInitialized] = useState(false)
  const [uploadingTrackIds, setUploadingTrackIds] = useState<ReadonlySet<string>>(() => new Set())
  const [audioSrc, setAudioSrc] = useState<string | null>(null)
  const [sourceReloadToken, setSourceReloadToken] = useState(0)
  const [isPlaying, setIsPlaying] = useState(false)
  const [isQueueOpen, setIsQueueOpen] = useState(false)
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [lyrics, setLyrics] = useState<LrcLine[]>([])
  const [lyricStatusMessage, setLyricStatusMessage] = useState<string | null>(null)
  const [statusMessage, setStatusMessage] = useState<string | null>(null)
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const objectUrlRef = useRef<string | null>(null)
  const pendingPlayRef = useRef(false)
  const sessionFilesRef = useRef(new Map<string, File>())
  const sessionLyricsRef = useRef(new Map<string, string>())
  const remoteInitialLoadRef = useRef(false)
  const remoteLoadingRef = useRef(false)

  const tracks = useMemo(() => {
    const remoteIds = new Set(remoteTracks.map((track) => track.id))
    return [...remoteTracks, ...library.tracks.filter((track) => !remoteIds.has(track.id))]
  }, [library.tracks, remoteTracks])

  const currentTrack = useMemo(
    () => tracks.find((track) => track.id === library.currentTrackId) ?? null,
    [library.currentTrackId, tracks],
  )
  const currentTrackRef = useRef<MusicTrack | null>(currentTrack)
  const currentTrackSourceKey = getTrackSourceKey(currentTrack)

  useEffect(() => {
    currentTrackRef.current = currentTrack
  }, [currentTrack])

  const currentLyricIndex = useMemo(() => {
    if (lyrics.length === 0) {
      return -1
    }

    for (let index = lyrics.length - 1; index >= 0; index -= 1) {
      if (currentTime + 0.2 >= lyrics[index].time) {
        return index
      }
    }

    return -1
  }, [currentTime, lyrics])

  const currentLyricLine = currentLyricIndex >= 0 ? lyrics[currentLyricIndex] : null

  useEffect(() => {
    saveMusicLibraryState(library)
  }, [library])

  useEffect(() => {
    if (!isPlaying || !library.currentTrackId) return
    setLibrary((value) => ({
      ...value,
      recentTrackIds: [library.currentTrackId!, ...value.recentTrackIds.filter((id) => id !== library.currentTrackId)].slice(0, 50),
    }))
  }, [isPlaying, library.currentTrackId])

  const loadMoreRemoteTracks = useCallback(async () => {
    if (remoteLoadingRef.current || (remoteTracksInitialized && !remoteCursor)) {
      return
    }
    remoteLoadingRef.current = true
    setRemoteTracksLoading(true)
    try {
      const page = await fetchRemoteMusicPage(remoteCursor)
      setRemoteTracks((value) => {
        const known = new Set(value.map((track) => track.id))
        return [...value, ...page.tracks.filter((track) => !known.has(track.id))]
      })
      setRemoteCursor(page.nextCursor)
      setRemoteTracksInitialized(true)
    } catch (error) {
      setStatusMessage(error instanceof Error ? `远程曲库加载失败：${error.message}` : '远程曲库加载失败。')
    } finally {
      remoteLoadingRef.current = false
      setRemoteTracksLoading(false)
    }
  }, [remoteCursor, remoteTracksInitialized])

  useEffect(() => {
    if (remoteInitialLoadRef.current) {
      return
    }
    remoteInitialLoadRef.current = true
    void loadMoreRemoteTracks()
  }, [loadMoreRemoteTracks])

  useEffect(() => {
    const audio = audioRef.current
    if (audio) {
      audio.volume = library.volume
    }
  }, [library.volume])

  useEffect(() => {
    let cancelled = false
    const track = currentTrackRef.current

    async function resolveSource(nextTrack: MusicTrack) {
      if (objectUrlRef.current) {
        const audio = audioRef.current
        if (audio?.src === objectUrlRef.current) {
          audio.pause()
          audio.removeAttribute('src')
          audio.load()
        }
        URL.revokeObjectURL(objectUrlRef.current)
        objectUrlRef.current = null
      }

      setCurrentTime(0)
      setDuration(nextTrack.duration ?? 0)
      setAudioSrc(null)

      if (nextTrack.source === 'remote') {
        if (nextTrack.fileAvailable === false) {
          setStatusMessage(nextTrack.recordIssue ?? '远程歌曲文件缺失，建议删除此歌曲记录。')
          pendingPlayRef.current = false
          setIsPlaying(false)
          return
        }
        if (!nextTrack.remoteUrl) {
          setStatusMessage('这首云端音乐缺少可播放 URL。')
          pendingPlayRef.current = false
          setIsPlaying(false)
          return
        }

        setStatusMessage(null)
        setAudioSrc(nextTrack.remoteUrl)
        return
      }

      try {
        let file = sessionFilesRef.current.get(nextTrack.id)
        if (!file && nextTrack.localHandleId) {
          const handle = await getLocalFileHandle(nextTrack.localHandleId)
          if (handle) {
            file = await readLocalHandleFile(handle)
          }
        }

        if (!file) {
          setStatusMessage('本地音乐需要重新选择文件后才能播放。')
          pendingPlayRef.current = false
          setIsPlaying(false)
          return
        }

        const objectUrl = URL.createObjectURL(file)
        if (!cancelled) {
          objectUrlRef.current = objectUrl
          setStatusMessage(null)
          setAudioSrc(objectUrl)
        } else {
          URL.revokeObjectURL(objectUrl)
        }
      } catch (error) {
        setStatusMessage(error instanceof Error ? error.message : '无法读取本地音乐文件。')
        pendingPlayRef.current = false
        setIsPlaying(false)
      }
    }

    if (!track) {
      pendingPlayRef.current = false
      setAudioSrc(null)
      setCurrentTime(0)
      setDuration(0)
      setIsPlaying(false)
      return undefined
    }

    void resolveSource(track)

    return () => {
      cancelled = true
    }
  }, [currentTrackSourceKey, sourceReloadToken])

  useEffect(() => {
    let cancelled = false

    async function resolveLyrics(track: MusicTrack) {
      setLyrics([])
      setLyricStatusMessage(null)

      try {
        let lyricText = sessionLyricsRef.current.get(track.id)
        if ((!lyricText || countEncodingArtifacts(lyricText) > 0) && track.lyricHandleId) {
          const handle = await getLocalFileHandle(track.lyricHandleId)
          if (handle) {
            const file = await readLocalHandleFile(handle)
            lyricText = await readLyricFileText(file)
            sessionLyricsRef.current.set(track.id, lyricText)
          }
        }

        if (!lyricText && track.lyricUrl) {
          const response = await fetch(track.lyricUrl)
          if (!response.ok) {
            throw new Error(`远程歌词加载失败：${response.status}`)
          }
          const lyricFile = new File([await response.blob()], track.lyricFileName ?? 'lyrics.lrc', {
            type: response.headers.get('Content-Type') ?? 'text/plain',
          })
          lyricText = await readLyricFileText(lyricFile)
          sessionLyricsRef.current.set(track.id, lyricText)
        }

        if (!lyricText) {
          return
        }

        const parsedLyrics = parseLrcText(lyricText)
        if (cancelled) {
          return
        }

        setLyrics(parsedLyrics)
        setLyricStatusMessage(parsedLyrics.length > 0 ? null : '歌词文件没有可识别的时间轴。')
      } catch (error) {
        if (!cancelled) {
          setLyrics([])
          setLyricStatusMessage(error instanceof Error ? error.message : '无法读取歌词文件。')
        }
      }
    }

    if (!currentTrack) {
      setLyrics([])
      setLyricStatusMessage(null)
      return undefined
    }

    void resolveLyrics(currentTrack)

    return () => {
      cancelled = true
    }
  }, [currentTrack])

  const requestAudioPlay = useCallback(() => {
    const audio = audioRef.current
    if (!audio || !audioSrc) {
      pendingPlayRef.current = true
      setSourceReloadToken((value) => value + 1)
      return
    }

    pendingPlayRef.current = false
    void audio
      .play()
      .then(() => {
        setIsPlaying(true)
        setStatusMessage(null)
      })
      .catch(() => {
        pendingPlayRef.current = false
        setIsPlaying(false)
        setStatusMessage('播放启动失败，请再次点击播放。')
      })
  }, [audioSrc])

  useEffect(() => {
    if (audioSrc && pendingPlayRef.current) {
      requestAudioPlay()
    }
  }, [audioSrc, requestAudioPlay])

  useEffect(() => {
    const audio = audioRef.current
    if (audio && !isPlaying) {
      audio.pause()
    }
  }, [isPlaying])

  useEffect(
    () => () => {
      const audio = audioRef.current
      if (audio) {
        audio.pause()
        audio.removeAttribute('src')
        audio.load()
      }
      if (objectUrlRef.current) {
        URL.revokeObjectURL(objectUrlRef.current)
        objectUrlRef.current = null
      }
    },
    [],
  )

  const setTrackAudioQuality = useCallback((trackId: string, audioQuality: MusicAudioQuality) => {
    setLibrary((value) => ({
      ...value,
      tracks: value.tracks.map((track) =>
        track.id === trackId
          ? {
              ...track,
              audioQuality,
              duration: audioQuality.duration ?? track.duration,
            }
          : track,
      ),
    }))
  }, [])

  const analyzeRemoteTrackAudioQuality = useCallback(
    (trackId: string, remoteUrl: string) => {
      void analyzeRemoteAudioQuality(remoteUrl).then((audioQuality) => {
        setTrackAudioQuality(trackId, audioQuality)
      })
    },
    [setTrackAudioQuality],
  )

  const addRemoteTrack = useCallback((payload: RemoteTrackPayload) => {
    const id = createMusicId('remote')
    const track: MusicTrack = {
      id,
      source: 'remote',
      title: payload.title,
      artist: payload.artist || undefined,
      note: payload.note || undefined,
      remoteUrl: payload.remoteUrl,
      audioQuality: inferAudioQuality({ fileName: payload.remoteUrl }),
      addedAt: new Date().toISOString(),
    }

    setLibrary((value) => ({
      ...value,
      tracks: [track, ...value.tracks],
      queue: value.queue.includes(track.id) ? value.queue : [...value.queue, track.id],
    }))
    setStatusMessage('已添加云端音乐。')
    analyzeRemoteTrackAudioQuality(id, payload.remoteUrl)
    return id
  }, [analyzeRemoteTrackAudioQuality])

  const addLocalFileHandles = useCallback(async (handles: LocalFileHandle[]) => {
    const tracks: MusicTrack[] = []
    const lyricHandles = new Map<string, LocalFileHandle>()
    let lyricsMatched = 0

    for (const handle of handles) {
      if (isSupportedLyricFile(handle.name)) {
        lyricHandles.set(getLyricMatchKey(handle.name), handle)
      }
    }

    for (const handle of handles) {
      if (isSupportedLyricFile(handle.name)) {
        continue
      }

      const file = await readLocalHandleFile(handle)
      if (!isSupportedAudioFile(file.name, file.type)) {
        continue
      }

      const trackId = createMusicId('local')
      const handleId = createMusicId('handle')
      const lyricHandle = lyricHandles.get(getLyricMatchKey(handle.name))
      let lyricHandleId: string | undefined

      if (lyricHandle) {
        lyricHandleId = createMusicId('lyric')
        await saveLocalFileHandle(lyricHandleId, lyricHandle)
        sessionLyricsRef.current.set(trackId, await readLyricFileText(await readLocalHandleFile(lyricHandle)))
        lyricsMatched += 1
      }

      await saveLocalFileHandle(handleId, handle)
      const audioQuality = await analyzeAudioFileQuality(file)
      sessionFilesRef.current.set(trackId, file)
      tracks.push(
        createLocalTrackFromFile(file, trackId, {
          localHandleId: handleId,
          lyricHandleId,
          lyricFileName: lyricHandle?.name,
          audioQuality,
        }),
      )
    }

    if (tracks.length === 0) {
      setStatusMessage('没有找到可识别的本地音乐文件。')
      return
    }

    setLibrary((value) => ({
      ...value,
      tracks: [...tracks, ...value.tracks],
      queue: [...tracks.map((track) => track.id), ...value.queue],
      currentTrackId: value.currentTrackId ?? tracks[0]?.id,
    }))
    setStatusMessage(`已添加 ${tracks.length} 个本地音乐文件${lyricsMatched > 0 ? `，自动匹配 ${lyricsMatched} 个歌词` : ''}。`)
  }, [])

  const addLocalFiles = useCallback(async (files: File[]) => {
    const lyricFiles = new Map<string, File>()
    let lyricsMatched = 0

    for (const file of files) {
      if (isSupportedLyricFile(file.name)) {
        lyricFiles.set(getLyricMatchKey(file.name), file)
      }
    }

    const audioFiles = files.filter((file) => isSupportedAudioFile(file.name, file.type))
    const tracks = await Promise.all(audioFiles.map(async (file) => {
      const trackId = createMusicId('local')
      const lyricFile = lyricFiles.get(getLyricMatchKey(file.name))
      const audioQuality = await analyzeAudioFileQuality(file)
      sessionFilesRef.current.set(trackId, file)
      if (lyricFile) {
        sessionLyricsRef.current.set(trackId, await readLyricFileText(lyricFile))
        lyricsMatched += 1
      }
      return createLocalTrackFromFile(file, trackId, {
        lyricFileName: lyricFile?.name,
        audioQuality,
      })
    }))

    if (tracks.length === 0) {
      setStatusMessage('没有找到可识别的本地音乐文件。')
      return
    }

    setLibrary((value) => ({
      ...value,
      tracks: [...tracks, ...value.tracks],
      queue: [...tracks.map((track) => track.id), ...value.queue],
      currentTrackId: value.currentTrackId ?? tracks[0]?.id,
    }))
    setStatusMessage(
      `已添加 ${tracks.length} 个本地音乐文件${lyricsMatched > 0 ? `，自动匹配 ${lyricsMatched} 个歌词` : ''}；当前浏览器不支持刷新后自动恢复。`,
    )
  }, [])

  const uploadLocalTrack = useCallback(async (id: string) => {
    const track = library.tracks.find((item) => item.id === id)
    if (!track || track.source !== 'local') {
      throw new Error('只能上传本地歌曲。')
    }

    setUploadingTrackIds((value) => new Set(value).add(id))
    try {
      let file = sessionFilesRef.current.get(id)
      if (!file && track.localHandleId) {
        const handle = await getLocalFileHandle(track.localHandleId)
        if (handle) {
          file = await readLocalHandleFile(handle)
        }
      }
      if (!file) {
        throw new Error('本地文件授权已失效，请重新选择该歌曲后再上传。')
      }

      let lyricFile: File | undefined
      if (track.lyricHandleId) {
        const lyricHandle = await getLocalFileHandle(track.lyricHandleId)
        if (lyricHandle) {
          lyricFile = await readLocalHandleFile(lyricHandle)
        }
      }
      if (!lyricFile && track.lyricFileName) {
        const lyricText = sessionLyricsRef.current.get(id)
        if (lyricText) {
          lyricFile = new File([lyricText], track.lyricFileName, { type: 'text/plain' })
        }
      }

      const result = await uploadMusicTrack(file, track, lyricFile)
      setRemoteTracks((value) => [result.track, ...value.filter((item) => item.id !== result.track.id)])
      setStatusMessage(
        result.duplicate
          ? `《${track.title}》已存在于远程曲库，未重复上传${result.track.lyricFileName ? '，歌词已同步' : ''}。`
          : `《${track.title}》已上传到远程曲库${result.track.lyricFileName ? '，歌词已同步' : ''}。`,
      )
      return { duplicate: result.duplicate }
    } catch (error) {
      const message = error instanceof Error ? error.message : '歌曲上传失败。'
      setStatusMessage(message)
      throw error
    } finally {
      setUploadingTrackIds((value) => {
        const next = new Set(value)
        next.delete(id)
        return next
      })
    }
  }, [library.tracks])

  const collectDirectoryTracks = useCallback(
    async (
      folderId: string,
      directoryHandle: LocalDirectoryHandle,
      existingTracksByKey: Map<string, MusicTrack>,
    ): Promise<FolderTrackCollectionResult> => {
      const tracks: MusicTrack[] = []
      const audioEntries: Array<{ entry: LocalFileHandle; relativePath: string }> = []
      const lyricEntries = new Map<string, { entry: LocalFileHandle; relativePath: string }>()
      const lyricUpdates: LyricUpdate[] = []
      let scanned = 0
      let skipped = 0
      let lyricsMatched = 0

      async function walk(handle: LocalDirectoryHandle, basePath: string) {
        for await (const [entryName, entry] of handle.entries()) {
          const relativePath = basePath ? `${basePath}/${entryName}` : entryName

          if (entry.kind === 'directory') {
            await walk(entry, relativePath)
            continue
          }

          if (isSupportedLyricFile(entry.name)) {
            lyricEntries.set(getLyricMatchKey(relativePath), { entry, relativePath })
            continue
          }

          if (!isSupportedAudioFile(entry.name)) {
            continue
          }

          audioEntries.push({ entry, relativePath })
        }
      }

      await ensureDirectoryReadPermission(directoryHandle)
      await walk(directoryHandle, '')

      for (const { entry, relativePath } of audioEntries) {
        scanned += 1

        const trackKey = getFolderTrackKey(folderId, relativePath)
        const lyricEntry = lyricEntries.get(getLyricMatchKey(relativePath))
        const existingTrack = existingTracksByKey.get(trackKey)

        if (existingTrack) {
          skipped += 1
          if (lyricEntry && !existingTrack.lyricHandleId) {
            const lyricHandleId = createMusicId('lyric')
            await saveLocalFileHandle(lyricHandleId, lyricEntry.entry)
            lyricUpdates.push({
              trackId: existingTrack.id,
              lyricHandleId,
              lyricFileName: lyricEntry.entry.name,
              lyricRelativePath: lyricEntry.relativePath,
            })
            lyricsMatched += 1
          }
          continue
        }

        const file = await readLocalHandleFile(entry)
        const trackId = createMusicId('local')
        const handleId = createMusicId('handle')
        let lyricHandleId: string | undefined

        if (lyricEntry) {
          lyricHandleId = createMusicId('lyric')
          await saveLocalFileHandle(lyricHandleId, lyricEntry.entry)
          sessionLyricsRef.current.set(trackId, await readLyricFileText(await readLocalHandleFile(lyricEntry.entry)))
          lyricsMatched += 1
        }

        await saveLocalFileHandle(handleId, entry)
        const audioQuality = await analyzeAudioFileQuality(file)
        sessionFilesRef.current.set(trackId, file)
        const track = createLocalTrackFromFile(file, trackId, {
          localHandleId: handleId,
          folderHandleId: folderId,
          relativePath,
          lyricHandleId,
          lyricFileName: lyricEntry?.entry.name,
          lyricRelativePath: lyricEntry?.relativePath,
          audioQuality,
        })
        tracks.push(track)
        existingTracksByKey.set(trackKey, track)
      }

      return { tracks, scanned, skipped, lyricsMatched, lyricUpdates }
    },
    [],
  )

  const scanLocalDirectory = useCallback(
    async (handle: LocalDirectoryHandle) => {
      await ensureDirectoryReadPermission(handle)

      let folderId = createMusicId('folder')
      let folderName = handle.name
      let isExistingFolder = false

      if (handle.isSameEntry) {
        for (const folder of library.folders) {
          const savedHandle = await getLocalDirectoryHandle(folder.id).catch(() => undefined)
          if (savedHandle && (await handle.isSameEntry(savedHandle))) {
            folderId = folder.id
            folderName = folder.name
            isExistingFolder = true
            break
          }
        }
      }

      await saveLocalDirectoryHandle(folderId, handle)

      const existingTracksByKey = new Map(
        library.tracks
          .filter((track) => track.folderHandleId === folderId && track.relativePath)
          .map((track) => [getFolderTrackKey(folderId, track.relativePath as string), track]),
      )
      const result = await collectDirectoryTracks(folderId, handle, existingTracksByKey)
      const scannedAt = new Date().toISOString()

      setLibrary((value) => {
        const lyricUpdatesById = new Map(result.lyricUpdates.map((update) => [update.trackId, update]))
        const nextTrackCount =
          value.tracks.filter((track) => track.folderHandleId === folderId).length + result.tracks.length
        const nextFolder: MusicLibraryFolder = {
          id: folderId,
          name: folderName,
          addedAt: value.folders.find((folder) => folder.id === folderId)?.addedAt ?? scannedAt,
          lastScannedAt: scannedAt,
          trackCount: nextTrackCount,
        }
        const folders = isExistingFolder
          ? value.folders.map((folder) => (folder.id === folderId ? nextFolder : folder))
          : [nextFolder, ...value.folders]

        return {
          ...value,
          folders,
          tracks: [
            ...result.tracks,
            ...value.tracks.map((track) => {
              const lyricUpdate = lyricUpdatesById.get(track.id)
              return lyricUpdate
                ? {
                    ...track,
                    lyricHandleId: lyricUpdate.lyricHandleId,
                    lyricFileName: lyricUpdate.lyricFileName,
                    lyricRelativePath: lyricUpdate.lyricRelativePath,
                  }
                : track
            }),
          ],
          queue: [...result.tracks.map((track) => track.id), ...value.queue],
          currentTrackId: value.currentTrackId ?? result.tracks[0]?.id,
        }
      })

      setStatusMessage(
        result.tracks.length > 0
          ? `已扫描 ${folderName}，新增 ${result.tracks.length} 首音乐${result.lyricsMatched > 0 ? `，匹配 ${result.lyricsMatched} 个歌词` : ''}。`
          : `已扫描 ${folderName}，没有发现新的音乐文件${result.lyricsMatched > 0 ? `，补全 ${result.lyricsMatched} 个歌词` : ''}。`,
      )

      return {
        folderId,
        folderName,
        added: result.tracks.length,
        scanned: result.scanned,
        skipped: result.skipped,
        lyricsMatched: result.lyricsMatched,
      }
    },
    [collectDirectoryTracks, library.folders, library.tracks],
  )

  const rescanLocalFolder = useCallback(
    async (folderId: string) => {
      const folder = library.folders.find((item) => item.id === folderId)
      if (!folder) {
        throw new Error('没有找到这个音乐文件夹。')
      }

      const handle = await getLocalDirectoryHandle(folderId)
      if (!handle) {
        throw new Error('这个文件夹需要重新选择后才能扫描。')
      }

      await ensureDirectoryReadPermission(handle)
      const existingTracksByKey = new Map(
        library.tracks
          .filter((track) => track.folderHandleId === folderId && track.relativePath)
          .map((track) => [getFolderTrackKey(folderId, track.relativePath as string), track]),
      )
      const result = await collectDirectoryTracks(folderId, handle, existingTracksByKey)
      const scannedAt = new Date().toISOString()

      setLibrary((value) => {
        const lyricUpdatesById = new Map(result.lyricUpdates.map((update) => [update.trackId, update]))
        const nextTrackCount =
          value.tracks.filter((track) => track.folderHandleId === folderId).length + result.tracks.length

        return {
          ...value,
          folders: value.folders.map((item) =>
            item.id === folderId
              ? {
                  ...item,
                  lastScannedAt: scannedAt,
                  trackCount: nextTrackCount,
                }
              : item,
          ),
          tracks: [
            ...result.tracks,
            ...value.tracks.map((track) => {
              const lyricUpdate = lyricUpdatesById.get(track.id)
              return lyricUpdate
                ? {
                    ...track,
                    lyricHandleId: lyricUpdate.lyricHandleId,
                    lyricFileName: lyricUpdate.lyricFileName,
                    lyricRelativePath: lyricUpdate.lyricRelativePath,
                  }
                : track
            }),
          ],
          queue: [...result.tracks.map((track) => track.id), ...value.queue],
          currentTrackId: value.currentTrackId ?? result.tracks[0]?.id,
        }
      })

      setStatusMessage(
        result.tracks.length > 0
          ? `已重新扫描 ${folder.name}，新增 ${result.tracks.length} 首音乐${result.lyricsMatched > 0 ? `，匹配 ${result.lyricsMatched} 个歌词` : ''}。`
          : `已重新扫描 ${folder.name}，没有发现新的音乐文件${result.lyricsMatched > 0 ? `，补全 ${result.lyricsMatched} 个歌词` : ''}。`,
      )

      return {
        folderId,
        folderName: folder.name,
        added: result.tracks.length,
        scanned: result.scanned,
        skipped: result.skipped,
        lyricsMatched: result.lyricsMatched,
      }
    },
    [collectDirectoryTracks, library.folders, library.tracks],
  )

  const removeLocalFolder = useCallback((folderId: string) => {
    setLibrary((value) => ({
      ...value,
      folders: value.folders.filter((folder) => folder.id !== folderId),
    }))
    void deleteLocalDirectoryHandle(folderId).catch(() => undefined)
    setStatusMessage('已停止跟踪这个音乐文件夹，已扫描歌曲仍保留在曲库。')
  }, [])

  const updateTrack = useCallback((id: string, payload: TrackEditPayload) => {
    setLibrary((value) => ({
      ...value,
      tracks: value.tracks.map((track) =>
        track.id === id
          ? {
              ...track,
              title: payload.title,
              artist: payload.artist || undefined,
              note: payload.note || undefined,
              remoteUrl: track.source === 'remote' ? payload.remoteUrl : track.remoteUrl,
            }
          : track,
      ),
    }))
    setStatusMessage('已更新音乐信息。')
    if (payload.remoteUrl) {
      analyzeRemoteTrackAudioQuality(id, payload.remoteUrl)
    }
  }, [analyzeRemoteTrackAudioQuality])

  const removeTrack = useCallback((id: string) => {
    setLibrary((value) => {
      const removedTrack = value.tracks.find((track) => track.id === id)
      const tracks = value.tracks.filter((track) => track.id !== id)
      const queue = value.queue.filter((trackId) => trackId !== id)
      const currentTrackId = value.currentTrackId === id ? queue[0] : value.currentTrackId

      if (removedTrack?.localHandleId) {
        void deleteLocalFileHandle(removedTrack.localHandleId).catch(() => undefined)
      }
      if (removedTrack?.lyricHandleId) {
        void deleteLocalFileHandle(removedTrack.lyricHandleId).catch(() => undefined)
      }
      sessionFilesRef.current.delete(id)
      sessionLyricsRef.current.delete(id)

      return {
        ...value,
        folders: removedTrack?.folderHandleId
          ? value.folders.map((folder) =>
              folder.id === removedTrack.folderHandleId
                ? { ...folder, trackCount: Math.max((folder.trackCount ?? 1) - 1, 0) }
                : folder,
            )
          : value.folders,
        tracks,
        queue,
        favoriteTrackIds: value.favoriteTrackIds.filter((trackId) => trackId !== id),
        recentTrackIds: value.recentTrackIds.filter((trackId) => trackId !== id),
        playlists: value.playlists.map((playlist) => ({ ...playlist, trackIds: playlist.trackIds.filter((trackId) => trackId !== id) })),
        currentTrackId,
      }
    })
    setStatusMessage('已删除音乐。')
  }, [])

  const removeRemoteTrack = useCallback(async (id: string) => {
    const track = remoteTracks.find((item) => item.id === id)
    if (!track?.remoteId) {
      throw new Error('只能删除服务器远程歌曲。')
    }
    await deleteRemoteMusicTrack(track.remoteId)
    setRemoteTracks((value) => value.filter((item) => item.id !== id))
    setLibrary((value) => {
      const queue = value.queue.filter((trackID) => trackID !== id)
      return {
        ...value,
        queue,
        favoriteTrackIds: value.favoriteTrackIds.filter((trackId) => trackId !== id),
        recentTrackIds: value.recentTrackIds.filter((trackId) => trackId !== id),
        playlists: value.playlists.map((playlist) => ({ ...playlist, trackIds: playlist.trackIds.filter((trackId) => trackId !== id) })),
        currentTrackId: value.currentTrackId === id ? queue[0] : value.currentTrackId,
      }
    })
    sessionLyricsRef.current.delete(id)
    setStatusMessage(`已从远程曲库删除《${track.title}》。`)
  }, [remoteTracks])

  const playTrack = useCallback(
    (id: string) => {
      pendingPlayRef.current = true
      setLibrary((value) => ({
        ...value,
        queue: value.queue.includes(id) ? value.queue : [id, ...value.queue],
        recentTrackIds: [id, ...value.recentTrackIds.filter((trackId) => trackId !== id)].slice(0, 50),
        currentTrackId: id,
      }))

      if (library.currentTrackId === id) {
        requestAudioPlay()
      }
    },
    [library.currentTrackId, requestAudioPlay],
  )

  const enqueueTrack = useCallback((id: string) => {
    setLibrary((value) => ({
      ...value,
      queue: value.queue.includes(id) ? value.queue : [...value.queue, id],
    }))
    setStatusMessage('已加入播放队列。')
  }, [])

  const removeFromQueue = useCallback((id: string) => {
    setLibrary((value) => {
      const queue = value.queue.filter((trackId) => trackId !== id)
      const currentTrackId = value.currentTrackId === id ? queue[0] : value.currentTrackId
      return {
        ...value,
        queue,
        currentTrackId,
      }
    })
    setStatusMessage('已从播放队列移除。')
  }, [])

  const clearQueue = useCallback(() => {
    setLibrary((value) => ({
      ...value,
      queue: value.currentTrackId ? [value.currentTrackId] : [],
    }))
    setStatusMessage('已清空待播队列。')
  }, [])

  const moveQueueItem = useCallback((id: string, direction: 'up' | 'down') => {
    setLibrary((value) => {
      const index = value.queue.indexOf(id)
      if (index < 0) {
        return value
      }

      const nextIndex = direction === 'up' ? index - 1 : index + 1
      if (nextIndex < 0 || nextIndex >= value.queue.length) {
        return value
      }

      const queue = [...value.queue]
      const [item] = queue.splice(index, 1)
      queue.splice(nextIndex, 0, item)
      return { ...value, queue }
    })
  }, [])

  const playNext = useCallback(() => {
    const nextTrackId = getNextTrackId(library)
    if (!nextTrackId) {
      pendingPlayRef.current = false
      setIsPlaying(false)
      return
    }

    pendingPlayRef.current = true
    if (nextTrackId === library.currentTrackId) {
      if (audioRef.current) {
        audioRef.current.currentTime = 0
      }
      requestAudioPlay()
      return
    }

    setLibrary((value) => ({ ...value, currentTrackId: nextTrackId }))
  }, [library, requestAudioPlay])

  const playPrevious = useCallback(() => {
    const previousTrackId = getPreviousTrackId(library)
    if (!previousTrackId) {
      return
    }

    pendingPlayRef.current = true
    setLibrary((value) => ({ ...value, currentTrackId: previousTrackId }))
    if (previousTrackId === library.currentTrackId) {
      requestAudioPlay()
    }
  }, [library, requestAudioPlay])

  const togglePlay = useCallback(() => {
    if (isPlaying) {
      pendingPlayRef.current = false
      audioRef.current?.pause()
      setIsPlaying(false)
      return
    }

    if (!library.currentTrackId && library.queue[0]) {
      pendingPlayRef.current = true
      setLibrary((value) => ({ ...value, currentTrackId: value.queue[0] }))
      return
    }

    if (!library.currentTrackId && library.tracks[0]) {
      pendingPlayRef.current = true
      setLibrary((value) => ({ ...value, currentTrackId: value.tracks[0].id, queue: value.queue.length ? value.queue : value.tracks.map((track) => track.id) }))
      return
    }

    pendingPlayRef.current = true
    requestAudioPlay()
  }, [isPlaying, library.currentTrackId, library.queue, library.tracks, requestAudioPlay])

  const seek = useCallback((time: number) => {
    const audio = audioRef.current
    if (!audio || !Number.isFinite(time)) {
      return
    }

    audio.currentTime = Math.min(Math.max(time, 0), Number.isFinite(audio.duration) ? audio.duration : time)
    setCurrentTime(audio.currentTime)
  }, [])

  const setVolume = useCallback((volume: number) => {
    setLibrary((value) => ({
      ...value,
      volume: Math.min(1, Math.max(0, volume)),
    }))
  }, [])

  const setMode = useCallback((mode: PlaybackMode) => {
    setLibrary((value) => ({ ...value, mode }))
  }, [])

  const toggleFavorite = useCallback((id: string) => {
    setLibrary((value) => ({
      ...value,
      favoriteTrackIds: value.favoriteTrackIds.includes(id)
        ? value.favoriteTrackIds.filter((trackId) => trackId !== id)
        : [id, ...value.favoriteTrackIds],
    }))
  }, [])

  const createPlaylist = useCallback((name: string) => {
    const id = createMusicId('playlist')
    setLibrary((value) => ({
      ...value,
      playlists: [...value.playlists, { id, name: name.trim() || '未命名歌单', trackIds: [], createdAt: new Date().toISOString() }],
    }))
    return id
  }, [])

  const addTrackToPlaylist = useCallback((trackId: string, playlistId: string) => {
    setLibrary((value) => ({
      ...value,
      playlists: value.playlists.map((playlist) => playlist.id === playlistId
        ? { ...playlist, trackIds: playlist.trackIds.includes(trackId) ? playlist.trackIds : [...playlist.trackIds, trackId] }
        : playlist),
    }))
    setStatusMessage('已加入歌单。')
  }, [])

  const deletePlaylist = useCallback((playlistId: string) => {
    setLibrary((value) => ({ ...value, playlists: value.playlists.filter((playlist) => playlist.id !== playlistId) }))
  }, [])

  const openQueue = useCallback(() => setIsQueueOpen(true), [])
  const closeQueue = useCallback(() => setIsQueueOpen(false), [])

  const playFromExternalControl = useCallback(() => {
    if (!isPlaying) togglePlay()
  }, [isPlaying, togglePlay])

  const pauseFromExternalControl = useCallback(() => {
    if (isPlaying) togglePlay()
  }, [isPlaying, togglePlay])

  useMediaSession({
    currentTime,
    duration,
    isPlaying,
    onNext: playNext,
    onPause: pauseFromExternalControl,
    onPlay: playFromExternalControl,
    onPrevious: playPrevious,
    onSeek: seek,
    track: currentTrack,
  })

  usePlaybackShortcuts({
    currentTime,
    onSeek: seek,
    onSetVolume: setVolume,
    onTogglePlay: togglePlay,
    volume: library.volume,
  })

  const handleEnded = () => {
    if (library.mode === 'repeat-one' && audioRef.current) {
      audioRef.current.currentTime = 0
      pendingPlayRef.current = true
      requestAudioPlay()
      return
    }

    const nextTrackId = getNextTrackId(library)
    if (nextTrackId) {
      pendingPlayRef.current = true
      if (nextTrackId === library.currentTrackId) {
        if (audioRef.current) {
          audioRef.current.currentTime = 0
        }
        requestAudioPlay()
        return
      }

      setLibrary((value) => ({ ...value, currentTrackId: nextTrackId }))
    } else {
      pendingPlayRef.current = false
      setIsPlaying(false)
    }
  }

  const handleLoadedMetadata = () => {
    const audio = audioRef.current
    if (!audio) {
      return
    }

    const nextDuration = Number.isFinite(audio.duration) ? audio.duration : 0
    setDuration(nextDuration)

    if (currentTrack && nextDuration > 0) {
      setLibrary((value) => ({
        ...value,
        tracks: value.tracks.map((track) =>
          track.id === currentTrack.id
            ? {
                ...track,
                duration: nextDuration,
                audioQuality: mergeDurationIntoQuality(track.audioQuality, nextDuration),
              }
            : track,
        ),
      }))
    }
  }

  const handleAudioError = () => {
    pendingPlayRef.current = false
    setIsPlaying(false)
    setStatusMessage('音频加载失败，请检查 URL、CORS、Range 请求或本地文件权限。')
  }

  const value = useMemo<MusicPlayerContextValue>(
    () => ({
      tracks,
      folders: library.folders,
      queue: library.queue,
      currentTrack,
      currentTrackId: library.currentTrackId,
      currentTime,
      duration,
      lyrics,
      currentLyricLine,
      currentLyricIndex,
      lyricStatusMessage,
      isPlaying,
      volume: library.volume,
      mode: library.mode,
      statusMessage,
      remoteTracksLoading,
      remoteTracksHasMore: !remoteTracksInitialized || Boolean(remoteCursor),
      uploadingTrackIds,
      favoriteTrackIds: library.favoriteTrackIds,
      recentTrackIds: library.recentTrackIds,
      playlists: library.playlists,
      addRemoteTrack,
      addLocalFileHandles,
      addLocalFiles,
      uploadLocalTrack,
      loadMoreRemoteTracks,
      scanLocalDirectory,
      rescanLocalFolder,
      removeLocalFolder,
      updateTrack,
      removeTrack,
      removeRemoteTrack,
      playTrack,
      enqueueTrack,
      removeFromQueue,
      clearQueue,
      moveQueueItem,
      togglePlay,
      playNext,
      playPrevious,
      seek,
      setVolume,
      setMode,
      toggleFavorite,
      createPlaylist,
      addTrackToPlaylist,
      deletePlaylist,
      openQueue,
      closeQueue,
      isQueueOpen,
      persistentLocalFilesSupported: supportsPersistentLocalFiles(),
      persistentMusicFoldersSupported: supportsPersistentMusicFolders(),
    }),
    [
      addLocalFileHandles,
      addLocalFiles,
      addRemoteTrack,
      addTrackToPlaylist,
      clearQueue,
      closeQueue,
      createPlaylist,
      currentTime,
      currentLyricIndex,
      currentLyricLine,
      currentTrack,
      deletePlaylist,
      duration,
      enqueueTrack,
      isPlaying,
      isQueueOpen,
      library.currentTrackId,
      library.favoriteTrackIds,
      library.folders,
      library.mode,
      library.playlists,
      library.queue,
      library.recentTrackIds,
      library.volume,
      lyricStatusMessage,
      lyrics,
      loadMoreRemoteTracks,
      moveQueueItem,
      openQueue,
      playNext,
      playPrevious,
      playTrack,
      removeLocalFolder,
      removeFromQueue,
      removeRemoteTrack,
      removeTrack,
      rescanLocalFolder,
      scanLocalDirectory,
      seek,
      setMode,
      setVolume,
      statusMessage,
      remoteCursor,
      remoteTracksInitialized,
      remoteTracksLoading,
      tracks,
      uploadLocalTrack,
      uploadingTrackIds,
      togglePlay,
      toggleFavorite,
      updateTrack,
    ],
  )

  return (
    <MusicPlayerContext.Provider value={value}>
      {children}
      <audio
        onEnded={handleEnded}
        onError={handleAudioError}
        onLoadedMetadata={handleLoadedMetadata}
        onPause={() => setIsPlaying(false)}
        onPlay={() => setIsPlaying(true)}
        onTimeUpdate={(event) => setCurrentTime(event.currentTarget.currentTime)}
        ref={audioRef}
        src={audioSrc ?? undefined}
      />
    </MusicPlayerContext.Provider>
  )
}

export { MusicMiniPlayer, MusicQueueDrawer } from './components/PlaybackSurfaces'
