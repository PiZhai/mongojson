import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react'
import {
  createStewardConversation,
  decideStewardAgentEpisode,
  decideStewardConversationExecution,
  decideStewardConversationSuggestion,
  getStewardAgentEpisodeTurns,
  getStewardExecutionControl,
  getStewardConversationMessages,
  getStewardConversations,
  sendStewardConversationMessage,
  updateStewardConversation,
} from '../api'
import type {
  StewardAgentEpisode,
  StewardAgentTurn,
  StewardConversation,
  StewardConversationExecution,
  StewardConversationMessage,
  StewardConversationSuggestion,
} from '../types'
import { formatDate } from './model'
import { authorityMatchesOrigin, issueWebAuthnApprovalProof } from './webauthnApproval'
import { ModelSettingsDialog } from './ModelSettingsDialog'
import { ArchivedConversationsDialog } from './ArchivedConversationsDialog'
import { isConversationNearBottom } from './conversationScroll'

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
  const [archiveOpen, setArchiveOpen] = useState(false)
  const [archivedConversations, setArchivedConversations] = useState<StewardConversation[]>([])
  const [archiveLoading, setArchiveLoading] = useState(false)
  const [archiveBusyId, setArchiveBusyId] = useState('')
  const [archiveError, setArchiveError] = useState('')
  const [showScrollToLatest, setShowScrollToLatest] = useState(false)
  const messagesRef = useRef<HTMLDivElement | null>(null)
  const followLatestRef = useRef(true)
  const scrollingToLatestRef = useRef(false)
  const nextScrollBehaviorRef = useRef<ScrollBehavior>('auto')
  const scrollCompletionTimerRef = useRef<number | null>(null)
  const composerRef = useRef<HTMLTextAreaElement | null>(null)
  const archiveTriggerRef = useRef<HTMLButtonElement | null>(null)

  const prepareToFollowLatest = useCallback((behavior: ScrollBehavior = 'auto') => {
    followLatestRef.current = true
    nextScrollBehaviorRef.current = behavior
    setShowScrollToLatest(false)
  }, [])

  const scrollToLatest = useCallback((behavior: ScrollBehavior = 'smooth') => {
    const container = messagesRef.current
    if (!container) return
    if (scrollCompletionTimerRef.current !== null) {
      window.clearTimeout(scrollCompletionTimerRef.current)
    }
    const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    const resolvedBehavior = reducedMotion ? 'auto' : behavior
    scrollingToLatestRef.current = true
    followLatestRef.current = true
    setShowScrollToLatest(false)
    container.scrollTo({ top: container.scrollHeight, behavior: resolvedBehavior })
    scrollCompletionTimerRef.current = window.setTimeout(() => {
      scrollingToLatestRef.current = false
      const isAtBottom = isConversationNearBottom(container)
      followLatestRef.current = isAtBottom
      setShowScrollToLatest(!isAtBottom)
      scrollCompletionTimerRef.current = null
    }, resolvedBehavior === 'smooth' ? 500 : 0)
  }, [])

  const handleMessageScroll = useCallback(() => {
    const container = messagesRef.current
    if (!container) return
    const isAtBottom = isConversationNearBottom(container)
    if (scrollingToLatestRef.current) {
      if (isAtBottom) {
        scrollingToLatestRef.current = false
        followLatestRef.current = true
        setShowScrollToLatest(false)
      }
      return
    }
    followLatestRef.current = isAtBottom
    setShowScrollToLatest(!isAtBottom)
  }, [])

  const loadConversations = async (preferredId?: string) => {
    const result = await getStewardConversations()
    setConversations(result.conversations)
    const requestedId = preferredId || activeId
    const nextId = result.conversations.some((item) => item.id === requestedId)
      ? requestedId
      : result.conversations[0]?.id || ''
    const conversationChanged = nextId !== activeId
    setActiveId(nextId)
    if (nextId) {
      const messageResult = await getStewardConversationMessages(nextId)
      if (conversationChanged) prepareToFollowLatest('auto')
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
          if (alive) {
            prepareToFollowLatest('auto')
            setMessages(messageResult.messages)
          }
        }
      })
      .catch((reason: unknown) => {
        if (alive) setError(reason instanceof Error ? reason.message : '加载对话失败')
      })
    return () => {
      alive = false
    }
  }, [prepareToFollowLatest])

  useEffect(() => {
    if (!followLatestRef.current) return
    if (!messages.length && !busy) return
    const behavior = nextScrollBehaviorRef.current
    nextScrollBehaviorRef.current = 'smooth'
    scrollToLatest(behavior)
  }, [messages, busy, scrollToLatest])

  useEffect(() => () => {
    if (scrollCompletionTimerRef.current !== null) {
      window.clearTimeout(scrollCompletionTimerRef.current)
    }
  }, [])

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
    prepareToFollowLatest('auto')
    setActiveId(id)
    setBusy(true)
    setError('')
    try {
      const result = await getStewardConversationMessages(id)
      prepareToFollowLatest('auto')
      setMessages(result.messages)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '加载消息失败')
    } finally {
      setBusy(false)
    }
  }

  const createConversation = async () => {
    prepareToFollowLatest('auto')
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
    prepareToFollowLatest('smooth')
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
      setError(reason instanceof Error ? reason.message : '处理操作失败')
    } finally {
      setBusy(false)
    }
  }

  const sendControlCommand = async (command: '继续' | '换到另一台电脑') => {
    if (!activeId) return
    prepareToFollowLatest('smooth')
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

  const openArchive = async () => {
    setArchiveOpen(true)
    setArchiveLoading(true)
    setArchiveError('')
    try {
      const result = await getStewardConversations(100, true)
      setArchivedConversations(result.conversations)
    } catch (reason) {
      setArchiveError(reason instanceof Error ? reason.message : '加载归档对话失败')
    } finally {
      setArchiveLoading(false)
    }
  }

  const closeArchive = useCallback(() => {
    setArchiveOpen(false)
    window.setTimeout(() => archiveTriggerRef.current?.focus(), 0)
  }, [])

  const archiveConversation = async (conversation: StewardConversation) => {
    setArchiveBusyId(conversation.id)
    setError('')
    try {
      await updateStewardConversation(conversation.id, { archived: true })
      await loadConversations()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '归档对话失败')
    } finally {
      setArchiveBusyId('')
    }
  }

  const restoreConversation = async (conversation: StewardConversation) => {
    setArchiveBusyId(conversation.id)
    setArchiveError('')
    try {
      await updateStewardConversation(conversation.id, { archived: false })
      setArchivedConversations((current) => current.filter((item) => item.id !== conversation.id))
      await loadConversations()
    } catch (reason) {
      setArchiveError(reason instanceof Error ? reason.message : '恢复对话失败')
    } finally {
      setArchiveBusyId('')
    }
  }

  const decideEpisode = async (episode: StewardAgentEpisode, decision: 'pause' | 'resume' | 'cancel') => {
    setBusy(true)
    setError('')
    try {
      await decideStewardAgentEpisode(episode.id, decision)
      if (activeId) {
        const result = await getStewardConversationMessages(activeId)
        setMessages(result.messages)
      }
      await onDataChanged()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '控制 Agent 任务失败')
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
            <button className="steward-icon-button steward-button-secondary" disabled={busy} onClick={() => void openArchive()} ref={archiveTriggerRef} type="button">归档</button>
            <button className="steward-icon-button steward-button-secondary" disabled={busy} onClick={createConversation} type="button">新建</button>
          </div>
        </div>
        <div className="steward-conversation-list-scroll">
          {conversations.map((item) => (
            <div className={`steward-conversation-entry${item.id === activeId ? ' is-active' : ''}`} key={item.id}>
              <button
                aria-current={item.id === activeId ? 'true' : undefined}
                className="steward-conversation-item"
                onClick={() => selectConversation(item.id)}
                type="button"
              >
                <strong>{item.title}</strong>
                <small>{item.message_count} 条 · {formatDate(item.last_message_at || item.updated_at)}</small>
              </button>
              <button
                aria-label={`归档对话：${item.title}`}
                className="steward-conversation-archive-action"
                disabled={Boolean(archiveBusyId)}
                onClick={() => void archiveConversation(item)}
                type="button"
              >
                {archiveBusyId === item.id ? '处理中' : '归档'}
              </button>
            </div>
          ))}
          {conversations.length === 0 ? <small className="steward-conversation-empty">暂无对话</small> : null}
        </div>
      </aside>

      <div className="steward-conversation-main">
        <div className="steward-conversation-message-stage">
          <div
            className="steward-conversation-messages"
            aria-busy={busy}
            aria-live="polite"
            onScroll={handleMessageScroll}
            ref={messagesRef}
          >
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
              {(message.suggestions ?? []).map((suggestion) => (
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
              {(message.episodes ?? []).map((episode) => (
                <div className={`steward-execution-card is-${episode.status}`} key={episode.id}>
                  <div className="steward-execution-card-header">
                    <div>
                      <small>任务</small>
                      <strong>{episode.goal}</strong>
                    </div>
                    <span>{agentEpisodeStatusLabel(episode.status)}</span>
                  </div>
                  <div className="steward-row-actions steward-execution-actions">
                    {['thinking', 'executing'].includes(episode.status) ? (
                      <button className="steward-button steward-button-secondary" disabled={busy} onClick={() => void decideEpisode(episode, 'pause')} type="button">暂停</button>
                    ) : null}
                    {['paused', 'blocked', 'failed'].includes(episode.status) ? (
                      <button className="steward-button" disabled={busy} onClick={() => void decideEpisode(episode, 'resume')} type="button">
                        {episode.status === 'failed' ? '重试' : '继续'}
                      </button>
                    ) : null}
                    {['thinking', 'executing', 'awaiting_input', 'paused', 'blocked'].includes(episode.status) ? (
                      <button className="steward-button steward-button-secondary" disabled={busy} onClick={() => void decideEpisode(episode, 'cancel')} type="button">取消</button>
                    ) : null}
                    {['paused', 'blocked', 'failed'].includes(episode.status) ? (
                      <button className="steward-button steward-button-secondary" disabled={busy} onClick={() => void sendControlCommand('换到另一台电脑')} type="button">换设备</button>
                    ) : null}
                  </div>
                </div>
              ))}
              {(message.executions ?? []).filter((execution) => !execution.episode_id).map((execution) => (
                <div className={`steward-execution-card is-${execution.status}`} key={execution.id}>
                  <div className="steward-execution-card-header">
                    <div>
                      <small>{execution.kind === 'question' ? '需要补充信息' : '操作'}</small>
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
              <span>管家正在思考</span>
            </div>
          ) : null}
          </div>
          {showScrollToLatest ? (
            <button
              aria-label="回到最新消息"
              className="steward-scroll-to-latest"
              onClick={() => scrollToLatest('smooth')}
              title="回到最新消息"
              type="button"
            >
              <svg aria-hidden="true" viewBox="0 0 24 24">
                <path d="M6 9l6 6 6-6" />
              </svg>
            </button>
          ) : null}
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
      <ArchivedConversationsDialog
        busyId={archiveBusyId}
        conversations={archivedConversations}
        error={archiveError}
        loading={archiveLoading}
        onClose={closeArchive}
        onRestore={(conversation) => void restoreConversation(conversation)}
        open={archiveOpen}
      />
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

function mergeAgentTurns(...groups: StewardAgentTurn[][]): StewardAgentTurn[] {
  const byID = new Map<string, StewardAgentTurn>()
  for (const group of groups) {
    for (const turn of group) byID.set(turn.id, turn)
  }
  return [...byID.values()].sort((left, right) => left.round_index - right.round_index)
}

const agentProgressPreviewLimit = 180

function summarizeAgentProgress(value?: string, limit = agentProgressPreviewLimit) {
  const normalized = value?.trim() ?? ''
  if (normalized.length <= limit) return { preview: normalized, truncated: false }
  return { preview: `${normalized.slice(0, Math.max(1, limit - 1)).trimEnd()}…`, truncated: true }
}

function recentAgentToolLabel(episode: StewardAgentEpisode) {
  const latestTurn = [...(episode.turns ?? [])].sort((left, right) => right.round_index - left.round_index)[0]
  const names = [...new Set([
    ...(latestTurn?.tool_calls ?? []).map((call) => call.tool_name),
    ...(latestTurn?.tool_results ?? []).map((result) => result.tool_name),
  ].filter(Boolean))]
  if (names.length === 0) return episode.status === 'thinking' ? '准备模型决策' : '尚无工具调用'
  if (names.length <= 3) return names.join('、')
  return `${names.slice(0, 3).join('、')} 等 ${names.length} 项`
}

export function AgentEpisodeProgress({ episode }: { episode: StewardAgentEpisode }) {
  const [detailsOpen, setDetailsOpen] = useState(false)
  const detailID = useId()
  const result = summarizeAgentProgress(episode.last_result_summary)
  const failure = summarizeAgentProgress(episode.failure_summary)
  const hasDetails = result.truncated || failure.truncated
  const detailsLabel = episode.failure_summary ? '错误详情' : '运行详情'
  const toolStateLabel = episode.status === 'executing' ? '当前工具' : '最近工具'

  return (
    <div className="steward-agent-progress">
      <div className="steward-agent-progress-tool" aria-label={`${toolStateLabel}：${recentAgentToolLabel(episode)}`}>
        <small>{toolStateLabel}</small>
        <strong>{recentAgentToolLabel(episode)}</strong>
      </div>
      {result.preview ? <p className="steward-agent-result-summary"><small>最近结果</small>{result.preview}</p> : null}
      {failure.preview ? <p className="steward-execution-failure" role="alert"><small>失败原因</small>{failure.preview}</p> : null}
      {hasDetails ? (
        <>
          <button
            aria-controls={detailID}
            aria-expanded={detailsOpen}
            className="steward-button steward-button-secondary steward-agent-details-toggle"
            onClick={() => setDetailsOpen((current) => !current)}
            type="button"
          >
            {detailsOpen ? `收起${detailsLabel}` : `查看${detailsLabel}`}
          </button>
          {detailsOpen ? (
            <div className="steward-agent-progress-details" id={detailID}>
              {episode.last_result_summary ? <p><small>完整结果</small>{episode.last_result_summary}</p> : null}
              {episode.failure_summary ? <p className="steward-execution-failure"><small>完整错误</small>{episode.failure_summary}</p> : null}
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  )
}

export function AgentEpisodeTurnHistory({ episode, initiallyOpen }: { episode: StewardAgentEpisode; initiallyOpen: boolean }) {
  const [open, setOpen] = useState(initiallyOpen)
  const historyID = useId()
  const [loadedTurns, setLoadedTurns] = useState<StewardAgentTurn[]>([])
  const [hasMore, setHasMore] = useState(episode.turns_has_more ?? false)
  const [nextBeforeRound, setNextBeforeRound] = useState(0)
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState('')
  const loadedRef = useRef(false)

  const loadPage = useCallback(async (loadOlder = false) => {
    if (loading) return
    setLoading(true)
    setLoadError('')
    try {
      const page = await getStewardAgentEpisodeTurns(episode.id, loadOlder ? nextBeforeRound : 0, 25)
      setLoadedTurns((current) => mergeAgentTurns(loadOlder ? current : [], page.turns))
      setHasMore(page.has_more)
      setNextBeforeRound(page.next_before_round ?? 0)
      loadedRef.current = true
    } catch (reason) {
      setLoadError(reason instanceof Error ? reason.message : '加载工具回合失败')
    } finally {
      setLoading(false)
    }
  }, [episode.id, loading, nextBeforeRound])

  useEffect(() => {
    if (initiallyOpen && !loadedRef.current) void loadPage(false)
  }, [initiallyOpen, loadPage])

  const turns = mergeAgentTurns(loadedTurns, episode.turns ?? [])
  const total = episode.turn_count ?? Math.max(episode.current_round, turns.length)
  const toggleHistory = () => {
    const nextOpen = !open
    setOpen(nextOpen)
    if (nextOpen && !loadedRef.current) void loadPage(false)
  }
  return (
    <div className="steward-agent-turn-history">
      <button
        aria-controls={historyID}
        aria-expanded={open}
        className="steward-agent-history-toggle"
        onClick={toggleHistory}
        type="button"
      >
        {open ? '收起工具记录' : `查看 ${total} 轮工具记录`}
      </button>
      {open ? (
        <div className="steward-agent-turn-list" id={historyID}>
          {turns.map((turn) => (
            <div className="steward-execution-evidence" key={turn.id}>
              <span>第 {turn.round_index} 轮</span>
              <span>{(turn.tool_calls ?? []).map((call) => call.tool_name).join('、') || '最终回答'}</span>
              {(turn.tool_results ?? []).some((result) => result.error) ? <span>有失败结果</span> : null}
            </div>
          ))}
          {loading ? <small>正在加载回合…</small> : null}
          {loadError ? <p className="steward-execution-failure" role="alert">{loadError}</p> : null}
          {hasMore && nextBeforeRound > 0 ? (
            <button className="steward-button steward-button-secondary" disabled={loading} onClick={() => void loadPage(true)} type="button">
              加载更早回合
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function agentEpisodeStatusLabel(status: StewardAgentEpisode['status']) {
  const labels: Record<StewardAgentEpisode['status'], string> = {
    thinking: '思考中',
    executing: '执行中',
    awaiting_input: '等待补充',
    paused: '已暂停',
    completed: '已完成',
    failed: '失败',
    cancelled: '已取消',
    blocked: '已停止',
  }
  return labels[status]
}
