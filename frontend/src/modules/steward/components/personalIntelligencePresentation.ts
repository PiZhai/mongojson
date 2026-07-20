import type {
  StewardBackgroundStatus,
  StewardIntelligenceSettings,
} from '../types'

export function backgroundStateLabel(value: StewardBackgroundStatus['state']) {
  return ({
    healthy: '后台工作正常',
    degraded: '后台部分降级',
    unhealthy: '后台未正常运行',
    disabled: '持续智能已关闭',
  } as const)[value]
}

export function slotLabel(value: number) {
  const hours = Math.floor(value / 2)
  const minutes = value % 2 ? '30' : '00'
  const end = (value + 1) % 48
  return `${String(hours).padStart(2, '0')}:${minutes}–${String(Math.floor(end / 2)).padStart(2, '0')}:${end % 2 ? '30' : '00'}`
}

export function findPersonalIntelligenceLoop(loops?: StewardBackgroundStatus['loops']) {
  return loops?.find((loop) => loop.name === 'continuous-intelligence')
    ?? loops?.find((loop) => loop.name === 'proactive')
}

export function intelligenceSettingsPayload(data: FormData, revision: number) {
  const integer = (name: string) => Number.parseInt(String(data.get(name) ?? ''), 10)
  const text = (name: string) => String(data.get(name) ?? '').trim()
  return {
    enabled: data.get('enabled') === 'on',
    mode: text('mode') as StewardIntelligenceSettings['mode'],
    capture_profile: text('capture_profile') as StewardIntelligenceSettings['capture_profile'],
    timezone: text('timezone'),
    activity_sample_seconds: integer('activity_sample_seconds'),
    sessionize_interval_seconds: integer('sessionize_interval_seconds'),
    batch_interval_seconds: integer('batch_interval_seconds'),
    boundary_grace_seconds: integer('boundary_grace_seconds'),
    daily_report_fallback_local: text('daily_report_fallback_local'),
    weekly_report_day: integer('weekly_report_day'),
    weekly_report_local: text('weekly_report_local'),
    monthly_report_local: text('monthly_report_local'),
    recent_profile_days: integer('recent_profile_days'),
    stable_min_evidence_days: integer('stable_min_evidence_days'),
    profile_bootstrap_days: integer('profile_bootstrap_days'),
    report_catchup_days: integer('report_catchup_days'),
    background_max_rounds: integer('background_max_rounds'),
    background_max_tool_calls: integer('background_max_tool_calls'),
    background_max_duration_seconds: integer('background_max_duration_seconds'),
    background_no_progress_limit: integer('background_no_progress_limit'),
    quiet_start_local: text('quiet_start_local'),
    quiet_end_local: text('quiet_end_local'),
    reminder_daily_soft_budget: integer('reminder_daily_soft_budget'),
    reminder_category_soft_budget: integer('reminder_category_soft_budget'),
    reminder_cooldown_seconds: integer('reminder_cooldown_seconds'),
    raw_metadata_retention_days: integer('raw_metadata_retention_days'),
    unreferenced_media_retention_days: integer('unreferenced_media_retention_days'),
    expected_revision: revision,
  }
}
