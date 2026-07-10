import type { ToolStatus } from '../../../types/tooling'

export type MongoMode = 'format' | 'diff' | 'table' | 'shell' | 'repair' | 'escape' | 'unescape'

export type DiffFocus = {
  side: 'left' | 'right'
  line: number
  key: string
  path: string
}

export type ShellFocus = {
  line: number
  label: string
  kind: 'collection' | 'method' | 'operator'
}

export type TableTypeFilter = 'all' | 'mixed' | 'nullable'

export type InputHint = {
  tone: 'ok' | 'warn' | 'error'
  text: string
}

export type SummaryTile = {
  label: string
  value: string | number
  helper: string
  accent: 'left' | 'right' | 'changed' | 'neutral'
}

export type StatusSetter = (status: ToolStatus) => void
