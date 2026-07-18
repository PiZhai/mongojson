const managementSessionTokens = new Map()

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('message', (event) => {
  const message = event.data
  const clientId = event.source?.id
  if (message?.type === 'steward-management-session' && clientId) {
    const token = typeof message.token === 'string' ? message.token : ''
    if (token) managementSessionTokens.set(clientId, token)
    else managementSessionTokens.delete(clientId)
  }
})

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url)
  const clientId = event.clientId || event.resultingClientId
  const managementSessionToken = managementSessionTokens.get(clientId) ?? ''
  if (!managementSessionToken || url.origin !== self.location.origin || !url.pathname.startsWith('/api/')) return

  event.respondWith((async () => {
    const headers = new Headers(event.request.headers)
    if (!headers.has('Authorization')) {
      headers.set('Authorization', `Bearer ${managementSessionToken}`)
    }
    const authenticatedRequest = event.request.mode === 'no-cors' && ['GET', 'HEAD'].includes(event.request.method)
      ? new Request(event.request.url, {
          method: event.request.method,
          headers,
          mode: 'same-origin',
          credentials: 'same-origin',
          cache: event.request.cache,
          redirect: event.request.redirect,
          referrer: event.request.referrer,
          referrerPolicy: event.request.referrerPolicy,
          integrity: event.request.integrity,
        })
      : new Request(event.request, { headers })
    const response = await fetch(authenticatedRequest)
    if (response.status === 401) {
      managementSessionTokens.delete(clientId)
      const client = await self.clients.get(clientId)
      client?.postMessage({ type: 'steward-management-unauthorized' })
    }
    return response
  })())
})
