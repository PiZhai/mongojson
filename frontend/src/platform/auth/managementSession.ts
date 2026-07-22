export function createManagementWebSocket(url: string | URL) {
  return new WebSocket(url)
}
