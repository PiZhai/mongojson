import { useMemo, useState } from 'react'
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
} from '../../../lib/tooling/jsonFormatter'
import { inspectMongoQuery } from '../../../lib/tooling/mongoInspector'
import { buildSchemaProfile, generateSchema } from '../../../lib/tooling/schemaProfile'
import { formatJsonPatch, getSemanticDiff } from '../../../lib/tooling/semanticDiff'
import { readWorkspaceTransfer } from '../../../lib/tooling/workspaceTransfer'
import type { DiffSummary, ShellValidation, TableData, ToolStatus } from '../../../types/tooling'
import { diffSampleRight, mongoSample, shellSample } from './samples'
import type { DiffFocus, InputHint, MongoMode, ShellFocus, SummaryTile, TableTypeFilter } from './types'
import { formatParseMessage, mapHintTone, modeLabels } from './modeMeta'
import { useCopyFeedback } from './useCopyFeedback'

export function useMongoJsonWorkspaceState(mode: MongoMode) {
  const transfer = useMemo(() => readWorkspaceTransfer('mongodb-json', mode), [mode])
  const [input, setInput] = useState(() => (transfer && (mode === 'format' || mode === 'table') ? transfer.input : mongoSample))
  const [output, setOutput] = useState('')
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '等待执行 MongoDB JSON 工具操作。' })
  const [stats, setStats] = useState({ chars: 0, lines: 0, depth: 0 })
  const [diffLeft, setDiffLeft] = useState(() => (transfer && mode === 'diff' ? transfer.input : mongoSample))
  const [diffRight, setDiffRight] = useState(diffSampleRight)
  const [tableData, setTableData] = useState<TableData | null>(null)
  const [escapeInput, setEscapeInput] = useState(() => (transfer && (mode === 'escape' || mode === 'unescape') ? transfer.input : mongoSample))
  const [escapeOutput, setEscapeOutput] = useState('')
  const [shellInput, setShellInput] = useState(() => (transfer && mode === 'shell' ? transfer.input : shellSample))
  const [shellOutput, setShellOutput] = useState('')
  const [shellChecks, setShellChecks] = useState<ShellValidation[]>([])
  const [generatedSchemaTarget, setGeneratedSchemaTarget] = useState<'typescript' | 'zod' | 'go'>('typescript')
  const [diffIgnoreInput, setDiffIgnoreInput] = useState('_id, updatedAt')
  const [arrayMatchKey, setArrayMatchKey] = useState('id')
  const [selectedRow, setSelectedRow] = useState(0)
  const [tableQuery, setTableQuery] = useState('')
  const [tableTypeFilter, setTableTypeFilter] = useState<TableTypeFilter>('all')
  const [diffFocus, setDiffFocus] = useState<DiffFocus | null>(null)
  const [shellFocus, setShellFocus] = useState<ShellFocus | null>(null)
  const { copied, copyText } = useCopyFeedback(setStatus)

  const activeModeLabel = modeLabels[mode]
  const normalizedDiffLeft = useMemo(() => normalizeForCompare(diffLeft), [diffLeft])
  const normalizedDiffRight = useMemo(() => normalizeForCompare(diffRight), [diffRight])
  const parsedShell = useMemo(() => parseShellStatement(shellInput), [shellInput])
  const formatInputCheck = useMemo(() => formatJson(input, false), [input])
  const tableInputCheck = useMemo(() => formatJson(input, false), [input])
  const escapeInputCheck = useMemo(() => formatJson(escapeInput, false), [escapeInput])
  const shellInputCheck = useMemo(() => {
    const parsed = parseShellStatement(shellInput)
    const validations = validateShellStatement(shellInput)
    return {
      parsed,
      validations,
      hasError: validations.some((item) => item.level === 'err'),
      hasWarn: validations.some((item) => item.level === 'warn'),
    }
  }, [shellInput])

  const diffSummary = useMemo<DiffSummary>(() => {
    return getFieldDiffSummary(normalizedDiffLeft.ast, normalizedDiffRight.ast)
  }, [normalizedDiffLeft.ast, normalizedDiffRight.ast])
  const semanticDiff = useMemo(() => {
    const ignorePaths = diffIgnoreInput
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)
    return getSemanticDiff(normalizedDiffLeft.ast, normalizedDiffRight.ast, { ignorePaths, arrayMatchKey: arrayMatchKey.trim() })
  }, [arrayMatchKey, diffIgnoreInput, normalizedDiffLeft.ast, normalizedDiffRight.ast])
  const schemaProfile = useMemo(() => {
    if (!tableData) return null
    const result = formatJson(input, false)
    return 'error' in result ? null : buildSchemaProfile(result.ast)
  }, [input, tableData])
  const generatedSchema = useMemo(() => {
    if (!schemaProfile) return null
    return generateSchema(schemaProfile, generatedSchemaTarget)
  }, [generatedSchemaTarget, schemaProfile])
  const mongoInspection = useMemo(() => inspectMongoQuery(shellInput), [shellInput])
  const formattedJsonPatch = useMemo(() => formatJsonPatch(semanticDiff.patch), [semanticDiff.patch])

  const tablePreview = useMemo(() => {
    if (!tableData) {
      return {
        filteredSchema: [],
        selectedCells: [],
        nullableCount: 0,
        mixedCount: 0,
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
        type: column.dominantType,
      }
    })
    const nullableCount = tableData.schema.filter((column) => column.nullRatio > 0).length
    const mixedCount = tableData.schema.filter((column) => column.isMixed).length
    return { filteredSchema, selectedCells, nullableCount, mixedCount }
  }, [selectedRow, tableData, tableQuery, tableTypeFilter])

  const diffOverview: SummaryTile[] = [
    {
      label: '仅左侧',
      value: diffSummary.leftOnly.length,
      helper: diffSummary.leftOnly[0] ?? '无缺失字段',
      accent: 'left',
    },
    {
      label: '仅右侧',
      value: diffSummary.rightOnly.length,
      helper: diffSummary.rightOnly[0] ?? '无新增字段',
      accent: 'right',
    },
    {
      label: '值变化',
      value: diffSummary.changed.length,
      helper: diffSummary.changed[0] ?? '无字段变化',
      accent: 'changed',
    },
  ]

  const tableOverview: SummaryTile[] | null = tableData
    ? [
        {
          label: '字段数',
          value: tableData.schema.length,
          helper: tablePreview.filteredSchema[0]?.path ?? '等待筛选字段',
          accent: 'left',
        },
        {
          label: '筛选命中',
          value: tablePreview.filteredSchema.length,
          helper:
            tableQuery || tableTypeFilter !== 'all'
              ? `${tableQuery ? `词: ${tableQuery}` : '未设关键词'} · ${tableTypeFilter === 'mixed' ? '仅 mixed' : tableTypeFilter === 'nullable' ? '仅可缺失' : '全部类型'}`
              : '全部字段',
          accent: 'right',
        },
        {
          label: '当前行',
          value: `${selectedRow + 1}/${tableData.docCount}`,
          helper: tablePreview.selectedCells[0]?.path ?? '等待行预览',
          accent: 'changed',
        },
      ]
    : null

  const tableHasNoResults = Boolean(tableData) && tablePreview.filteredSchema.length === 0

  const shellOverview: SummaryTile[] | null = parsedShell
    ? [
        {
          label: '集合',
          value: parsedShell.collection,
          helper: `第 ${shellInput.slice(0, parsedShell.collectionStart).split('\n').length} 行`,
          accent: 'left',
        },
        {
          label: '方法链',
          value: parsedShell.methods.length,
          helper: parsedShell.methods.map((item) => item.name).join(' -> ') || '无方法',
          accent: 'right',
        },
        {
          label: '操作符',
          value: parsedShell.operators.length,
          helper: parsedShell.operators.slice(0, 3).map((item) => item.name).join(', ') || '未识别操作符',
          accent: 'changed',
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

  const liveStatus = useMemo((): ToolStatus => {
    if (mode === 'format') {
      if ('error' in formatInputCheck) {
        return {
          kind: 'error',
          message: formatParseMessage(formatInputCheck.error, '输入解析失败：', formatInputCheck.position),
        }
      }
      return { kind: 'success', message: '输入已通过 MongoDB JSON 解析，可执行格式化。' }
    }

    if (mode === 'table') {
      if ('error' in tableInputCheck) {
        return {
          kind: 'error',
          message: formatParseMessage(tableInputCheck.error, '输入解析失败：', tableInputCheck.position),
        }
      }
      return { kind: 'success', message: '输入已通过 MongoDB JSON 解析，可构建表格。' }
    }

    if (mode === 'shell') {
      if (!shellInput.trim()) {
        return { kind: 'idle', message: '等待输入 MongoDB Shell 语句。' }
      }
      if (!shellInputCheck.parsed) {
        return { kind: 'warning', message: '当前输入还不是可识别的 MongoDB Shell 语句。' }
      }
      if (shellInputCheck.hasError) {
        return { kind: 'error', message: shellInputCheck.validations.find((item) => item.level === 'err')?.msg ?? 'Shell 语句存在错误。' }
      }
      if (shellInputCheck.hasWarn) {
        return { kind: 'warning', message: shellInputCheck.validations.find((item) => item.level === 'warn')?.msg ?? 'Shell 语句有警告，请检查。' }
      }
      return { kind: 'success', message: 'Shell 输入结构有效，可执行格式化。' }
    }

    if (mode === 'escape' || mode === 'unescape') {
      if (!escapeInput.trim()) {
        return { kind: 'idle', message: '等待输入 MongoDB JSON 文本。' }
      }
      if (mode === 'escape') {
        if ('error' in escapeInputCheck) {
          return {
            kind: 'error',
            message: formatParseMessage(escapeInputCheck.error, '输入解析失败：', escapeInputCheck.position),
          }
        }
        return { kind: 'success', message: '输入已通过 MongoDB JSON 解析，可执行转义。' }
      }

      try {
        const unescaped = unescapeJsonString(escapeInput)
        if (unescaped.error) {
          return { kind: 'error', message: `输入还原失败：${unescaped.error}` }
        }
        const parsed = formatJson(unescaped.output ?? '', false)
        if ('error' in parsed) {
          return {
            kind: 'warning',
            message: formatParseMessage(parsed.error, '字符串可还原，但还原后的 MongoDB JSON 无法解析：', parsed.position),
          }
        }
        return { kind: 'success', message: '输入可还原且还原后的 MongoDB JSON 结构有效。' }
      } catch {
        return { kind: 'error', message: '当前输入不是可还原的字符串内容。' }
      }
    }

    return status
  }, [escapeInput, escapeInputCheck, formatInputCheck, mode, shellInput, shellInputCheck, status, tableInputCheck])

  const inputHint = useMemo<InputHint | null>(() => {
    if (liveStatus.kind === 'idle') {
      return null
    }

    if (mode === 'format') {
      return {
        tone: mapHintTone(liveStatus.kind),
        text: liveStatus.kind === 'error' ? liveStatus.message : '校验通过，可执行格式化。',
      }
    }

    if (mode === 'table') {
      return {
        tone: mapHintTone(liveStatus.kind),
        text: liveStatus.kind === 'error' ? liveStatus.message : '校验通过，可继续构建表格。',
      }
    }

    if (mode === 'shell') {
      return {
        tone: mapHintTone(liveStatus.kind),
        text: liveStatus.kind === 'success' ? '结构已识别，可执行 Shell 格式化。' : liveStatus.message,
      }
    }

    if (mode === 'escape') {
      return {
        tone: mapHintTone(liveStatus.kind),
        text: liveStatus.kind === 'error' ? liveStatus.message : '校验通过，可执行转义。',
      }
    }

    if (mode === 'unescape') {
      return {
        tone: mapHintTone(liveStatus.kind),
        text: liveStatus.kind === 'success' ? '输入可还原，且还原后的 MongoDB JSON 结构有效。' : liveStatus.message,
      }
    }

    return null
  }, [liveStatus, mode])

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

  const jumpToShellOffset = (offset: number, label: string, kind: ShellFocus['kind']) => {
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

  return {
    activeModeLabel,
    arrayMatchKey,
    contextTrail,
    copied,
    copyText,
    diffFocus,
    diffIgnoreInput,
    diffOverview,
    diffSummary,
    escapeInput,
    escapeOutput,
    formattedJsonPatch,
    generatedSchema,
    generatedSchemaTarget,
    input,
    inputHint,
    jumpToDiffPath,
    jumpToShellOffset,
    liveStatus,
    normalizedDiffLeft,
    normalizedDiffRight,
    output,
    parsedShell,
    primaryDiffPath,
    mongoInspection,
    runEscape,
    runFormat,
    runShell,
    runTable,
    selectedRow,
    setDiffFocus,
    setDiffLeft,
    setDiffRight,
    setDiffIgnoreInput,
    setEscapeInput,
    setGeneratedSchemaTarget,
    setInput,
    setSelectedRow,
    setArrayMatchKey,
    setShellFocus,
    setShellInput,
    setTableQuery,
    setTableTypeFilter,
    shellChecks,
    shellFocus,
    shellInput,
    shellOutput,
    shellOverview,
    schemaProfile,
    semanticDiff,
    stats,
    status,
    tableData,
    tableHasNoResults,
    tableOverview,
    tablePreview,
    tableQuery,
    tableTypeFilter,
  }
}
