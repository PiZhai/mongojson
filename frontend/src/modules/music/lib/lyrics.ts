import type { LrcLine } from '../MusicPlayerContext'

export function parseLrcTimestamp(value: string) {
  const match = value.match(/^(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?$/)
  if (!match) {
    return null
  }

  const minutes = Number(match[1])
  const seconds = Number(match[2])
  const fraction = match[3] ?? '0'
  if (!Number.isFinite(minutes) || !Number.isFinite(seconds)) {
    return null
  }

  return minutes * 60 + seconds + Number(fraction.padEnd(3, '0').slice(0, 3)) / 1000
}

export function parseLrcText(value: string): LrcLine[] {
  const lines: LrcLine[] = []

  for (const rawLine of value.replace(/^\uFEFF/, '').split(/\r?\n/)) {
    const matches = Array.from(rawLine.matchAll(/\[(\d{1,3}:\d{2}(?:[.:]\d{1,3})?)\]/g))
    if (matches.length === 0) {
      continue
    }

    const text = rawLine.replace(/\[[^\]]+\]/g, '').trim()
    if (!text) {
      continue
    }

    for (const match of matches) {
      const time = parseLrcTimestamp(match[1])
      if (time !== null) {
        lines.push({ time, text })
      }
    }
  }

  return lines.sort((left, right) => left.time - right.time)
}

export function countEncodingArtifacts(value: string) {
  const replacementCount = (value.match(/\uFFFD/g) ?? []).length
  const mojibakeCount = (value.match(/锟斤拷|ï»¿|â€|â€™|Ã./g) ?? []).length
  return replacementCount * 4 + mojibakeCount * 3
}

export function decodeLyricBuffer(buffer: ArrayBuffer) {
  const bytes = new Uint8Array(buffer)

  if (bytes.length >= 3 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) {
    return new TextDecoder('utf-8').decode(bytes.subarray(3))
  }

  if (bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xfe) {
    return new TextDecoder('utf-16le').decode(bytes.subarray(2))
  }

  if (bytes.length >= 2 && bytes[0] === 0xfe && bytes[1] === 0xff) {
    return new TextDecoder('utf-16be').decode(bytes.subarray(2))
  }

  const candidates: string[] = []
  try {
    candidates.push(new TextDecoder('utf-8', { fatal: true }).decode(bytes))
  } catch {
    // GBK/GB18030 lyrics often fail strict UTF-8 decoding.
  }

  for (const encoding of ['gb18030', 'gbk', 'utf-8']) {
    try {
      const decoded = new TextDecoder(encoding).decode(bytes)
      if (!candidates.includes(decoded)) {
        candidates.push(decoded)
      }
    } catch {
      // Some browsers may not expose every legacy label.
    }
  }

  return candidates
    .filter(Boolean)
    .sort((left, right) => countEncodingArtifacts(left) - countEncodingArtifacts(right))[0] ?? ''
}

export async function readLyricFileText(file: File) {
  return decodeLyricBuffer(await file.arrayBuffer())
}
