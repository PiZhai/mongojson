import { useEffect, useMemo, useState } from 'react'
import { getPresets, savePreset } from '../../lib/api/client'
import { summarizeRows, tableDataToRows } from '../../lib/tooling/charting'
import { buildTableFromAst, formatJson } from '../../lib/tooling/jsonFormatter'
import type { ChartSeriesRow, ToolStatus } from '../../types/tooling'
import { Panel } from '../common/Panel'
import { StatusBanner } from '../common/StatusBanner'
import { CodeEditor } from '../editor/CodeEditor'

const visualizationSample = `[
  { "month": "Jan", "orders": 42, "revenue": 1180 },
  { "month": "Feb", "orders": 51, "revenue": 1460 },
  { "month": "Mar", "orders": 49, "revenue": 1375 },
  { "month": "Apr", "orders": 63, "revenue": 1822 }
]`

function formatChartValue(value: number) {
  if (Math.abs(value) >= 1000) return value.toLocaleString()
  return String(value)
}

export function VisualizationWorkspace() {
  const [input, setInput] = useState(visualizationSample)
  const [status, setStatus] = useState<ToolStatus>({ kind: 'idle', message: '输入 JSON 后生成图表。' })
  const [rows, setRows] = useState<ChartSeriesRow[]>([])
  const [xKey, setXKey] = useState('month')
  const [yKey, setYKey] = useState('orders')
  const [chartType, setChartType] = useState<'bar' | 'line'>('bar')
  const [presets, setPresets] = useState<Array<{ id: string; name: string; payload: Record<string, unknown> }>>([])

  const summary = useMemo(() => summarizeRows(rows), [rows])
  const chartData = useMemo(() => {
    const values = rows.map((row) => {
      const value = Number(row[yKey] ?? 0)
      return Number.isFinite(value) ? value : 0
    })
    const max = Math.max(...values, 0)
    const scaleMax = max > 0 ? max : 1
    const width = 720
    const height = 300
    const left = 48
    const right = 24
    const top = 24
    const bottom = 48
    const plotWidth = width - left - right
    const plotHeight = height - top - bottom
    const step = values.length > 0 ? plotWidth / values.length : plotWidth
    const barWidth = Math.max(12, Math.min(46, step * 0.58))
    const points = values.map((value, index) => {
      const x = left + step * index + step / 2
      const y = top + plotHeight - (value / scaleMax) * plotHeight
      return { x, y, value, label: String(rows[index]?.[xKey] ?? '') }
    })

    return {
      axisMaxLabel: formatChartValue(max),
      barWidth,
      bottom,
      height,
      left,
      linePath: points.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' '),
      plotHeight,
      step,
      top,
      width,
      points,
    }
  }, [rows, xKey, yKey])

  useEffect(() => {
    void (async () => {
      try {
        const response = await getPresets('visualize')
        setPresets(response.presets ?? [])
      } catch {
        setPresets([])
      }
    })()
  }, [])

  const run = () => {
    const result = formatJson(input, false)
    if ('error' in result) {
      setRows([])
      setStatus({ kind: 'error', message: result.error })
      return
    }
    const table = buildTableFromAst(result.ast)
    if (!table) {
      setRows([])
      setStatus({ kind: 'warning', message: '当前 JSON 无法映射为表格数据，建议输入对象数组。' })
      return
    }
    const nextRows = tableDataToRows(table)
    const meta = summarizeRows(nextRows)
    setRows(nextRows)
    setXKey(meta.dimensionKeys[0] ?? meta.keys[0] ?? '')
    setYKey(meta.numericKeys[0] ?? meta.keys[1] ?? meta.keys[0] ?? '')
    setStatus({ kind: 'success', message: `图表数据已就绪，共 ${nextRows.length} 行。` })
  }

  return (
    <div className="page-shell">
      <Panel
        actions={
          <>
            <button className="button button-primary" onClick={run} type="button">
              生成图表
            </button>
            <button
              className="button"
              onClick={async () => {
                if (!xKey || !yKey) return
                await savePreset({
                  tool_type: 'visualize',
                  name: `chart-${Date.now()}`,
                  payload: { xKey, yKey, chartType },
                })
                const response = await getPresets('visualize')
                setPresets(response.presets ?? [])
                setStatus({ kind: 'success', message: '图表配置已保存为预设。' })
              }}
              type="button"
            >
              保存预设
            </button>
            {rows.length > 0 ? (
              <button className="button button-ghost" onClick={() => setChartType((value) => (value === 'bar' ? 'line' : 'bar'))} type="button">
                {chartType === 'bar' ? '折线图' : '柱状图'}
              </button>
            ) : null}
          </>
        }
        eyebrow="Visualize"
        title="图表配置"
      >
        <div className="workspace-grid">
          <div className="panel">
            <div className="panel-header">
              <div className="panel-header-copy">
                <div className="panel-eyebrow">Input</div>
                <h3 className="panel-title">原始数据</h3>
              </div>
            </div>
            <CodeEditor language="json" onChange={setInput} value={input} />
          </div>

          <div className="stack">
            <div className="panel">
              <div className="panel-header">
                <div className="panel-header-copy">
                  <div className="panel-eyebrow">Config</div>
                  <h3 className="panel-title">维度映射</h3>
                </div>
              </div>
              <div className="stack" style={{ padding: 16 }}>
                <label className="field-label">
                  <span>X 轴字段</span>
                  <select className="select" onChange={(event) => setXKey(event.target.value)} value={xKey}>
                    {summary.keys.map((key) => (
                      <option key={key} value={key}>
                        {key}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field-label">
                  <span>Y 轴字段</span>
                  <select className="select" onChange={(event) => setYKey(event.target.value)} value={yKey}>
                    {summary.numericKeys.map((key) => (
                      <option key={key} value={key}>
                        {key}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field-label">
                  <span>图表类型</span>
                  <select className="select" onChange={(event) => setChartType(event.target.value as 'bar' | 'line')} value={chartType}>
                    <option value="bar">柱状图</option>
                    <option value="line">折线图</option>
                  </select>
                </label>
                <label className="field-label">
                  <span>已保存预设</span>
                  <select
                    className="select"
                    onChange={(event) => {
                      const selected = presets.find((item) => item.id === event.target.value)
                      if (!selected) return
                      const nextXKey = String(selected.payload.xKey ?? xKey)
                      const nextYKey = String(selected.payload.yKey ?? yKey)
                      const nextChartType = selected.payload.chartType === 'line' ? 'line' : 'bar'
                      setXKey(nextXKey)
                      setYKey(nextYKey)
                      setChartType(nextChartType)
                      setStatus({ kind: 'success', message: `已应用预设 ${selected.name}。` })
                    }}
                    value=""
                  >
                    <option value="">选择预设</option>
                    {presets.map((preset) => (
                      <option key={preset.id} value={preset.id}>
                        {preset.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
            </div>

            <div className="panel">
              <div className="panel-header">
                <div className="panel-header-copy">
                  <div className="panel-eyebrow">Chart</div>
                  <h3 className="panel-title">预览</h3>
                </div>
              </div>
              <div className="chart-host">
                {rows.length > 0 && xKey && yKey ? (
                  <svg aria-label={`${xKey} 到 ${yKey} 的${chartType === 'line' ? '折线图' : '柱状图'}`} className="chart-svg" role="img" viewBox={`0 0 ${chartData.width} ${chartData.height}`}>
                    <line className="chart-axis" x1={chartData.left} x2={chartData.width - 24} y1={chartData.top + chartData.plotHeight} y2={chartData.top + chartData.plotHeight} />
                    <line className="chart-axis" x1={chartData.left} x2={chartData.left} y1={chartData.top} y2={chartData.top + chartData.plotHeight} />
                    <text className="chart-axis-label" x={chartData.left - 8} y={chartData.top + 8}>
                      {chartData.axisMaxLabel}
                    </text>
                    {chartType === 'line' ? (
                      <>
                        <path className="chart-line" d={chartData.linePath} />
                        {chartData.points.map((point) => (
                          <circle className="chart-point" cx={point.x} cy={point.y} key={`${point.label}-${point.x}`} r="4">
                            <title>{`${point.label}: ${formatChartValue(point.value)}`}</title>
                          </circle>
                        ))}
                      </>
                    ) : (
                      chartData.points.map((point) => (
                        <rect
                          className="chart-bar"
                          height={chartData.top + chartData.plotHeight - point.y}
                          key={`${point.label}-${point.x}`}
                          rx="4"
                          width={chartData.barWidth}
                          x={point.x - chartData.barWidth / 2}
                          y={point.y}
                        >
                          <title>{`${point.label}: ${formatChartValue(point.value)}`}</title>
                        </rect>
                      ))
                    )}
                    {chartData.points.map((point, index) => (
                      <text className="chart-label" key={`${point.label}-${index}`} textAnchor="middle" x={point.x} y={chartData.height - 18}>
                        {point.label}
                      </text>
                    ))}
                  </svg>
                ) : (
                  <div className="inline-empty-state">
                    <p className="inline-empty-state-title">暂无图表</p>
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>
        <StatusBanner
          right={`字段 ${summary.keys.length} · 数值列 ${summary.numericKeys.length}`}
          status={status}
        />
      </Panel>
    </div>
  )
}
