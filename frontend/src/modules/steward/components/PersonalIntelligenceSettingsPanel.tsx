import { useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import {
  updateStewardCollector,
  updateStewardIntelligenceSettings,
} from '../api'
import type {
  StewardBackgroundStatus,
  StewardCollectorConfig,
  StewardIntelligenceSettings,
} from '../types'
import { CollectorSettings } from './CollectorSettings'
import { intelligenceSettingsPayload } from './personalIntelligencePresentation'

type Props = {
  collectors: StewardCollectorConfig[]
  error?: string
  loading: boolean
  onChanged: () => Promise<void>
  settings: StewardIntelligenceSettings | null
  status: StewardBackgroundStatus | null
}

export function PersonalIntelligenceSettingsPanel({ collectors, error, loading, onChanged, settings, status }: Props) {
  const [savingSettings, setSavingSettings] = useState(false)
  const [savingCollector, setSavingCollector] = useState('')
  const [actionError, setActionError] = useState('')
  const [success, setSuccess] = useState('')
  const collectorHealth = useMemo(() => {
    const health = new Map<string, { fresh: number; total: number; error: string }>()
    for (const source of status?.pipeline.sources ?? []) {
      const current = health.get(source.collector_name) ?? { fresh: 0, total: 0, error: '' }
      current.total += 1
      if (source.fresh) current.fresh += 1
      if (source.last_error) current.error = source.last_error
      health.set(source.collector_name, current)
    }
    return health
  }, [status])

  const saveSettings = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!settings) return
    setSavingSettings(true)
    setActionError('')
    setSuccess('')
    try {
      await updateStewardIntelligenceSettings(intelligenceSettingsPayload(new FormData(event.currentTarget), settings.revision))
      setSuccess('持续智能配置已保存，并已切换到新修订。')
      await onChanged()
    } catch (reason) {
      setActionError(readableError(reason, '持续智能配置保存失败'))
    } finally {
      setSavingSettings(false)
    }
  }

  const saveCollector = async (collector: StewardCollectorConfig, payload: { enabled?: boolean; settings?: Record<string, unknown> }) => {
    setSavingCollector(collector.name)
    setActionError('')
    setSuccess('')
    try {
      await updateStewardCollector(collector.name, payload)
      setSuccess(`${collectorLabel(collector.name)} 已更新。`)
      await onChanged()
    } catch (reason) {
      setActionError(readableError(reason, `${collectorLabel(collector.name)} 更新失败`))
    } finally {
      setSavingCollector('')
    }
  }

  if (!settings && loading) return <PanelMessage kind="loading" text="正在读取持续智能配置与采集器…" />
  if (!settings) return <PanelMessage kind="error" text={error || '持续智能配置不可用。请检查后端后重试。'} />

  return <div className="steward-intelligence-stack">
    {error ? <PanelMessage kind="warning" text={`部分配置刷新失败：${error}`} /> : null}
    {actionError ? <PanelMessage kind="error" text={actionError} /> : null}
    {success ? <PanelMessage kind="success" text={success} /> : null}

    <section className="steward-intelligence-section">
      <header>
        <div>
          <h3>活动采集器</h3>
          <small>开关决定是否采集；运行位置和真实心跳分开显示。修改会保留审计记录。</small>
        </div>
        <span className="steward-revision-badge">{collectors.filter((item) => item.enabled).length}/{collectors.length} 已启用</span>
      </header>
      <div className="steward-collector-config-list">
        {collectors.map((collector) => {
          const health = collectorHealth.get(collector.name)
          const busy = savingCollector === collector.name
          return <article className="steward-collector-config-card" key={collector.id || collector.name}>
            <div className="steward-collector-config-heading">
              <div>
                <strong>{collectorLabel(collector.name)}</strong>
                <small>{collector.scope_summary || '未提供采集范围说明'}</small>
              </div>
              <label className="steward-toggle">
                <input
                  checked={collector.enabled}
                  disabled={Boolean(savingCollector)}
                  onChange={(event) => void saveCollector(collector, { enabled: event.target.checked })}
                  type="checkbox"
                />
                <span>{collector.enabled ? '已启用' : '已停用'}</span>
              </label>
            </div>
            <div className="steward-collector-meta">
              <span>运行位置：{executionTargetLabel(collector.execution_target)}</span>
              <span>{collector.user_overridden ? '用户已调整' : '采用系统默认'}</span>
              <span className={health && health.total > 0 && health.fresh === health.total ? 'is-positive' : collector.enabled ? 'is-warning' : ''}>
                {!collector.enabled ? '未运行' : health?.total ? `${health.fresh}/${health.total} 数据新鲜` : '等待首次心跳'}
              </span>
              {collector.last_run_at ? <span>最近运行：{formatDate(collector.last_run_at)}</span> : null}
            </div>
            {collector.last_error || health?.error ? <p className="steward-inline-warning">最近错误：{collector.last_error || health?.error}</p> : null}
            <CollectorSettings
              busy={busy}
              collector={collector}
              key={`${collector.name}:${collector.updated_at}`}
              onSave={(collectorSettings) => saveCollector(collector, { settings: collectorSettings })}
            />
          </article>
        })}
        {!collectors.length ? <PanelMessage kind="empty" text="后端尚未注册任何活动采集器。" /> : null}
      </div>
    </section>

    <form className="steward-intelligence-settings-form" key={settings.revision} onSubmit={(event) => void saveSettings(event)}>
      <section className="steward-intelligence-section">
        <header><div><h3>持续智能总开关</h3><small>关闭后停止新采集和后台整理；已有报告、画像与证据仍可查看。</small></div><span className="steward-revision-badge">修订 {settings.revision}</span></header>
        <div className="steward-settings-grid">
          <label className="steward-settings-toggle is-wide">
            <input defaultChecked={settings.enabled} name="enabled" type="checkbox" />
            <span><strong>启用持续智能</strong><small>让采集、批次整理、报告与画像循环在后台运行。</small></span>
          </label>
          <label>处理模式<select defaultValue={settings.mode} name="mode"><option value="batch">持久化批次（推荐）</option><option value="legacy">兼容旧流程</option></select></label>
          <label>采集深度<select defaultValue={settings.capture_profile} name="capture_profile"><option value="metadata">只采集元数据</option><option value="hybrid">混合上下文</option><option value="deep">深度上下文</option></select></label>
          <label className="is-wide">时区<input defaultValue={settings.timezone} name="timezone" placeholder="Asia/Shanghai" required /><small>IANA 时区名称，用于日报边界、周报和提醒时间。</small></label>
        </div>
      </section>

      <section className="steward-intelligence-section">
        <header><div><h3>采样、会话与批次</h3><small>这些值控制数据进入持久化流水线的节奏，不改变历史记录。</small></div></header>
        <div className="steward-settings-grid">
          <NumberField label="活动采样（秒）" min={1} name="activity_sample_seconds" value={settings.activity_sample_seconds} />
          <NumberField label="会话整理（秒）" min={1} name="sessionize_interval_seconds" value={settings.sessionize_interval_seconds} />
          <NumberField label="批次间隔（秒）" min={1} name="batch_interval_seconds" value={settings.batch_interval_seconds} />
          <NumberField label="跨日宽限（秒）" min={1} name="boundary_grace_seconds" value={settings.boundary_grace_seconds} />
        </div>
      </section>

      <section className="steward-intelligence-section">
        <header><div><h3>报告与画像周期</h3><small>日报、周报和月报按本地时间生成；失败后按追赶天数补齐。</small></div></header>
        <div className="steward-settings-grid">
          <label>日报兜底时间<input defaultValue={settings.daily_report_fallback_local} name="daily_report_fallback_local" required type="time" /></label>
          <label>周报星期<select defaultValue={settings.weekly_report_day} name="weekly_report_day">{weekdays.map((label, index) => <option key={label} value={index}>{label}</option>)}</select></label>
          <label>周报时间<input defaultValue={settings.weekly_report_local} name="weekly_report_local" required type="time" /></label>
          <label>月报时间<input defaultValue={settings.monthly_report_local} name="monthly_report_local" required type="time" /></label>
          <NumberField label="最近画像窗口（天）" min={1} name="recent_profile_days" value={settings.recent_profile_days} />
          <NumberField label="长期事实最少证据天数" min={1} name="stable_min_evidence_days" value={settings.stable_min_evidence_days} />
          <NumberField label="画像启动回看（天）" min={1} name="profile_bootstrap_days" value={settings.profile_bootstrap_days} />
          <NumberField label="报告追赶范围（天）" min={1} name="report_catchup_days" value={settings.report_catchup_days} />
        </div>
      </section>

      <details className="steward-intelligence-form-section">
        <summary>高级运行、提醒基线与保留期限</summary>
        <div className="steward-settings-grid">
          <NumberField label="后台最大模型轮次（0 不限制）" min={0} name="background_max_rounds" value={settings.background_max_rounds} />
          <NumberField label="后台最大工具调用（0 不限制）" min={0} name="background_max_tool_calls" value={settings.background_max_tool_calls} />
          <NumberField label="后台最长运行秒数（0 不限制）" min={0} name="background_max_duration_seconds" value={settings.background_max_duration_seconds} />
          <NumberField label="无进展停止轮数" min={1} name="background_no_progress_limit" value={settings.background_no_progress_limit} />
          <label>默认安静时段开始<input defaultValue={settings.quiet_start_local} name="quiet_start_local" required type="time" /></label>
          <label>默认安静时段结束<input defaultValue={settings.quiet_end_local} name="quiet_end_local" required type="time" /></label>
          <NumberField label="默认每日提醒软预算" min={0} name="reminder_daily_soft_budget" value={settings.reminder_daily_soft_budget} />
          <NumberField label="默认单类提醒软预算" min={0} name="reminder_category_soft_budget" value={settings.reminder_category_soft_budget} />
          <NumberField label="默认提醒冷却（秒）" min={0} name="reminder_cooldown_seconds" value={settings.reminder_cooldown_seconds} />
          <NumberField label="原始元数据保留（天）" min={0} name="raw_metadata_retention_days" value={settings.raw_metadata_retention_days} />
          <NumberField label="未引用媒体保留（天）" min={0} name="unreferenced_media_retention_days" value={settings.unreferenced_media_retention_days} />
        </div>
      </details>

      <div className="steward-settings-actions">
        <small>保存时使用修订号防止覆盖其他页面或后台刚完成的修改。</small>
        <button className="steward-button" disabled={savingSettings || Boolean(savingCollector)} type="submit">{savingSettings ? '正在保存…' : '保存持续智能配置'}</button>
      </div>
    </form>
  </div>
}

function NumberField({ label, min, name, value }: { label: string; min: number; name: keyof StewardIntelligenceSettings; value: number }) {
  return <label>{label}<input defaultValue={value} min={min} name={name} required step="1" type="number" /></label>
}

function PanelMessage({ kind, text }: { kind: 'empty' | 'error' | 'loading' | 'success' | 'warning'; text: string }) {
  return <p aria-live={kind === 'error' ? 'assertive' : 'polite'} className={`steward-intelligence-message is-${kind}`} role={kind === 'error' ? 'alert' : 'status'}>{text}</p>
}

function collectorLabel(value: string) {
  return ({
    'manual-input': '手动输入',
    'windows-activity': 'Windows 活动',
    'browser-link': '浏览器活动',
    'clipboard-summary': '剪贴板摘要',
    'watched-directory': '目录变更',
    'system-status': '系统状态',
    'screenpipe-bridge': 'Screenpipe 增强',
    'activitywatch-bridge': 'ActivityWatch 增强',
  } as Record<string, string>)[value] || value
}

function executionTargetLabel(value: string) {
  return ({ main: '主服务', companion: '登录会话 Companion', system: '系统服务', auto: '自动选址' } as Record<string, string>)[value] || value || '未声明'
}

function readableError(reason: unknown, fallback: string) {
  const value = reason instanceof Error ? reason.message : fallback
  try {
    const parsed = JSON.parse(value) as { error?: string }
    return parsed.error || value
  } catch {
    return value
  }
}

function formatDate(value: string) {
  return new Date(value).toLocaleString('zh-CN', { dateStyle: 'short', timeStyle: 'short' })
}

const weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
