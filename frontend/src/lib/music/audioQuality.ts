import type { IAudioMetadata } from 'music-metadata'
import type { MusicAudioQuality } from '../../types/tooling'

type AudioQualityInput = {
  fileName?: string
  mimeType?: string
  fileSize?: number
}

const AUDIO_EXTENSION_LABELS: Record<string, string> = {
  aac: 'AAC',
  flac: 'FLAC',
  m4a: 'M4A',
  mp3: 'MP3',
  ogg: 'OGG',
  opus: 'Opus',
  wav: 'WAV',
  webm: 'WebM',
}

function nowIso() {
  return new Date().toISOString()
}

function getExtension(fileName?: string) {
  return fileName?.match(/\.([a-z0-9]+)$/i)?.[1]?.toLowerCase()
}

function getContainerFromInput(input: AudioQualityInput) {
  const extension = getExtension(input.fileName)
  if (extension && AUDIO_EXTENSION_LABELS[extension]) {
    return AUDIO_EXTENSION_LABELS[extension]
  }

  if (input.mimeType?.startsWith('audio/')) {
    return input.mimeType.replace(/^audio\//, '').toUpperCase()
  }

  return undefined
}

function getKnownLossless(container?: string) {
  if (!container) {
    return undefined
  }

  if (/FLAC|WAV|AIFF|ALAC/i.test(container)) {
    return true
  }

  if (/MP3|AAC|OGG|OPUS|WEBM|M4A/i.test(container)) {
    return false
  }

  return undefined
}

function normalizeMetadata(metadata: IAudioMetadata, input: AudioQualityInput): MusicAudioQuality {
  const container = metadata.format.container || getContainerFromInput(input)
  const lossless = metadata.format.lossless ?? getKnownLossless(container)

  return {
    container,
    codec: metadata.format.codec,
    bitrate: metadata.format.bitrate,
    sampleRate: metadata.format.sampleRate,
    bitsPerSample: metadata.format.bitsPerSample,
    numberOfChannels: metadata.format.numberOfChannels,
    lossless,
    duration: metadata.format.duration,
    fileSize: input.fileSize,
    analyzedAt: nowIso(),
    analysisSource: 'metadata',
  }
}

export function inferAudioQuality(input: AudioQualityInput, error?: string): MusicAudioQuality {
  const container = getContainerFromInput(input)

  return {
    container,
    fileSize: input.fileSize,
    lossless: getKnownLossless(container),
    analyzedAt: nowIso(),
    analysisSource: 'inferred',
    error,
  }
}

export async function analyzeAudioFileQuality(file: File): Promise<MusicAudioQuality> {
  try {
    const { parseBlob } = await import('music-metadata')
    const metadata = await parseBlob(file, { duration: true, skipCovers: true })
    return normalizeMetadata(metadata, {
      fileName: file.name,
      fileSize: file.size,
      mimeType: file.type,
    })
  } catch (error) {
    return inferAudioQuality(
      {
        fileName: file.name,
        fileSize: file.size,
        mimeType: file.type,
      },
      error instanceof Error ? error.message : '无法解析音频元数据。',
    )
  }
}

export async function analyzeRemoteAudioQuality(remoteUrl: string): Promise<MusicAudioQuality> {
  try {
    const response = await fetch(remoteUrl)
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`)
    }

    if (!response.body) {
      throw new Error('远程响应没有可读取的音频流。')
    }

    const contentLength = response.headers.get('Content-Length')
    const contentType = response.headers.get('Content-Type') || undefined
    const size = contentLength ? Number.parseInt(contentLength, 10) : undefined
    const { parseWebStream } = await import('music-metadata')
    const metadata = await parseWebStream(
      response.body,
      {
        mimeType: contentType,
        size: Number.isFinite(size) ? size : undefined,
        url: remoteUrl,
      },
      { duration: true, skipCovers: true },
    )

    return normalizeMetadata(metadata, {
      fileName: remoteUrl,
      fileSize: Number.isFinite(size) ? size : undefined,
      mimeType: contentType,
    })
  } catch (error) {
    return inferAudioQuality(
      {
        fileName: remoteUrl,
      },
      error instanceof Error ? error.message : '无法读取远程音频元数据。',
    )
  }
}

export function estimateBitrateFromSize(fileSize?: number, duration?: number) {
  if (!fileSize || !duration || duration <= 0) {
    return undefined
  }

  return Math.round((fileSize * 8) / duration)
}

export function mergeDurationIntoQuality(audioQuality: MusicAudioQuality | undefined, duration: number) {
  if (!audioQuality || duration <= 0) {
    return audioQuality
  }

  const bitrate = audioQuality.bitrate ?? estimateBitrateFromSize(audioQuality.fileSize, duration)

  return {
    ...audioQuality,
    bitrate,
    duration: audioQuality.duration ?? duration,
  }
}

function formatBitrate(value?: number) {
  if (!value || !Number.isFinite(value)) {
    return undefined
  }

  return `约 ${Math.round(value / 1000)} kbps`
}

function formatSampleRate(value?: number) {
  if (!value || !Number.isFinite(value)) {
    return undefined
  }

  return `${Number(value / 1000).toFixed(value % 1000 === 0 ? 0 : 1)} kHz`
}

export function summarizeAudioQuality(audioQuality?: MusicAudioQuality) {
  if (!audioQuality) {
    return '音质待识别'
  }

  const parts = [
    audioQuality.container || audioQuality.codec,
    audioQuality.lossless === true ? '无损' : audioQuality.lossless === false ? '有损' : undefined,
    formatSampleRate(audioQuality.sampleRate),
    audioQuality.bitsPerSample ? `${audioQuality.bitsPerSample}-bit` : undefined,
    audioQuality.numberOfChannels ? `${audioQuality.numberOfChannels}ch` : undefined,
    formatBitrate(audioQuality.bitrate),
  ].filter(Boolean)

  return parts.length > 0 ? parts.join(' · ') : '音质待识别'
}

export function compactAudioQualityLabel(audioQuality?: MusicAudioQuality) {
  if (!audioQuality) {
    return undefined
  }

  const parts = [
    audioQuality.container || audioQuality.codec,
    audioQuality.lossless === true ? '无损' : audioQuality.lossless === false ? '有损' : undefined,
    audioQuality.bitrate ? `${Math.round(audioQuality.bitrate / 1000)} kbps` : formatSampleRate(audioQuality.sampleRate),
  ].filter(Boolean)

  return parts.length > 0 ? parts.join(' · ') : undefined
}
