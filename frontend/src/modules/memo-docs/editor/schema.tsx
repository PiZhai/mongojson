import { BlockNoteSchema, defaultBlockSpecs } from '@blocknote/core'
import { createCodeBlockSpec } from '@blocknote/core/blocks'
import { createReactBlockSpec } from '@blocknote/react'

const CODE_LANGUAGES: Record<string, { name: string; aliases?: string[] }> = {
  text: { name: '纯文本', aliases: ['txt', 'plaintext'] },
  javascript: { name: 'JavaScript', aliases: ['js'] },
  typescript: { name: 'TypeScript', aliases: ['ts'] },
  json: { name: 'JSON' },
  html: { name: 'HTML' },
  css: { name: 'CSS' },
  bash: { name: 'Shell', aliases: ['sh', 'shell'] },
  powershell: { name: 'PowerShell', aliases: ['ps1'] },
  sql: { name: 'SQL' },
  python: { name: 'Python', aliases: ['py'] },
  java: { name: 'Java' },
  go: { name: 'Go' },
  markdown: { name: 'Markdown', aliases: ['md'] },
  yaml: { name: 'YAML', aliases: ['yml'] },
  dockerfile: { name: 'Dockerfile', aliases: ['docker'] },
}

const highlightedCodeBlock = createCodeBlockSpec({
  defaultLanguage: 'text',
  supportedLanguages: CODE_LANGUAGES,
  createHighlighter: async () => {
    const [{ createHighlighterCore }, { createJavaScriptRegexEngine }, theme, ...languages] = await Promise.all([
      import('@shikijs/core'),
      import('@shikijs/engine-javascript'),
      import('@shikijs/themes/catppuccin-latte'),
      import('@shikijs/langs/javascript'),
      import('@shikijs/langs/typescript'),
      import('@shikijs/langs/json'),
      import('@shikijs/langs/html'),
      import('@shikijs/langs/css'),
      import('@shikijs/langs/bash'),
      import('@shikijs/langs/powershell'),
      import('@shikijs/langs/sql'),
      import('@shikijs/langs/python'),
      import('@shikijs/langs/java'),
      import('@shikijs/langs/go'),
      import('@shikijs/langs/markdown'),
      import('@shikijs/langs/yaml'),
      import('@shikijs/langs/dockerfile'),
    ])
    return createHighlighterCore({
      themes: [theme.default],
      langs: languages.map((language) => language.default),
      engine: createJavaScriptRegexEngine(),
    })
  },
})

const calloutBlock = createReactBlockSpec(
  {
    type: 'callout',
    propSchema: {
      tone: { default: 'info', values: ['info', 'warning', 'success'] as const },
    },
    content: 'inline',
  },
  {
    render: ({ block, contentRef }) => (
      <aside className={`memo-custom-callout memo-custom-callout-${block.props.tone}`}>
        <span aria-hidden="true" className="memo-custom-block-symbol">i</span>
        <div className="memo-custom-block-content" ref={contentRef} />
      </aside>
    ),
  },
)()

const fileCardBlock = createReactBlockSpec(
  {
    type: 'fileCard',
    propSchema: {
      fileName: { default: '附件' },
      mimeType: { default: 'application/octet-stream' },
      sizeLabel: { default: '' },
      url: { default: '' },
    },
    content: 'none',
  },
  {
    render: ({ block }) => (
      <a className="memo-custom-reference" href={block.props.url || undefined} rel="noreferrer">
        <span aria-hidden="true" className="memo-custom-block-symbol">F</span>
        <span>
          <strong>{block.props.fileName}</strong>
          <small>{[block.props.mimeType, block.props.sizeLabel].filter(Boolean).join(' · ')}</small>
        </span>
      </a>
    ),
  },
)()

const embedBlock = createReactBlockSpec(
  {
    type: 'embed',
    propSchema: {
      label: { default: '嵌入链接' },
      url: { default: '' },
    },
    content: 'none',
  },
  {
    render: ({ block }) => (
      <a className="memo-custom-reference" href={block.props.url || undefined} rel="noreferrer" target="_blank">
        <span aria-hidden="true" className="memo-custom-block-symbol">↗</span>
        <span>
          <strong>{block.props.label}</strong>
          <small>{block.props.url || '尚未设置地址'}</small>
        </span>
      </a>
    ),
  },
)()

const canvasReferenceBlock = createReactBlockSpec(
  {
    type: 'canvasReference',
    propSchema: {
      boardId: { default: '' },
      title: { default: '无界画布' },
      url: { default: '' },
    },
    content: 'none',
  },
  {
    render: ({ block }) => {
      const content = (
        <>
        <span aria-hidden="true" className="memo-custom-block-symbol">∞</span>
        <span>
          <strong>{block.props.title}</strong>
          <small>{block.props.boardId ? `画板 ${block.props.boardId}` : '尚未关联画板'}</small>
        </span>
        </>
      )
      return block.props.url
        ? <a className="memo-custom-reference memo-custom-canvas-reference" href={block.props.url}>{content}</a>
        : <div className="memo-custom-reference memo-custom-canvas-reference">{content}</div>
    },
  },
)()

export const memoBlockNoteSchema = BlockNoteSchema.create({
  blockSpecs: {
    ...defaultBlockSpecs,
    codeBlock: highlightedCodeBlock,
    callout: calloutBlock,
    fileCard: fileCardBlock,
    embed: embedBlock,
    canvasReference: canvasReferenceBlock,
  },
})

export type MemoBlockNoteEditor = typeof memoBlockNoteSchema.BlockNoteEditor
