import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  evaluateStewardLifecycle,
  getStewardActivitySessions,
  getStewardEntities,
  getStewardEntityRelations,
  getStewardHabits,
  getStewardInsights,
  getStewardLifecycleStatus,
  getStewardObservations,
  purgeStewardLifecycle,
  updateStewardHabit,
  updateStewardInsight,
  updateStewardRetentionPolicy,
} from '../../../lib/api/client'
import type {
  StewardActivitySession,
  StewardEntity,
  StewardHabit,
  StewardInsight,
  StewardLifecycleEvaluation,
  StewardLifecycleStatus,
  StewardObservation,
  StewardRelation,
  StewardRetentionPolicy,
} from '../../../types/tooling'
import { formatDate } from './model'
import { EmptyState, Panel } from './presentation'

type Props = {
  onDataChanged: () => Promise<void>
}

export function ActivityMemoryWorkspace({ onDataChanged }: Props) {
  const [observations, setObservations] = useState<StewardObservation[]>([])
  const [sessions, setSessions] = useState<StewardActivitySession[]>([])
  const [entities, setEntities] = useState<StewardEntity[]>([])
  const [selectedEntityID, setSelectedEntityID] = useState('')
  const [relations, setRelations] = useState<StewardRelation[]>([])
  const [habits, setHabits] = useState<StewardHabit[]>([])
  const [insights, setInsights] = useState<StewardInsight[]>([])
  const [lifecycle, setLifecycle] = useState<StewardLifecycleStatus | null>(null)
  const [evaluation, setEvaluation] = useState<StewardLifecycleEvaluation | null>(null)
  const [activityMode, setActivityMode] = useState<'observations' | 'sessions'>('sessions')
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    const [observationResult, sessionResult, entityResult, habitResult, insightResult, lifecycleResult] = await Promise.all([
      getStewardObservations(60),
      getStewardActivitySessions(60),
      getStewardEntities(80),
      getStewardHabits(60),
      getStewardInsights(60),
      getStewardLifecycleStatus(),
    ])
    setObservations(observationResult.observations)
    setSessions(sessionResult.sessions)
    setEntities(entityResult.entities)
    setHabits(habitResult.habits)
    setInsights(insightResult.insights)
    setLifecycle(lifecycleResult.lifecycle)
    setSelectedEntityID((current) => current || entityResult.entities[0]?.id || '')
  }, [])

  useEffect(() => {
		void Promise.resolve()
			.then(reload)
			.catch((cause: unknown) => setError(cause instanceof Error ? cause.message : '加载活动记忆失败'))
  }, [reload])

  useEffect(() => {
    if (!selectedEntityID) {
      return
    }
    getStewardEntityRelations(selectedEntityID, 100)
      .then((result) => setRelations(result.relations))
      .catch((cause: unknown) => setError(cause instanceof Error ? cause.message : '加载关系证据失败'))
  }, [selectedEntityID])

  const run = async (label: string, action: () => Promise<void>) => {
    setBusy(label)
    setError(null)
    try {
      await action()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : `${label}失败`)
    } finally {
      setBusy(null)
    }
  }

  const selectedEntity = useMemo(
    () => entities.find((entity) => entity.id === selectedEntityID) ?? null,
    [entities, selectedEntityID],
  )

  const previewLifecycle = () => run('模拟评估', async () => {
    const result = await evaluateStewardLifecycle(2000)
    setEvaluation(result.evaluation)
    await reload()
  })

  const executeLifecycle = () => run('执行清理', async () => {
    if (!evaluation) return
    await purgeStewardLifecycle(evaluation.id)
    setEvaluation(null)
    await Promise.all([reload(), onDataChanged()])
  })

  const decideHabit = (habit: StewardHabit, status: 'confirmed' | 'ignored') => run('更新习惯', async () => {
    await updateStewardHabit(habit.id, { status, user_confirmed: status === 'confirmed' })
    await Promise.all([reload(), onDataChanged()])
  })

  const decideInsight = (insight: StewardInsight, status: 'confirmed' | 'ignored') => run('更新洞察', async () => {
    await updateStewardInsight(insight.id, { status, user_confirmed: status === 'confirmed' })
    await Promise.all([reload(), onDataChanged()])
  })

  return (
    <>
      {error ? <div className="steward-alert" role="alert">{error}</div> : null}
      <section className="steward-grid steward-grid-s2 steward-activity-grid">
        <Panel
          actions={(
            <div className="steward-segmented" role="group" aria-label="活动数据视图">
              <button className={activityMode === 'sessions' ? 'is-active' : ''} onClick={() => setActivityMode('sessions')} type="button">会话</button>
              <button className={activityMode === 'observations' ? 'is-active' : ''} onClick={() => setActivityMode('observations')} type="button">观察</button>
            </div>
          )}
          help="观察是脱敏证据信封；连续重复状态会合并为会话。原始负载不会在此视图返回。"
          title="活动时间线"
        >
          <div className="steward-table-list steward-scroll-list">
            {activityMode === 'sessions' ? sessions.map((session) => (
              <article className="steward-list-item steward-activity-row" key={session.id}>
                <div>
                  <strong>{session.title}</strong>
                  <p>{session.summary || session.type}</p>
                  <small>{formatDate(session.started_at)} · {session.source} · {session.data_level}</small>
                </div>
                <div className="steward-row-meta">
                  <span>观察 {session.observation_count}</span>
                  <span>价值 {formatScore(session.value_score)}</span>
                  <span>置信 {formatScore(session.confidence)}</span>
                </div>
              </article>
            )) : observations.map((observation) => (
              <article className="steward-list-item steward-activity-row" key={`${observation.id}-${observation.occurred_at}`}>
                <div>
                  <strong>{observation.summary || observation.type}</strong>
                  <p>{observation.context_key || observation.source}</p>
                  <small>{formatDate(observation.occurred_at)} · {observation.data_level} · {observation.payload_encrypted ? '负载已加密' : '结构化元数据'}</small>
                </div>
                <div className="steward-row-meta">
                  <span>合并 {observation.duplicate_count}</span>
                  <span>{observation.has_media ? `${formatBytes(observation.media_size_bytes ?? 0)} 密文媒体` : '无媒体'}</span>
                  <span>到期 {formatDate(observation.expires_at)}</span>
                </div>
              </article>
            ))}
            {(activityMode === 'sessions' ? sessions : observations).length === 0 ? <EmptyState text="暂无活动数据" /> : null}
          </div>
        </Panel>

        <Panel help="关系只显示带 SourceRef 或 RelationEvidence 的边；候选关系不会自动成为长期事实。" title="关系图">
          <div className="steward-relation-layout">
            <div className="steward-entity-list" aria-label="实体列表">
              {entities.map((entity) => (
                <button
                  className={`steward-entity-select${selectedEntityID === entity.id ? ' is-active' : ''}`}
                  key={entity.id}
                  onClick={() => setSelectedEntityID(entity.id)}
                  type="button"
                >
                  <strong>{entity.display_name}</strong>
                  <small>{entity.type} · 证据 {entity.evidence_count}</small>
                </button>
              ))}
            </div>
            <div className="steward-relation-list">
              {selectedEntity ? (
                <div className="steward-relation-focus">
                  <strong>{selectedEntity.display_name}</strong>
                  <span>{selectedEntity.type} · {selectedEntity.data_level} · 置信 {formatScore(selectedEntity.confidence)}</span>
                </div>
              ) : null}
              {relations.map((relation) => (
                <article className="steward-relation-edge" key={relation.id}>
                  <div>
                    <span>{relation.source_entity?.display_name ?? relation.source_entity_id}</span>
                    <strong>{relation.relation_type}</strong>
                    <span>{relation.target_entity?.display_name ?? relation.target_entity_id}</span>
                  </div>
                  <small>{relation.inference_state} · 证据 {relation.evidence_count} · 置信 {formatScore(relation.confidence)}</small>
                  {relation.evidence[0] ? <p>{relation.evidence[0].summary || relation.evidence[0].evidence_type}</p> : null}
                </article>
              ))}
              {relations.length === 0 ? <EmptyState text="当前实体暂无关系证据" /> : null}
            </div>
          </div>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2 steward-activity-grid">
        <Panel
          actions={(
            <div className="steward-panel-actions">
              <button className="steward-button steward-button-secondary" disabled={busy !== null} onClick={previewLifecycle} type="button">模拟评估</button>
              <button className="steward-button steward-danger" disabled={busy !== null || !evaluation || evaluation.actions.length === 0} onClick={executeLifecycle} type="button">执行授权清理</button>
            </div>
          )}
          help="清理只执行保留策略允许的原始数据和未确认系统推断；用户确认记忆、正式任务和导入知识不会自动删除。"
          title="数据生命周期"
        >
          <div className="steward-lifecycle-body">
            <div className="steward-lifecycle-summary">
              <span>配置 {lifecycle?.profile ?? 'deep'}</span>
              <span>本地密钥 {lifecycle?.local_encryption_ready ? '可用' : '缺失'}</span>
              <span>向量 {lifecycle?.vector_search_enabled ? 'pgvector' : '全文 + 关系'}</span>
              <span>下次到期 {formatDate(lifecycle?.next_expiring_at)}</span>
            </div>
            <div className="steward-layer-grid">
              {(lifecycle?.layers ?? []).map((layer) => (
                <div className="steward-layer-metric" key={layer.kind}>
                  <span>{layerLabel(layer.kind)}</span>
                  <strong>{layer.count}</strong>
                  <small>{layer.bytes ? formatBytes(layer.bytes) : `到期 ${layer.expired_count} · 隔离 ${layer.quarantined_count}`}</small>
                </div>
              ))}
            </div>
            {evaluation ? (
              <div className="steward-evaluation-preview">
                <strong>评估 {evaluation.id.slice(0, 8)} · {evaluation.actions.length} 项</strong>
                <div>{Object.entries(evaluation.counts).map(([name, count]) => <span key={name}>{actionLabel(name)} {count}</span>)}</div>
                <small>评估有效期 1 小时；执行时会重新计算并写入删除审计。</small>
              </div>
            ) : null}
            <div className="steward-policy-list">
              {(lifecycle?.retention_policies ?? []).map((policy) => (
                <RetentionPolicyRow
                  busy={busy !== null}
                  key={policy.id}
                  policy={policy}
                  onSave={(payload) => run('更新保留策略', async () => {
                    await updateStewardRetentionPolicy(policy.id, payload)
                    await reload()
                  })}
                />
              ))}
            </div>
          </div>
        </Panel>

        <Panel help="系统推断保留证据数、置信度和价值分；确认后会加保留锁，忽略后不会再次主动建议。" title="习惯与洞察">
          <div className="steward-table-list steward-scroll-list">
            {habits.map((habit) => (
              <InferenceRow
                busy={busy !== null}
                dataLevel={habit.data_level}
                evidenceCount={habit.evidence_count}
                key={`habit-${habit.id}`}
                kind="习惯"
                onConfirm={() => decideHabit(habit, 'confirmed')}
                onIgnore={() => decideHabit(habit, 'ignored')}
                status={habit.status}
                summary={habit.summary || habit.pattern}
                title={habit.title}
                value={habit.value_score}
              />
            ))}
            {insights.map((insight) => (
              <InferenceRow
                busy={busy !== null}
                dataLevel={insight.data_level}
                evidenceCount={insight.evidence_count}
                key={`insight-${insight.id}`}
                kind="洞察"
                onConfirm={() => decideInsight(insight, 'confirmed')}
                onIgnore={() => decideInsight(insight, 'ignored')}
                status={insight.status}
                summary={insight.summary}
                title={insight.title}
                value={insight.value_score}
              />
            ))}
            {habits.length + insights.length === 0 ? <EmptyState text="暂无习惯或洞察候选" /> : null}
          </div>
        </Panel>
      </section>
    </>
  )
}

function InferenceRow({ busy, kind, title, summary, status, dataLevel, evidenceCount, value, onConfirm, onIgnore }: {
  busy: boolean
  kind: string
  title: string
  summary: string
  status: string
  dataLevel: string
  evidenceCount: number
  value: number
  onConfirm: () => void
  onIgnore: () => void
}) {
  return (
    <article className="steward-list-item steward-inference-row">
      <div>
        <strong>{kind} · {title}</strong>
        <p>{summary}</p>
        <small>{status} · {dataLevel} · 证据 {evidenceCount} · 价值 {formatScore(value)}</small>
      </div>
      <div className="steward-row-actions">
        <button className="steward-icon-button" disabled={busy || status === 'confirmed'} onClick={onConfirm} type="button">确认</button>
        <button className="steward-icon-button steward-button-secondary" disabled={busy || status === 'ignored'} onClick={onIgnore} type="button">忽略</button>
      </div>
    </article>
  )
}

function RetentionPolicyRow({ policy, busy, onSave }: {
  policy: StewardRetentionPolicy
  busy: boolean
  onSave: (payload: Record<string, unknown>) => Promise<void>
}) {
  const [ttl, setTTL] = useState(policy.ttl_days)
  const [autoPurge, setAutoPurge] = useState(policy.auto_purge)
  return (
    <div className="steward-policy-row">
      <div>
        <strong>{policy.data_kind} · {policy.source_pattern}</strong>
        <small>{policy.description}</small>
      </div>
      <label><span>TTL 天</span><input min="0.04" max="3650" step="0.04" type="number" value={ttl} onChange={(event) => setTTL(Number(event.target.value))} /></label>
      <label className="steward-policy-toggle"><input checked={autoPurge} onChange={(event) => setAutoPurge(event.target.checked)} type="checkbox" /><span>自动</span></label>
      <button className="steward-icon-button" disabled={busy} onClick={() => onSave({ ttl_days: ttl, auto_purge: autoPurge })} type="button">保存</button>
    </div>
  )
}

function formatScore(value: number) {
  return Number.isFinite(value) ? value.toFixed(2) : '0.00'
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`
  return `${(value / 1024 / 1024 / 1024).toFixed(1)} GB`
}

function layerLabel(value: string) {
  return ({
    raw_evidence: '原始证据',
    activity_facts: '活动事实',
    inferences: '系统推断',
    long_term_assets: '长期资产',
    audit: '审计证据',
    deletion_tombstones: '删除墓碑',
  } as Record<string, string>)[value] ?? value
}

function actionLabel(value: string) {
  return ({
    delete_observation: '原始删除',
    quarantine_inference: '进入隔离',
    archive_inference: '归档推断',
    delete_inference: '删除推断',
    delete_orphan_blob: '孤立媒体',
  } as Record<string, string>)[value] ?? value
}
