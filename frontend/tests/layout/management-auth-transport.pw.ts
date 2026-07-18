import { createServer, type Server } from 'node:http'
import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from '@playwright/test'

const transparentPixel = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+XwP/2QAAAABJRU5ErkJggg==',
  'base64',
)

let server: Server
let origin = ''
const observedAuthorization = new Map<string, string>()
const expectedAuthorization = new Map<string, string>()

test.beforeAll(async () => {
  const workerPath = join(fileURLToPath(new URL('../..', import.meta.url)), 'public', 'management-auth-sw.js')
  const workerSource = await readFile(workerPath)
  server = createServer((request, response) => {
    if (request.url === '/management-auth-sw.js') {
      response.writeHead(200, {
        'Content-Type': 'application/javascript',
        'Service-Worker-Allowed': '/',
        'Cache-Control': 'no-store',
      })
      response.end(workerSource)
      return
    }
    const requestURL = new URL(request.url ?? '/', 'http://127.0.0.1')
    if (requestURL.pathname === '/api/pixel') {
      const client = requestURL.searchParams.get('client') ?? 'default'
      const authorization = request.headers.authorization ?? ''
      observedAuthorization.set(client, authorization)
      if (authorization !== expectedAuthorization.get(client)) {
        response.writeHead(401)
        response.end()
        return
      }
      response.writeHead(200, { 'Content-Type': 'image/png', 'Cache-Control': 'no-store' })
      response.end(transparentPixel)
      return
    }
    response.writeHead(200, { 'Content-Type': 'text/html', 'Cache-Control': 'no-store' })
    response.end('<!doctype html><html><body></body></html>')
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('test HTTP server did not expose a TCP address')
  origin = `http://127.0.0.1:${address.port}`
})

test.afterAll(async () => {
  await new Promise<void>((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())))
})

test('service worker authenticates native no-cors resources without URL credentials', async ({ page }) => {
  observedAuthorization.clear()
  expectedAuthorization.set('default', 'Bearer browser-session-test')
  await page.goto(origin)
  const loaded = await page.evaluate(async () => {
    const registration = await navigator.serviceWorker.register('/management-auth-sw.js', { scope: '/' })
    await navigator.serviceWorker.ready
    if (!navigator.serviceWorker.controller) {
      await new Promise<void>((resolve) => navigator.serviceWorker.addEventListener('controllerchange', () => resolve(), { once: true }))
    }
    ;(registration.active ?? navigator.serviceWorker.controller)?.postMessage({
      type: 'steward-management-session',
      token: 'browser-session-test',
    })
    await new Promise((resolve) => setTimeout(resolve, 100))
    return new Promise<boolean>((resolve) => {
      const image = new Image()
      image.onload = () => resolve(true)
      image.onerror = () => resolve(false)
      image.src = '/api/pixel'
      document.body.append(image)
    })
  })

  expect(loaded).toBe(true)
  await expect.poll(() => observedAuthorization.get('default')).toBe('Bearer browser-session-test')
})

test('service worker keeps native-resource sessions isolated per browser tab', async ({ context }) => {
  observedAuthorization.clear()
  expectedAuthorization.set('first', 'Bearer first-tab-session')
  expectedAuthorization.set('second', 'Bearer second-tab-session')
  const first = await context.newPage()
  const second = await context.newPage()

  const prepare = async (page: typeof first, token: string) => {
    await page.goto(origin)
    await page.evaluate(async (sessionToken) => {
      const registration = await navigator.serviceWorker.register('/management-auth-sw.js', { scope: '/' })
      await navigator.serviceWorker.ready
      if (!navigator.serviceWorker.controller) {
        await new Promise<void>((resolve) => navigator.serviceWorker.addEventListener('controllerchange', () => resolve(), { once: true }))
      }
      ;(registration.active ?? navigator.serviceWorker.controller)?.postMessage({
        type: 'steward-management-session',
        token: sessionToken,
      })
      await new Promise((resolve) => setTimeout(resolve, 100))
    }, token)
  }

  await prepare(first, 'first-tab-session')
  await prepare(second, 'second-tab-session')
  const load = (page: typeof first, client: string) => page.evaluate((key) => new Promise<boolean>((resolve) => {
    const image = new Image()
    image.onload = () => resolve(true)
    image.onerror = () => resolve(false)
    image.src = `/api/pixel?client=${encodeURIComponent(key)}`
    document.body.append(image)
  }), client)

  expect(await Promise.all([load(first, 'first'), load(second, 'second')])).toEqual([true, true])
  await expect.poll(() => observedAuthorization.get('first')).toBe('Bearer first-tab-session')
  await expect.poll(() => observedAuthorization.get('second')).toBe('Bearer second-tab-session')
})
