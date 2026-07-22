import { expect, test, type Locator, type Page } from '@playwright/test'

type WorkspaceCase = {
  path: string
  frame: string
  first: string
  second: string
  firstMin: number
  secondMin: number
}

const workspaces: WorkspaceCase[] = [
  { path: '/tools/inspect', frame: 'inspect-workspace', first: ':scope > [data-layout-region="panel"]', second: 'inspect-rail', firstMin: 720, secondMin: 280 },
  { path: '/tools/json', frame: 'json-workspace', first: '.editor-split > :first-child', second: '.editor-split > :nth-child(2)', firstMin: 420, secondMin: 420 },
  { path: '/tools/mongodb-json', frame: 'mongo-workspace', first: '.editor-split > :first-child', second: '.editor-split > :nth-child(2)', firstMin: 420, secondMin: 420 },
  { path: '/tools/visualize', frame: 'visualization-workspace', first: 'visualization-input', second: 'visualization-rail', firstMin: 420, secondMin: 360 },
]

function region(page: Page, selector: string): Locator {
  return selector.startsWith(':') || selector.startsWith('.')
    ? page.locator(selector).first()
    : page.locator(`[data-layout-region="${selector}"]`).first()
}

async function assertNoRootOverflow(page: Page) {
  const overflow = await page.evaluate(() => ({
    client: document.documentElement.clientWidth,
    scroll: document.documentElement.scrollWidth,
  }))
  expect(overflow.scroll).toBeLessThanOrEqual(overflow.client + 1)
}

for (const width of [800, 1024, 1080, 1280, 1404, 1920]) {
  test.describe(`landscape/desktop ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } })

    for (const workspace of workspaces) {
      test(`${workspace.path} keeps its horizontal minimum grid`, async ({ page }) => {
        await page.goto(workspace.path)
        const frame = region(page, workspace.frame)
        await expect(frame).toBeVisible()

        const first = workspace.first.startsWith(':') || workspace.first.startsWith('.')
          ? frame.locator(workspace.first).first()
          : region(page, workspace.first)
        const second = workspace.second.startsWith(':') || workspace.second.startsWith('.')
          ? frame.locator(workspace.second).first()
          : region(page, workspace.second)
        const [firstBox, secondBox] = await Promise.all([first.boundingBox(), second.boundingBox()])
        expect(firstBox).not.toBeNull()
        expect(secondBox).not.toBeNull()
        expect(firstBox!.width).toBeGreaterThanOrEqual(workspace.firstMin - 1)
        expect(secondBox!.width).toBeGreaterThanOrEqual(workspace.secondMin - 1)
        expect(secondBox!.x).toBeGreaterThanOrEqual(firstBox!.x + firstBox!.width - 1)
        await assertNoRootOverflow(page)
      })
    }
  })
}

for (const viewport of [
  { width: 360, height: 800 },
  { width: 390, height: 844 },
  { width: 430, height: 932 },
  { width: 768, height: 1024 },
]) {
  test.describe(`portrait ${viewport.width}x${viewport.height}`, () => {
    test.use({ viewport })

    for (const workspace of workspaces) {
      test(`${workspace.path} stacks without overlap`, async ({ page }) => {
        await page.goto(workspace.path)
        const frame = region(page, workspace.frame)
        await expect(frame).toBeVisible()
        const first = workspace.first.startsWith(':') || workspace.first.startsWith('.')
          ? frame.locator(workspace.first).first()
          : region(page, workspace.first)
        const second = workspace.second.startsWith(':') || workspace.second.startsWith('.')
          ? frame.locator(workspace.second).first()
          : region(page, workspace.second)
        const [firstBox, secondBox] = await Promise.all([first.boundingBox(), second.boundingBox()])
        expect(firstBox).not.toBeNull()
        expect(secondBox).not.toBeNull()
        expect(secondBox!.y).toBeGreaterThanOrEqual(firstBox!.y + firstBox!.height - 1)
        await assertNoRootOverflow(page)
      })
    }
  })
}

test.describe('collapsed desktop sidebar', () => {
  test.use({ viewport: { width: 1024, height: 768 } })

  for (const workspace of workspaces) {
    test(`${workspace.path} preserves its horizontal grid after collapse`, async ({ page }) => {
      await page.goto(workspace.path)
      await page.getByRole('button', { name: '收起左侧导航' }).click()
      await expect(page.locator('[data-layout-region="app-shell"]')).toHaveAttribute('data-sidebar', 'collapsed')
      const first = workspace.first.startsWith(':') || workspace.first.startsWith('.')
        ? region(page, workspace.frame).locator(workspace.first).first()
        : region(page, workspace.first)
      const second = workspace.second.startsWith(':') || workspace.second.startsWith('.')
        ? region(page, workspace.frame).locator(workspace.second).first()
        : region(page, workspace.second)
      const [firstBox, secondBox] = await Promise.all([first.boundingBox(), second.boundingBox()])
      expect(secondBox!.x).toBeGreaterThanOrEqual(firstBox!.x + firstBox!.width - 1)
      await assertNoRootOverflow(page)
    })
  }
})
