import type { ToolStatus } from '../../../types/tooling'
import type { MongoDiagnostic } from '../../../lib/mongodb-core'
import { InputHealthHint } from '../../common/InputHealthHint'
import { Panel } from '../../common/Panel'
import { StatusBanner } from '../../common/StatusBanner'
import { CodeEditor } from '../../editor/CodeEditor'
import { ResultPane } from '../../editor/ResultPane'
import type { InputHint } from './types'

type RepairModeProps = {
  copied: string | null
  copyText: (value: string, key: string, message: string) => Promise<void>
  inputHint: InputHint | null
  inputDiagnostics: MongoDiagnostic[]
  liveStatus: ToolStatus
  repairInput: string
  repairOutput: string
  runRepair: () => void
  setRepairInput: (value: string) => void
  status: ToolStatus
}

export function RepairMode({
  copied,
  copyText,
  inputHint,
  inputDiagnostics,
  liveStatus,
  repairInput,
  repairOutput,
  runRepair,
  setRepairInput,
  status,
}: RepairModeProps) {
  return (
    <Panel
      actions={
        <button className="button button-primary" onClick={runRepair} type="button">
          修复为标准 JSON
        </button>
      }
      eyebrow="Repair"
      subtitle="显式使用 jsonrepair 处理缺引号、尾逗号、注释、NDJSON 和 MongoDB shell 类型，输出标准 JSON。"
      title="JSON 修复"
    >
      <div className="editor-split">
        <div className="editor-pane">
          <div className="editor-pane-header">
            <span className="editor-pane-title">Input</span>
            <div className="editor-pane-actions">
              <button className="button button-ghost button-sm" onClick={() => copyText(repairInput, 'repair-input', '已复制输入内容。')} type="button">
                {copied === 'repair-input' ? '已复制' : '复制输入'}
              </button>
            </div>
          </div>
          <CodeEditor diagnostics={inputDiagnostics} language="json" onChange={setRepairInput} value={repairInput} />
          {inputHint ? <InputHealthHint text={inputHint.text} tone={inputHint.tone} /> : null}
        </div>
        <ResultPane
          actions={
            <button className="button button-ghost button-sm" onClick={() => copyText(repairOutput, 'repair-output', '已复制修复结果。')} type="button">
              {copied === 'repair-output' ? '已复制' : '复制结果'}
            </button>
          }
          language="json"
          placeholder="执行修复后会显示标准 JSON。"
          title="Standard JSON"
          value={repairOutput}
        />
      </div>
      <StatusBanner status={liveStatus.kind === 'error' || liveStatus.kind === 'warning' ? liveStatus : status.kind === 'success' ? status : liveStatus} />
    </Panel>
  )
}
