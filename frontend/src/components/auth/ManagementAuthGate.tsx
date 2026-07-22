import { type FormEvent, type ReactNode, useEffect, useState } from 'react'

type SessionStatus = {
  required: boolean
  authenticated: boolean
}

type ManagementAuthGateProps = {
  children: ReactNode
}

const sessionEndpoint = '/api/auth/session'
const sessionCheckIntervalMs = 60_000

function isSameOriginRequest(input: Parameters<typeof window.fetch>[0]) {
  try {
    const requestUrl = input instanceof Request ? input.url : input.toString()
    return new URL(requestUrl, window.location.href).origin === window.location.origin
  } catch {
    return false
  }
}

async function readSessionStatus(): Promise<SessionStatus> {
  const response = await fetch(sessionEndpoint, {
    cache: 'no-store',
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  })
  if (!response.ok) {
    throw new Error(`无法检查本机管理会话（HTTP ${response.status}）`)
  }
  return (await response.json()) as SessionStatus
}

export function ManagementAuthGate({ children }: ManagementAuthGateProps) {
  const [status, setStatus] = useState<SessionStatus | null>(null)
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    let active = true
    let sessionCheck: Promise<SessionStatus | null> | null = null
    const originalFetch = window.fetch

    const checkSession = () => {
      if (sessionCheck) return sessionCheck
      sessionCheck = readSessionStatus()
        .then((nextStatus) => {
          if (!active) return null
          setStatus(nextStatus)
          setError('')
          if (!nextStatus.authenticated) setToken('')
          return nextStatus
        })
        .catch((cause: unknown) => {
          if (!active) return null
          setStatus(null)
          setError(cause instanceof Error ? cause.message : '无法连接本机管理服务')
          return null
        })
        .finally(() => {
          sessionCheck = null
        })
      return sessionCheck
    }

    const observedFetch: typeof window.fetch = async (...args) => {
      const [input, init] = args
      const response = await originalFetch.call(window, input, init)
      const requestUrl = input instanceof Request ? input.url : input.toString()
      const url = new URL(requestUrl, window.location.href)
      if (response.status === 401 && isSameOriginRequest(args[0]) && url.pathname !== sessionEndpoint) {
        lockSession()
      }
      return response
    }

    const lockSession = () => {
      if (!active) return
      setStatus({ required: true, authenticated: false })
      setToken('')
      setSubmitting(false)
      setError('管理会话已过期。请从 MongoJSON Steward 启动器重新打开，或输入恢复密钥。')
    }

    const checkSessionWhenVisible = () => {
      if (document.visibilityState === 'visible') void checkSession()
    }

    window.fetch = observedFetch
    window.addEventListener('focus', checkSession)
    document.addEventListener('visibilitychange', checkSessionWhenVisible)
    const interval = window.setInterval(checkSession, sessionCheckIntervalMs)

    void checkSession()

    return () => {
      active = false
      window.clearInterval(interval)
      window.removeEventListener('focus', checkSession)
      document.removeEventListener('visibilitychange', checkSessionWhenVisible)
      if (window.fetch === observedFetch) window.fetch = originalFetch
    }
  }, [])

  async function authenticate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!token.trim()) return
    setSubmitting(true)
    setError('')
    try {
      const response = await fetch(sessionEndpoint, {
        method: 'POST',
        cache: 'no-store',
        credentials: 'same-origin',
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ token }),
      })
      if (!response.ok) {
        throw new Error(response.status === 401 ? '管理密钥不正确。' : `认证失败（HTTP ${response.status}）。`)
      }
      const nextStatus = (await response.json()) as SessionStatus
      if (!nextStatus.authenticated) {
        throw new Error('管理服务没有建立浏览器会话。')
      }
      setToken('')
      setStatus({ required: nextStatus.required, authenticated: nextStatus.authenticated })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '认证失败，请重试。')
    } finally {
      setSubmitting(false)
    }
  }

  if (status?.authenticated) return children

  return (
    <main className="management-auth-page">
      <section className="management-auth-card" aria-labelledby="management-auth-title">
        <span className="management-auth-mark" aria-hidden="true">S</span>
        <p className="management-auth-eyebrow">Mongojson Steward</p>
        <h1 id="management-auth-title">解锁本机管理界面</h1>
        <p className="management-auth-description">
          通常请从 Windows 的 MongoJSON Steward 启动器进入，系统会使用当前登录用户自动解锁。
          如果自动解锁不可用，可输入安装时生成的恢复密钥；默认位于当前用户的
          <code>%LOCALAPPDATA%\MongojsonSteward\management-access-token.txt</code>。
        </p>
        {status === null && !error ? <p className="management-auth-loading">正在检查管理服务…</p> : null}
        {status?.required ? (
          <form onSubmit={authenticate}>
            <label htmlFor="management-auth-token">管理密钥</label>
            <input
              id="management-auth-token"
              type="password"
              name="management-token"
              autoComplete="off"
              autoFocus
              value={token}
              onChange={(event) => setToken(event.target.value)}
              disabled={submitting}
            />
            <button type="submit" disabled={submitting || !token.trim()}>
              {submitting ? '正在验证…' : '进入工作区'}
            </button>
          </form>
        ) : null}
        {error ? (
          <div className="management-auth-error" role="alert">
            <strong>无法进入工作区</strong>
            <span>{error}</span>
            {status === null ? <button type="button" onClick={() => window.location.reload()}>重新检查</button> : null}
          </div>
        ) : null}
      </section>
    </main>
  )
}
