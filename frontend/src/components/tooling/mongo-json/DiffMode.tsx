import type { DiffSummary, FormatMeta } from '../../../types/tooling'
import { Panel } from '../../common/Panel'
import { StatusBanner } from '../../common/StatusBanner'
import { DiffEditorPanel } from '../../editor/DiffEditorPanel'
import type { DiffFocus, SummaryTile } from './types'

type DiffModeProps = {
  diffFocus: DiffFocus | null
  diffOverview: SummaryTile[]
  diffSummary: DiffSummary
  jumpToDiffPath: (path: string, preferredSide?: 'left' | 'right') => void
  normalizedDiffLeft: FormatMeta
  normalizedDiffRight: FormatMeta
  setDiffFocus: (focus: DiffFocus | null) => void
  setDiffLeft: (value: string) => void
  setDiffRight: (value: string) => void
}

export function DiffMode({
  diffFocus,
  diffOverview,
  diffSummary,
  jumpToDiffPath,
  normalizedDiffLeft,
  normalizedDiffRight,
  setDiffFocus,
  setDiffLeft,
  setDiffRight,
}: DiffModeProps) {
  return (
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
  )
}
