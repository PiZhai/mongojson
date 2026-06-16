import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  astNodeToDisplay,
  buildTableFromAst,
  escapeJsonString,
  formatJson,
  formatShellStatement,
  getFieldDiffSummary,
  normalizeForCompare,
  parseShellStatement,
  unescapeJsonString,
  validateShellStatement,
} from '../../lib/tooling/jsonFormatter'
import type { DiffSummary, ShellValidation, TableData, ToolStatus } from '../../types/tooling'
import { Panel } from '../common/Panel'
import { StatusBanner } from '../common/StatusBanner'
import { CodeEditor } from '../editor/CodeEditor'
import { DiffEditorPanel } from '../editor/DiffEditorPanel'
import { ResultPane } from '../editor/ResultPane'

const mongoSample = `{
  "_id" : ObjectId("69c364c67ab3eb212eaf507d"),
  "bundleId" : "B001407",
  "bundleName" : "游泳健身权益",
  "conditional" : {
    "rules" : [
      {
        "method" : "ConditionalRule",
        "params" : [
          {
            "refreshCycle" : {
              "type" : "M"
            },
            "limit" : 1.0
          }
        ]
      }
    ]
  }
}`

const diffSampleRight = `{
  "bundleName" : "游泳健身权益",
  "bundleId" : "B001407",
  "_id" : ObjectId("69c364c67ab3eb212eaf507d"),
  "conditional" : {
    "rules" : [
      {
        "method" : "ConditionalRule",
        "params" : [
          {
            "limit" : 2.0,
            "refreshCycle" : {
              "type" : "W"
            }
          }
        ]
      }
    ]
  }
}`

const shellSample = `db.users.updateOne(
  { _id: ObjectId("69c364c67ab3eb212eaf507d") },
  { $set: { status: "active", score: 99 } }
)`

type MongoMode = 'format' | 'diff' | 'table' | 'shell' | 'escape' | 'unescape'

function isMongoMode(value: string | null): value is MongoMode {
  return value === 'format' || value === 'diff' || value === 'table' || value === 'shell' || value === 'escape' || value === 'unescape'
}

export function MongoJsonWorkspace() {
  const [searchParams, setSearchParams] = useSearchParams()
  const rawMode = searchParams.get('mode')
  const mode: MongoMode = isMongoMode(rawMode) ? rawMode : 'format'
  const [input, setInput] = useState(mongoSample)
  const [output, setOutput] = useState('')
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '等待执行 MongoDB JSON 工具操作。' })
  const [stats, setStats] = useState({ chars: 0, lines: 0, depth: 0 })
  const [diffLeft, setDiffLeft] = useState(mongoSample)
  const [diffRight, setDiffRight] = useState(diffSampleRight)
  const [tableData, setTableData] = useState<TableData | null>(null)
  const [escapeInput, setEscapeInput] = useState(mongoSample)
  const [escapeOutput, setEscapeOutput] = useState('')
  const [shellInput, setShellInput] = useState(shellSample)
  const [shellOutput, setShellOutput] = useState('')
  const [shellChecks, setShellChecks] = useState<ShellValidation[]>([])
  const [copied, setCopied] = useState<string | null>(null)
  const [selectedRow, setSelectedRow] = useState(0)
  const [tableQuery, setTableQuery] = useState('')
  const [tableTypeFilter, setTableTypeFilter] = useState<'all' | 'mixed' | 'nullable'>('all')
  const [diffFocus, setDiffFocus] = useState<{ side: 'left' | 'right'; line: number; key: string; path: string } | null>(null)
  const [shellFocus, setShellFocus] = useState<{ line: number; label: string; kind: 'collection' | 'method' | 'operator' } | null>(null)
  const activeModeLabel = {
    format: '格式化',
    diff: '对比',
    table: '表格',
    shell: 'Shell',
    escape: '转义',
    unescape: '还原',
  }[mode]

  const normalizedDiffLeft = useMemo(() => normalizeForCompare(diffLeft), [diffLeft])
  const normalizedDiffRight = useMemo(() => normalizeForCompare(diffRight), [diffRight])
  const parsedShell = useMemo(() => parseShellStatement(shellInput), [shellInput])

  const diffSummary = useMemo<DiffSummary>(() => {
    return getFieldDiffSummary(normalizedDiffLeft.ast, normalizedDiffRight.ast)
  }, [normalizedDiffLeft.ast, normalizedDiffRight.ast])

  const activeModeDescription = {
    format: '面向 Extended JSON 的宽松解析与标准化输出。',
    diff: '聚焦字段新增、缺失和值变化，并支持路径定位。',
    table: '展平对象结构，查看字段主类型、缺失率和逐行预览。',
    shell: '提取集合、方法链和操作符，辅助排查 Shell 语句。',
    escape: '将 JSON 文本转成可嵌入字符串。',
    unescape: '将转义后的 JSON 字符串还原为可读文本。',
  }[mode]

  const tablePreview = useMemo(() => {
    if (!tableData) {
      return {
        filteredSchema: [],
        selectedCells: [] as Array<{ path: string; value: string }>,
      }
    }

    const query = tableQuery.trim().toLowerCase()
    const filteredSchema = tableData.schema.filter((column) => {
      const matchesQuery = !query || column.path.toLowerCase().includes(query) || column.dominantType.toLowerCase().includes(query)
      const matchesType =
        tableTypeFilter === 'all' ||
        (tableTypeFilter === 'mixed' && column.isMixed) ||
        (tableTypeFilter === 'nullable' && column.nullRatio > 0)
      return matchesQuery && matchesType
    })

    const row = tableData.rows[selectedRow] ?? []
    const selectedCells = filteredSchema.slice(0, 24).map((column) => {
      const columnIndex = tableData.schema.findIndex((item) => item.path === column.path)
      return {
        path: column.path,
        value: astNodeToDisplay(row[columnIndex]),
      }
    })

    const nullableCount = tableData.schema.filter((column) => column.nullRatio > 0).length
    const mixedCount = tableData.schema.filter((column) => column.isMixed).length

    return { filteredSchema, selectedCells, nullableCount, mixedCount }
  }, [selectedRow, tableData, tableQuery, tableTypeFilter])

  const diffOverview = [
    {
      label: '左侧缺失',
      value: diffSummary.leftOnly.length,
      accent: 'danger',
      helper: diffSummary.leftOnly[0] ?? '无缺失字段',
    },
    {
      label: '右侧新增',
      value: diffSummary.rightOnly.length,
      accent: 'success',
      helper: diffSummary.rightOnly[0] ?? '无新增字段',
    },
    {
      label: '值变化',
      value: diffSummary.changed.length,
      accent: 'warning',
      helper: diffSummary.changed[0] ?? '无字段变化',
    },
  ]

  const tableOverview = tableData
    ? [
        {
          label: '字段数',
          value: tableData.schema.length,
          accent: 'neutral' as const,
          helper: tablePreview.filteredSchema[0]?.path ?? '等待筛选字段',
        },
        {
          label: '当前命中',
          value: tablePreview.filteredSchema.length,
          accent: 'success' as const,
          helper:
            tableQuery || tableTypeFilter !== 'all'
              ? `${tableQuery ? `词: ${tableQuery}` : '未设关键词'} · ${tableTypeFilter === 'mixed' ? '仅 mixed' : tableTypeFilter === 'nullable' ? '仅可缺失' : '全部类型'}`
              : '未设置筛选',
        },
        {
          label: '当前行',
          value: `${selectedRow + 1}/${tableData.docCount}`,
          accent: 'warning' as const,
          helper: tablePreview.selectedCells[0]?.path ?? '等待行预览',
        },
      ]
    : null

  const tableHasNoResults = Boolean(tableData) && tablePreview.filteredSchema.length === 0

  const shellOverview = parsedShell
    ? [
        {
          label: '集合',
          value: parsedShell.collection,
          accent: 'success' as const,
          helper: `第 ${shellInput.slice(0, parsedShell.collectionStart).split('\n').length} 行`,
        },
        {
          label: '方法链',
          value: parsedShell.methods.length,
          accent: 'neutral' as const,
          helper: parsedShell.methods[0]?.name ?? '未识别方法',
        },
        {
          label: '操作符',
          value: parsedShell.operators.length,
          accent: 'warning' as const,
          helper: parsedShell.operators[0]?.name ?? '未识别操作符',
        },
      ]
    : null

  const primaryDiffPath = diffSummary.changed[0] ?? diffSummary.leftOnly[0] ?? diffSummary.rightOnly[0] ?? null

  const contextTrail = useMemo(() => {
    const base = ['MongoDB JSON', activeModeLabel]

    if (mode === 'diff') {
      return {
        crumb: diffFocus ? [...base, diffFocus.path] : [...base, '差异路径'],
        helper: diffFocus
          ? `${diffFocus.side === 'left' ? '左侧' : '右侧'}第 ${diffFocus.line} 行，继续沿字段路径 drill down。`
          : '从差异摘要点击字段，可直接跳到对应行并保留上下文。',
      }
    }

    if (mode === 'table') {
      const selectedPath = tablePreview.selectedCells[0]?.path
      return {
        crumb: tableData ? [...base, `第 ${selectedRow + 1} 行`, selectedPath ?? '字段结构'] : [...base, '字段结构'],
        helper: tableData
          ? `当前筛选命中 ${tablePreview.filteredSchema.length} 个字段。${tableQuery ? `筛选词为 ${tableQuery}。` : ''}${tableTypeFilter !== 'all' ? ` 类型筛查为 ${tableTypeFilter === 'mixed' ? 'mixed' : '可缺失'}。` : ''} 行预览与字段列表同步。`
          : '构建表格后，可沿字段路径和文档行继续下钻。',
      }
    }

    if (mode === 'shell') {
      return {
        crumb: parsedShell ? [...base, parsedShell.collection, shellFocus?.label ?? '语句摘要'] : [...base, '语句摘要'],
        helper: parsedShell
          ? `已识别 ${parsedShell.methods.length} 个方法调用和 ${parsedShell.operators.length} 个操作符。${shellFocus ? ` 当前定位到${shellFocus.kind === 'collection' ? '集合' : shellFocus.kind === 'method' ? '方法' : '操作符'} ${shellFocus.label} 的第 ${shellFocus.line} 行。` : ''}`
          : '识别集合、方法链和操作符后，可从摘要跳回输入区。',
      }
    }

    if (mode === 'escape' || mode === 'unescape') {
      return {
        crumb: [...base, '字符串处理'],
        helper: mode === 'escape' ? '将 JSON 转成适合嵌入代码或文本的字符串。' : '将转义后的字符串还原为可读 JSON 文本。',
      }
    }

    return {
      crumb: [...base, '文本整理'],
      helper: '在输入、输出和状态栏之间快速往返，完成 Extended JSON 的标准化处理。',
    }
  }, [activeModeLabel, diffFocus, mode, parsedShell, selectedRow, shellFocus, tableData, tablePreview.filteredSchema.length, tablePreview.selectedCells, tableQuery, tableTypeFilter])

  const setMode = (nextMode: MongoMode) => {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set('mode', nextMode)
    setSearchParams(nextParams, { replace: true })
  }

  const runFormat = () => {
    const result = formatJson(input, false)
    if ('error' in result) {
      setOutput(result.error)
      setStatus({ kind: 'error', message: result.error })
      setStats({ chars: 0, lines: 0, depth: 0 })
      return
    }
    setOutput(result.formatted)
    setStatus({ kind: 'success', message: 'MongoDB JSON 已格式化。' })
    setStats({ chars: result.charCount, lines: result.lineCount, depth: result.maxDepth })
  }

  const runTable = () => {
    const result = formatJson(input, false)
    if ('error' in result) {
      setTableData(null)
      setStatus({ kind: 'error', message: result.error })
      return
    }
    const table = buildTableFromAst(result.ast)
    if (!table) {
      setTableData(null)
      setStatus({ kind: 'warning', message: '当前输入无法构建表格，需为对象或对象数组。' })
      return
    }
    setTableData(table)
    setSelectedRow(0)
    setTableQuery('')
    setTableTypeFilter('all')
    setStatus({ kind: 'success', message: `表格已构建，共 ${table.docCount} 条文档。` })
  }

  const runEscape = () => {
    const action = mode === 'escape' ? escapeJsonString : unescapeJsonString
    const result = action(escapeInput)
    if (result.error) {
      setEscapeOutput(result.error)
      setStatus({ kind: 'error', message: result.error })
      return
    }
    setEscapeOutput(result.output ?? '')
    setStatus({ kind: 'success', message: mode === 'escape' ? '已完成转义。' : '已完成还原。' })
  }

  const runShell = () => {
    const formatted = formatShellStatement(shellInput)
    const validations = validateShellStatement(shellInput)
    setShellChecks(validations)
    if (!parseShellStatement(shellInput)) {
      setShellOutput('未识别为 MongoDB Shell 语句。')
      setStatus({ kind: 'warning', message: '当前输入不是可识别的 MongoDB Shell 语句。' })
      return
    }
    setShellOutput(formatted ?? shellInput)
    const hasError = validations.some((item) => item.level === 'err')
    const hasWarn = validations.some((item) => item.level === 'warn')
    setStatus({
      kind: hasError ? 'error' : hasWarn ? 'warning' : 'success',
      message: hasError ? 'Shell 语句存在错误。' : hasWarn ? 'Shell 语句有警告，请检查。' : 'Shell 语句已格式化。',
    })
  }

  const copyText = async (value: string, key: string, message: string) => {
    await navigator.clipboard.writeText(value)
    setCopied(key)
    setStatus({ kind: 'success', message })
  }

  const jumpToShellOffset = (offset: number, label: string, kind: 'collection' | 'method' | 'operator') => {
    const line = shellInput.slice(0, offset).split('\n').length
    setShellFocus({ line, label, kind })
    setStatus({ kind: 'success', message: `已跳转到第 ${line} 行。` })
  }

  const jumpToDiffPath = (path: string, preferredSide?: 'left' | 'right') => {
    const leftLine = normalizedDiffLeft.keyLineMap[path]
    const rightLine = normalizedDiffRight.keyLineMap[path]

    if (preferredSide === 'left' && leftLine) {
      setDiffFocus({ side: 'left', line: leftLine, key: `${path}-left-${leftLine}`, path })
      return
    }
    if (preferredSide === 'right' && rightLine) {
      setDiffFocus({ side: 'right', line: rightLine, key: `${path}-right-${rightLine}`, path })
      return
    }
    if (leftLine) {
      setDiffFocus({ side: 'left', line: leftLine, key: `${path}-left-${leftLine}`, path })
      return
    }
    if (rightLine) {
      setDiffFocus({ side: 'right', line: rightLine, key: `${path}-right-${rightLine}`, path })
    }
  }

  return (
    <div className="page-shell">
      <div className="page-hero">
        <div className="page-hero-main">
          <h2 className="page-hero-title">MongoDB JSON 数据调试工作台</h2>
          <p className="page-hero-copy">
            覆盖扩展类型格式化、结构对比、表格化校验、Shell 语句整理和字符串转义，保留了现有工具最有价值的能力。
          </p>
          <div className="page-hero-meta">
            <span className="meta-chip">Extended JSON</span>
            <span className="meta-chip">结构对比</span>
            <span className="meta-chip">表格视图</span>
            <span className="meta-chip">Shell</span>
          </div>
        </div>
        <div className="page-hero-side">
          <div className="hero-stat-grid">
            <article className="hero-stat">
              <span className="hero-stat-label">当前模块</span>
              <strong className="hero-stat-value">{activeModeLabel}</strong>
            </article>
            <article className="hero-stat">
              <span className="hero-stat-label">可用能力</span>
              <strong className="hero-stat-value">6</strong>
            </article>
            <article className="hero-stat hero-stat-wide">
              <span className="hero-stat-label">当前工作说明</span>
              <strong className="hero-stat-value">{activeModeDescription}</strong>
            </article>
          </div>
        </div>
      </div>

      <section className="mode-strip" aria-label="MongoDB JSON 模块切换">
        <div className="mode-strip-copy">
          <p className="mode-strip-label">Modules</p>
          <p className="mode-strip-text">主操作区只展示当前模块，避免对比、表格、Shell 和字符串处理互相串场。</p>
        </div>
        <div className="mode-switch" role="tablist" aria-label="工具模式">
          {[
            ['format', '格式化'],
            ['diff', '对比'],
            ['table', '表格'],
            ['shell', 'Shell'],
            ['escape', '转义'],
            ['unescape', '还原'],
          ].map(([nextMode, label]) => (
            <button
              aria-selected={mode === nextMode}
              className={`mode-switch-button${mode === nextMode ? ' mode-switch-button-active' : ''}`}
              key={nextMode}
              onClick={() => setMode(nextMode as MongoMode)}
              role="tab"
              type="button"
            >
              {label}
            </button>
          ))}
        </div>
      </section>

      <section className="context-strip" aria-label="当前上下文">
        <div className="context-strip-copy">
          <p className="context-strip-label">Current Context</p>
          <div className="context-breadcrumb" role="list">
            {contextTrail.crumb.map((item, index) => (
              <span className="context-breadcrumb-item" key={`${item}-${index}`} role="listitem">
                {index > 0 ? <span className="context-breadcrumb-separator">/</span> : null}
                <span>{item}</span>
              </span>
            ))}
          </div>
          <p className="context-strip-text">{contextTrail.helper}</p>
        </div>
        <div className="context-strip-actions">
          {mode === 'diff' && diffFocus ? (
            <button className="button button-ghost button-sm" onClick={() => setDiffFocus(null)} type="button">
              清除当前路径
            </button>
          ) : null}
          {mode === 'diff' && !diffFocus && primaryDiffPath ? (
            <button className="button button-ghost button-sm" onClick={() => jumpToDiffPath(primaryDiffPath)} type="button">
              跳到首个差异
            </button>
          ) : null}
          {mode === 'table' && tableQuery ? (
            <button className="button button-ghost button-sm" onClick={() => setTableQuery('')} type="button">
              清除字段筛选
            </button>
          ) : null}
          {mode === 'table' && tableTypeFilter !== 'all' ? (
            <button className="button button-ghost button-sm" onClick={() => setTableTypeFilter('all')} type="button">
              重置类型筛查
            </button>
          ) : null}
          {mode === 'table' && tableData && selectedRow > 0 ? (
            <button className="button button-ghost button-sm" onClick={() => setSelectedRow(0)} type="button">
              回到首行
            </button>
          ) : null}
          {mode === 'shell' && shellFocus ? (
            <button className="button button-ghost button-sm" onClick={() => setShellFocus(null)} type="button">
              清除当前定位
            </button>
          ) : null}
        </div>
      </section>

      {mode === 'format' ? (
        <Panel
          actions={
            <button className="button button-primary" onClick={runFormat} type="button">
              执行格式化
            </button>
          }
          eyebrow="MongoDB JSON"
          subtitle="针对扩展类型的宽松解析与格式化。"
          title="格式化工作区"
        >
          <div className="editor-split">
            <div className="editor-pane">
              <div className="editor-pane-header">
                <span className="editor-pane-title">Input</span>
                <div className="editor-pane-actions">
                  <button className="button button-ghost button-sm" onClick={() => copyText(input, 'format-input', '已复制输入内容。')} type="button">
                    {copied === 'format-input' ? '已复制' : '复制输入'}
                  </button>
                </div>
              </div>
              <CodeEditor language="javascript" onChange={setInput} value={input} />
            </div>
            <ResultPane
              actions={
                <button
                  className="button button-ghost button-sm"
                  onClick={() => copyText(output, 'format-output', '已复制格式化结果。')}
                  type="button"
                >
                  {copied === 'format-output' ? '已复制' : '复制结果'}
                </button>
              }
              language="javascript"
              placeholder="执行格式化后，结果会出现在这里。"
              title="Output"
              value={output}
            />
          </div>
          <StatusBanner
            right={`字符 ${stats.chars} · 行数 ${stats.lines} · 深度 ${stats.depth}`}
            status={status}
          />
        </Panel>
      ) : null}

      {mode === 'diff' ? (
        <Panel
          actions={
            <button className="button button-ghost" onClick={() => setDiffFocus(null)} type="button">
              清除定位
            </button>
          }
          eyebrow="Compare"
          subtitle="按字段排序后对比两份 MongoDB JSON，聚焦字段新增、缺失和数值变化，并支持从摘要跳到对应字段。"
          title="结构对比"
        >
          <DiffEditorPanel
            focus={diffFocus}
            modified={normalizedDiffRight.text}
            onModifiedChange={setDiffRight}
            onOriginalChange={setDiffLeft}
            original={normalizedDiffLeft.text}
          />
          <StatusBanner
            right={`左侧缺失 ${diffSummary.leftOnly.length} · 右侧新增 ${diffSummary.rightOnly.length} · 值变化 ${diffSummary.changed.length}`}
            status={{ kind: 'success', message: '已按字段排序后展示差异。' }}
          />
          <div className="summary-strip">
            {diffOverview.map((item) => (
              <article className={`summary-tile summary-tile-${item.accent}`} key={item.label}>
                <span className="summary-tile-label">{item.label}</span>
                <strong className="summary-tile-value">{item.value}</strong>
                <span className="summary-tile-helper">{item.helper}</span>
              </article>
            ))}
          </div>
          <div className="workspace-grid">
            <div className="panel">
              <div className="panel-header">
                <div className="panel-header-copy">
                  <div className="panel-eyebrow">Diff Paths</div>
                  <h3 className="panel-title">差异摘要</h3>
                </div>
              </div>
              <div className="stack panel-body-compact">
                <article className="info-card">
                  <p className="info-card-title">仅左侧存在</p>
                  <div className="path-list">
                    {diffSummary.leftOnly.length > 0 ? (
                      diffSummary.leftOnly.map((path) => (
                        <button className="path-chip path-chip-left" key={`left-${path}`} onClick={() => jumpToDiffPath(path, 'left')} type="button">
                          {path}
                        </button>
                      ))
                    ) : (
                      <p className="info-card-text">无</p>
                    )}
                  </div>
                </article>
                <article className="info-card">
                  <p className="info-card-title">仅右侧存在</p>
                  <div className="path-list">
                    {diffSummary.rightOnly.length > 0 ? (
                      diffSummary.rightOnly.map((path) => (
                        <button className="path-chip path-chip-right" key={`right-${path}`} onClick={() => jumpToDiffPath(path, 'right')} type="button">
                          {path}
                        </button>
                      ))
                    ) : (
                      <p className="info-card-text">无</p>
                    )}
                  </div>
                </article>
                <article className="info-card">
                  <p className="info-card-title">值发生变化</p>
                  <div className="path-list">
                    {diffSummary.changed.length > 0 ? (
                      diffSummary.changed.map((path) => (
                        <button className="path-chip path-chip-changed" key={`changed-${path}`} onClick={() => jumpToDiffPath(path)} type="button">
                          {path}
                        </button>
                      ))
                    ) : (
                      <p className="info-card-text">无</p>
                    )}
                  </div>
                </article>
              </div>
            </div>

            <div className="panel">
              <div className="panel-header">
                <div className="panel-header-copy">
                  <div className="panel-eyebrow">Detail</div>
                  <h3 className="panel-title">路径定位说明</h3>
                </div>
              </div>
              <div className="stack panel-body-compact">
                <article className="info-card">
                  <p className="info-card-title">跳转规则</p>
                  <p className="info-card-text">点击路径后，会优先跳到存在该字段的一侧，并将对应行滚动到编辑器中心，形成从摘要到详情的定位链路。</p>
                </article>
                <article className="info-card">
                  <p className="info-card-title">字段路径</p>
                  <p className="info-card-text">对象字段使用 `a.b.c`，数组项使用 `[0]` 的路径形式，便于对应实际 JSON 结构。</p>
                </article>
                <article className="info-card">
                  <p className="info-card-title">当前索引</p>
                  <p className="info-card-text">
                    左侧已索引 {Object.keys(normalizedDiffLeft.keyLineMap).length} 个字段，右侧已索引 {Object.keys(normalizedDiffRight.keyLineMap).length} 个字段，可继续沿字段路径向下排查。
                  </p>
                </article>
              </div>
            </div>
          </div>
        </Panel>
      ) : null}

      {mode === 'table' ? (
        <Panel
          actions={
            <>
              <button className="button button-primary" onClick={runTable} type="button">
                构建表格
              </button>
              <button className="button button-ghost" onClick={() => setTableQuery('')} type="button">
                清空筛选
              </button>
            </>
          }
          eyebrow="Table"
          subtitle="将对象或对象数组展平，查看字段路径、主类型、缺失率和当前文档行预览。"
          title="表格视图"
        >
          <div className="editor-split">
            <div className="editor-pane">
              <div className="editor-pane-header">
                <span className="editor-pane-title">Input</span>
                <div className="editor-pane-actions">
                  <button className="button button-ghost button-sm" onClick={() => copyText(input, 'table-input', '已复制表格输入。')} type="button">
                    {copied === 'table-input' ? '已复制' : '复制输入'}
                  </button>
                </div>
              </div>
              <CodeEditor language="javascript" onChange={setInput} value={input} />
            </div>
            <div className="editor-pane">
              <div className="editor-pane-header">
                <span className="editor-pane-title">Table</span>
                <div className="editor-pane-actions">
                  <input
                    className="field-input field-input-sm"
                    onChange={(event) => setTableQuery(event.target.value)}
                    placeholder="筛选字段"
                    value={tableQuery}
                  />
                </div>
              </div>
              {tableData ? (
                <>
                  <div className="filter-chip-row">
                    <button
                      className={`filter-chip${tableTypeFilter === 'all' ? ' filter-chip-active' : ''}`}
                      onClick={() => setTableTypeFilter('all')}
                      type="button"
                    >
                      全部字段
                    </button>
                    <button
                      className={`filter-chip${tableTypeFilter === 'mixed' ? ' filter-chip-active' : ''}`}
                      onClick={() => setTableTypeFilter('mixed')}
                      type="button"
                    >
                      mixed {tablePreview.mixedCount}
                    </button>
                    <button
                      className={`filter-chip${tableTypeFilter === 'nullable' ? ' filter-chip-active' : ''}`}
                      onClick={() => setTableTypeFilter('nullable')}
                      type="button"
                    >
                      可缺失 {tablePreview.nullableCount}
                    </button>
                  </div>
                  {tableHasNoResults ? (
                    <div className="inline-empty-state">
                      <p className="inline-empty-state-title">当前筛查没有命中字段</p>
                      <p className="inline-empty-state-text">
                        {tableQuery ? `已按关键词 "${tableQuery}" ` : '已按当前条件 '}
                        {tableTypeFilter === 'mixed' ? '和 mixed 类型' : tableTypeFilter === 'nullable' ? '和可缺失字段' : ''}
                        进行筛查。可以清空筛选，或切换到其他字段类型继续查看。
                      </p>
                    </div>
                  ) : (
                    <div className="table-wrap">
                      <table className="data-table">
                        <thead>
                          <tr>
                            <th>字段路径</th>
                            <th>主类型</th>
                            <th>缺失率</th>
                          </tr>
                        </thead>
                        <tbody>
                          {tablePreview.filteredSchema.slice(0, 32).map((column) => (
                            <tr className={tablePreview.selectedCells[0]?.path === column.path ? 'row-highlight' : ''} key={column.path}>
                              <td>
                                <code>{column.path}</code>
                              </td>
                              <td>{column.isMixed ? `mixed (${Object.keys(column.typeCounts).join('/')})` : column.dominantType}</td>
                              <td>{Math.round(column.nullRatio * 100)}%</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                  <div className="table-caption">
                    当前过滤命中 {tablePreview.filteredSchema.length} 个字段，字段列表与右侧文档行预览同步更新。
                  </div>
                </>
              ) : (
                <div className="empty-state">构建表格后，这里会展示展平后的字段结构。</div>
              )}
            </div>
          </div>
          <StatusBanner
            right={tableData ? `字段 ${tablePreview.filteredSchema.length}/${tableData.schema.length} · 文档 ${tableData.docCount}` : '等待构建'}
            status={status}
          />
          {tableOverview ? (
            <div className="summary-strip">
              {tableOverview.map((item) => (
                <article className={`summary-tile summary-tile-${item.accent}`} key={item.label}>
                  <span className="summary-tile-label">{item.label}</span>
                  <strong className="summary-tile-value">{item.value}</strong>
                  <span className="summary-tile-helper">{item.helper}</span>
                </article>
              ))}
            </div>
          ) : null}
          {tableData ? (
            <div className="workspace-grid">
              <div className="panel">
                <div className="panel-header">
                  <div className="panel-header-copy">
                    <div className="panel-eyebrow">Validation</div>
                    <h3 className="panel-title">表结构检查</h3>
                  </div>
                </div>
                <div className="stack panel-body-compact">
                  {tableData.validation.map((item, index) => (
                    <article className="info-card" key={`${item.msg}-${index}`}>
                      <p className="info-card-title">{item.level.toUpperCase()}</p>
                      <p className="info-card-text">{item.msg}</p>
                    </article>
                  ))}
                </div>
              </div>

              <div className="panel">
                <div className="panel-header">
                  <div className="panel-header-copy">
                    <div className="panel-eyebrow">Row Preview</div>
                    <h3 className="panel-title">文档行预览</h3>
                  </div>
                  <div className="toolbar">
                    <button
                      className="button button-ghost button-sm"
                      disabled={selectedRow <= 0}
                      onClick={() => setSelectedRow((value) => Math.max(0, value - 1))}
                      type="button"
                    >
                      上一行
                    </button>
                    <button
                      className="button button-ghost button-sm"
                      disabled={selectedRow >= tableData.docCount - 1}
                      onClick={() => setSelectedRow((value) => Math.min(tableData.docCount - 1, value + 1))}
                      type="button"
                    >
                      下一行
                    </button>
                  </div>
                </div>
                <div className="table-wrap">
                  {tablePreview.selectedCells.length > 0 ? (
                    <table className="data-table">
                      <thead>
                        <tr>
                          <th>字段</th>
                          <th>当前行值</th>
                        </tr>
                      </thead>
                      <tbody>
                        {tablePreview.selectedCells.map((cell, index) => (
                          <tr className={index === 0 ? 'row-highlight row-highlight-strong' : ''} key={cell.path}>
                            <td>
                              <code>{cell.path}</code>
                            </td>
                            <td>
                              <code>{cell.value}</code>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  ) : (
                    <div className="inline-empty-state">
                      <p className="inline-empty-state-title">当前行没有可预览字段</p>
                      <p className="inline-empty-state-text">修改字段筛选后，这里会同步展示当前文档行的命中字段值。</p>
                    </div>
                  )}
                </div>
                <div className="table-caption">
                  当前第 {selectedRow + 1} 行，共 {tableData.docCount} 行。使用上下按钮可以逐行 drill down。
                </div>
              </div>
            </div>
          ) : null}
        </Panel>
      ) : null}

      {mode === 'shell' ? (
        <Panel
          actions={
            <>
              <button className="button button-primary" onClick={runShell} type="button">
                格式化 Shell
              </button>
              <button className="button button-ghost" onClick={() => setShellFocus(null)} type="button">
                清除定位
              </button>
            </>
          }
          eyebrow="Shell"
          subtitle="识别 MongoDB Shell 语句，整理参数缩进，并把集合、方法链和操作符拆成可定位摘要。"
          title="Shell 工作区"
        >
        <div className="editor-split">
          <div className="editor-pane">
            <div className="editor-pane-header">
              <span className="editor-pane-title">Shell Input</span>
              <div className="editor-pane-actions">
                <button className="button button-ghost button-sm" onClick={() => copyText(shellInput, 'shell-input', '已复制 Shell 输入。')} type="button">
                  {copied === 'shell-input' ? '已复制' : '复制输入'}
                </button>
              </div>
            </div>
            <CodeEditor focusLine={shellFocus?.line ?? null} language="javascript" onChange={setShellInput} value={shellInput} />
          </div>
          <ResultPane
            actions={
              <button
                className="button button-ghost button-sm"
                onClick={() => copyText(shellOutput, 'shell-output', '已复制 Shell 结果。')}
                type="button"
              >
                {copied === 'shell-output' ? '已复制' : '复制结果'}
              </button>
            }
            language="javascript"
            placeholder="执行 Shell 格式化后，结果会出现在这里。"
            title="Formatted"
            value={shellOutput}
          />
        </div>
          {shellOverview ? (
          <div className="summary-strip">
            {shellOverview.map((item) => (
              <article
                className={`summary-tile summary-tile-${item.accent}${
                  (item.label === '集合' && shellFocus?.kind === 'collection') ||
                  (item.label === '方法链' && shellFocus?.kind === 'method') ||
                  (item.label === '操作符' && shellFocus?.kind === 'operator')
                    ? ' summary-tile-active'
                    : ''
                }`}
                key={item.label}
              >
                <span className="summary-tile-label">{item.label}</span>
                <strong className="summary-tile-value">{item.value}</strong>
                <span className="summary-tile-helper">{item.helper}</span>
              </article>
            ))}
          </div>
        ) : null}
        {parsedShell ? (
          <div className="workspace-grid">
            <div className="panel">
              <div className="panel-header">
                <div className="panel-header-copy">
                  <div className="panel-eyebrow">Summary</div>
                  <h3 className="panel-title">结构化摘要</h3>
                </div>
              </div>
              <div className="stack panel-body-compact">
                <article className="info-card">
                  <p className="info-card-title">集合名</p>
                  <div className="path-list">
                    <button className="path-chip path-chip-right" onClick={() => jumpToShellOffset(parsedShell.collectionStart, parsedShell.collection, 'collection')} type="button">
                      {parsedShell.collection}
                    </button>
                  </div>
                </article>
                <article className="info-card">
                  <p className="info-card-title">方法链</p>
                  <div className="path-list">
                    {parsedShell.methods.map((method, index) => (
                      <button
                        className="path-chip"
                        key={`${method.name}-${method.nameStart}-${index}`}
                        onClick={() => jumpToShellOffset(method.nameStart, method.name, 'method')}
                        type="button"
                      >
                        {index + 1}. {method.name}({method.argsRaw.length})
                      </button>
                    ))}
                  </div>
                </article>
                <article className="info-card">
                  <p className="info-card-title">操作符</p>
                  <div className="path-list">
                    {parsedShell.operators.length > 0 ? (
                      parsedShell.operators.map((operator, index) => (
                        <button
                          className="path-chip path-chip-changed"
                          key={`${operator.name}-${operator.pos}-${index}`}
                          onClick={() => jumpToShellOffset(operator.pos, operator.name, 'operator')}
                          type="button"
                        >
                          {operator.name}
                        </button>
                      ))
                    ) : (
                      <p className="info-card-text">当前语句未识别出操作符。</p>
                    )}
                  </div>
                </article>
              </div>
            </div>

            <div className="panel">
              <div className="panel-header">
                <div className="panel-header-copy">
                  <div className="panel-eyebrow">Detail</div>
                  <h3 className="panel-title">方法明细</h3>
                </div>
              </div>
              <div className="table-wrap">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>方法</th>
                      <th>参数数</th>
                      <th>参数预览</th>
                    </tr>
                  </thead>
                  <tbody>
                    {parsedShell.methods.map((method, index) => (
                      <tr key={`${method.name}-${method.nameStart}-${index}`}>
                        <td>
                          <button className="path-link" onClick={() => jumpToShellOffset(method.nameStart, method.name, 'method')} type="button">
                            {method.name}
                          </button>
                        </td>
                        <td>{method.argsRaw.length}</td>
                        <td>
                          <code>
                            {method.argsRaw.length > 0
                              ? method.argsRaw
                                  .map((item) => item.text.replace(/\s+/g, ' ').slice(0, 48))
                                  .join(' | ')
                              : '无参数'}
                          </code>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="table-caption">
                已识别 {parsedShell.methods.length} 个方法调用和 {parsedShell.operators.length} 个操作符，可从摘要直接跳到输入区。
              </div>
            </div>
          </div>
        ) : null}
        <div className="card-grid">
          {shellChecks.length > 0 ? (
            shellChecks.map((item, index) => (
              <article className="info-card" key={`${item.msg}-${index}`}>
                <p className="info-card-title">{item.level.toUpperCase()}</p>
                <p className="info-card-text">{item.msg}</p>
              </article>
            ))
          ) : (
            <article className="info-card">
              <p className="info-card-title">校验结果</p>
              <p className="info-card-text">执行后会展示集合、方法和潜在操作符告警。</p>
            </article>
          )}
        </div>
        <StatusBanner status={status} />
      </Panel>
      ) : null}

      {mode === 'escape' || mode === 'unescape' ? (
        <Panel
          actions={
            <button className="button button-primary" onClick={runEscape} type="button">
              {mode === 'escape' ? '执行转义' : '执行还原'}
            </button>
          }
          eyebrow="String"
          subtitle="显式区分转义与还原动作，不再复用含义不清的全局按钮。"
          title={mode === 'escape' ? '转义 JSON' : '还原 JSON'}
        >
          <div className="editor-split">
            <div className="editor-pane">
              <div className="editor-pane-header">
                <span className="editor-pane-title">Input</span>
                <div className="editor-pane-actions">
                  <button className="button button-ghost button-sm" onClick={() => copyText(escapeInput, 'escape-input', '已复制输入内容。')} type="button">
                    {copied === 'escape-input' ? '已复制' : '复制输入'}
                  </button>
                </div>
              </div>
              <CodeEditor language="javascript" onChange={setEscapeInput} value={escapeInput} />
            </div>
            <ResultPane
              actions={
                <button
                  className="button button-ghost button-sm"
                  onClick={() => copyText(escapeOutput, 'escape-output', '已复制转换结果。')}
                  type="button"
                >
                  {copied === 'escape-output' ? '已复制' : '复制结果'}
                </button>
              }
              language="javascript"
              placeholder="执行后会显示转换结果。"
              title="Output"
              value={escapeOutput}
            />
          </div>
          <StatusBanner status={status} />
        </Panel>
      ) : null}
    </div>
  )
}
