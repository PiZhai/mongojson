import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import {
  decideStewardNotification,
  getStewardNotificationEndpoints,
  getStewardNotifications,
  saveStewardNotificationEndpoint,
  testStewardNotificationEndpoint,
} from '../api'
import type { StewardNotification, StewardNotificationEndpoint } from '../types'

type Props = { open: boolean; onClose: () => void }

export function NotificationCenterDialog({ open, onClose }: Props) {
  const [notifications, setNotifications] = useState<StewardNotification[]>([])
  const [endpoints, setEndpoints] = useState<StewardNotificationEndpoint[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setBusy(true); setError('')
    try {
      const [messages, channels] = await Promise.all([getStewardNotifications('', 100), getStewardNotificationEndpoints()])
      setNotifications(messages.notifications); setEndpoints(channels.endpoints)
    } catch (reason) { setError(reason instanceof Error ? reason.message : '通知中心加载失败') }
    finally { setBusy(false) }
  }, [])

  useEffect(() => {
    if (!open) return
    const initialLoad = window.setTimeout(() => {
      void load()
    }, 0)
    const timer = window.setInterval(() => {
      void load()
    }, 10_000)
    return () => {
      window.clearTimeout(initialLoad)
      window.clearInterval(timer)
    }
  }, [load, open])
  useEffect(() => {
    if (!open) return
    const listener = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', listener)
    return () => window.removeEventListener('keydown', listener)
  }, [onClose, open])

  if (!open) return null
  const decide = async (id: string, decision: 'acknowledge' | 'snooze' | 'cancel' | 'resend', seconds = 0) => {
    setBusy(true); setError('')
    try { await decideStewardNotification(id, decision, seconds); await load() }
    catch (reason) { setError(reason instanceof Error ? reason.message : '通知操作失败') }
    finally { setBusy(false) }
  }
  const test = async (id: string) => {
    setBusy(true); setError('')
    try { await testStewardNotificationEndpoint(id); await load() }
    catch (reason) { setError(reason instanceof Error ? reason.message : '测试通知发送失败') }
    finally { setBusy(false) }
  }

  return <div className="steward-archive-modal" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
    <section aria-modal="true" className="steward-notification-dialog" role="dialog">
      <header className="steward-archive-dialog-header">
        <div><h2>通知中心</h2><small>网页只显示历史；实际通知由系统、其他设备或邮件送达</small></div>
        <button className="steward-archive-close" onClick={onClose} type="button">关闭</button>
      </header>
      {error ? <div className="steward-tool-error" role="alert">{error}</div> : null}
      <div className="steward-notification-layout" aria-busy={busy}>
        <section className="steward-notification-history">
          <h3>通知历史</h3>
          {notifications.map((item) => <article className={`is-${item.priority}`} key={item.id}>
            <header><div><strong>{item.title}</strong><small>{new Date(item.scheduled_at).toLocaleString('zh-CN')} · {item.category}</small></div><span>{statusLabel(item.status)}</span></header>
            <p>{item.body}</p>
            <div className="steward-notification-deliveries">{item.deliveries.map((delivery) => <span className={delivery.status === 'accepted' ? 'is-ok' : delivery.status === 'failed' ? 'is-error' : ''} key={delivery.id} title={delivery.last_error}>{channelLabel(delivery.channel)}：{statusLabel(delivery.status)}</span>)}</div>
            {item.deliveries.some((delivery) => delivery.last_error) ? <details><summary>查看错误与处理建议</summary>{item.deliveries.filter((delivery) => delivery.last_error).map((delivery) => <p key={delivery.id}>{channelLabel(delivery.channel)}：{delivery.last_error}<br />请检查对应端点、网络和凭据，然后点击重新发送。</p>)}</details> : null}
            <footer>
              {!['acknowledged', 'cancelled'].includes(item.status) ? <button onClick={() => void decide(item.id, 'acknowledge')} type="button">知道了</button> : null}
              {!['acknowledged', 'cancelled'].includes(item.status) ? <button onClick={() => void decide(item.id, 'snooze', 1800)} type="button">30 分钟后</button> : null}
              {item.status === 'failed' ? <button onClick={() => void decide(item.id, 'resend')} type="button">重新发送</button> : null}
            </footer>
          </article>)}
          {!busy && notifications.length === 0 ? <p className="steward-notification-empty">暂无通知。</p> : null}
        </section>
        <section className="steward-notification-endpoints">
          <h3>投递渠道</h3>
          {endpoints.map((endpoint) => <div className="steward-notification-endpoint" key={endpoint.id}>
            <div><strong>{endpoint.name}</strong><small>{channelLabel(endpoint.channel)} · {endpoint.enabled ? '已启用' : '已停用'}</small></div>
            <button disabled={busy || !endpoint.enabled} onClick={() => void test(endpoint.id)} type="button">发送测试</button>
            {endpoint.last_error ? <p className="is-error">{endpoint.last_error}</p> : endpoint.last_success_at ? <p>最近成功：{new Date(endpoint.last_success_at).toLocaleString('zh-CN')}</p> : null}
          </div>)}
          <NtfyForm busy={busy} onSaved={load} setError={setError} />
          <EmailForm busy={busy} onSaved={load} setError={setError} />
        </section>
      </div>
    </section>
  </div>
}

function NtfyForm({ busy, onSaved, setError }: FormProps) {
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); const data = new FormData(event.currentTarget); setError('')
    try {
      await saveStewardNotificationEndpoint({ channel: 'ntfy', name: 'ntfy', enabled: true, config: { url: data.get('url'), topic: data.get('topic') }, secret: data.get('token') ? { token: data.get('token') } : {} })
      await onSaved(); event.currentTarget.reset()
    } catch (reason) { setError(reason instanceof Error ? reason.message : 'ntfy 保存失败') }
  }
  return <form className="steward-notification-form" onSubmit={(event) => void submit(event)}><h4>ntfy 跨设备推送</h4><input name="url" placeholder="https://ntfy.sh" required /><input name="topic" placeholder="私有 Topic" required /><input name="token" placeholder="访问令牌（可选，已配置时留空保留）" type="password" /><button disabled={busy} type="submit">保存 ntfy</button></form>
}

function EmailForm({ busy, onSaved, setError }: FormProps) {
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); const data = new FormData(event.currentTarget); setError('')
    try {
      await saveStewardNotificationEndpoint({ channel: 'email', name: '邮件', enabled: true, config: { host: data.get('host'), port: Number(data.get('port')), from: data.get('from'), to: data.get('to'), username: data.get('username') }, secret: data.get('password') ? { password: data.get('password') } : {} })
      await onSaved(); event.currentTarget.reset()
    } catch (reason) { setError(reason instanceof Error ? reason.message : '邮件渠道保存失败') }
  }
  return <form className="steward-notification-form" onSubmit={(event) => void submit(event)}><h4>邮件降级渠道</h4><input name="host" placeholder="SMTP 主机" required /><input defaultValue="587" name="port" placeholder="端口" required type="number" /><input name="from" placeholder="发件邮箱" required type="email" /><input name="to" placeholder="接收邮箱" required type="email" /><input name="username" placeholder="SMTP 用户名" /><input name="password" placeholder="SMTP 密码（已配置时留空保留）" type="password" /><button disabled={busy} type="submit">保存邮件</button></form>
}

type FormProps = { busy: boolean; onSaved: () => Promise<void>; setError: (value: string) => void }
function channelLabel(value: string) { return ({ system: '本机系统', linux_desktop: 'Linux 桌面', ntfy: '其他设备', email: '邮件' } as Record<string, string>)[value] || value }
function statusLabel(value: string) { return ({ queued: '等待投递', retrying: '等待重试', sending: '发送中', accepted: '已提交', sent: '已发送', acknowledged: '已确认', cancelled: '已取消', failed: '失败', expired: '已过期' } as Record<string, string>)[value] || value }
