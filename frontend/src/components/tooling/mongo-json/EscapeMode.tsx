import { MONGO_LANGUAGE_ID } from '../../../lib/editor/mongoLanguage'
import type { ToolStatus } from '../../../types/tooling'
import { InputHealthHint } from '../../common/InputHealthHint'
import { Panel } from '../../common/Panel'
import { StatusBanner } from '../../common/StatusBanner'
import { CodeEditor } from '../../editor/CodeEditor'
import { ResultPane } from '../../editor/ResultPane'
import type { InputHint, MongoMode } from './types'

type EscapeModeProps = {
  copied: string | null
  escapeInput: string
  escapeOutput: string
  inputHint: InputHint | null
  liveStatus: ToolStatus
  mode: Extract<MongoMode, 'escape' | 'unescape'>
  runEscape: () => void
  setEscapeInput: (value: string) => void
  status: ToolStatus
  copyText: (value: string, key: string, message: string) => Promise<void>
}

export function EscapeMode({
  copied,
  escapeInput,
  escapeOutput,
  inputHint,
  liveStatus,
  mode,
  runEscape,
  setEscapeInput,
  status,
  copyText,
}: EscapeModeProps) {
  return (
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
          <CodeEditor language={MONGO_LANGUAGE_ID} onChange={setEscapeInput} value={escapeInput} />
          {inputHint ? <InputHealthHint text={inputHint.text} tone={inputHint.tone} /> : null}
        </div>
        <ResultPane
          actions={
            <button className="button button-ghost button-sm" onClick={() => copyText(escapeOutput, 'escape-output', '已复制转换结果。')} type="button">
              {copied === 'escape-output' ? '已复制' : '复制结果'}
            </button>
          }
          language={MONGO_LANGUAGE_ID}
          placeholder="执行后会显示转换结果。"
          title="Output"
          value={escapeOutput}
        />
      </div>
      <StatusBanner status={liveStatus.kind === 'error' || liveStatus.kind === 'warning' ? liveStatus : status.kind === 'success' ? status : liveStatus} />
    </Panel>
  )
}
