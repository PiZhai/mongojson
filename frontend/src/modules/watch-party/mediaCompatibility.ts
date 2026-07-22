const unsupportedBrowserAudioCodecs = [
  /^AC-?3$/i,
  /^(?:E-?AC-?3|EC-?3)$/i,
  /^DTS(?:[-\s].*)?$/i,
  /^(?:DOLBY\s+)?TRUEHD$/i,
]

export type VideoAudioCompatibility = {
  audioCodec?: string
  unsupported: boolean
}

export function classifyVideoAudioCodec(audioCodec?: string): VideoAudioCompatibility {
  const normalizedCodec = audioCodec?.trim()
  if (!normalizedCodec) {
    return { unsupported: false }
  }

  return {
    audioCodec: normalizedCodec,
    unsupported: unsupportedBrowserAudioCodecs.some((pattern) => pattern.test(normalizedCodec)),
  }
}

export async function inspectVideoAudioCompatibility(file: File): Promise<VideoAudioCompatibility> {
  try {
    const { parseBlob } = await import('music-metadata')
    const metadata = await parseBlob(file, { duration: false, skipCovers: true })
    return classifyVideoAudioCodec(metadata.format.codec)
  } catch {
    // Container inspection is best-effort. The browser remains the source of
    // truth for formats the metadata parser does not recognize.
    return { unsupported: false }
  }
}
