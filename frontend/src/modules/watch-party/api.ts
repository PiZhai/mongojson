import { resolveApiUrl } from '../../platform/http/client'

export function getWatchRoomWebSocketUrl(roomId: string, clientId: string) {
  const url = resolveApiUrl(`/watch/rooms/${encodeURIComponent(roomId)}/ws`)
  url.searchParams.set('client_id', clientId)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}
