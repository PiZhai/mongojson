import { useEffect, useState, type FormEvent } from 'react'
import {
  getStewardModelSettings,
  probeStewardModelConnection,
  updateStewardModelSettings,
} from '../api'
import type { StewardModelSettings } from '../types'

type Props = {
  open: boolean
  onClose: () => void
}

type FormState = {
  provider: string
  baseURL: string
  model: string
  apiKey: string
  allowNoAPIKey: boolean
  timeoutSeconds: number
}

const emptyForm: FormState = {
  provider: 'openai-compatible',
  baseURL: 'https://api.openai.com/v1',
  model: '',
  apiKey: '',
  allowNoAPIKey: false,
  timeoutSeconds: 30,
}

export function ModelSettingsDialog({ open, onClose }: Props) {
  const [settings, setSettings] = useState<StewardModelSettings | null>(null)
  const [form, setForm] = useState<FormState>(emptyForm)
  const [apiKeyChanged, setAPIKeyChanged] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const applySettings = (value: StewardModelSettings) => {
    setSettings(value)
    setForm({
      provider: value.provider,
      baseURL: value.base_url,
      model: value.model,
      apiKey: '',
      allowNoAPIKey: value.allow_no_api_key,
      timeoutSeconds: value.timeout_seconds || 30,
    })
    setAPIKeyChanged(false)
  }

  useEffect(() => {
    if (!open) return
    let alive = true
    getStewardModelSettings()
      .then(({ settings: value }) => {
        if (alive) applySettings(value)
      })
      .catch((reason: unknown) => {
        if (alive) setError(reason instanceof Error ? reason.message : '加载模型配置失败')
      })
    return () => {
      alive = false
    }
  }, [open])

  if (!open) return null

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const payload: Parameters<typeof updateStewardModelSettings>[0] = {
        provider: form.provider,
        base_url: form.baseURL,
        model: form.model,
        allow_no_api_key: form.allowNoAPIKey,
        max_data_level: 'D6',
        timeout_seconds: form.timeoutSeconds,
      }
      if (apiKeyChanged) payload.api_key = form.apiKey
      const result = await updateStewardModelSettings(payload)
      applySettings(result.settings)
      setNotice('配置已加密保存并立即生效。')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '保存模型配置失败')
    } finally {
      setBusy(false)
    }
  }

  const probe = async () => {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const { probe: result } = await probeStewardModelConnection()
      if (!result.ok) throw new Error(result.error || '模型连接测试失败')
      setNotice(`连接成功，模型在 ${result.duration_ms} ms 内返回了有效响应。`)
      const refreshed = await getStewardModelSettings()
      applySettings(refreshed.settings)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '模型连接测试失败')
    } finally {
      setBusy(false)
    }
  }

  const enabled = form.provider !== 'disabled'
  const advisorReady = settings?.advisor.enabled === true
  const plannerReady = settings?.planner.enabled === true

  return (
    <div className="steward-model-modal" role="presentation" onMouseDown={(event) => {
      if (event.target === event.currentTarget && !busy) onClose()
    }}>
      <section aria-labelledby="steward-model-title" aria-modal="true" className="steward-model-dialog" role="dialog">
        <header className="steward-model-dialog-header">
          <div>
            <small>对话、规划与自主整理共用</small>
            <h2 id="steward-model-title">AI 模型配置</h2>
          </div>
          <button aria-label="关闭模型配置" className="steward-model-close" disabled={busy} onClick={onClose} type="button">×</button>
        </header>

        <form className="steward-model-form" onSubmit={save}>
          <div className="steward-model-status-row">
            <span className={advisorReady ? 'is-ready' : ''}>对话理解 {advisorReady ? '已就绪' : '未就绪'}</span>
            <span className={plannerReady ? 'is-ready' : ''}>执行规划 {plannerReady ? '已就绪' : '本地规则模式'}</span>
            {settings ? <small>来源：{settings.source === 'database' ? '网页安全配置' : settings.source === 'environment' ? '启动环境变量' : '默认值'}</small> : null}
          </div>

          <label>
            <span>模型服务</span>
            <select disabled={busy} onChange={(event) => setForm({ ...form, provider: event.target.value })} value={form.provider}>
              <option value="openai-compatible">OpenAI 兼容接口</option>
              <option value="disabled">停用大模型</option>
            </select>
          </label>

          <label>
            <span>接口地址</span>
            <input disabled={busy || !enabled} onChange={(event) => setForm({ ...form, baseURL: event.target.value })} placeholder="https://api.example.com/v1" required={enabled} type="url" value={form.baseURL} />
            <small>填写兼容 OpenAI Chat Completions 的基础地址，不要包含 /chat/completions。</small>
          </label>

          <label>
            <span>模型名称</span>
            <input disabled={busy || !enabled} onChange={(event) => setForm({ ...form, model: event.target.value })} placeholder="服务商提供的模型 ID" required={enabled} value={form.model} />
          </label>

          <label>
            <span>API Key</span>
            <div className="steward-model-secret-row">
              <input
                autoComplete="new-password"
                disabled={busy || !enabled || form.allowNoAPIKey}
                onChange={(event) => {
                  setForm({ ...form, apiKey: event.target.value })
                  setAPIKeyChanged(true)
                }}
                placeholder={settings?.api_key_configured ? `已配置 ${settings.api_key_mask || '••••••••'}，留空则保持不变` : '输入 API Key'}
                type="password"
                value={form.apiKey}
              />
              {settings?.api_key_configured ? (
                <button className="steward-button-secondary" disabled={busy || !enabled} onClick={() => {
                  setForm({ ...form, apiKey: '', allowNoAPIKey: true })
                  setAPIKeyChanged(true)
                }} type="button">移除</button>
              ) : null}
            </div>
            <small>密钥使用本机加密密钥加密保存；服务端不会向网页回传原文。</small>
          </label>

          <label className="steward-model-checkbox">
            <input checked={form.allowNoAPIKey} disabled={busy || !enabled} onChange={(event) => setForm({ ...form, allowNoAPIKey: event.target.checked })} type="checkbox" />
            <span>本机模型不使用 API Key</span>
            <small>只允许 localhost、127.0.0.1 或 ::1，适用于 Ollama、LM Studio 等本机服务。</small>
          </label>

          <div className="steward-model-grid">
            <label>
              <span>请求超时（秒）</span>
              <input disabled={busy || !enabled} max={120} min={1} onChange={(event) => setForm({ ...form, timeoutSeconds: Number(event.target.value) })} type="number" value={form.timeoutSeconds} />
            </label>
          </div>

          {error ? <div className="steward-model-feedback is-error" role="alert">{error}</div> : null}
          {notice ? <div className="steward-model-feedback is-success" role="status">{notice}</div> : null}

          <footer className="steward-model-actions">
            <button className="steward-button steward-button-secondary" disabled={busy || !settings?.advisor.enabled} onClick={() => void probe()} type="button">测试连接</button>
            <button className="steward-button" disabled={busy} type="submit">{busy ? '处理中…' : '保存并生效'}</button>
          </footer>
        </form>
      </section>
    </div>
  )
}
