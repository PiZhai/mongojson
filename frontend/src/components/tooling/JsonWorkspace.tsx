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
            <button className="button button-ghost" onClick={() => setShowTree((value) => !value)} type="button">
              {showTree ? '文本结果' : '结构树'}
            </button>
          </>
        }
        eyebrow="JSON"
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
