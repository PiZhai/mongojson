import { useMemo, useState } from 'react'
import { formatJson } from '../../lib/tooling/jsonFormatter'
import { readWorkspaceTransfer } from '../../lib/tooling/workspaceTransfer'
import type { ToolStatus } from '../../types/tooling'
import { Panel } from '../common/Panel'
import { StatusBanner } from '../common/StatusBanner'
import { CodeEditor } from '../editor/CodeEditor'
import { ResultPane } from '../editor/ResultPane'

const sampleJson = `{
  "user": {
    "id": 42,
    "name": "Ada",
    "roles": ["admin", "analyst"],
    "active": true
  },
  "metrics": {
    "sessions": 18,
    "conversionRate": 0.42
  }
}`

export function JsonWorkspace() {
  const [input, setInput] = useState(() => readWorkspaceTransfer('json')?.input ?? sampleJson)
  const [output, setOutput] = useState('')
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '等待执行格式化或校验。' })
  const [showTree, setShowTree] = useState(false)
  const [stats, setStats] = useState({ chars: 0, lines: 0, depth: 0 })
  const [copied, setCopied] = useState<'input' | 'output' | null>(null)

  const treePreview = useMemo(() => {
    const result = formatJson(input, false)
    if ('error' in result) return result.error
    return JSON.stringify(result.ast, null, 2)
  }, [input])

  const inputSummary = useMemo(() => {
    const result = formatJson(input, false)
    if ('error' in result) {
      return { valid: false, fields: 0, topLevel: 'invalid' }
    }

    const topLevel =
      result.ast.type === 'object'
        ? `object · ${result.ast.entries.length} fields`
        : result.ast.type === 'array'
          ? `array · ${result.ast.items.length} items`
          : result.ast.type

    return {
      valid: true,
      fields: result.ast.type === 'object' ? result.ast.entries.length : result.ast.type === 'array' ? result.ast.items.length : 1,
      topLevel,
    }
  }, [input])

  const jsonContext = showTree
    ? {
        crumb: ['JSON', '结构树'],
        helper: inputSummary.valid ? '当前查看解析后的结构树，可继续切回文本结果。' : '输入尚未通过解析，结构树会展示错误信息。',
      }
    : {
        crumb: ['JSON', '文本输出'],
        helper: inputSummary.valid ? '当前以文本结果为主，适合格式化、压缩和复制。' : '先修正语法错误，再执行格式化或压缩。',
      }

  const run = (compact = false) => {
    const result = formatJson(input, compact)
    if ('error' in result) {
      setOutput(result.error)
      setStatus({ kind: 'error', message: result.error })
      setStats({ chars: 0, lines: 0, depth: 0 })
      return
    }
    setOutput(result.formatted)
    setStatus({ kind: 'success', message: compact ? '已完成压缩输出。' : '已完成 JSON 格式化。' })
    setStats({ chars: result.charCount, lines: result.lineCount, depth: result.maxDepth })
  }

  const copyText = async (value: string, target: 'input' | 'output') => {
    await navigator.clipboard.writeText(value)
    setCopied(target)
    setStatus({ kind: 'success', message: target === 'input' ? '已复制输入内容。' : '已复制结果内容。' })
  }

  return (
    <div className="page-shell">
      <div className="page-hero">
        <div className="page-hero-main">
          <h2 className="page-hero-title">标准 JSON 格式化与校验</h2>
          <p className="page-hero-copy">
            面向常规 JSON 的文本整理工作台，支持格式化、压缩、快速校验，以及结构树预览。
          </p>
          <div className="page-hero-meta">
            <span className="meta-chip">文本视图</span>
            <span className="meta-chip">结构树</span>
            <span className="meta-chip">即时错误反馈</span>
          </div>
        </div>
        <div className="page-hero-side">
          <div className="hero-stat-grid">
            <article className="hero-stat">
              <span className="hero-stat-label">当前输入</span>
              <strong className="hero-stat-value">{inputSummary.topLevel}</strong>
            </article>
            <article className="hero-stat">
              <span className="hero-stat-label">状态</span>
              <strong className="hero-stat-value">{inputSummary.valid ? '可解析' : '待修正'}</strong>
            </article>
            <article className="hero-stat hero-stat-wide">
              <span className="hero-stat-label">工作方式</span>
              <strong className="hero-stat-value">编辑、校验、输出在同一面板闭环完成</strong>
            </article>
          </div>
        </div>
      </div>

      <section className="context-strip" aria-label="当前上下文">
        <div className="context-strip-copy">
          <p className="context-strip-label">Current Context</p>
          <div className="context-breadcrumb" role="list">
            {jsonContext.crumb.map((item, index) => (
              <span className="context-breadcrumb-item" key={`${item}-${index}`} role="listitem">
                {index > 0 ? <span className="context-breadcrumb-separator">/</span> : null}
                <span>{item}</span>
              </span>
            ))}
          </div>
          <p className="context-strip-text">{jsonContext.helper}</p>
        </div>
        <div className="context-strip-actions">
          <button className="button button-ghost button-sm" onClick={() => setShowTree((value) => !value)} type="button">
            {showTree ? '切回文本' : '切到结构树'}
          </button>
        </div>
      </section>

      <Panel
        actions={
          <>
            <button className="button button-primary" onClick={() => run(false)} type="button">
              格式化
            </button>
            <button className="button" onClick={() => run(true)} type="button">
              压缩
            </button>
            <button
              className="button button-ghost"
              onClick={() => {
                const result = formatJson(input, false)
                if ('error' in result) {
                  setStatus({ kind: 'error', message: result.error })
                  return
                }
                setStatus({ kind: 'success', message: 'JSON 校验通过。' })
              }}
              type="button"
            >
              校验
            </button>
            <button
              className="button button-danger"
              onClick={() => {
                setInput('')
                setOutput('')
                setStatus({ kind: 'idle', message: '已清空输入与输出。' })
                setStats({ chars: 0, lines: 0, depth: 0 })
              }}
              type="button"
            >
              清空
            </button>
          </>
        }
        eyebrow="JSON"
        subtitle="输入输出一体的工作区，所有执行型操作都显式区分为格式化、压缩或复制。"
        title="工作区"
      >
        <div className="editor-split">
          <div className="editor-pane">
              <div className="editor-pane-header">
                <span className="editor-pane-title">Input</span>
                <div className="editor-pane-actions">
                  <button className="button button-ghost button-sm" onClick={() => copyText(input, 'input')} type="button">
                    {copied === 'input' ? '已复制' : '复制输入'}
                  </button>
                </div>
              </div>
              <CodeEditor language="json" onChange={setInput} value={input} />
            </div>
          <ResultPane
            actions={
              !showTree ? (
                <button
                  className="button button-ghost button-sm"
                  onClick={() => copyText(output, 'output')}
                  type="button"
                >
                  {copied === 'output' ? '已复制' : '复制结果'}
                </button>
              ) : undefined
            }
            language={showTree ? 'json' : 'json'}
            placeholder={showTree ? '解析结构后，这里会展示树形结果。' : '执行格式化后，结果会出现在这里。'}
            title={showTree ? 'Tree' : 'Output'}
            value={showTree ? treePreview : output}
          />
        </div>
        <StatusBanner
          right={`字符 ${stats.chars} · 行数 ${stats.lines} · 深度 ${stats.depth}`}
          status={status}
        />
      </Panel>
    </div>
  )
}
