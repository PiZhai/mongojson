import { useMemo, useState } from 'react'
import {
  astNodeToDisplay,
  buildTableFromAst,
  getFieldDiffSummary,
  parseShellStatement,
} from '../../../lib/tooling/jsonFormatter'
import {
  escapeMongoJsonString,
  formatMongoJson,
  formatMongoShell,
  normalizeMongoForCompare,
  repairStandardJson,
  unescapeMongoJsonString,
  validateMongoShell,
} from '../../../lib/mongodb-core'
import { inspectMongoQuery } from '../../../lib/tooling/mongoInspector'
import { buildSchemaProfile, generateSchema } from '../../../lib/tooling/schemaProfile'
import { formatJsonPatch, getSemanticDiff } from '../../../lib/tooling/semanticDiff'
import { readWorkspaceTransfer } from '../../../lib/tooling/workspaceTransfer'
import type { DiffSummary, ShellValidation, TableData, ToolStatus } from '../../../types/tooling'
import type { MongoDiagnostic } from '../../../lib/mongodb-core'
import { diffSampleRight, mongoSample, shellSample } from './samples'
import type { DiffFocus, InputHint, MongoMode, ShellFocus, SummaryTile, TableTypeFilter } from './types'
import { formatParseMessage, mapHintTone, modeLabels } from './modeMeta'
import { useCopyFeedback } from './useCopyFeedback'

export function useMongoJsonWorkspaceState(mode: MongoMode) {
  const transfer = useMemo(() => readWorkspaceTransfer('mongodb-json', mode), [mode])
  const [input, setInput] = useState(() => (transfer && (mode === 'format' || mode === 'table') ? transfer.input : mongoSample))
  const [output, setOutput] = useState('')
  const [extendedJsonOutput, setExtendedJsonOutput] = useState('')
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '等待执行 MongoDB JSON 工具操作。' })
  const [stats, setStats] = useState({ chars: 0, lines: 0, depth: 0 })
  const [diffLeft, setDiffLeft] = useState(() => (transfer && mode === 'diff' ? transfer.input : mongoSample))
  const [diffRight, setDiffRight] = useState(diffSampleRight)
  const [tableData, setTableData] = useState<TableData | null>(null)
  const [repairInput, setRepairInput] = useState(() => (transfer && mode === 'repair' ? transfer.input : '{ name: "Ada", trailing: true, }'))
  const [repairOutput, setRepairOutput] = useState('')
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
  const normalizedDiffLeft = useMemo(() => normalizeMongoForCompare(diffLeft), [diffLeft])
  const normalizedDiffRight = useMemo(() => normalizeMongoForCompare(diffRight), [diffRight])
  const parsedShell = useMemo(() => parseShellStatement(shellInput), [shellInput])
  const formatInputCheck = useMemo(() => formatMongoJson(input, false), [input])
  const tableInputCheck = useMemo(() => formatMongoJson(input, false), [input])
  const repairInputCheck = useMemo(() => repairStandardJson(repairInput), [repairInput])
  const escapeInputCheck = useMemo(() => formatMongoJson(escapeInput, false), [escapeInput])
  const shellInputCheck = useMemo(() => {
    const parsed = parseShellStatement(shellInput)
    const validations = validateMongoShell(shellInput)
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
    const result = formatMongoJson(input, false)
    return result.ok && result.ast ? buildSchemaProfile(result.ast) : null
  }, [input, tableData])
  const generatedSchema = useMemo(() => {
    if (!schemaProfile) return null
    return generateSchema(schemaProfile, generatedSchemaTarget)
  }, [generatedSchemaTarget, schemaProfile])
  const mongoInspection = useMemo(() => inspectMongoQuery(shellInput), [shellInput])
  const formattedJsonPatch = useMemo(() => formatJsonPatch(semanticDiff.patch), [semanticDiff.patch])
  const inputDiagnostics = useMemo<MongoDiagnostic[]>(() => {
    if (mode === 'format') return formatInputCheck.diagnostics
    if (mode === 'table') return tableInputCheck.diagnostics
    if (mode === 'repair') return repairInputCheck.diagnostics
    if (mode === 'escape' || mode === 'unescape') return escapeInputCheck.diagnostics
    return []
  }, [escapeInputCheck.diagnostics, formatInputCheck.diagnostics, mode, repairInputCheck.diagnostics, tableInputCheck.diagnostics])

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

  const liveStatus = useMemo((): ToolStatus => {
    if (mode === 'format') {
      if (!formatInputCheck.ok) {
        return {
          kind: 'error',
          message: formatParseMessage(formatInputCheck.diagnostics[0]?.message ?? '输入解析失败', '输入解析失败：', formatInputCheck.diagnostics[0]?.offset),
        }
      }
      if (formatInputCheck.diagnostics.some((diagnostic) => diagnostic.severity === 'warning')) {
        return { kind: 'warning', message: formatInputCheck.diagnostics[0]?.message ?? '输入已通过兼容解析器解析。' }
      }
      return { kind: 'success', message: '输入已通过 MongoDB JSON 解析，可执行格式化。' }
    }

    if (mode === 'table') {
      if (!tableInputCheck.ok) {
        return {
          kind: 'error',
          message: formatParseMessage(tableInputCheck.diagnostics[0]?.message ?? '输入解析失败', '输入解析失败：', tableInputCheck.diagnostics[0]?.offset),
        }
      }
      if (tableInputCheck.diagnostics.some((diagnostic) => diagnostic.severity === 'warning')) {
        return { kind: 'warning', message: tableInputCheck.diagnostics[0]?.message ?? '输入已通过兼容解析器解析。' }
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

    if (mode === 'repair') {
      if (!repairInput.trim()) {
        return { kind: 'idle', message: '等待输入需要修复的 JSON 文本。' }
      }
      if (!repairInputCheck.ok) {
        return {
          kind: 'error',
          message: formatParseMessage(repairInputCheck.diagnostics[0]?.message ?? '修复失败', '修复失败：', repairInputCheck.diagnostics[0]?.offset),
        }
      }
      return { kind: 'success', message: '输入可以修复为标准 JSON。' }
    }

    if (mode === 'escape' || mode === 'unescape') {
      if (!escapeInput.trim()) {
        return { kind: 'idle', message: '等待输入 MongoDB JSON 文本。' }
      }
      if (mode === 'escape') {
        if (!escapeInputCheck.ok) {
          return {
            kind: 'error',
            message: formatParseMessage(escapeInputCheck.diagnostics[0]?.message ?? '输入解析失败', '输入解析失败：', escapeInputCheck.diagnostics[0]?.offset),
          }
        }
        return { kind: 'success', message: '输入已通过 MongoDB JSON 解析，可执行转义。' }
      }

      try {
        const unescaped = unescapeMongoJsonString(escapeInput)
        if (unescaped.error) {
          return { kind: 'error', message: `输入还原失败：${unescaped.error}` }
        }
        const parsed = formatMongoJson(unescaped.output ?? '', false)
        if (!parsed.ok) {
          return {
            kind: 'warning',
            message: formatParseMessage(parsed.diagnostics[0]?.message ?? '还原后无法解析', '字符串可还原，但还原后的 MongoDB JSON 无法解析：', parsed.diagnostics[0]?.offset),
          }
        }
        return { kind: 'success', message: '输入可还原且还原后的 MongoDB JSON 结构有效。' }
      } catch {
        return { kind: 'error', message: '当前输入不是可还原的字符串内容。' }
      }
    }

    return status
  }, [escapeInput, escapeInputCheck, formatInputCheck, mode, repairInput, repairInputCheck, shellInput, shellInputCheck, status, tableInputCheck])

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

    if (mode === 'repair') {
      return {
        tone: mapHintTone(liveStatus.kind),
        text: liveStatus.kind === 'error' ? liveStatus.message : '可以修复为标准 JSON。',
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
    const result = formatMongoJson(input, false)
    if (!result.ok) {
      const message = result.diagnostics[0]?.message ?? 'MongoDB JSON 解析失败。'
      setOutput(message)
      setExtendedJsonOutput('')
      setStatus({ kind: 'error', message })
      setStats({ chars: 0, lines: 0, depth: 0 })
      return
    }
    setOutput(result.text)
    setExtendedJsonOutput(result.extendedJson ?? '')
    setStatus({
      kind: result.diagnostics.some((diagnostic) => diagnostic.severity === 'warning') ? 'warning' : 'success',
      message: result.extendedJson ? 'MongoDB JSON 已格式化，可复制 Canonical Extended JSON。' : 'MongoDB JSON 已通过兼容解析器格式化。',
    })
    setStats({ chars: result.stats?.chars ?? 0, lines: result.stats?.lines ?? 0, depth: result.stats?.maxDepth ?? 0 })
  }

  const runTable = () => {
    const result = formatMongoJson(input, false)
    if (!result.ok || !result.ast) {
      setTableData(null)
      setStatus({ kind: 'error', message: result.diagnostics[0]?.message ?? '当前输入无法解析。' })
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
    const action = mode === 'escape' ? escapeMongoJsonString : unescapeMongoJsonString
    const result = action(escapeInput)
    if (result.error) {
      setEscapeOutput(result.error)
      setStatus({ kind: 'error', message: result.error })
      return
    }
    setEscapeOutput(result.output ?? '')
    setStatus({ kind: 'success', message: mode === 'escape' ? '已完成转义。' : '已完成还原。' })
  }

  const runRepair = () => {
    const result = repairStandardJson(repairInput)
    if (!result.ok) {
      const message = result.diagnostics[0]?.message ?? '修复失败。'
      setRepairOutput(message)
      setStatus({ kind: 'error', message })
      return
    }
    setRepairOutput(result.text)
    setStatus({ kind: 'success', message: '已修复为标准 JSON。' })
  }

  const runShell = () => {
    const formatted = formatMongoShell(shellInput)
    const validations = validateMongoShell(shellInput)
    setShellChecks(validations)
    if (!formatted.ok) {
      const message = formatted.diagnostics[0]?.message ?? '当前输入不是可识别的 MongoDB Shell 语句。'
      setShellOutput(formatted.text ?? message)
      setStatus({ kind: 'warning', message })
      return
    }
    setShellOutput(formatted.text)
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
    copied,
    copyText,
    diffFocus,
    diffIgnoreInput,
    diffOverview,
    diffSummary,
    escapeInput,
    escapeOutput,
    extendedJsonOutput,
    formattedJsonPatch,
    generatedSchema,
    generatedSchemaTarget,
    input,
    inputDiagnostics,
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
    repairInput,
    repairOutput,
    runEscape,
    runFormat,
    runRepair,
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
    setRepairInput,
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
