import { expect, test } from '@playwright/test'

async function expectNoRootOverflow(page: import('@playwright/test').Page) {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }))
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth + 1)
}

test('canvas renders a stable full-size editing surface', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/documents/canvas')
  await expect(page.locator('.canvas-stage .excalidraw')).toBeVisible()
  await expect(page.getByRole('button', { name: '插入便签' })).toBeEnabled()

  const [stage, commandBar] = await Promise.all([
    page.locator('.canvas-stage').boundingBox(),
    page.locator('.canvas-command-bar').boundingBox(),
  ])
  expect(stage).not.toBeNull()
  expect(stage!.height).toBeGreaterThan(600)
  expect(stage!.y).toBeGreaterThanOrEqual(commandBar!.y + commandBar!.height - 1)
  await expectNoRootOverflow(page)
})

test('canvas mobile drawer overlays without widening the page', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/documents/canvas')
  await expect(page.locator('.canvas-stage .excalidraw')).toBeVisible()

  const [drawer, stage] = await Promise.all([
    page.locator('.canvas-board-drawer').boundingBox(),
    page.locator('.canvas-stage').boundingBox(),
  ])
  expect(drawer).not.toBeNull()
  expect(drawer!.width).toBeLessThanOrEqual(320)
  expect(stage).not.toBeNull()
  expect(stage!.width).toBeGreaterThanOrEqual(358)
  await expectNoRootOverflow(page)

  await page.getByRole('button', { name: '只读预览' }).click()
  await expect(page.getByRole('textbox', { name: '画板名称' })).toBeDisabled()
  await expect(page.getByRole('button', { name: '插入便签' })).toBeDisabled()
})
