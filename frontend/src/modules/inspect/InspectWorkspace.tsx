import { useMemo, useState } from 'react'
import './styles.css'
import { inspectInput } from '../../lib/tooling/inputInspector'
import { useCapabilityNavigation } from '../../platform/workspace/useCapabilityNavigation'
import type { CapabilityId } from '../../platform/contracts/modules'
import type { InspectInputKind, InspectSuggestedAction } from '../../shared/data/types'
import type { ToolStatus } from '../../shared/ui/toolStatus'
import { Panel } from '../../components/common/Panel'
import { StatusBanner } from '../../components/common/StatusBanner'
import { CodeEditor } from '../../components/editor/CodeEditor'
import { ResultPane } from '../../components/editor/ResultPane'

const inspectSample = `2026-06-19 10:20:13 WARN payload={"_id": ObjectId("507f1f77bcf86cd799439011"), "user": "Ada", "roles": ["admin"], "updatedAt": ISODate("2026-06-19T02:20:13.000Z")}`

const kindLabels: Record<InspectInputKind, string> = {
  'standard-json': '标准 JSON',
  'mongo-json': 'MongoDB JSON',
  'escaped-json-string': '转义 JSON 字符串',
  'mongo-shell': 'Mongo Shell',
  curl: 'curl 片段',
  'log-json-fragment': '日志 JSON 片段',
  ndjson: 'NDJSON',
  unknown: '未识别',
}

function statusFromResult(kind: InspectInputKind, confidence: number): ToolStatus {
  if (kind === 'unknown') return { kind: 'warning', message: '暂未识别出稳定结构，可先尝试提取片段。' }
  if (confidence >= 0.85) return { kind: 'success', message: `已识别为 ${kindLabels[kind]}。` }
  return { kind: 'warning', message: `可能是 ${kindLabels[kind]}，建议检查提取结果。` }
}

export function InspectWorkspace() {
  const { hasCapability, openCapability } = useCapabilityNavigation()
  const [input, setInput] = useState(inspectSample)
  const [copied, setCopied] = useState<string | null>(null)
  const result = useMemo(() => inspectInput(input), [input])
  const status = statusFromResult(result.kind, result.confidence)

  const getActionCapability = (action: InspectSuggestedAction): CapabilityId | null => {
    if (action.id === 'extract') return null
    if (action.id === 'format') {
      if (result.kind === 'standard-json' && hasCapability('json.format')) return 'json.format'
      return 'mongo-json.format'
    }
    return `mongo-json.${action.id}` as CapabilityId
  }

  const availableActions = result.suggestedActions.filter((action) => {
    const capability = getActionCapability(action)
    return capability === null || hasCapability(capability)
  })

  const runAction = (action: InspectSuggestedAction) => {
    const payload = result.extractedText || input
    const capability = getActionCapability(action)

    if (capability && openCapability({ capability, input: payload })) return
    setInput(payload)
  }

  const copyExtracted = async () => {
    await navigator.clipboard.writeText(result.extractedText)
    setCopied('extracted')
  }

  return (
    <div className="page-shell inspect-page-shell layout-frame layout-min-grid" data-layout-region="inspect-workspace">
      <Panel
        actions={
          <>
            <button className="button button-ghost" onClick={() => setInput('')} type="button">
              清空
            </button>
            <button className="button button-primary" onClick={copyExtracted} type="button">
              {copied === 'extracted' ? '已复制' : '复制提取结果'}
            </button>
          </>
        }
        eyebrow="Inspect"
        title="粘贴诊断"
      >
        <div className="editor-split layout-min-grid" data-layout-region="inspect-editors">
          <div className="editor-pane layout-cell">
            <div className="editor-pane-header">
              <span className="editor-pane-title">Raw Input</span>
            </div>
            <CodeEditor language="json" onChange={setInput} value={input} />
          </div>
          <ResultPane language="json" placeholder="识别到结构化片段后会显示在这里。" title="Extracted" value={result.extractedText} />
        </div>
        <StatusBanner right={`${kindLabels[result.kind]} · ${Math.round(result.confidence * 100)}%`} status={status} />
      </Panel>

      <div className="workspace-grid layout-cell" data-layout-region="inspect-rail">
        <Panel eyebrow="Actions" title="推荐下一步">
          <div className="stack panel-body-compact">
            {availableActions.length > 0 ? (
              availableActions.map((action) => (
                <article className="info-card" key={action.id}>
                  <p className="info-card-title">{action.label}</p>
                  <div className="toolbar">
                    <button className="button button-sm" onClick={() => runAction(action)} type="button">
                      执行
                    </button>
                  </div>
                </article>
              ))
            ) : (
              <div className="empty-state">暂无动作</div>
            )}
          </div>
        </Panel>

        <Panel eyebrow="Issues" title="识别提示">
          <div className="stack panel-body-compact">
            {result.issues.length > 0 ? (
              result.issues.map((issue, index) => (
                <article className="info-card" key={`${issue.message}-${index}`}>
                  <p className="info-card-title">{issue.level.toUpperCase()}</p>
                  <p className="info-card-text">{issue.message}</p>
                </article>
              ))
            ) : (
              <article className="info-card">
                <p className="info-card-title">OK</p>
              </article>
            )}
          </div>
        </Panel>
      </div>
    </div>
  )
}
