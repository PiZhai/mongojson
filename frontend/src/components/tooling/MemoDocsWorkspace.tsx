import { useEffect, useMemo, useState } from 'react'
import { Panel } from '../common/Panel'
import { StatusBanner } from '../common/StatusBanner'
import type { ToolStatus } from '../../types/tooling'

type MemoStatus = 'draft' | 'active' | 'review'

type MemoDoc = {
  id: string
  title: string
  body: string
  tags: string[]
  status: MemoStatus
  pinned: boolean
  updatedAt: string
  snapshots: Array<{ id: string; label: string; body: string; createdAt: string }>
}

const STORAGE_KEY = 'personal-tooling-memo-docs'

const sampleDocs: MemoDoc[] = [
  {
    id: 'memo-product-roadmap',
    title: '在线备忘录产品草图',
    body: `# 在线备忘录产品草图

- [ ] 做一个像 Apple Notes 一样低摩擦的入口
- [x] 保留 Notion 风格的标签、状态和可组织性
- [ ] 引入 Google Docs 的版本感，但先用本地快照落地

核心想法：备忘录不只是写下来，还要帮助用户在回顾时重新找到上下文。`,
    tags: ['产品', '灵感', 'MVP'],
    status: 'active',
    pinned: true,
    updatedAt: new Date().toISOString(),
    snapshots: [],
  },
  {
    id: 'memo-weekly-review',
    title: '周回顾模板',
    body: `# 周回顾

## 本周推进
- 

## 下周优先级
- [ ] 

## 决策记录
- 
`,
    tags: ['模板', '复盘'],
    status: 'draft',
    pinned: false,
    updatedAt: new Date(Date.now() - 1000 * 60 * 60 * 12).toISOString(),
    snapshots: [],
  },
]

const statusCopy: Record<MemoStatus, string> = {
  draft: '草稿',
  active: '进行中',
  review: '待回顾',
}

function createId() {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }

  return `memo-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function loadDocs() {
  if (typeof window === 'undefined') return sampleDocs

  const stored = window.localStorage.getItem(STORAGE_KEY)
  if (!stored) return sampleDocs

  try {
    const parsed = JSON.parse(stored) as MemoDoc[]
    return parsed.length > 0 ? parsed : sampleDocs
  } catch {
    return sampleDocs
  }
}

function extractTasks(body: string) {
  return body
    .split('\n')
    .map((line) => line.match(/^\s*-\s+\[( |x)\]\s+(.+)/i))
    .filter((match): match is RegExpMatchArray => Boolean(match))
    .map((match) => ({
      done: match[1].toLowerCase() === 'x',
      text: match[2].trim(),
    }))
}

function estimateReadingMinutes(body: string) {
  const wordLikeCount = body.trim().split(/\s+/).filter(Boolean).length
  const chineseCount = (body.match(/[\u4e00-\u9fa5]/g) ?? []).length
  return Math.max(1, Math.ceil(Math.max(wordLikeCount, chineseCount / 2) / 220))
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

export function MemoDocsWorkspace() {
  const [docs, setDocs] = useState<MemoDoc[]>(loadDocs)
  const [activeId, setActiveId] = useState(() => docs[0]?.id ?? '')
  const [query, setQuery] = useState('')
  const [tagFilter, setTagFilter] = useState('全部')
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '备忘录会自动保存到当前浏览器。' })

  useEffect(() => {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(docs))
  }, [docs])

  const activeDoc = docs.find((doc) => doc.id === activeId) ?? docs[0]
  const activeTasks = useMemo(() => extractTasks(activeDoc?.body ?? ''), [activeDoc?.body])
  const allTags = useMemo(() => ['全部', ...Array.from(new Set(docs.flatMap((doc) => doc.tags))).sort()], [docs])

  const filteredDocs = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    return docs
      .filter((doc) => {
        const matchesQuery =
          !normalizedQuery ||
          `${doc.title}\n${doc.body}\n${doc.tags.join(' ')}`.toLowerCase().includes(normalizedQuery)
        const matchesTag = tagFilter === '全部' || doc.tags.includes(tagFilter)
        return matchesQuery && matchesTag
      })
      .sort((left, right) => Number(right.pinned) - Number(left.pinned) || right.updatedAt.localeCompare(left.updatedAt))
  }, [docs, query, tagFilter])

  const insight = useMemo(() => {
    if (!activeDoc) return '选择或新建一篇备忘录后，这里会生成回顾提示。'
    const openTasks = activeTasks.filter((task) => !task.done).length
    const tags = activeDoc.tags.length > 0 ? activeDoc.tags.join('、') : '未归类'
    return `这篇文档属于 ${tags}，预计 ${estimateReadingMinutes(activeDoc.body)} 分钟读完，当前还有 ${openTasks} 个未完成事项。`
  }, [activeDoc, activeTasks])

  const updateActiveDoc = (patch: Partial<MemoDoc>) => {
    if (!activeDoc) return
    setDocs((currentDocs) =>
      currentDocs.map((doc) =>
        doc.id === activeDoc.id ? { ...doc, ...patch, updatedAt: new Date().toISOString() } : doc,
      ),
    )
  }

  const createDoc = () => {
    const nextDoc: MemoDoc = {
      id: createId(),
      title: '未命名备忘录',
      body: '# 未命名备忘录\n\n- [ ] 写下第一件要记住的事\n',
      tags: ['收集箱'],
      status: 'draft',
      pinned: false,
      updatedAt: new Date().toISOString(),
      snapshots: [],
    }
    setDocs((currentDocs) => [nextDoc, ...currentDocs])
    setActiveId(nextDoc.id)
    setStatus({ kind: 'success', message: '已创建新的备忘录。' })
  }

  const addSnapshot = () => {
    if (!activeDoc) return
    const snapshot = {
      id: createId(),
      label: `快照 ${activeDoc.snapshots.length + 1}`,
      body: activeDoc.body,
      createdAt: new Date().toISOString(),
    }
    updateActiveDoc({ snapshots: [snapshot, ...activeDoc.snapshots].slice(0, 8) })
    setStatus({ kind: 'success', message: '已保存当前正文快照。' })
  }

  const exportMarkdown = async () => {
    if (!activeDoc) return
    const content = `---
title: ${activeDoc.title}
tags: ${activeDoc.tags.join(', ')}
status: ${statusCopy[activeDoc.status]}
updated: ${activeDoc.updatedAt}
---

${activeDoc.body}
`
    await navigator.clipboard.writeText(content)
    setStatus({ kind: 'success', message: '已复制 Markdown 导出内容。' })
  }

  const deleteDoc = () => {
    if (!activeDoc || docs.length === 1) {
      setStatus({ kind: 'error', message: '至少保留一篇备忘录。' })
      return
    }
    const nextDocs = docs.filter((doc) => doc.id !== activeDoc.id)
    setDocs(nextDocs)
    setActiveId(nextDocs[0]?.id ?? '')
    setStatus({ kind: 'success', message: '已删除当前备忘录。' })
  }

  const tagValue = activeDoc?.tags.join(', ') ?? ''

  return (
    <div className="page-shell memo-docs-shell">
      <div className="page-hero memo-hero">
        <div className="page-hero-main">
          <h2 className="page-hero-title">把备忘录做成一个会回看的文档系统</h2>
          <p className="page-hero-copy">
            参考 Apple Notes 的快速记录、Notion 的组织方式、Google Docs 的版本意识，先以浏览器本地存储实现一套可直接使用的在线备忘录。
          </p>
          <div className="page-hero-meta">
            <span className="meta-chip">本地自动保存</span>
            <span className="meta-chip">任务抽取</span>
            <span className="meta-chip">快照版本</span>
            <span className="meta-chip">Markdown 导出</span>
          </div>
        </div>
        <div className="page-hero-side">
          <div className="hero-stat-grid">
            <article className="hero-stat">
              <span className="hero-stat-label">文档数</span>
              <strong className="hero-stat-value">{docs.length}</strong>
            </article>
            <article className="hero-stat">
              <span className="hero-stat-label">未完成任务</span>
              <strong className="hero-stat-value">{docs.flatMap((doc) => extractTasks(doc.body)).filter((task) => !task.done).length}</strong>
            </article>
            <article className="hero-stat hero-stat-wide">
              <span className="hero-stat-label">设计方向</span>
              <strong className="hero-stat-value">轻量文档、知识收集箱、个人任务回顾合并到一个工作台</strong>
            </article>
          </div>
        </div>
      </div>

      <section className="memo-command-bar" aria-label="备忘录操作">
        <label className="memo-search-label" htmlFor="memo-search">
          搜索文档
          <input
            className="field-input"
            id="memo-search"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索标题、正文或标签"
            value={query}
          />
        </label>
        <div className="memo-tag-filter" aria-label="标签筛选">
          {allTags.map((tag) => (
            <button
              className={`filter-chip${tagFilter === tag ? ' filter-chip-active' : ''}`}
              key={tag}
              onClick={() => setTagFilter(tag)}
              type="button"
            >
              {tag}
            </button>
          ))}
        </div>
        <button className="button button-primary" onClick={createDoc} type="button">
          新建
        </button>
      </section>

      <div className="memo-workspace-grid">
        <aside className="memo-doc-list" aria-label="备忘录列表">
          {filteredDocs.map((doc) => (
            <button
              className={`memo-doc-item${doc.id === activeDoc?.id ? ' memo-doc-item-active' : ''}`}
              key={doc.id}
              onClick={() => setActiveId(doc.id)}
              type="button"
            >
              <span className="memo-doc-item-title">{doc.pinned ? '置顶 · ' : ''}{doc.title}</span>
              <span className="memo-doc-item-meta">{statusCopy[doc.status]} · {formatDate(doc.updatedAt)}</span>
              <span className="memo-doc-tags">{doc.tags.map((tag) => <span key={tag}>{tag}</span>)}</span>
            </button>
          ))}
        </aside>

        <Panel
          actions={
            <>
              <button className="button button-ghost" onClick={addSnapshot} type="button">
                保存快照
              </button>
              <button className="button" onClick={exportMarkdown} type="button">
                导出 Markdown
              </button>
              <button className="button button-danger" onClick={deleteDoc} type="button">
                删除
              </button>
            </>
          }
          eyebrow="Memo Docs"
          subtitle="正文支持 Markdown 习惯写法，任务项使用 - [ ] 和 - [x] 自动进入右侧回顾。"
          title="文档编辑"
        >
          {activeDoc ? (
            <div className="memo-editor-layout">
              <div className="memo-editor-main">
                <label className="field-label" htmlFor="memo-title">
                  <span>标题</span>
                  <input
                    className="field-input memo-title-input"
                    id="memo-title"
                    onChange={(event) => updateActiveDoc({ title: event.target.value })}
                    value={activeDoc.title}
                  />
                </label>
                <div className="field-row">
                  <label className="field-label" htmlFor="memo-tags">
                    <span>标签</span>
                    <input
                      className="field-input"
                      id="memo-tags"
                      onChange={(event) =>
                        updateActiveDoc({
                          tags: event.target.value
                            .split(',')
                            .map((tag) => tag.trim())
                            .filter(Boolean),
                        })
                      }
                      value={tagValue}
                    />
                  </label>
                  <label className="field-label" htmlFor="memo-status">
                    <span>状态</span>
                    <select
                      className="select"
                      id="memo-status"
                      onChange={(event) => updateActiveDoc({ status: event.target.value as MemoStatus })}
                      value={activeDoc.status}
                    >
                      <option value="draft">草稿</option>
                      <option value="active">进行中</option>
                      <option value="review">待回顾</option>
                    </select>
                  </label>
                </div>
                <label className="memo-pin-row" htmlFor="memo-pinned">
                  <input
                    checked={activeDoc.pinned}
                    id="memo-pinned"
                    onChange={(event) => updateActiveDoc({ pinned: event.target.checked })}
                    type="checkbox"
                  />
                  置顶这篇备忘录
                </label>
                <label className="field-label memo-body-label" htmlFor="memo-body">
                  <span>正文</span>
                  <textarea
                    className="field-textarea memo-body-input"
                    id="memo-body"
                    onChange={(event) => updateActiveDoc({ body: event.target.value })}
                    value={activeDoc.body}
                  />
                </label>
              </div>

              <aside className="memo-review-panel" aria-label="智能回顾">
                <section className="memo-review-section">
                  <p className="memo-review-kicker">Context Brief</p>
                  <p className="memo-review-text">{insight}</p>
                </section>
                <section className="memo-review-section">
                  <p className="memo-review-kicker">任务</p>
                  {activeTasks.length > 0 ? (
                    <ul className="memo-task-list">
                      {activeTasks.map((task, index) => (
                        <li className={task.done ? 'memo-task-done' : ''} key={`${task.text}-${index}`}>
                          {task.text}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="memo-review-text">正文里写入 - [ ] 任务后，会自动出现在这里。</p>
                  )}
                </section>
                <section className="memo-review-section">
                  <p className="memo-review-kicker">快照</p>
                  {activeDoc.snapshots.length > 0 ? (
                    <div className="memo-snapshot-list">
                      {activeDoc.snapshots.map((snapshot) => (
                        <button
                          className="memo-snapshot"
                          key={snapshot.id}
                          onClick={() => updateActiveDoc({ body: snapshot.body })}
                          type="button"
                        >
                          <span>{snapshot.label}</span>
                          <span>{formatDate(snapshot.createdAt)}</span>
                        </button>
                      ))}
                    </div>
                  ) : (
                    <p className="memo-review-text">需要保留阶段性版本时，点击保存快照。</p>
                  )}
                </section>
              </aside>
            </div>
          ) : (
            <div className="empty-state">没有匹配的备忘录。</div>
          )}
          <StatusBanner
            right={activeDoc ? `更新 ${formatDate(activeDoc.updatedAt)} · ${activeDoc.body.length} 字符` : undefined}
            status={status}
          />
        </Panel>
      </div>
    </div>
  )
}
