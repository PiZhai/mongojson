import { describe, expect, it } from 'vitest'
import { getGeneratedMusicArtworkUrl, getMusicArtworkPalette } from './artwork'

describe('generated music artwork', () => {
  it('is deterministic for the same title and artist', () => {
    expect(getMusicArtworkPalette('Midnight', 'Echo')).toEqual(getMusicArtworkPalette('Midnight', 'Echo'))
    expect(getGeneratedMusicArtworkUrl('Midnight', 'Echo')).toBe(getGeneratedMusicArtworkUrl('Midnight', 'Echo'))
  })

  it('escapes user text before embedding it in SVG', () => {
    const url = decodeURIComponent(getGeneratedMusicArtworkUrl('<script>', 'Echo'))
    expect(url).toContain('&lt;script&gt;')
    expect(url).not.toContain('<script>')
  })
})
