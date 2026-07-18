const managementWorkerPath = '/management-auth-sw.js'
const managementWorkerScope = '/'
const managementWorkerMessageType = 'steward-management-session'
const managementUnauthorizedMessageType = 'steward-management-unauthorized'
const unauthorizedEventName = 'steward:management-auth-unauthorized'

let managementSessionToken = ''
let workerRegistration: ServiceWorkerRegistration | null = null
let workerSetup: Promise<boolean> | null = null
let workerEventsInstalled = false

function postSessionToWorker(worker?: ServiceWorker | null) {
  worker?.postMessage({ type: managementWorkerMessageType, token: managementSessionToken })
}

function installWorkerEvents() {
  if (workerEventsInstalled || !('serviceWorker' in navigator)) return
  workerEventsInstalled = true
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    postSessionToWorker(navigator.serviceWorker.controller)
  })
  navigator.serviceWorker.addEventListener('message', (event: MessageEvent<unknown>) => {
    const message = event.data as { type?: string } | null
    if (message?.type !== managementUnauthorizedMessageType) return
    clearManagementSessionToken()
    window.dispatchEvent(new Event(unauthorizedEventName))
  })
}

export async function ensureManagementTransport() {
  if (!('serviceWorker' in navigator)) return false
  installWorkerEvents()
  if (!workerSetup) {
    workerSetup = navigator.serviceWorker
      .register(managementWorkerPath, { scope: managementWorkerScope })
      .then(async (registration) => {
        workerRegistration = registration
        await navigator.serviceWorker.ready
        postSessionToWorker(registration.active ?? registration.waiting ?? registration.installing)
        postSessionToWorker(navigator.serviceWorker.controller)
        return true
      })
      .catch(() => false)
  }
  const ready = await workerSetup
  if (ready) {
    postSessionToWorker(workerRegistration?.active)
    postSessionToWorker(navigator.serviceWorker.controller)
  }
  return ready
}

export function getManagementSessionToken() {
  return managementSessionToken
}

export async function setManagementSessionToken(token: string) {
  managementSessionToken = token.trim()
  return ensureManagementTransport()
}

export function clearManagementSessionToken() {
  managementSessionToken = ''
  postSessionToWorker(workerRegistration?.active)
  postSessionToWorker(navigator.serviceWorker.controller)
}

export function createManagementWebSocket(url: string | URL) {
  if (!managementSessionToken) return new WebSocket(url)
  return new WebSocket(url, [`steward-management.${managementSessionToken}`])
}

export { unauthorizedEventName }
