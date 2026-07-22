import { parseBlob } from 'music-metadata'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { analyzeAudioFile, MAX_MUSIC_ARTWORK_BYTES } from './audioQuality'

vi.mock('music-metadata', () => ({ parseBlob: vi.fn() }))

const parseBlobMock = vi.mocked(parseBlob)

function metadataWithPicture(format: string, size: number) {
  return {
    format: { container: 'MP3', duration: 120, lossless: false },
    common: { picture: [{ format, data: new Uint8Array(size) }] },
    native: {},
    quality: { warnings: [] },
  }
}

describe('analyzeAudioFile artwork', () => {
  beforeEach(() => parseBlobMock.mockReset())

  it.each([
    ['image/jpeg', 'image/jpeg'],
    ['image/jpg', 'image/jpeg'],
    ['image/png', 'image/png'],
    ['image/webp', 'image/webp'],
  ])('keeps supported embedded artwork %s', async (format, expected) => {
    parseBlobMock.mockResolvedValue(metadataWithPicture(format, 16) as never)

    const result = await analyzeAudioFile(new File([new Uint8Array(32)], 'song.mp3', { type: 'audio/mpeg' }))

    expect(result.artwork?.mimeType).toBe(expected)
    expect(result.artwork?.blob.size).toBe(16)
  })

  it('silently falls back when artwork is unsupported or oversized', async () => {
    parseBlobMock.mockResolvedValue(metadataWithPicture('image/gif', 16) as never)
    const unsupported = await analyzeAudioFile(new File([new Uint8Array(32)], 'song.mp3'))
    expect(unsupported.artwork).toBeUndefined()

    parseBlobMock.mockResolvedValue(metadataWithPicture('image/png', MAX_MUSIC_ARTWORK_BYTES + 1) as never)
    const oversized = await analyzeAudioFile(new File([new Uint8Array(32)], 'song.mp3'))
    expect(oversized.artwork).toBeUndefined()
  })
})
