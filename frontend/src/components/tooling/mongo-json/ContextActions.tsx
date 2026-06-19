import type { DiffFocus, MongoMode, ShellFocus, TableTypeFilter } from './types'

type ContextActionsProps = {
  diffFocus: DiffFocus | null
  mode: MongoMode
  primaryDiffPath: string | null
  selectedRow: number
  shellFocus: ShellFocus | null
  tableDataExists: boolean
  tableQuery: string
  tableTypeFilter: TableTypeFilter
  jumpToDiffPath: (path: string) => void
  setDiffFocus: (focus: DiffFocus | null) => void
  setSelectedRow: (updater: (value: number) => number) => void
  setShellFocus: (focus: ShellFocus | null) => void
  setTableQuery: (value: string) => void
  setTableTypeFilter: (value: TableTypeFilter) => void
}

export function ContextActions({
  diffFocus,
  mode,
  primaryDiffPath,
  selectedRow,
  shellFocus,
  tableDataExists,
  tableQuery,
  tableTypeFilter,
  jumpToDiffPath,
  setDiffFocus,
  setSelectedRow,
  setShellFocus,
  setTableQuery,
  setTableTypeFilter,
}: ContextActionsProps) {
  return (
    <>
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
      {mode === 'table' && tableDataExists && selectedRow > 0 ? (
        <button className="button button-ghost button-sm" onClick={() => setSelectedRow(() => 0)} type="button">
          回到首行
        </button>
      ) : null}
      {mode === 'shell' && shellFocus ? (
        <button className="button button-ghost button-sm" onClick={() => setShellFocus(null)} type="button">
          清除当前定位
        </button>
      ) : null}
    </>
  )
}
