import { MONGO_LANGUAGE_ID } from '../../../lib/editor/mongoLanguage'
import type { MongoDiagnostic } from '../../../lib/mongodb-core'
import type { GeneratedSchema, GeneratedSchemaTarget, SchemaProfile, TableData } from '../../../shared/data/types'
import type { ToolStatus } from '../../../shared/ui/toolStatus'
import { InputHealthHint } from '../../../components/common/InputHealthHint'
import { Panel } from '../../../components/common/Panel'
import { StatusBanner } from '../../../components/common/StatusBanner'
import { CodeEditor } from '../../../components/editor/CodeEditor'
import type { InputHint, SummaryTile, TableTypeFilter } from './types'

type TablePreview = {
  filteredSchema: TableData['schema']
  selectedCells: Array<{ path: string; value: string; type: string }>
  nullableCount: number
  mixedCount: number
}

type TableModeProps = {
  copied: string | null
  input: string
  inputHint: InputHint | null
  inputDiagnostics: MongoDiagnostic[]
  liveStatus: ToolStatus
  generatedSchema: GeneratedSchema | null
  generatedSchemaTarget: GeneratedSchemaTarget
  runTable: () => void
  schemaProfile: SchemaProfile | null
  selectedRow: number
  setGeneratedSchemaTarget: (target: GeneratedSchemaTarget) => void
  setInput: (value: string) => void
  setSelectedRow: (updater: (value: number) => number) => void
  setTableQuery: (value: string) => void
  setTableTypeFilter: (value: TableTypeFilter) => void
  status: ToolStatus
  tableData: TableData | null
  tableHasNoResults: boolean
  tableOverview: SummaryTile[] | null
  tablePreview: TablePreview
  tableQuery: string
  tableTypeFilter: TableTypeFilter
  copyText: (value: string, key: string, message: string) => Promise<void>
}

export function TableMode({
  copied,
  input,
  inputHint,
  inputDiagnostics,
  liveStatus,
  generatedSchema,
  generatedSchemaTarget,
  runTable,
  schemaProfile,
  selectedRow,
  setGeneratedSchemaTarget,
  setInput,
  setSelectedRow,
  setTableQuery,
  setTableTypeFilter,
  status,
  tableData,
  tableHasNoResults,
  tableOverview,
  tablePreview,
  tableQuery,
  tableTypeFilter,
  copyText,
}: TableModeProps) {
  return (
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
          <CodeEditor diagnostics={inputDiagnostics} language={MONGO_LANGUAGE_ID} onChange={setInput} value={input} />
          {inputHint ? <InputHealthHint text={inputHint.text} tone={inputHint.tone} /> : null}
        </div>
        <div className="editor-pane">
          <div className="editor-pane-header">
            <span className="editor-pane-title">Table</span>
            <div className="editor-pane-actions">
              <input className="field-input field-input-sm" onChange={(event) => setTableQuery(event.target.value)} placeholder="筛选字段" value={tableQuery} />
            </div>
          </div>
          {tableData ? (
            <>
              <div className="filter-chip-row">
                <button className={`filter-chip${tableTypeFilter === 'all' ? ' filter-chip-active' : ''}`} onClick={() => setTableTypeFilter('all')} type="button">
                  全部字段
                </button>
                <button className={`filter-chip${tableTypeFilter === 'mixed' ? ' filter-chip-active' : ''}`} onClick={() => setTableTypeFilter('mixed')} type="button">
                  mixed {tablePreview.mixedCount}
                </button>
                <button className={`filter-chip${tableTypeFilter === 'nullable' ? ' filter-chip-active' : ''}`} onClick={() => setTableTypeFilter('nullable')} type="button">
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
              <div className="table-caption">当前过滤命中 {tablePreview.filteredSchema.length} 个字段，字段列表与右侧文档行预览同步更新。</div>
            </>
          ) : (
            <div className="empty-state">构建表格后，这里会展示展平后的字段结构。</div>
          )}
        </div>
      </div>
      <StatusBanner
        right={tableData ? `字段 ${tablePreview.filteredSchema.length}/${tableData.schema.length} · 文档 ${tableData.docCount}` : '等待构建'}
        status={liveStatus.kind === 'error' || liveStatus.kind === 'warning' ? liveStatus : tableData ? status : liveStatus}
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
      {schemaProfile ? (
        <div className="workspace-grid">
          <div className="panel">
            <div className="panel-header">
              <div className="panel-header-copy">
                <div className="panel-eyebrow">Schema Profile</div>
                <h3 className="panel-title">Schema 体检</h3>
              </div>
            </div>
            <div className="summary-strip summary-strip-compact">
              <article className="summary-tile summary-tile-left">
                <span className="summary-tile-label">字段</span>
                <strong className="summary-tile-value">{schemaProfile.fieldCount}</strong>
                <span className="summary-tile-helper">{schemaProfile.docCount} 条文档</span>
              </article>
              <article className="summary-tile summary-tile-right">
                <span className="summary-tile-label">可缺失</span>
                <strong className="summary-tile-value">{schemaProfile.nullableFieldCount}</strong>
                <span className="summary-tile-helper">存在 null 或缺失</span>
              </article>
              <article className="summary-tile summary-tile-changed">
                <span className="summary-tile-label">风险字段</span>
                <strong className="summary-tile-value">{schemaProfile.riskFieldCount}</strong>
                <span className="summary-tile-helper">{schemaProfile.mixedFieldCount} 个 mixed</span>
              </article>
            </div>
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>字段</th>
                    <th>出现率</th>
                    <th>示例</th>
                    <th>风险</th>
                  </tr>
                </thead>
                <tbody>
                  {schemaProfile.fields.slice(0, 32).map((field) => (
                    <tr className={field.risks.length > 0 ? 'row-highlight' : ''} key={field.path}>
                      <td>
                        <code>{field.path}</code>
                      </td>
                      <td>{Math.round(field.presenceRatio * 100)}%</td>
                      <td>
                        <code>{field.examples.join(' | ') || 'NULL'}</code>
                      </td>
                      <td>{field.risks.join('、') || '稳定'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          <div className="panel">
            <div className="panel-header">
              <div className="panel-header-copy">
                <div className="panel-eyebrow">Generate</div>
                <h3 className="panel-title">结构生成</h3>
              </div>
              <div className="toolbar">
                <select className="select select-sm" onChange={(event) => setGeneratedSchemaTarget(event.target.value as GeneratedSchemaTarget)} value={generatedSchemaTarget}>
                  <option value="typescript">TypeScript</option>
                  <option value="zod">Zod</option>
                  <option value="go">Go</option>
                </select>
                <button
                  className="button button-ghost button-sm"
                  onClick={() => generatedSchema ? copyText(generatedSchema.code, `schema-${generatedSchema.target}`, '已复制结构代码。') : undefined}
                  type="button"
                >
                  {copied === `schema-${generatedSchema?.target}` ? '已复制' : '复制'}
                </button>
              </div>
            </div>
            <pre className="code-preview">{generatedSchema?.code ?? '构建表格后生成结构代码。'}</pre>
          </div>
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
                <button className="button button-ghost button-sm" disabled={selectedRow <= 0} onClick={() => setSelectedRow((value) => Math.max(0, value - 1))} type="button">
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
  )
}
