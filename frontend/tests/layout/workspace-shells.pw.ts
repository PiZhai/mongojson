import { expect, test, type Page } from '@playwright/test'

const memoDocument = {
  id: '11111111-1111-4111-8111-111111111111',
  slug: 'layout-test',
  title: '布局验证文档',
  content_json: [
    { id: 'heading-1', type: 'heading', props: { level: 1 }, content: [{ type: 'text', text: '布局验证', styles: {} }], children: [] },
    { id: 'paragraph-1', type: 'paragraph', props: {}, content: [{ type: 'text', text: '正文内容', styles: {} }], children: [] },
  ],
  content_html: '<h1>布局验证</h1><p>正文内容</p>',
  content_text: '# 布局验证\n\n正文内容',
  floating_cards: [],
  schema_version: 1,
  revision: 1,
  editor_type: 'blocknote',
  created_at: '2026-07-12T00:00:00Z',
  updated_at: '2026-07-12T00:00:00Z',
}

async function mockWorkspaceApi(page: Page) {
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname

    if (path === '/api/auth/session') {
      await route.fulfill({ json: { required: true, authenticated: true } })
      return
    }
    if (path === '/api/steward/background/status') {
      await route.fulfill({
        json: {
          status: {
            state: 'healthy',
            pipeline: { enabled: true, sources: [] },
            model: { enabled: true, circuit_open: false, consecutive_failures: 0, model: '测试模型' },
          },
        },
      })
      return
    }
    if (path === '/api/steward/conversations') {
      await route.fulfill({ json: { conversations: [] } })
      return
    }
    if (path === '/api/music/tracks') {
      await route.fulfill({ json: { tracks: [] } })
      return
    }
    if (path === '/api/memo/documents/layout-test') {
      await route.fulfill({ json: { document: memoDocument } })
      return
    }
    if (path === '/api/memo/documents') {
      await route.fulfill({ json: { documents: [{ ...memoDocument, note_count: 0 }] } })
      return
    }
    if (path === `/api/memo/documents/${memoDocument.id}/notes`) {
      await route.fulfill({ json: { notes: [] } })
      return
    }

    await route.fulfill({ json: {} })
  })
}

async function expectNoRootOverflow(page: Page) {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }))
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth + 1)
}

test.beforeEach(async ({ page }) => {
  await mockWorkspaceApi(page)
})

test('root defaults to Steward and launcher supports keyboard navigation', async ({ page }) => {
  await page.goto('/')
  await expect(page).toHaveURL(/\/steward$/)
  await expect(page.locator('[data-workspace="steward"]')).toBeVisible()

  const launcher = page.getByRole('button', { name: '切换工作区' })
  await launcher.focus()
  await page.keyboard.press('Control+K')
  await expect(page.getByRole('menu', { name: '工作区' })).toBeVisible()
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/tools\/inspect$/)
  await expect(page.locator('[data-workspace="tools"]')).toBeVisible()

  await page.keyboard.press('Control+K')
  await page.keyboard.press('Escape')
  await expect(launcher).toBeFocused()
})

test('legacy workspace routes preserve query and hash', async ({ page }) => {
  const redirects = [
    { from: '/tools/steward?source=legacy#conversation', to: '/steward?source=legacy#conversation', workspace: 'steward' },
    { from: '/tools/memo-docs?slug=layout-test#editor', to: '/documents/memo?slug=layout-test#editor', workspace: 'documents' },
    { from: '/tools/canvas?source=legacy#board', to: '/documents/canvas?source=legacy#board', workspace: 'documents' },
    { from: '/tools/music?source=legacy#queue', to: '/entertainment/music?source=legacy#queue', workspace: 'entertainment' },
    { from: '/tools/watch-party?source=legacy#room', to: '/entertainment/watch?source=legacy#room', workspace: 'entertainment' },
  ] as const

  for (const redirect of redirects) {
    await page.goto(redirect.from)
    await expect.poll(() => page.evaluate(() => `${location.pathname}${location.search}${location.hash}`)).toBe(redirect.to)
    await expect(page.locator(`[data-workspace="${redirect.workspace}"]`)).toBeVisible()
  }
})

for (const viewport of [
  { width: 375, height: 812 },
  { width: 768, height: 1024 },
  { width: 1024, height: 768 },
  { width: 1440, height: 900 },
]) {
  test(`four workspaces stay bounded at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize(viewport)
    const cases = [
      { path: '/steward', workspace: 'steward' },
      { path: '/tools/inspect', workspace: 'tools' },
      { path: '/documents/memo?slug=layout-test', workspace: 'documents' },
      { path: '/entertainment/music', workspace: 'entertainment' },
    ] as const

    for (const item of cases) {
      await page.goto(item.path)
      await expect(page.locator(`[data-workspace="${item.workspace}"]`)).toBeVisible()
      await expectNoRootOverflow(page)
    }
  })
}

test('Steward keeps operational details in the optional inspector', async ({ page }) => {
  await page.goto('/steward')
  await expect(page.getByText('暂无对话')).toBeVisible()
  await expect(page.getByText('执行计划')).toHaveCount(0)

  const trigger = page.getByRole('button', { name: '状态与设置' })
  await trigger.click()
  await expect(page.getByRole('dialog', { name: '管家状态与设置' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '管家状态与设置' })).toHaveCount(0)
  await expect(trigger).toBeFocused()
})

test('documents pin the library on desktop and use a drawer on compact screens', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/documents/memo?slug=layout-test')
  const library = page.getByRole('complementary', { name: '文档库' })
  await expect(library).toBeVisible()
  await expect(library).toHaveCSS('position', 'relative')
  await expect(page.getByRole('searchbox', { name: '搜索文档' })).toBeVisible()

  await page.setViewportSize({ width: 1024, height: 768 })
  await expect(library).toBeHidden()
  await page.getByRole('button', { name: '展开文档库' }).click()
  await expect(library).toBeVisible()
  await expect(library).toHaveCSS('position', 'absolute')
})

for (const viewport of [
  { width: 375, height: 812 },
  { width: 768, height: 1024 },
  { width: 1024, height: 768 },
  { width: 1440, height: 900 },
]) {
  test(`entertainment layouts adapt without root overflow at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize(viewport)

    await page.goto('/entertainment/music')
    await expect(page.getByRole('heading', { name: '曲库' })).toBeVisible()
    await expectNoRootOverflow(page)
    await expect(page.locator('.music-collection-rail')).toHaveCSS('display', viewport.width >= 1024 ? 'flex' : 'none')
    await expect(page.locator('.music-now-desktop')).toHaveCSS('display', viewport.width >= 1280 ? 'block' : 'none')

    await page.goto('/entertainment/watch')
    await expect(page.getByRole('heading', { name: '把同一刻留在屏幕上' })).toBeVisible()
    await expectNoRootOverflow(page)
    await expect(page.locator('.watch-context-desktop')).toHaveCSS('display', viewport.width >= 1200 ? 'block' : 'none')
  })
}

test('entertainment Base UI drawers trap and restore focus', async ({ page }) => {
  await page.setViewportSize({ width: 768, height: 1024 })
  await page.goto('/entertainment/music')
  const nowPlayingTrigger = page.getByRole('button', { name: '打开正在播放' })
  await nowPlayingTrigger.click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(nowPlayingTrigger).toBeFocused()

  await page.goto('/entertainment/watch')
  const roomStatusTrigger = page.getByRole('button', { name: '房间状态' })
  await roomStatusTrigger.click()
  await expect(page.getByRole('dialog', { name: '房间状态' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '房间状态' })).toHaveCount(0)
  await expect(roomStatusTrigger).toBeFocused()
})

test('watch controls reserve Space activation for Tab focus', async ({ page }) => {
  await page.goto('/entertainment/watch')
  await page.locator("input[type='file']").first().setInputFiles({
    name: 'focus-test.mp4',
    mimeType: 'video/mp4',
    buffer: Buffer.from([0, 0, 0, 24, 102, 116, 121, 112, 105, 115, 111, 109]),
  })

  const controls = page.locator('.watch-video-controls')
  await expect(controls).toBeAttached()

  const volumeTrigger = page.locator('.watch-video-volume-control > .watch-video-icon-button')
  await volumeTrigger.click({ force: true })
  await expect(volumeTrigger).toHaveAttribute('aria-expanded', 'true')
  await expect(volumeTrigger).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await expect(volumeTrigger).toHaveCSS('outline-style', 'none')
  await expect(page.locator('.watch-video-volume-panel')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')

  await page.keyboard.press('Space')
  await expect(volumeTrigger).toHaveAttribute('aria-expanded', 'true')

  await volumeTrigger.click({ force: true })
  await expect(volumeTrigger).toHaveAttribute('aria-expanded', 'false')
  const playbackRateTrigger = page.getByRole('button', { name: /播放倍速/ })
  await playbackRateTrigger.click({ force: true })
  await expect(page.locator('.watch-rate-menu')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await expect(page.locator('.watch-rate-menu')).toHaveCSS('outline-style', 'none')
  await page.keyboard.press('Escape')
  await playbackRateTrigger.focus()
  await page.keyboard.press('Tab')
  await expect(volumeTrigger).toBeFocused()
  await expect(volumeTrigger).toHaveCSS('outline-style', 'solid')

  await page.keyboard.press('Space')
  await expect(volumeTrigger).toHaveAttribute('aria-expanded', 'true')

  await page.locator('.watch-stage-footer').click({ position: { x: 8, y: 8 } })
  await expect(volumeTrigger).not.toBeFocused()
  await expect.poll(() => page.evaluate(() => document.activeElement?.closest('.watch-video-controls, .watch-rate-menu') === null)).toBe(true)
})
