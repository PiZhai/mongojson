import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  createStewardConversation,
  decideStewardConversationExecution,
  decideStewardConversationSuggestion,
  getStewardExecutionControl,
  getStewardConversationMessages,
  getStewardConversations,
  sendStewardConversationMessage,
} from '../api'
import type {
  StewardConversation,
  StewardConversationExecution,
  StewardConversationMessage,
  StewardConversationSuggestion,
} from '../types'
import { formatDate } from './model'
import { authorityMatchesOrigin, issueWebAuthnApprovalProof } from './webauthnApproval'
import { ModelSettingsDialog } from './ModelSettingsDialog'

type Props = {
  onDataChanged: () => Promise<void>
}

export function ConversationWorkspace({ onDataChanged }: Props) {
  const [conversations, setConversations] = useState<StewardConversation[]>([])
  const [activeId, setActiveId] = useState('')
  const [messages, setMessages] = useState<StewardConversationMessage[]>([])
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [modelSettingsOpen, setModelSettingsOpen] = useState(false)
  const messageEndRef = useRef<HTMLDivElement | null>(null)
  const composerRef = useRef<HTMLTextAreaElement | null>(null)

  const loadConversations = async (preferredId?: string) => {
    const result = await getStewardConversations()
    setConversations(result.conversations)
    const nextId = preferredId || activeId || result.conversations[0]?.id || ''
    setActiveId(nextId)
    if (nextId) {
      const messageResult = await getStewardConversationMessages(nextId)
      setMessages(messageResult.messages)
    } else {
      setMessages([])
    }
  }

  useEffect(() => {
    let alive = true
    getStewardConversations()
      .then(async (result) => {
        if (!alive) return
        setConversations(result.conversations)
        const nextId = result.conversations[0]?.id || ''
        setActiveId(nextId)
        if (nextId) {
          const messageResult = await getStewardConversationMessages(nextId)
          if (alive) setMessages(messageResult.messages)
        }
      })
      .catch((reason: unknown) => {
        if (alive) setError(reason instanceof Error ? reason.message : '加载对话失败')
      })
    return () => {
      alive = false
    }
  }, [])

  useEffect(() => {
    messageEndRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
  }, [messages, busy])

  useEffect(() => {
    const composer = composerRef.current
    if (!composer) return
    composer.style.height = 'auto'
    composer.style.height = `${Math.min(composer.scrollHeight, 160)}px`
  }, [draft])

  useEffect(() => {
    if (!activeId) return
    const timer = window.setInterval(() => {
      void getStewardConversationMessages(activeId)
        .then((result) => setMessages(result.messages))
        .catch(() => undefined)
    }, 2000)
    return () => window.clearInterval(timer)
  }, [activeId])

  const selectConversation = async (id: string) => {
    setActiveId(id)
    setBusy(true)
    setError('')
    try {
      const result = await getStewardConversationMessages(id)
      setMessages(result.messages)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '加载消息失败')
    } finally {
      setBusy(false)
    }
  }

  const createConversation = async () => {
    setBusy(true)
    setError('')
    try {
      const result = await createStewardConversation({})
      await loadConversations(result.conversation.id)
      window.setTimeout(() => composerRef.current?.focus(), 0)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '创建对话失败')
    } finally {
      setBusy(false)
    }
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const content = draft.trim()
    if (!content || busy) return
    setBusy(true)
    setError('')
    try {
      let conversationId = activeId
      if (!conversationId) {
        const created = await createStewardConversation({})
        conversationId = created.conversation.id
        setActiveId(conversationId)
      }
      setDraft('')
      await sendStewardConversationMessage(conversationId, {
        content,
        context_limit: 10,
      })
      await loadConversations(conversationId)
      await onDataChanged()
    } catch (reason) {
      setDraft((current) => current.trim() ? current : content)
      setError(reason instanceof Error ? reason.message : '发送消息失败')
    } finally {
      setBusy(false)
      window.setTimeout(() => composerRef.current?.focus(), 0)
    }
  }

  const decide = async (suggestion: StewardConversationSuggestion, decision: 'accepted' | 'dismissed') => {
    setBusy(true)
    setError('')
    try {
      await decideStewardConversationSuggestion(suggestion.id, decision)
      if (activeId) {
        const result = await getStewardConversationMessages(activeId)
        setMessages(result.messages)
      }
      await onDataChanged()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '处理候选失败')
    } finally {
      setBusy(false)
    }
  }

  const decideExecution = async (execution: StewardConversationExecution, decision: 'confirm' | 'pause' | 'cancel') => {
    setBusy(true)
    setError('')
    try {
      let proof
      const reason = decision === 'confirm' ? '在管家对话中确认执行' : `在管家对话中${decision === 'pause' ? '暂停' : '取消'}`
      if (decision === 'confirm' && execution.capability) {
        const { control } = await getStewardExecutionControl()
        const origin = window.location.origin
        const authority = control.broker.approval_authorities.find((item) => authorityMatchesOrigin(item, origin))
        if (!authority) throw new Error('没有匹配当前站点的 WebAuthn 审批身份，请先在执行控制面完成注册和 Broker policy 配置。')
        proof = await issueWebAuthnApprovalProof({
          authority,
          subject: execution.approval_subject || `runtime:${execution.run_id}`,
          planHash: execution.plan_hash,
          capability: execution.capability,
          controlGeneration: execution.control_generation ?? control.generation,
          grantedBy: 'conversation-user',
          reason,
          origin,
        })
      }
      await decideStewardConversationExecution(execution.id, decision, reason, proof)
      if (activeId) {
        const result = await getStewardConversationMessages(activeId)
        setMessages(result.messages)
      }
      await onDataChanged()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '处理执行计划失败')
    } finally {
      setBusy(false)
    }
  }

  const sendControlCommand = async (command: '继续' | '换到另一台电脑') => {
    if (!activeId) return
    setBusy(true)
    setError('')
    try {
      await sendStewardConversationMessage(activeId, { content: command, context_limit: 10 })
      await loadConversations(activeId)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '控制任务失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="steward-conversation-shell" aria-label="管家对话">
      <aside className="steward-conversation-list">
        <div className="steward-conversation-list-header">
          <strong>对话</strong>
          <div className="steward-conversation-header-actions">
            <button className="steward-icon-button steward-button-secondary" disabled={busy} onClick={() => setModelSettingsOpen(true)} type="button">模型</button>
            <button className="steward-icon-button steward-button-secondary" disabled={busy} onClick={createConversation} type="button">新建</button>
          </div>
        </div>
        <div className="steward-conversation-list-scroll">
          {conversations.map((item) => (
            <button
              className={`steward-conversation-item${item.id === activeId ? ' is-active' : ''}`}
              key={item.id}
              onClick={() => selectConversation(item.id)}
              type="button"
            >
              <strong>{item.title}</strong>
              <small>{item.message_count} 条 · {formatDate(item.last_message_at || item.updated_at)}</small>
            </button>
          ))}
          {conversations.length === 0 ? <small className="steward-conversation-empty">暂无对话</small> : null}
        </div>
      </aside>

      <div className="steward-conversation-main">
        <div className="steward-conversation-messages" aria-busy={busy} aria-live="polite">
          {messages.length === 0 ? (
            <div className="steward-conversation-welcome">
              <strong>私人管家</strong>
              <span>输入你正在做的事、要记住的信息或需要安排的工作。</span>
            </div>
          ) : null}
          {messages.map((message) => (
            <article className={`steward-message is-${message.role}`} key={message.id}>
              <div className="steward-message-meta">
                <strong>{message.role === 'user' ? '你' : '管家'}</strong>
                <span>{formatDate(message.created_at)}</span>
              </div>
              <p>{message.content}</p>
              {message.suggestions.map((suggestion) => (
                <div className="steward-conversation-suggestion" key={suggestion.id}>
                  <div>
                    <small>{suggestion.kind === 'memory' ? '记忆候选' : suggestion.kind === 'task' ? '任务候选' : '意图候选'}</small>
                    <strong>{suggestion.title}</strong>
                    {suggestion.summary ? <p>{suggestion.summary}</p> : null}
                  </div>
                  {suggestion.status === 'candidate' ? (
                    <div className="steward-row-actions">
                      <button className="steward-icon-button" disabled={busy} onClick={() => decide(suggestion, 'accepted')} type="button">采纳</button>
                      <button className="steward-icon-button steward-button-secondary" disabled={busy} onClick={() => decide(suggestion, 'dismissed')} type="button">忽略</button>
                    </div>
                  ) : <small>{suggestion.status === 'accepted' ? '已采纳' : '已忽略'}</small>}
                </div>
              ))}
              {(message.executions ?? []).map((execution) => (
                <div className={`steward-execution-card is-${execution.status}`} key={execution.id}>
                  <div className="steward-execution-card-header">
                    <div>
                      <small>{execution.kind === 'orchestration' ? '多 Agent 计划' : execution.kind === 'run' ? '执行计划' : '需要补充信息'}</small>
                      <strong>{execution.summary}</strong>
                    </div>
                    <span>{executionStatusLabel(execution.status)}</span>
                  </div>
                  {execution.question ? <p>{execution.question}</p> : null}
                  {execution.kind !== 'question' ? (
                    <>
                      <div className="steward-execution-meta">
                        <span>{execution.target_device_name || execution.target_device_id}</span>
                        <span>{execution.risk_level === 'low' ? '低影响' : '需要确认'}</span>
                      </div>
                      {execution.confirmation_reason ? <small className="steward-execution-reason">{execution.confirmation_reason}</small> : null}
                      {execution.failure_summary ? <p className="steward-execution-failure">{execution.failure_summary}</p> : null}
                      {execution.evidence?.artifact_count !== undefined && ['succeeded', 'failed', 'blocked', 'cancelled'].includes(execution.status) ? (
                        <div className="steward-execution-evidence">
                          <span>{execution.evidence.artifact_count} 份证据</span>
                          <span>{execution.evidence.redacted_count ?? 0} 份已脱敏</span>
                          {execution.evidence.manifest_sha256 ? <code title={execution.evidence.manifest_sha256}>{execution.evidence.manifest_sha256.slice(0, 12)}</code> : null}
                        </div>
                      ) : null}
                      <div className="steward-row-actions steward-execution-actions">
                        {execution.status === 'awaiting_confirmation' ? (
                          <button className="steward-button" disabled={busy} onClick={() => void decideExecution(execution, 'confirm')} type="button">确认执行</button>
                        ) : null}
                        {['queued', 'running'].includes(execution.status) ? (
                          <button className="steward-button steward-button-secondary" disabled={busy} onClick={() => void decideExecution(execution, 'pause')} type="button">暂停</button>
                        ) : null}
                        {execution.status === 'paused' ? (
                          <button className="steward-button" disabled={busy} onClick={() => void sendControlCommand('继续')} type="button">继续</button>
                        ) : null}
                        {['awaiting_confirmation', 'queued', 'running', 'paused', 'blocked'].includes(execution.status) ? (
                          <button className="steward-button steward-button-secondary" disabled={busy} onClick={() => void decideExecution(execution, 'cancel')} type="button">取消</button>
                        ) : null}
                        {['awaiting_confirmation', 'paused'].includes(execution.status) ? (
                          <button className="steward-button steward-button-secondary" disabled={busy} onClick={() => void sendControlCommand('换到另一台电脑')} type="button">换设备</button>
                        ) : null}
                      </div>
                    </>
                  ) : null}
                </div>
              ))}
            </article>
          ))}
          {busy ? (
            <div className="steward-message-pending" role="status">
              <span className="steward-thinking-dots" aria-hidden="true"><i /><i /><i /></span>
              <span>管家正在思考并检查可用工具</span>
            </div>
          ) : null}
          <div ref={messageEndRef} />
        </div>
        {error ? <div className="steward-conversation-error" role="alert">{error}</div> : null}
        <form className="steward-conversation-composer" onSubmit={submit}>
          <textarea
            aria-label="消息"
            autoFocus
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault()
                event.currentTarget.form?.requestSubmit()
              }
            }}
            placeholder="告诉管家你想做什么…"
            ref={composerRef}
            rows={1}
            value={draft}
          />
          <div className="steward-conversation-actions">
            <small>Enter 发送<br />Shift + Enter 换行</small>
            <button className="steward-button steward-send-button" disabled={busy || !draft.trim()} type="submit">
              {busy ? '处理中' : '发送'}
            </button>
          </div>
        </form>
      </div>
      <ModelSettingsDialog open={modelSettingsOpen} onClose={() => setModelSettingsOpen(false)} />
    </section>
  )
}

function executionStatusLabel(status: StewardConversationExecution['status']) {
  const labels: Record<StewardConversationExecution['status'], string> = {
    needs_input: '等待补充',
    awaiting_confirmation: '等待确认',
    queued: '排队中',
    running: '执行中',
    paused: '已暂停',
    succeeded: '已完成',
    failed: '失败',
    cancelled: '已取消',
    blocked: '已阻断',
  }
  return labels[status]
}
