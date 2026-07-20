import { useCallback, useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import {
  correctStewardProfileFact,
  getStewardBackgroundStatus,
  getStewardCollectors,
  getStewardIntelligenceSettings,
  getStewardProfile,
  getStewardProfileHistory,
  getStewardReminderFeedback,
  getStewardReminderPolicy,
  getStewardReceptivityWindows,
  getStewardReports,
  regenerateStewardReport,
  runStewardActivityBatches,
  updateStewardIntelligenceSettings,
  updateStewardReminderPolicy,
} from '../api'
import type {
  StewardBackgroundStatus,
  StewardCollectorConfig,
  StewardIntelligenceSettings,
  StewardModelUsageMetrics,
  StewardProfileFact,
  StewardProfileView,
  StewardReminderFeedback,
  StewardReminderPolicy,
  StewardReport,
  StewardReceptivityWindow,
} from '../types'
import { backgroundStateLabel, slotLabel } from './personalIntelligencePresentation'
import { PersonalIntelligenceSettingsPanel } from './PersonalIntelligenceSettingsPanel'

type Props = { open: boolean; onClose: () => void }
type Tab = 'status' | 'reports' | 'profile' | 'reminders' | 'settings'

const tabs: Array<{ id: Tab; label: string }> = [
  { id: 'status', label: '后台状态' },
  { id: 'reports', label: '每日与周期报告' },
  { id: 'profile', label: '用户画像' },
  { id: 'reminders', label: '提醒节律' },
  { id: 'settings', label: '采集与周期设置' },
]

export function PersonalIntelligenceDialog({ open, onClose }: Props) {
  const [tab, setTab] = useState<Tab>('status')
  const [status, setStatus] = useState<StewardBackgroundStatus | null>(null)
  const [reports, setReports] = useState<StewardReport[]>([])
  const [profile, setProfile] = useState<StewardProfileView | null>(null)
  const [policy, setPolicy] = useState<StewardReminderPolicy | null>(null)
  const [feedback, setFeedback] = useState<StewardReminderFeedback[]>([])
  const [windows, setWindows] = useState<StewardReceptivityWindow[]>([])
  const [collectors, setCollectors] = useState<StewardCollectorConfig[]>([])
  const [settings, setSettings] = useState<StewardIntelligenceSettings | null>(null)
  const [busy, setBusy] = useState(false)
  const [configurationBusy, setConfigurationBusy] = useState(true)
  const [error, setError] = useState('')
  const [configurationError, setConfigurationError] = useState('')

  const loadStatus = useCallback(async () => {
    const result = await getStewardBackgroundStatus()
    setStatus(result.status)
  }, [])

  const loadConfiguration = useCallback(async () => {
    setConfigurationBusy(true)
    setConfigurationError('')
    try {
      const [settingsResult, collectorResult] = await Promise.all([
        getStewardIntelligenceSettings(),
        getStewardCollectors(),
      ])
      setSettings(settingsResult.settings)
      setCollectors(collectorResult.collectors)
    } catch (reason) {
      setConfigurationError(reason instanceof Error ? reason.message : '持续智能配置加载失败')
    } finally {
      setConfigurationBusy(false)
    }
  }, [])

  const loadAll = useCallback(async () => {
    setBusy(true)
    setError('')
    try {
      const [statusResult, reportResult, profileResult, policyResult, feedbackResult, windowResult] = await Promise.all([
        getStewardBackgroundStatus(), getStewardReports('', 60), getStewardProfile(),
        getStewardReminderPolicy(), getStewardReminderFeedback(100), getStewardReceptivityWindows(100),
      ])
      setStatus(statusResult.status)
      setReports(reportResult.reports)
      setProfile(profileResult.profile)
      setPolicy(policyResult.policy)
      setFeedback(feedbackResult.feedback)
      setWindows(windowResult.windows)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '个人智能数据加载失败')
    } finally {
      setBusy(false)
    }
    await loadConfiguration()
  }, [loadConfiguration])

  useEffect(() => {
    if (!open) return
    const initial = window.setTimeout(() => void loadAll(), 0)
    const timer = window.setInterval(() => void loadStatus().catch((reason) => {
      setError(reason instanceof Error ? reason.message : '后台状态刷新失败')
    }), 10_000)
    return () => { window.clearTimeout(initial); window.clearInterval(timer) }
  }, [loadAll, loadStatus, open])

  useEffect(() => {
    if (!open) return
    const listener = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', listener)
    return () => window.removeEventListener('keydown', listener)
  }, [onClose, open])

  if (!open) return null
  return <div className="steward-archive-modal" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
    <section aria-labelledby="steward-intelligence-title" aria-modal="true" className="steward-intelligence-dialog" role="dialog">
      <header className="steward-archive-dialog-header">
        <div>
          <h2 id="steward-intelligence-title">个人智能</h2>
          <small>检查后台是否真的工作，并查看报告、画像依据、提醒学习结果与采集配置</small>
        </div>
        <button aria-label="关闭个人智能" className="steward-archive-close" onClick={onClose} type="button">关闭</button>
      </header>
      <nav aria-label="个人智能视图" className="steward-intelligence-tabs" role="tablist">
        {tabs.map((item) => <button aria-selected={tab === item.id} className={tab === item.id ? 'is-active' : ''} key={item.id} onClick={() => setTab(item.id)} role="tab" type="button">{item.label}</button>)}
      </nav>
      {error ? <div className="steward-tool-error" role="alert">{humanizeApiError(error)}</div> : null}
      <div aria-busy={busy} className="steward-intelligence-content">
        {tab === 'status' ? <BackgroundPanel onChanged={loadAll} settings={settings} status={status} /> : null}
        {tab === 'reports' ? <ReportsPanel onChanged={loadAll} reports={reports} /> : null}
        {tab === 'profile' ? <ProfilePanel onChanged={loadAll} profile={profile} /> : null}
        {tab === 'reminders' ? <ReminderPanel feedback={feedback} onChanged={loadAll} policy={policy} windows={windows} /> : null}
        {tab === 'settings' ? <PersonalIntelligenceSettingsPanel collectors={collectors} error={configurationError} loading={configurationBusy} onChanged={loadConfiguration} settings={settings} status={status} /> : null}
      </div>
    </section>
  </div>
}

export function BackgroundPanel({ onChanged, settings, status }: {
  onChanged: () => Promise<void>
  settings: StewardIntelligenceSettings | null
  status: StewardBackgroundStatus | null
}) {
  const [action, setAction] = useState<'toggle' | 'consolidate' | ''>('')
  const [feedback, setFeedback] = useState<{ tone: 'success' | 'error'; message: string } | null>(null)
  if (!status) return <EmptyState text="正在读取后台状态…" />
  const freshSources = status.pipeline.sources.filter((source) => source.fresh).length
  const healthyLoops = status.loops.filter((loop) => loop.enabled && loop.consecutive_failures === 0).length
  const queueTotal = status.intelligence_queue.pending + status.intelligence_queue.processing + status.intelligence_queue.waiting_model
  const issueCount = status.issue_details?.length || status.issues.length
  const metrics = status.metrics
  const batchCount = metrics ? sumMetricCounts(metrics.batch_status_counts) : null
  const enabled = settings?.enabled ?? status.enabled

  const toggleBackground = async () => {
    if (!settings) {
      setFeedback({ tone: 'error', message: '持续智能设置尚未加载，暂时不能安全修改运行状态。' })
      return
    }
    setAction('toggle'); setFeedback(null)
    try {
      const result = await updateStewardIntelligenceSettings({ expected_revision: settings.revision, enabled: !enabled })
      setFeedback({ tone: 'success', message: result.settings.enabled ? '持续智能已继续运行。' : '持续智能已暂停；采集和后台归纳都将停止，已有数据仍会保留。' })
      await onChanged()
    } catch (reason) {
      setFeedback({ tone: 'error', message: humanizeApiError(reason instanceof Error ? reason.message : '持续智能状态修改失败') })
    } finally {
      setAction('')
    }
  }

  const consolidateNow = async () => {
    setAction('consolidate'); setFeedback(null)
    try {
      const result = await runStewardActivityBatches()
      setFeedback({
        tone: 'success',
        message: result.batches.length
          ? `已创建 ${result.batches.length} 个持久化归纳批次，后台 Agent 将继续处理。`
          : '已检查当前活动，没有发现达到边界且尚未归纳的新批次。',
      })
      await onChanged()
    } catch (reason) {
      setFeedback({ tone: 'error', message: humanizeApiError(reason instanceof Error ? reason.message : '立即归纳失败') })
    } finally {
      setAction('')
    }
  }

  return <div className="steward-intelligence-stack">
    <section className={`steward-intelligence-health is-${status.state}`}>
      <div><span aria-hidden="true" /><div><strong>{backgroundStateLabel(status.state)}</strong><small>检查于 {formatDate(status.checked_at)}</small></div></div>
      <p>{issueCount ? `发现 ${issueCount} 项需要处理的问题，具体原因和处理建议见下方。` : '采集、后台循环、模型与队列均未发现异常。'}</p>
    </section>
    <section className="steward-intelligence-actions" aria-label="持续智能操作">
      <div><strong>后台控制</strong><small>暂停或继续整个持续智能管线，也可以立即把已到边界的活动整理成持久化批次。</small></div>
      <div>
        <button className="steward-button steward-button-secondary" disabled={Boolean(action) || !settings} onClick={() => void toggleBackground()} type="button">
          {action === 'toggle' ? '处理中…' : enabled ? '暂停持续智能' : '继续持续智能'}
        </button>
        <button className="steward-button" disabled={Boolean(action) || !enabled} onClick={() => void consolidateNow()} type="button">
          {action === 'consolidate' ? '正在检查…' : '立即归纳'}
        </button>
      </div>
    </section>
    {feedback ? <p aria-live="polite" className={`steward-intelligence-message is-${feedback.tone}`} role={feedback.tone === 'error' ? 'alert' : 'status'}>{feedback.message}</p> : null}
    {status.issue_details?.length ? <section className="steward-intelligence-section">
      <header><div><h3>问题与处理建议</h3><small>以下内容来自后端结构化健康检查，不是网页根据颜色猜测</small></div></header>
      <div className="steward-health-issue-list">
        {status.issue_details.map((issue, index) => <article key={`${issue.code}:${index}`}>
          <div><strong>{issue.message}</strong><code>{issue.code}</code></div>
          <p><span>建议：</span>{issue.action || '尚未提供'}</p>
        </article>)}
      </div>
    </section> : status.issues.length ? <section className="steward-intelligence-section">
      <header><div><h3>问题</h3><small>当前后端只返回了旧版文字问题，结构化处理建议尚未提供</small></div></header>
      <div className="steward-health-issue-list">{status.issues.map((issue, index) => <article key={`${issue}:${index}`}><div><strong>{issue}</strong></div><p><span>建议：</span>尚未提供</p></article>)}</div>
    </section> : null}
    <div className="steward-intelligence-metrics">
      <Metric label="后台循环" value={`${healthyLoops}/${status.loops.filter((loop) => loop.enabled).length} 健康`} detail={latestLoopDetail(status)} />
      <Metric label="采集新鲜度" value={`${freshSources}/${status.pipeline.sources.length} 新鲜`} detail={status.pipeline.last_batch_at ? `最近批次 ${formatDate(status.pipeline.last_batch_at)}` : '尚无完成批次'} />
      <Metric label="智能队列" value={`${queueTotal} 个处理中`} detail={`失败 ${status.intelligence_queue.failed} 个`} />
      <Metric label="模型服务" value={status.model.enabled && !status.model.circuit_open ? '可用' : status.model.circuit_open ? '暂时熔断' : '未配置'} detail={status.model.last_error || status.model.model || status.model.provider} />
    </div>
    <section className="steward-intelligence-section">
      <header><div><h3>下一次后台动作</h3><small>只显示后端明确提供的计划时间，不根据轮询间隔自行推算</small></div></header>
      <div className="steward-intelligence-metrics">
        <Metric label="下一次归纳" value={formatProvidedDate(status.next_consolidation_at)} detail={status.next_consolidation_at ? '后端计划时间' : '后端状态尚未提供 next_consolidation_at'} />
        <Metric label="下一次日报" value={formatProvidedDate(status.next_daily_report_at)} detail={status.next_daily_report_at ? '后端计划时间' : '后端状态尚未提供 next_daily_report_at'} />
      </div>
    </section>
    <section className="steward-intelligence-section">
      <header><div><h3>运行指标</h3><small>{metrics ? `${formatDate(metrics.window_start)} 至 ${formatDate(metrics.window_end)}` : 'background_status.metrics 尚未提供'}</small></div></header>
      <div className="steward-intelligence-metrics">
        <Metric label="近一小时观察" value={metrics ? formatInteger(metrics.observations_1h) : '尚未提供'} detail="持久化 observation 数量" />
        <Metric label="近一小时会话" value={metrics ? formatInteger(metrics.sessions_1h) : '尚未提供'} detail="由观察归并出的活动会话" />
        <Metric label="会话压缩比" value={metrics ? metrics.session_compression_ratio.available ? `${metrics.session_compression_ratio.value?.toFixed(2) ?? '0.00'}:1` : '不可用' : '尚未提供'} detail={metrics?.session_compression_ratio.reason || (metrics ? `${metrics.session_compression_ratio.numerator}/${metrics.session_compression_ratio.denominator}` : '后端未提供指标')} />
        <Metric label="活动批次" value={batchCount == null ? '尚未提供' : `${formatInteger(batchCount)} 个`} detail={metrics ? formatMetricCounts(metrics.batch_status_counts) : '后端未提供指标'} />
        <Metric label="模型 Episode" value={metrics ? `${formatInteger(metrics.model_episodes_1h.completed)} 成功 / ${formatInteger(metrics.model_episodes_1h.failed)} 失败` : '尚未提供'} detail="最近一小时持久化终态" />
        <Metric label="报告证据覆盖" value={metrics ? metrics.report_coverage.available ? `${Math.round((metrics.report_coverage.average ?? 0) * 100)}%` : '不可用' : '尚未提供'} detail={metrics?.report_coverage.reason || (metrics ? `${metrics.report_coverage.report_count} 份报告` : '后端未提供指标')} />
        <Metric label="提醒反馈" value={metrics ? `${formatInteger(metrics.reminder_feedback_1h.total)} 次` : '尚未提供'} detail={metrics ? formatMetricCounts(metrics.reminder_feedback_1h.by_action) : '后端未提供指标'} />
        <Metric label="模型用量" value={metrics?.model_usage.available && metrics.model_usage.total_tokens != null ? `${formatInteger(metrics.model_usage.total_tokens)} tokens` : '尚未提供'} detail={metrics?.model_usage.reason || formatModelUsageDetail(metrics?.model_usage)} />
      </div>
    </section>
    <section className="steward-intelligence-section">
      <header><div><h3>采集源</h3><small>“运行中”与“数据新鲜”分开显示，避免假绿灯</small></div></header>
      <div className="steward-source-list">
        {status.pipeline.sources.map((source) => <article key={`${source.device_id}:${source.collector_name}:${source.source_key}`}>
          <span className={source.fresh ? 'is-ok' : 'is-warning'} aria-hidden="true" />
          <div><strong>{source.collector_name}</strong><small>{source.source_key} · {source.execution_target}</small></div>
          <div><strong>{source.fresh ? '数据新鲜' : '需要检查'}</strong><small>{source.last_ingested_at ? `最近写入 ${formatDate(source.last_ingested_at)}` : source.last_poll_at ? `最近轮询 ${formatDate(source.last_poll_at)}` : '尚无心跳'}</small></div>
        </article>)}
        {!status.pipeline.sources.length ? <EmptyState text="尚未收到任何活动采集源心跳。" /> : null}
      </div>
    </section>
    <section className="steward-intelligence-section">
      <header><div><h3>最近结果</h3><small>报告和画像更新时间来自持久化结果，不是进程推测</small></div></header>
      {status.latest_outcome ? <div className="steward-latest-outcome"><strong>{status.latest_outcome.kind} · {status.latest_outcome.status}</strong><span>{formatDate(status.latest_outcome.at)}</span><p>{status.latest_outcome.summary || '该轮没有附加摘要。'}</p></div> : <EmptyState text="尚无后台处理结果。" />}
      <div className="steward-intelligence-footnotes"><span>最近报告：{formatOptionalDate(status.latest_report_at)}</span><span>画像更新：{formatOptionalDate(status.profile_updated_at)}</span><span>通知等待：{status.notifications.queued + status.notifications.retrying}</span></div>
    </section>
  </div>
}

export function ReportsPanel({ onChanged, reports }: { onChanged: () => Promise<void>; reports: StewardReport[] }) {
  const [cadence, setCadence] = useState<'all' | StewardReport['cadence']>('all')
  const visible = useMemo(() => reports.filter((item) => cadence === 'all' || item.cadence === cadence), [cadence, reports])
  const [selectedID, setSelectedID] = useState('')
  const [regeneratingID, setRegeneratingID] = useState('')
  const [feedback, setFeedback] = useState<{ tone: 'success' | 'error'; message: string } | null>(null)
  const selected = visible.find((item) => item.id === selectedID) || visible[0]

  const regenerate = async (report: StewardReport) => {
    setRegeneratingID(report.id); setFeedback(null)
    try {
      const result = await regenerateStewardReport(report.id, '用户在个人智能工作台请求重新生成')
      const shortID = result.regeneration.job.id.slice(0, 8)
      setFeedback({
        tone: 'success',
        message: result.regeneration.created
          ? `已创建后台重新生成任务 ${shortID}，完成后会生成新的报告修订版。`
          : `报告已有进行中的重新生成任务 ${shortID}，已继续跟踪该任务。`,
      })
      await onChanged()
    } catch (reason) {
      setFeedback({ tone: 'error', message: humanizeApiError(reason instanceof Error ? reason.message : '报告重新生成失败') })
    } finally {
      setRegeneratingID('')
    }
  }

  return <div className="steward-report-layout">
    <aside className="steward-report-list">
      <div className="steward-segmented" aria-label="报告周期">
        {(['all', 'daily', 'weekly', 'monthly'] as const).map((value) => <button className={cadence === value ? 'is-active' : ''} key={value} onClick={() => { setCadence(value); setSelectedID('') }} type="button">{cadenceLabel(value)}</button>)}
      </div>
      {visible.map((report) => <button className={selected?.id === report.id ? 'is-active' : ''} key={report.id} onClick={() => setSelectedID(report.id)} type="button">
        <strong>{report.title || report.period_key}</strong><small>{formatDate(report.period_end)} · {report.status} · {report.evidence_count} 条依据</small><span>{report.summary}</span>
      </button>)}
      {!visible.length ? <EmptyState text="还没有生成此周期的报告。" /> : null}
    </aside>
    <article className="steward-report-detail">
      {selected ? <>
        <header><div><span>{cadenceLabel(selected.cadence)} · 修订 {selected.revision}</span><h3>{selected.title || selected.period_key}</h3><small>{formatDate(selected.period_start)} 至 {formatDate(selected.period_end)}</small></div><div className="steward-report-actions"><span className={`is-${selected.status}`}>{selected.status}</span><button className="steward-button steward-button-secondary" disabled={Boolean(regeneratingID)} onClick={() => void regenerate(selected)} type="button">{regeneratingID === selected.id ? '正在创建任务…' : '重新生成报告'}</button></div></header>
        {feedback ? <p aria-live="polite" className={`steward-intelligence-message is-${feedback.tone}`} role={feedback.tone === 'error' ? 'alert' : 'status'}>{feedback.message}</p> : null}
        <p className="steward-report-summary">{selected.summary}</p>
        <div className="steward-report-body">{selected.body || '该报告暂时没有正文。'}</div>
        <details><summary>查看依据（{selected.evidence_count}）</summary>{selected.evidence?.map((item) => <div className="steward-evidence-row" key={item.id}><strong>{item.source_type}</strong><span>{item.summary || item.source_id}</span><code>{item.content_hash || '无哈希'}</code></div>)}</details>
        {selected.error_summary ? <p className="steward-inline-warning">部分生成失败：{selected.error_summary}</p> : null}
      </> : <EmptyState text="选择一份报告查看详情。" />}
    </article>
  </div>
}

export function ProfilePanel({ onChanged, profile }: { onChanged: () => Promise<void>; profile: StewardProfileView | null }) {
  const [horizon, setHorizon] = useState<keyof StewardProfileView>('merged')
  const [historyOpen, setHistoryOpen] = useState(false)
  const [history, setHistory] = useState<StewardProfileFact[]>([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const [historyError, setHistoryError] = useState('')
  const snapshot = profile?.[horizon]
  const facts = snapshot?.facts ?? []
  const loadHistory = useCallback(async (selectedHorizon: keyof StewardProfileView = horizon) => {
    setHistoryLoading(true); setHistoryError('')
    try {
      const result = await getStewardProfileHistory({ horizon: selectedHorizon === 'merged' ? '' : selectedHorizon, limit: 500 })
      setHistory(result.facts)
    } catch (reason) {
      setHistoryError(humanizeApiError(reason instanceof Error ? reason.message : '画像历史加载失败'))
    } finally {
      setHistoryLoading(false)
    }
  }, [horizon])

  const selectHorizon = (value: keyof StewardProfileView) => {
    setHorizon(value)
    if (historyOpen) void loadHistory(value)
  }

  const toggleHistory = () => {
    if (historyOpen) {
      setHistoryOpen(false)
      return
    }
    setHistoryOpen(true)
    void loadHistory()
  }

  const profileChanged = async () => {
    await onChanged()
    if (historyOpen) await loadHistory()
  }

  return <div className="steward-intelligence-stack">
    <div className="steward-profile-toolbar">
      <div className="steward-segmented" aria-label="画像时间层">
        {(['merged', 'recent', 'stable', 'explicit'] as const).map((value) => <button className={horizon === value ? 'is-active' : ''} key={value} onClick={() => selectHorizon(value)} type="button">{profileHorizonLabel(value)}</button>)}
      </div>
      <button aria-expanded={historyOpen} className="steward-button steward-button-secondary" onClick={toggleHistory} type="button">{historyOpen ? '收起画像历史' : '查看画像历史'}</button>
    </div>
    <section className="steward-intelligence-section">
      <header><div><h3>{profileHorizonLabel(horizon)}画像</h3><small>{snapshot ? `修订 ${snapshot.revision} · ${facts.length} 条事实` : '尚未形成此层画像'}</small></div></header>
      <div className="steward-profile-grid">
        {facts.map((fact) => <ProfileFactCard fact={fact} key={fact.id} />)}
        {!facts.length ? <EmptyState text="证据积累后，模型会在这里形成可追溯的画像事实。" /> : null}
      </div>
    </section>
    {historyOpen ? <section className="steward-intelligence-section">
      <header><div><h3>{horizon === 'merged' ? '全部画像历史' : `${profileHorizonLabel(horizon)}画像历史`}</h3><small>包括已被后续版本取代的事实；这些记录来自持久化版本链</small></div><button className="steward-button steward-button-secondary" disabled={historyLoading} onClick={() => void loadHistory()} type="button">{historyLoading ? '加载中…' : '刷新历史'}</button></header>
      {historyError ? <p className="steward-intelligence-message is-error" role="alert">{historyError}</p> : null}
      <div className="steward-profile-history">
        {history.map((fact) => <article key={fact.id}>
          <header><div><strong>{fact.key}</strong><small>{profileHorizonLabel(fact.horizon)} · 版本 {fact.version}</small></div><span className={`is-${fact.status}`}>{fact.status === 'superseded' ? '已被取代' : fact.status}</span></header>
          <p>{fact.summary || stringifyValue(fact.value)}</p>
          <small>有效期：{formatDate(fact.valid_from)} 至 {fact.valid_to ? formatDate(fact.valid_to) : '当前'} · {fact.evidence_count} 条依据</small>
          <details><summary>查看该版本的值与证据</summary><pre>{JSON.stringify(fact.value, null, 2)}</pre>{fact.evidence?.map((item) => <div className="steward-evidence-row" key={item.id}><strong>{item.source_type}</strong><span>{item.summary || item.source_id}</span><small>{formatDate(item.evidence_day)}</small></div>)}</details>
        </article>)}
        {!historyLoading && !history.length ? <EmptyState text="此画像层还没有历史版本。" /> : null}
      </div>
    </section> : null}
    <ProfileCorrectionForm onChanged={profileChanged} />
  </div>
}

function ProfileFactCard({ fact }: { fact: StewardProfileFact }) {
  return <article className="steward-profile-fact">
    <header><strong>{fact.key}</strong><span>{Math.round(fact.confidence * 100)}%</span></header>
    <p>{fact.summary || stringifyValue(fact.value)}</p>
    <small>{fact.evidence_count} 条依据 · {fact.evidence_days} 天 · 版本 {fact.version}</small>
    <details><summary>查看值和证据</summary><pre>{JSON.stringify(fact.value, null, 2)}</pre>{fact.evidence?.map((item) => <div className="steward-evidence-row" key={item.id}><strong>{item.source_type}</strong><span>{item.summary || item.source_id}</span><small>{formatDate(item.evidence_day)}</small></div>)}</details>
  </article>
}

function ProfileCorrectionForm({ onChanged }: { onChanged: () => Promise<void> }) {
  const [error, setError] = useState('')
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setError('')
    const form = event.currentTarget; const data = new FormData(form)
    try {
      const key = String(data.get('key') || '').trim()
      const raw = String(data.get('value') || '').trim()
      const parsed = raw.startsWith('{') ? JSON.parse(raw) as Record<string, unknown> : { value: raw }
      await correctStewardProfileFact({ key, value: parsed, summary: String(data.get('summary') || '').trim() })
      form.reset(); await onChanged()
    } catch (reason) { setError(reason instanceof Error ? reason.message : '画像纠正失败') }
  }
  return <details className="steward-intelligence-form-section"><summary>纠正或明确告诉管家一项事实</summary><form onSubmit={(event) => void submit(event)}>
    <label>事实键<input name="key" placeholder="例如 preferred_work_hours" required /></label>
    <label>准确内容<input name="value" placeholder="例如 09:30-12:00，或 JSON 对象" required /></label>
    <label>说明<textarea name="summary" placeholder="为什么需要纠正（可选）" /></label>
    {error ? <p className="steward-inline-warning">{error}</p> : null}<button className="steward-button" type="submit">保存明确事实</button>
  </form></details>
}

function ReminderPanel({ feedback, onChanged, policy, windows }: { feedback: StewardReminderFeedback[]; onChanged: () => Promise<void>; policy: StewardReminderPolicy | null; windows: StewardReceptivityWindow[] }) {
  const topWindows = [...windows].sort((a, b) => b.score - a.score || b.sample_count - a.sample_count).slice(0, 12)
  return <div className="steward-reminder-layout">
    <section className="steward-intelligence-section"><header><div><h3>当前软策略</h3><small>这是模型可调整的偏好与预算，不是阻止执行的权限门槛</small></div></header><ReminderPolicyForm onChanged={onChanged} policy={policy} /></section>
    <section className="steward-intelligence-section"><header><div><h3>更容易被接受的时段</h3><small>根据打开、行动、稍后、忽略等真实反馈逐步学习</small></div></header><div className="steward-receptivity-list">
      {topWindows.map((item) => <article key={item.id}><div><strong>{weekdayLabel(item.weekday)} {slotLabel(item.time_slot)}</strong><small>{item.category} · {item.channel || '任意渠道'} · {item.sample_count} 次样本</small></div><span className={item.score >= 0 ? 'is-positive' : 'is-negative'}>{Math.round(item.score * 100)}%</span></article>)}
      {!topWindows.length ? <EmptyState text="收到提醒反馈后，这里会显示更合适的时间窗口。" /> : null}
    </div></section>
    <section className="steward-intelligence-section"><header><div><h3>最近反馈</h3><small>同一通知的重新安排会使用新修订号，旧回调不会覆盖新计划</small></div></header><div className="steward-feedback-list">
      {feedback.slice(0, 30).map((item) => <article key={item.id}><span>{feedbackLabel(item.action)}</span><div><strong>{item.category}</strong><small>{formatDate(item.created_at)} · {item.channel || '未知渠道'} · 修订 {item.schedule_revision}</small></div><small>{item.response_seconds == null ? '' : formatDuration(item.response_seconds)}</small></article>)}
      {!feedback.length ? <EmptyState text="还没有提醒反馈。" /> : null}
    </div></section>
  </div>
}

function ReminderPolicyForm({ onChanged, policy }: { onChanged: () => Promise<void>; policy: StewardReminderPolicy | null }) {
  const [error, setError] = useState('')
  if (!policy) return <EmptyState text="正在读取提醒策略…" />
  const quiet = objectValue(policy.policy.quiet_hours)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setError('')
    const form = event.currentTarget; const data = new FormData(form)
    const next = { ...policy.policy, quiet_hours: { ...quiet, start: data.get('quiet_start'), end: data.get('quiet_end'), mode: 'soft' }, daily_soft_budget: Number(data.get('daily_budget')), category_soft_budget: Number(data.get('category_budget')), cooldown_seconds: Number(data.get('cooldown_minutes')) * 60 }
    try { await updateStewardReminderPolicy({ profile_scope: policy.profile_scope, category: policy.category, policy: next, rationale: String(data.get('rationale') || '').trim(), evidence_manifest: policy.evidence_manifest }); await onChanged() }
    catch (reason) { setError(reason instanceof Error ? reason.message : '提醒策略保存失败') }
  }
  return <form className="steward-reminder-policy-form" onSubmit={(event) => void submit(event)}>
    <label>安静时段开始<input defaultValue={String(quiet.start || '23:00')} name="quiet_start" pattern="[0-2][0-9]:[0-5][0-9]" required /></label>
    <label>安静时段结束<input defaultValue={String(quiet.end || '08:00')} name="quiet_end" pattern="[0-2][0-9]:[0-5][0-9]" required /></label>
    <label>每日软预算<input defaultValue={numberValue(policy.policy.daily_soft_budget, 8)} min="0" name="daily_budget" type="number" /></label>
    <label>单类软预算<input defaultValue={numberValue(policy.policy.category_soft_budget, 3)} min="0" name="category_budget" type="number" /></label>
    <label>冷却时间（分钟）<input defaultValue={Math.round(numberValue(policy.policy.cooldown_seconds, 1200) / 60)} min="0" name="cooldown_minutes" type="number" /></label>
    <label className="is-wide">调整理由<input defaultValue={policy.rationale} name="rationale" placeholder="记录模型或用户为什么调整" /></label>
    {error ? <p className="steward-inline-warning is-wide">{error}</p> : null}<button className="steward-button is-wide" type="submit">保存新策略版本</button>
  </form>
}

function Metric({ detail, label, value }: { detail: string; label: string; value: string }) { return <article><small>{label}</small><strong>{value}</strong><span title={detail}>{detail || '暂无补充信息'}</span></article> }
function EmptyState({ text }: { text: string }) { return <p className="steward-intelligence-empty">{text}</p> }
function latestLoopDetail(status: StewardBackgroundStatus) { const latest = [...status.loops].filter((loop) => loop.last_success_at).sort((a, b) => String(b.last_success_at).localeCompare(String(a.last_success_at)))[0]; return latest?.last_success_at ? `${latest.name} 最近成功 ${formatDate(latest.last_success_at)}` : '尚无循环成功记录' }
function cadenceLabel(value: 'all' | StewardReport['cadence']) { return ({ all: '全部', daily: '日报', weekly: '周报', monthly: '月报' } as const)[value] }
function profileHorizonLabel(value: keyof StewardProfileView) { return ({ merged: '综合', recent: '最近', stable: '长期', explicit: '用户明确' } as const)[value] }
function weekdayLabel(value: number) { return ['周日', '周一', '周二', '周三', '周四', '周五', '周六'][value] || `星期 ${value}` }
function feedbackLabel(value: string) { return ({ opened: '已打开', acted: '已行动', acknowledged: '已确认', snoozed: '稍后提醒', dismissed: '已忽略', ignored: '未响应', cancelled: '已取消', auto_resolved: '自动解决' } as Record<string, string>)[value] || value }
function formatDate(value?: string | null) { return value ? new Date(value).toLocaleString('zh-CN', { dateStyle: 'short', timeStyle: 'short' }) : '暂无' }
function formatOptionalDate(value?: string | null) { return value ? formatDate(value) : '暂无' }
function formatProvidedDate(value?: string | null) { return value ? formatDate(value) : '尚未提供' }
function formatInteger(value: number) { return new Intl.NumberFormat('zh-CN').format(value) }
function sumMetricCounts(values: Record<string, number>) { return Object.values(values).reduce((total, value) => total + value, 0) }
function formatMetricCounts(values: Record<string, number>) {
  const entries = Object.entries(values).sort(([left], [right]) => left.localeCompare(right))
  return entries.length ? entries.map(([key, value]) => `${key} ${formatInteger(value)}`).join(' · ') : '没有记录'
}
function formatModelUsageDetail(value?: StewardModelUsageMetrics) {
  if (!value) return '后端未提供指标'
  if (!value.available) return value.reason || '提供商用量尚未持久化'
  const parts = [
    value.input_tokens == null ? '' : `输入 ${formatInteger(value.input_tokens)}`,
    value.output_tokens == null ? '' : `输出 ${formatInteger(value.output_tokens)}`,
    value.cost == null ? '' : `费用 ${value.cost} ${value.currency || ''}`.trim(),
  ].filter(Boolean)
  return parts.join(' · ') || '用量可用，但后端未提供明细'
}
function formatDuration(seconds: number) { if (seconds < 60) return `${seconds} 秒响应`; if (seconds < 3600) return `${Math.round(seconds / 60)} 分钟响应`; return `${(seconds / 3600).toFixed(1)} 小时响应` }
function objectValue(value: unknown): Record<string, unknown> { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {} }
function numberValue(value: unknown, fallback: number) { const parsed = Number(value); return Number.isFinite(parsed) ? parsed : fallback }
function stringifyValue(value: Record<string, unknown>) { try { return JSON.stringify(value) } catch { return '无法显示此值' } }
function humanizeApiError(value: string) { try { const parsed = JSON.parse(value) as { error?: string }; return parsed.error || value } catch { return value } }
