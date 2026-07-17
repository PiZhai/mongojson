import { expect, test, type Page } from '@playwright/test'

const documentFixture = {
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

async function mockMemoApi(page: Page) {
  await page.route('**/api/memo/documents/layout-test', async (route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ json: { document: documentFixture } })
      return
    }
    await route.fulfill({ json: { document: { ...documentFixture, revision: 2, updated_at: '2026-07-12T00:01:00Z' } } })
  })
  await page.route(`**/api/memo/documents/${documentFixture.id}/notes`, async (route) => {
    await route.fulfill({ json: { notes: [] } })
  })
}

async function expectNoRootOverflow(page: Page) {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }))
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth + 1)
}

for (const width of [1920, 2560, 3440]) {
  test(`memo standard workspace stays bounded at ${width}px`, async ({ page }) => {
    await mockMemoApi(page)
    await page.setViewportSize({ width, height: 1000 })
    await page.goto('/tools/memo-docs?slug=layout-test')
    await expect(page.locator('.memo-block-editor .bn-editor')).toBeVisible()

    const [workspace, grid, primary, editor, outlineHeader, documentMeta, noteHeader] = await Promise.all([
      page.locator('.memo-workspace').boundingBox(),
      page.locator('.memo-workbench').boundingBox(),
      page.locator('.memo-document-surface').boundingBox(),
      page.locator('.memo-block-editor .bn-editor').boundingBox(),
      page.locator('.memo-outline-panel > .memo-panel-header').boundingBox(),
      page.locator('.memo-document-meta').boundingBox(),
      page.locator('.memo-side-note-rail > .memo-panel-header').boundingBox(),
    ])
    expect(grid!.width).toBeLessThanOrEqual(1600)
    expect(Math.abs(grid!.y + grid!.height - (workspace!.y + workspace!.height))).toBeLessThanOrEqual(1)
    expect(grid!.height).toBeGreaterThan(800)
    expect(primary!.width - editor!.width).toBeLessThanOrEqual(96)
    expect(outlineHeader!.y).toBe(documentMeta!.y)
    expect(noteHeader!.y).toBe(documentMeta!.y)
    expect(outlineHeader!.height).toBe(documentMeta!.height)
    expect(noteHeader!.height).toBe(documentMeta!.height)
    await expect(page.locator('.memo-outline-panel')).toHaveCSS('position', 'static')
    await expect(page.locator('.memo-side-note-rail')).toHaveCSS('position', 'static')
    await expectNoRootOverflow(page)
  })
}

test('memo uses an adjacent outline and note drawer on medium desktop', async ({ page }) => {
  await mockMemoApi(page)
  await page.setViewportSize({ width: 1600, height: 900 })
  await page.goto('/tools/memo-docs?slug=layout-test')
  await expect(page.locator('.memo-outline-panel')).toHaveCSS('position', 'static')
  await expect(page.locator('.memo-side-note-rail')).toHaveCSS('position', 'absolute')
  await page.getByRole('button', { name: '打开便签栏' }).click()
  await expect(page.locator('.memo-side-note-rail')).toHaveClass(/memo-panel-open/)
  await expectNoRootOverflow(page)
})

test('memo converts both secondary panels to drawers on compact landscape', async ({ page }) => {
  await mockMemoApi(page)
  await page.setViewportSize({ width: 1024, height: 768 })
  await page.goto('/tools/memo-docs?slug=layout-test')
  await expect(page.locator('.memo-outline-panel')).toHaveCSS('position', 'absolute')
  await expect(page.locator('.memo-side-note-rail')).toHaveCSS('position', 'absolute')
  await page.getByRole('button', { name: '打开目录' }).click()
  await expect(page.locator('.memo-outline-panel')).toHaveClass(/memo-panel-open/)
  await expectNoRootOverflow(page)
})

test('memo uses a bottom note sheet on portrait mobile', async ({ page }) => {
  await mockMemoApi(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/tools/memo-docs?slug=layout-test')
  await page.getByRole('button', { name: '打开便签栏' }).click()
  const rail = page.locator('.memo-side-note-rail')
  await expect(rail).toHaveCSS('position', 'fixed')
  await expect(rail).toHaveClass(/memo-panel-open/)
  await page.waitForTimeout(250)
  const box = await rail.boundingBox()
  expect(box!.width).toBe(390)
  expect(Math.abs(box!.y + box!.height - 844)).toBeLessThanOrEqual(1)
  await expectNoRootOverflow(page)
})

test('memo focus mode removes secondary panels from layout', async ({ page }) => {
  await mockMemoApi(page)
  await page.setViewportSize({ width: 1920, height: 1000 })
  await page.goto('/tools/memo-docs?slug=layout-test')
  await page.getByRole('button', { name: '专注' }).click()
  await expect(page.locator('.memo-workspace')).toHaveAttribute('data-workspace-mode', 'focus')
  await expect(page.locator('.memo-outline-panel')).toBeHidden()
  await expect(page.locator('.memo-side-note-rail')).toBeHidden()
  await expectNoRootOverflow(page)
})
