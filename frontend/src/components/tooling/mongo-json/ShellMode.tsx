import { MONGO_LANGUAGE_ID } from '../../../lib/editor/mongoLanguage'
import type { ShellStatement, ShellValidation, ToolStatus } from '../../../types/tooling'
import { InputHealthHint } from '../../common/InputHealthHint'
import { Panel } from '../../common/Panel'
import { StatusBanner } from '../../common/StatusBanner'
import { CodeEditor } from '../../editor/CodeEditor'
import { ResultPane } from '../../editor/ResultPane'
import type { InputHint, ShellFocus, SummaryTile } from './types'

type ShellModeProps = {
  copied: string | null
  inputHint: InputHint | null
  jumpToShellOffset: (offset: number, label: string, kind: ShellFocus['kind']) => void
  liveStatus: ToolStatus
  parsedShell: ShellStatement | null
  runShell: () => void
  setShellFocus: (focus: ShellFocus | null) => void
  setShellInput: (value: string) => void
  shellChecks: ShellValidation[]
  shellFocus: ShellFocus | null
  shellInput: string
  shellOutput: string
  shellOverview: SummaryTile[] | null
  status: ToolStatus
  copyText: (value: string, key: string, message: string) => Promise<void>
}

export function ShellMode({
  copied,
  inputHint,
  jumpToShellOffset,
  liveStatus,
  parsedShell,
  runShell,
  setShellFocus,
  setShellInput,
  shellChecks,
  shellFocus,
  shellInput,
  shellOutput,
  shellOverview,
  status,
  copyText,
}: ShellModeProps) {
  return (
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
          <CodeEditor focusLine={shellFocus?.line ?? null} language={MONGO_LANGUAGE_ID} onChange={setShellInput} value={shellInput} />
          {inputHint ? <InputHealthHint text={inputHint.text} tone={inputHint.tone} /> : null}
        </div>
        <ResultPane
          actions={
            <button className="button button-ghost button-sm" onClick={() => copyText(shellOutput, 'shell-output', '已复制 Shell 结果。')} type="button">
              {copied === 'shell-output' ? '已复制' : '复制结果'}
            </button>
          }
          language={MONGO_LANGUAGE_ID}
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
                    <button className="path-chip" key={`${method.name}-${method.nameStart}-${index}`} onClick={() => jumpToShellOffset(method.nameStart, method.name, 'method')} type="button">
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
                      <button className="path-chip path-chip-changed" key={`${operator.name}-${operator.pos}-${index}`} onClick={() => jumpToShellOffset(operator.pos, operator.name, 'operator')} type="button">
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
                        <code>{method.argsRaw.length > 0 ? method.argsRaw.map((item) => item.text.replace(/\s+/g, ' ').slice(0, 48)).join(' | ') : '无参数'}</code>
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
      <StatusBanner status={liveStatus.kind === 'error' || liveStatus.kind === 'warning' ? liveStatus : status.kind === 'success' ? status : liveStatus} />
    </Panel>
  )
}
