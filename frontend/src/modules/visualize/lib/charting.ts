import type { TableData } from '../../../shared/data/types'
import type { ChartSeriesRow } from '../types'
import { astNodeToDisplay } from '../../../lib/tooling/jsonFormatter'

export function tableDataToRows(tableData: TableData): ChartSeriesRow[] {
  return tableData.rows.map((row) => {
    const record: ChartSeriesRow = {}
    row.forEach((cell, index) => {
      const path = tableData.schema[index]?.path ?? `field_${index}`
      const raw = astNodeToDisplay(cell)
      const num = Number(raw)
      record[path] = Number.isFinite(num) && raw !== '' ? num : raw
    })
    return record
  })
}

export function summarizeRows(rows: ChartSeriesRow[]) {
  const first = rows[0] ?? {}
  const keys = Object.keys(first)
  const numericKeys = keys.filter((key) => rows.some((row) => typeof row[key] === 'number'))
  const dimensionKeys = keys.filter((key) => rows.some((row) => typeof row[key] === 'string'))
  return { keys, numericKeys, dimensionKeys }
}
