import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  createStewardConversation,
  decideStewardConversationSuggestion,
  getStewardConversationMessages,
  getStewardConversations,
  sendStewardConversationMessage,
} from '../../../lib/api/client'
import type {
  StewardConversation,
  StewardConversationMessage,
  StewardConversationSuggestion,
} from '../../../types/tooling'
import { formatDate } from './model'

type Props = {
  onDataChanged: () => Promise<void>
}

export function ConversationWorkspace({ onDataChanged }: Props) {
  const [conversations, setConversations] = useState<StewardConversation[]>([])
  const [activeId, setActiveId] = useState('')
  const [messages, setMessages] = useState<StewardConversationMessage[]>([])
  const [draft, setDraft] = useState('')
  const [dataLevel, setDataLevel] = useState('D0')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const messageEndRef = useRef<HTMLDivElement | null>(null)

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
      const result = await createStewardConversation({ data_level: dataLevel })
      await loadConversations(result.conversation.id)
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
        const created = await createStewardConversation({ data_level: dataLevel })
        conversationId = created.conversation.id
        setActiveId(conversationId)
      }
      setDraft('')
      await sendStewardConversationMessage(conversationId, {
        content,
        data_level: dataLevel,
        context_limit: 10,
      })
      await loadConversations(conversationId)
      await onDataChanged()
    } catch (reason) {
      setDraft(content)
      setError(reason instanceof Error ? reason.message : '发送消息失败')
    } finally {
      setBusy(false)
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

  return (
    <section className="steward-conversation-shell" aria-label="管家对话">
      <aside className="steward-conversation-list">
        <div className="steward-conversation-list-header">
          <strong>对话</strong>
          <button className="steward-icon-button steward-button-secondary" disabled={busy} onClick={createConversation} type="button">
            新建
          </button>
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
        <div className="steward-conversation-messages" aria-live="polite">
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
                <span>{message.data_level} · {formatDate(message.created_at)}</span>
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
            </article>
          ))}
          {busy ? <div className="steward-message-pending">处理中...</div> : null}
          <div ref={messageEndRef} />
        </div>
        {error ? <div className="steward-conversation-error" role="alert">{error}</div> : null}
        <form className="steward-conversation-composer" onSubmit={submit}>
          <textarea
            aria-label="消息"
            disabled={busy}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault()
                event.currentTarget.form?.requestSubmit()
              }
            }}
            placeholder="给管家发送消息"
            rows={3}
            value={draft}
          />
          <div className="steward-conversation-actions">
            <select aria-label="数据级别" onChange={(event) => setDataLevel(event.target.value)} value={dataLevel}>
              <option value="D0">D0 手动输入</option>
              <option value="D1">D1 公开资料</option>
            </select>
            <button className="steward-button" disabled={busy || !draft.trim()} type="submit">发送</button>
          </div>
        </form>
      </div>
    </section>
  )
}
