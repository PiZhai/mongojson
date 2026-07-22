import { parseBlob } from 'music-metadata'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { classifyVideoAudioCodec, inspectVideoAudioCompatibility } from './mediaCompatibility'

vi.mock('music-metadata', () => ({ parseBlob: vi.fn() }))

const parseBlobMock = vi.mocked(parseBlob)

describe('video audio compatibility', () => {
  beforeEach(() => parseBlobMock.mockReset())

  it.each(['AC-3', 'AC3', 'E-AC-3', 'EAC3', 'EC-3', 'DTS', 'DTS-HD MA', 'TrueHD', 'Dolby TrueHD'])(
    'marks %s as unsupported by the browser player',
    (codec) => {
      expect(classifyVideoAudioCodec(codec)).toEqual({ audioCodec: codec, unsupported: true })
    },
  )

  it.each(['AAC', 'Opus', 'MP3', 'FLAC', undefined])('does not reject %s', (codec) => {
    expect(classifyVideoAudioCodec(codec).unsupported).toBe(false)
  })

  it('reads the audio codec from the selected video', async () => {
    parseBlobMock.mockResolvedValue({ format: { codec: 'AC-3' } } as never)

    const result = await inspectVideoAudioCompatibility(new File([new Uint8Array(8)], 'episode.mp4'))

    expect(result).toEqual({ audioCodec: 'AC-3', unsupported: true })
    expect(parseBlobMock).toHaveBeenCalledWith(expect.any(File), { duration: false, skipCovers: true })
  })

  it('keeps video selection usable when metadata inspection fails', async () => {
    parseBlobMock.mockResolvedValue(undefined as never)

    await expect(inspectVideoAudioCompatibility(new File([], 'episode.mkv'))).resolves.toEqual({
      unsupported: false,
    })
  })
})
