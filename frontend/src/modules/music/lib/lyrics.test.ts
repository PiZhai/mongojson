import { describe, expect, it } from 'vitest'
import { decodeLyricBuffer, parseLrcText, parseLrcTimestamp } from './lyrics'

describe('LRC parser', () => {
  it('parses timestamps with centiseconds and milliseconds', () => {
    expect(parseLrcTimestamp('01:02.34')).toBe(62.34)
    expect(parseLrcTimestamp('01:02:345')).toBe(62.345)
    expect(parseLrcTimestamp('invalid')).toBeNull()
  })

  it('expands multiple timestamps, ignores metadata, and sorts the result', () => {
    const lyrics = parseLrcText([
      '[ar:Artist]',
      '[00:10.00]Second',
      '[00:02.50][00:05.00]First',
      '[00:11.00]',
    ].join('\n'))

    expect(lyrics).toEqual([
      { time: 2.5, text: 'First' },
      { time: 5, text: 'First' },
      { time: 10, text: 'Second' },
    ])
  })

  it('decodes a UTF-8 BOM without leaking it into the lyrics', () => {
    const content = new TextEncoder().encode('[00:01.00]你好')
    const bytes = new Uint8Array(content.length + 3)
    bytes.set([0xef, 0xbb, 0xbf])
    bytes.set(content, 3)
    expect(decodeLyricBuffer(bytes.buffer)).toBe('[00:01.00]你好')
  })
})
