import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type {
  StewardBackgroundStatus,
  StewardIntelligenceSettings,
  StewardReport,
} from '../types'
import {
  BackgroundPanel,
  ProfilePanel,
  ReportsPanel,
} from './PersonalIntelligenceDialog'

const noChange = async () => undefined

function backgroundStatus(): StewardBackgroundStatus {
  return {
    state: 'degraded',
    enabled: true,
    mode: 'batch',
    checked_at: '2026-07-20T08:00:00Z',
    agent: {
      agent_id: 'local',
      device_name: 'desktop',
      platform: 'windows',
      status: 'running',
      version: '5.3.0',
      enabled_collectors: [],
      background_loops: [],
      updated_at: '2026-07-20T08:00:00Z',
    },
    loops: [],
    pipeline: {
      enabled: true,
      mode: 'batch',
      sources: [],
      pending_batches: 1,
      processing_batches: 0,
      waiting_model: 0,
      failed_batches: 0,
      updated_at: '2026-07-20T08:00:00Z',
    },
    intelligence_queue: { pending: 1, processing: 0, waiting_model: 0, failed: 0 },
    notifications: { queued: 0, sending: 0, retrying: 0, failed: 0, accepted: 0 },
    model: {
      enabled: true,
      provider: 'openai-compatible',
      model: 'test-model',
      circuit_open: false,
      consecutive_failures: 0,
    },
    issues: ['ActivityWatch 数据陈旧'],
    issue_details: [{
      code: 'activity_source_stale',
      message: 'ActivityWatch 数据陈旧',
      action: '检查 Session Companion 心跳',
    }],
    metrics: {
      window_start: '2026-07-20T07:00:00Z',
      window_end: '2026-07-20T08:00:00Z',
      observations_1h: 1234,
      sessions_1h: 12,
      session_compression_ratio: { available: true, value: 102.83, numerator: 1234, denominator: 12 },
      batch_status_counts: { pending: 2, completed: 3 },
      model_episodes_1h: { completed: 4, failed: 1 },
      report_coverage: { available: true, report_count: 8, average: 0.75 },
      reminder_feedback_1h: { total: 3, by_action: { opened: 2, snoozed: 1 } },
      model_usage: { available: false, reason: 'provider token and cost usage is not persisted' },
    },
  }
}

function settings(): StewardIntelligenceSettings {
  return {
    enabled: true,
    mode: 'batch',
    capture_profile: 'deep',
    timezone: 'Asia/Shanghai',
    activity_sample_seconds: 5,
    sessionize_interval_seconds: 60,
    batch_interval_seconds: 900,
    boundary_grace_seconds: 30,
    daily_report_fallback_local: '21:30',
    weekly_report_day: 0,
    weekly_report_local: '20:00',
    monthly_report_local: '20:00',
    recent_profile_days: 14,
    stable_min_evidence_days: 3,
    profile_bootstrap_days: 30,
    report_catchup_days: 7,
    background_max_rounds: 0,
    background_max_tool_calls: 0,
    background_max_duration_seconds: 0,
    background_no_progress_limit: 3,
    quiet_start_local: '23:00',
    quiet_end_local: '08:00',
    reminder_daily_soft_budget: 8,
    reminder_category_soft_budget: 3,
    reminder_cooldown_seconds: 1200,
    raw_metadata_retention_days: 30,
    unreferenced_media_retention_days: 30,
    revision: 7,
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
  }
}

describe('personal intelligence controls and observability', () => {
  it('renders structured issues, real metrics, honest missing schedules, and background controls', () => {
    const markup = renderToStaticMarkup(<BackgroundPanel onChanged={noChange} settings={settings()} status={backgroundStatus()} />)

    expect(markup).toContain('activity_source_stale')
    expect(markup).toContain('检查 Session Companion 心跳')
    expect(markup).toContain('近一小时观察')
    expect(markup).toContain('1,234')
    expect(markup).toContain('75%')
    expect(markup).toContain('下一次归纳')
    expect(markup).toContain('下一次日报')
    expect(markup.match(/尚未提供/g)?.length).toBeGreaterThanOrEqual(3)
    expect(markup).toContain('暂停持续智能')
    expect(markup).toContain('立即归纳')
  })

  it('exposes report regeneration and profile history entry points', () => {
    const report: StewardReport = {
      id: 'report-1',
      cadence: 'daily',
      period_key: '2026-07-20',
      period_start: '2026-07-20T00:00:00Z',
      period_end: '2026-07-20T23:59:59Z',
      revision: 1,
      status: 'complete',
      title: '日报',
      summary: '今天完成了工作。',
      body: '正文',
      metrics: {},
      silent: false,
      evidence_count: 0,
      created_at: '2026-07-20T23:59:59Z',
      updated_at: '2026-07-20T23:59:59Z',
    }

    const reportsMarkup = renderToStaticMarkup(<ReportsPanel onChanged={noChange} reports={[report]} />)
    const profileMarkup = renderToStaticMarkup(<ProfilePanel onChanged={noChange} profile={null} />)

    expect(reportsMarkup).toContain('重新生成报告')
    expect(profileMarkup).toContain('查看画像历史')
    expect(profileMarkup).toContain('aria-expanded="false"')
  })
})
