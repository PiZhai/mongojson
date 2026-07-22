export function getMusicArtworkPalette(title?: string, artist?: string) {
  const seed = `${title || 'music'}:${artist || ''}`
  let hash = 0
  for (const character of seed) hash = character.charCodeAt(0) + ((hash << 5) - hash)
  const hue = Math.abs(hash) % 360
  return { hue, alternateHue: (hue + 58) % 360 }
}

export function getGeneratedMusicArtworkUrl(title?: string, artist?: string) {
  const { hue, alternateHue } = getMusicArtworkPalette(title, artist)
  const safeTitle = (title || 'Music').slice(0, 24)
  const escapedTitle = safeTitle.replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;' })[character] || character)
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop stop-color="hsl(${hue} 72% 56%)"/><stop offset="1" stop-color="hsl(${alternateHue} 70% 34%)"/></linearGradient></defs><rect width="512" height="512" rx="72" fill="url(#g)"/><circle cx="390" cy="122" r="150" fill="white" opacity=".08"/><text x="52" y="390" fill="white" font-family="system-ui,sans-serif" font-size="42" font-weight="700">${escapedTitle}</text><text x="52" y="448" fill="white" opacity=".72" font-family="system-ui,sans-serif" font-size="24">${artist ? 'MIDNIGHT LOUNGE' : 'PERSONAL MUSIC'}</text></svg>`
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`
}
