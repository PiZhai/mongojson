import { type FormEvent, type ReactNode, useEffect, useState } from 'react'
import {
  clearManagementSessionToken,
  ensureManagementTransport,
  getManagementSessionToken,
  setManagementSessionToken,
  unauthorizedEventName,
} from '../../platform/auth/managementSession'

type SessionStatus = {
  required: boolean
  authenticated: boolean
  session_token?: string
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
      const requestUrl = input instanceof Request ? input.url : input.toString()
      const url = new URL(requestUrl, window.location.href)
      const headers = new Headers(input instanceof Request ? input.headers : undefined)
      new Headers(init?.headers).forEach((value, name) => headers.set(name, value))
      if (
        getManagementSessionToken()
        && url.origin === window.location.origin
        && url.pathname.startsWith('/api/')
        && !headers.has('Authorization')
      ) {
        headers.set('Authorization', `Bearer ${getManagementSessionToken()}`)
      }
      const response = await originalFetch.call(window, input, { ...init, headers })
      if (response.status === 401 && isSameOriginRequest(args[0])) {
        clearManagementSessionToken()
        window.dispatchEvent(new Event(unauthorizedEventName))
      }
      return response
    }

    const lockSession = () => {
      if (!active) return
      setStatus({ required: true, authenticated: false })
      clearManagementSessionToken()
      setToken('')
      setSubmitting(false)
      setError('管理会话已过期，请重新输入管理密钥。')
    }

    const checkSessionWhenVisible = () => {
      if (document.visibilityState === 'visible') void checkSession()
    }

    window.fetch = observedFetch
    window.addEventListener(unauthorizedEventName, lockSession)
    window.addEventListener('focus', checkSession)
    document.addEventListener('visibilitychange', checkSessionWhenVisible)
    const interval = window.setInterval(checkSession, sessionCheckIntervalMs)

    void (async () => {
      const nextStatus = await checkSession()
      if (nextStatus?.required) {
        const transportReady = await ensureManagementTransport()
        if (!transportReady && active) {
          setError('当前浏览器无法启动安全的本机资源认证通道，请使用支持 Service Worker 的现代浏览器。')
        }
      }
    })()

    return () => {
      active = false
      window.clearInterval(interval)
      window.removeEventListener(unauthorizedEventName, lockSession)
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
      clearManagementSessionToken()
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
      if (nextStatus.required && !nextStatus.session_token) {
        throw new Error('管理服务没有返回可用的短期会话令牌。')
      }
      const transportReady = await setManagementSessionToken(nextStatus.session_token ?? '')
      if (!transportReady) {
        clearManagementSessionToken()
        throw new Error('无法启动安全的本机资源认证通道。')
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
          请输入安装时生成的管理密钥。密钥只用于换取当前页面内存中的短期会话令牌；刷新或关闭页面后需要重新解锁。
          默认可从当前用户的 <code>%LOCALAPPDATA%\MongojsonSteward\management-access-token.txt</code> 读取。
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
