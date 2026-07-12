import { MONGO_LANGUAGE_ID } from '../../../lib/editor/mongoLanguage'
import type { MongoDiagnostic } from '../../../lib/mongodb-core'
import type { ToolStatus } from '../../../shared/ui/toolStatus'
import { InputHealthHint } from '../../../components/common/InputHealthHint'
import { Panel } from '../../../components/common/Panel'
import { StatusBanner } from '../../../components/common/StatusBanner'
import { CodeEditor } from '../../../components/editor/CodeEditor'
import { ResultPane } from '../../../components/editor/ResultPane'
import type { InputHint } from './types'

type FormatModeProps = {
  copied: string | null
  extendedJsonOutput: string
  input: string
  inputHint: InputHint | null
  inputDiagnostics: MongoDiagnostic[]
  liveStatus: ToolStatus
  output: string
  runFormat: () => void
  setInput: (value: string) => void
  stats: { chars: number; lines: number; depth: number }
  status: ToolStatus
  copyText: (value: string, key: string, message: string) => Promise<void>
}

export function FormatMode({
  copied,
  extendedJsonOutput,
  input,
  inputHint,
  inputDiagnostics,
  liveStatus,
  output,
  runFormat,
  setInput,
  stats,
  status,
  copyText,
}: FormatModeProps) {
  return (
    <Panel
      actions={
        <>
          <button className="button button-primary" onClick={runFormat} type="button">
            执行格式化
          </button>
          <button className="button button-ghost" disabled={!extendedJsonOutput} onClick={() => copyText(extendedJsonOutput, 'format-ejson', '已复制 Canonical Extended JSON。')} type="button">
            {copied === 'format-ejson' ? '已复制 EJSON' : '复制 EJSON'}
          </button>
        </>
      }
      eyebrow="MongoDB JSON"
      subtitle="针对扩展类型的宽松解析与格式化。"
      title="格式化工作区"
    >
      <div className="editor-split">
        <div className="editor-pane">
          <div className="editor-pane-header">
            <span className="editor-pane-title">Input</span>
            <div className="editor-pane-actions">
              <button className="button button-ghost button-sm" onClick={() => copyText(input, 'format-input', '已复制输入内容。')} type="button">
                {copied === 'format-input' ? '已复制' : '复制输入'}
              </button>
            </div>
          </div>
          <CodeEditor diagnostics={inputDiagnostics} language={MONGO_LANGUAGE_ID} onChange={setInput} value={input} />
          {inputHint ? <InputHealthHint text={inputHint.text} tone={inputHint.tone} /> : null}
        </div>
        <ResultPane
          actions={
            <button className="button button-ghost button-sm" onClick={() => copyText(output, 'format-output', '已复制格式化结果。')} type="button">
              {copied === 'format-output' ? '已复制' : '复制结果'}
            </button>
          }
          language={MONGO_LANGUAGE_ID}
          placeholder="执行格式化后，结果会出现在这里。"
          title="Output"
          value={output}
        />
      </div>
      <StatusBanner
        right={`字符 ${stats.chars} · 行数 ${stats.lines} · 深度 ${stats.depth}`}
        status={liveStatus.kind === 'error' || liveStatus.kind === 'warning' ? liveStatus : status.kind === 'success' ? status : liveStatus}
      />
    </Panel>
  )
}
