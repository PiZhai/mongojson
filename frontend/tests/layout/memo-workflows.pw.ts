import { expect, test, type Page, type Route } from '@playwright/test'

type MockMemoOptions = { conflict?: boolean }

async function installMemoApi(page: Page, options: MockMemoOptions = {}) {
  let document = {
    id: '22222222-2222-4222-8222-222222222222',
    slug: 'workflow-test',
    title: '工作流验证',
    content_json: [
      { id: 'heading-1', type: 'heading', props: { level: 1 }, content: [{ type: 'text', text: '工作流', styles: {} }], children: [] },
      { id: 'paragraph-1', type: 'paragraph', props: {}, content: [{ type: 'text', text: '编辑这里', styles: {} }], children: [] },
    ] as unknown[],
    content_html: '<h1>工作流</h1><p>编辑这里</p>',
    content_text: '# 工作流\n\n编辑这里',
    floating_cards: [],
    schema_version: 1,
    revision: 1,
    editor_type: 'blocknote',
    created_at: '2026-07-12T00:00:00Z',
    updated_at: '2026-07-12T00:00:00Z',
  }
  let notes: Record<string, unknown>[] = []
  const documentSaves: Record<string, unknown>[] = []
  const noteSaves: Record<string, unknown>[] = []

  const setRemoteDocument = (text: string) => {
    document = {
      ...document,
      content_json: [
        { id: 'heading-1', type: 'heading', props: { level: 1 }, content: [{ type: 'text', text: '工作流', styles: {} }], children: [] },
        { id: 'paragraph-remote', type: 'paragraph', props: {}, content: [{ type: 'text', text, styles: {} }], children: [] },
      ],
      content_html: `<h1>工作流</h1><p>${text}</p>`,
      content_text: `# 工作流\n\n${text}`,
      revision: document.revision + 1,
      updated_at: '2026-07-12T00:03:00Z',
    }
    return document.revision
  }

  const setRemoteNote = (text: string) => {
    notes = [{
      id: 'remote-note',
      document_id: document.id,
      anchor_block_id: null,
      body_json: { text },
      color: '#fff7d6',
      sort_order: 0,
      collapsed: false,
      status: 'active',
      revision: 1,
      created_at: '2026-07-12T00:04:00Z',
      updated_at: '2026-07-12T00:04:00Z',
    }]
  }

  await page.route('**/api/memo/documents/workflow-test', async (route) => {
    await route.fulfill({ json: { document } })
  })
  await page.route(`**/api/memo/documents/${document.id}`, async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>
    documentSaves.push(body)
    if (options.conflict) {
      await route.fulfill({ status: 409, json: { error: 'memo revision conflict' } })
      return
    }
    document = {
      ...document,
      title: String(body.title),
      content_json: body.content_json as unknown[],
      content_html: String(body.content_html),
      content_text: String(body.content_markdown),
      revision: document.revision + 1,
      updated_at: '2026-07-12T00:01:00Z',
    }
    await route.fulfill({ json: { document } })
  })
  await page.route(`**/api/memo/documents/${document.id}/notes`, async (route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ json: { notes } })
      return
    }
    const body = route.request().postDataJSON() as Record<string, unknown>
    const note = {
      id: 'note-1',
      document_id: document.id,
      anchor_block_id: body.anchor_block_id ?? null,
      body_json: body.body_json,
      color: body.color,
      sort_order: body.sort_order,
      collapsed: body.collapsed,
      status: 'active',
      revision: 1,
      created_at: '2026-07-12T00:00:00Z',
      updated_at: '2026-07-12T00:00:00Z',
    }
    notes = [...notes, note]
    await route.fulfill({ status: 201, json: { note } })
  })
  await page.route('**/api/memo/notes/note-1', async (route: Route) => {
    if (route.request().method() === 'DELETE') {
      notes = []
      await route.fulfill({ status: 204, body: '' })
      return
    }
    const body = route.request().postDataJSON() as Record<string, unknown>
    noteSaves.push(body)
    const note = { ...notes[0], ...body, revision: 2, updated_at: '2026-07-12T00:02:00Z' }
    notes = [note]
    await route.fulfill({ json: { note } })
  })

  return { documentSaves, noteSaves, setRemoteDocument, setRemoteNote }
}

async function installMemoWebSocket(page: Page) {
  await page.addInitScript(() => {
    class MockMemoWebSocket extends EventTarget {
      static readonly CONNECTING = 0
      static readonly OPEN = 1
      static readonly CLOSING = 2
      static readonly CLOSED = 3
      readonly url: string
      readyState = MockMemoWebSocket.CONNECTING

      constructor(url: string | URL) {
        super()
        this.url = String(url)
        ;(window as typeof window & { __memoWebSocket?: MockMemoWebSocket }).__memoWebSocket = this
        window.setTimeout(() => {
          this.readyState = MockMemoWebSocket.OPEN
          this.dispatchEvent(new Event('open'))
        }, 0)
      }

      close() {
        this.readyState = MockMemoWebSocket.CLOSED
        this.dispatchEvent(new CloseEvent('close'))
      }

      send() {}
    }
    Object.defineProperty(window, 'WebSocket', { configurable: true, value: MockMemoWebSocket })
  })
}

async function openMemoWorkspace(page: Page) {
  await page.goto('/documents/memo?slug=workflow-test')
  await expect(page.getByText('编辑这里')).toBeVisible()
  await page.waitForLoadState('networkidle')
}

async function getCaretBlockState(page: Page) {
  return page.evaluate(() => {
    const selection = window.getSelection()
    const anchorElement = selection?.anchorNode instanceof Element
      ? selection.anchorNode
      : selection?.anchorNode?.parentElement
    const activeBlock = anchorElement?.closest<HTMLElement>('[data-node-type="blockOuter"][data-id]')
    const blocks = Array.from(document.querySelectorAll<HTMLElement>('[data-node-type="blockOuter"][data-id]'))
    const lastBlock = blocks.at(-1)
    return {
      activeBlockId: activeBlock?.dataset.id ?? null,
      lastBlockId: lastBlock?.dataset.id ?? null,
      lastBlockText: lastBlock?.textContent ?? null,
    }
  })
}

test('BlockNote saves structured edits and custom callout blocks', async ({ page }) => {
  const requests = await installMemoApi(page)
  await page.setViewportSize({ width: 1920, height: 1000 })
  await openMemoWorkspace(page)
  await page.getByText('编辑这里').click()
  await page.keyboard.press('End')
  await page.keyboard.press('Enter')
  await page.keyboard.type('/提示')
  await page.getByRole('option', { name: /提示块/ }).click()
  await expect(page.getByText('文档已自动保存。')).toBeVisible()

  expect(requests.documentSaves.length).toBeGreaterThan(0)
  const blocks = requests.documentSaves.at(-1)!.content_json as Array<{ type?: string }>
  expect(blocks.some((block) => block.type === 'callout')).toBe(true)
})

test('remote document revisions appear without reloading the page', async ({ page }) => {
  await installMemoWebSocket(page)
  const requests = await installMemoApi(page)
  await page.setViewportSize({ width: 1920, height: 1000 })
  await openMemoWorkspace(page)
  await expect(page.getByText('实时同步')).toBeVisible()

  const revision = requests.setRemoteDocument('其他用户刚刚修改的内容')
  await page.evaluate(({ documentId, nextRevision }) => {
    const socket = (window as typeof window & { __memoWebSocket?: WebSocket }).__memoWebSocket
    socket?.dispatchEvent(new MessageEvent('message', {
      data: JSON.stringify({
        type: 'document_updated',
        document_id: documentId,
        revision: nextRevision,
        actor_client_id: 'another-client',
      }),
    }))
  }, { documentId: '22222222-2222-4222-8222-222222222222', nextRevision: revision })

  await expect(page.getByText('其他用户刚刚修改的内容')).toBeVisible()
  await expect(page.getByText('已实时同步远端修改。')).toBeVisible()

  requests.setRemoteNote('其他用户添加的便签')
  await page.evaluate((documentId) => {
    const socket = (window as typeof window & { __memoWebSocket?: WebSocket }).__memoWebSocket
    socket?.dispatchEvent(new MessageEvent('message', {
      data: JSON.stringify({ type: 'notes_updated', document_id: documentId, actor_client_id: 'another-client' }),
    }))
  }, '22222222-2222-4222-8222-222222222222')
  await expect(page.getByRole('textbox', { name: '文档便签 1 内容' })).toHaveValue('其他用户添加的便签')
  await expect(page.getByText('已实时同步远端便签。')).toBeVisible()
})

test('the caret remains in the editor tail after ArrowDown or a blank-area click', async ({ page }) => {
  await installMemoApi(page)
  await page.setViewportSize({ width: 1920, height: 1000 })
  await openMemoWorkspace(page)
  await expect(page.locator('[data-node-type="blockOuter"][data-id]')).toHaveCount(3)

  await page.getByText('编辑这里').click()
  await page.keyboard.press('End')
  await page.keyboard.press('ArrowDown')
  await expect.poll(() => getCaretBlockState(page)).toMatchObject({
    activeBlockId: expect.any(String),
    lastBlockId: expect.any(String),
    lastBlockText: '',
  })
  let state = await getCaretBlockState(page)
  expect(state.activeBlockId).toBe(state.lastBlockId)
  await page.waitForTimeout(500)
  state = await getCaretBlockState(page)
  expect(state.activeBlockId).toBe(state.lastBlockId)

  const editorBox = await page.locator('.memo-block-editor .bn-editor').boundingBox()
  await page.mouse.click(editorBox!.x + editorBox!.width / 2, editorBox!.y + editorBox!.height - 80)
  state = await getCaretBlockState(page)
  expect(state.activeBlockId).toBe(state.lastBlockId)
  await page.waitForTimeout(500)
  state = await getCaretBlockState(page)
  expect(state.activeBlockId).toBe(state.lastBlockId)
})

test('code blocks provide language highlighting and whole-block copy', async ({ context, page }) => {
  const requests = await installMemoApi(page)
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: 'http://127.0.0.1:4175' })
  await page.setViewportSize({ width: 1920, height: 1000 })
  await openMemoWorkspace(page)
  const code = 'const answer = 42;\nconsole.log(answer);'
  await page.locator('input[type="file"]').setInputFiles({
    name: 'code.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify({
      blocks: [{
        id: 'code-1',
        type: 'codeBlock',
        props: { language: 'javascript' },
        content: [{ type: 'text', text: code, styles: {} }],
        children: [],
      }],
    })),
  })

  const codeBlock = page.locator('.bn-block-content[data-content-type="codeBlock"]')
  await expect(codeBlock.locator('select')).toHaveValue('javascript')
  await expect.poll(() => codeBlock.locator('code span[style*="color"]').count()).toBeGreaterThan(0)
  await expect(codeBlock).toHaveCSS('background-color', 'rgb(255, 255, 255)')
  await expect(codeBlock.locator('pre')).toHaveCSS('background-color', 'rgb(255, 255, 255)')
  const copyButton = page.getByRole('button', { name: '复制代码块内容' })
  await copyButton.click()
  await expect(copyButton).toHaveText('已复制')
  await expect.poll(() => page.evaluate(async () => (await navigator.clipboard.readText()).replace(/\r\n?/g, '\n'))).toBe(code)
  await expect.poll(() => requests.documentSaves.length).toBeGreaterThan(0)
})

test('a block side note persists independently with a stable anchor', async ({ page }) => {
  const requests = await installMemoApi(page)
  await page.setViewportSize({ width: 1920, height: 1000 })
  await openMemoWorkspace(page)
  await page.getByText('编辑这里').click()
  await page.getByRole('button', { name: '+ 块便签' }).click()
  await page.getByRole('textbox', { name: '块便签 1 内容' }).fill('关联正文的便签')
  await expect.poll(() => requests.noteSaves.length).toBeGreaterThan(0)

  expect(requests.noteSaves.at(-1)).toMatchObject({
    anchor_block_id: 'paragraph-1',
    body_json: { text: '关联正文的便签' },
    revision: 1,
  })
  const marker = page.getByRole('button', { name: '此正文块有 1 条便签，点击查看' })
  await expect(marker.locator('.memo-block-note-count')).toHaveText('1')
  await marker.hover()
  await expect(marker.locator('.memo-block-note-preview')).toContainText('关联正文的便签')
  await marker.click()
  await expect(page.locator('.memo-side-note')).toHaveClass(/memo-side-note-active/)
})

test('dragging a document note anchors it without inserting its id into the document', async ({ page }) => {
  const requests = await installMemoApi(page)
  await page.setViewportSize({ width: 1920, height: 1000 })
  await openMemoWorkspace(page)
  await page.getByRole('button', { name: '+ 文档便签' }).click()
  await page.locator('.memo-side-note').dragTo(page.locator('[data-node-type="blockOuter"][data-id="paragraph-1"]'))
  await expect.poll(() => requests.noteSaves.length).toBeGreaterThan(0)

  expect(requests.noteSaves.at(-1)).toMatchObject({ anchor_block_id: 'paragraph-1' })
  await expect(page.locator('.memo-block-editor')).not.toContainText('note-1')
})

test('a side note converts into a callout and removes the source note', async ({ page }) => {
  const requests = await installMemoApi(page)
  await page.setViewportSize({ width: 1920, height: 1000 })
  await openMemoWorkspace(page)
  await page.getByText('编辑这里').click()
  await page.getByRole('button', { name: '+ 块便签' }).click()
  await page.getByRole('textbox', { name: '块便签 1 内容' }).fill('转入正文的重要提醒')
  await expect.poll(() => requests.noteSaves.length).toBeGreaterThan(0)
  await page.getByRole('button', { name: '转为正文提示块' }).click()

  await expect(page.getByText('便签已转为正文提示块。')).toBeVisible()
  await expect(page.getByRole('textbox', { name: '块便签 1 内容' })).toHaveCount(0)
  await expect.poll(() => requests.documentSaves.length).toBeGreaterThan(0)
  const blocks = requests.documentSaves.at(-1)!.content_json as Array<{ type?: string }>
  expect(blocks.some((block) => block.type === 'callout')).toBe(true)
})

test('Markdown import becomes canonical block JSON', async ({ page }) => {
  const requests = await installMemoApi(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await openMemoWorkspace(page)
  await page.locator('input[type="file"]').setInputFiles({
    name: 'import.md',
    mimeType: 'text/markdown',
    buffer: Buffer.from('# 导入标题\n\n导入正文'),
  })
  await expect(page.getByRole('button', { name: '导入标题' })).toBeVisible()
  await expect.poll(() => requests.documentSaves.length).toBeGreaterThan(0)

  const lastSave = requests.documentSaves.at(-1)!
  expect(lastSave.content_markdown).toContain('导入标题')
  expect(Array.isArray(lastSave.content_json)).toBe(true)
})

test('revision conflicts preserve a recoverable local snapshot', async ({ page }) => {
  await installMemoApi(page, { conflict: true })
  await page.setViewportSize({ width: 1440, height: 900 })
  await openMemoWorkspace(page)
  await page.getByText('编辑这里').click()
  await page.keyboard.press('End')
  await page.keyboard.type('本地修改')
  await expect(page.getByText('远端文档已被修改。')).toBeVisible()
  await expect(page.getByRole('button', { name: '保存为副本' })).toBeVisible()

  await page.reload()
  await expect(page.getByText(/发现 .* 的本地恢复内容/)).toBeVisible()
  await expect(page.getByRole('button', { name: '恢复本地内容' })).toBeVisible()
})
