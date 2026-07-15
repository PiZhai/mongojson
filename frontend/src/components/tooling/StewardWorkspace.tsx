import {
  approveStewardApprovalRequest,
  deleteStewardTimelineSegment,
  dismissStewardAutonomyProposals,
  rejectStewardApprovalRequest,
  resolveStewardSyncConflict,
  revokeStewardDevice,
  runStewardAutonomyCycle,
  startStewardAgent,
  stopStewardAgent,
  syncStewardDevice,
  updateStewardDevicePermission,
  updateStewardAutonomyRule,
  updateStewardAutonomySettings,
  updateStewardCollector,
  verifyStewardDeviceTrust,
} from "../../lib/api/client";
import {
  ApprovalRow,
  AuditRow,
  AutonomousRunRow,
  AutonomyProposalRow,
  AutonomyRuleRow,
  DevicePermissionRow,
  DeviceRow,
  DiscoveredPeerRow,
  EventRow,
  IntentRow,
  KnowledgeRow,
  MemoryRow,
  SyncChangeRow,
  SyncConflictRow,
  TaskRow,
} from "./steward/rows";
import {
  DataLevelSelect,
  EmptyState,
  HelpIcon,
  InfoTerm,
  Metric,
  Panel,
} from "./steward/presentation";
import {
  advisorStatusText,
  collectorHelp,
  entityText,
  formatDate,
  helpText,
  statusText,
} from "./steward/model";
import {
  useStewardWorkspace,
  type TaskDraft,
} from "./steward/useStewardWorkspace";
import { CollectorSettings } from "./steward/CollectorSettings";
import { ConversationWorkspace } from "./steward/ConversationWorkspace";
import { ActivityMemoryWorkspace } from "./steward/ActivityMemoryWorkspace";
import { AutomationPolicyWorkspace } from "./steward/AutomationPolicyWorkspace";

export function StewardWorkspace() {
  const {
    overview,
    counts,
    collectors,
    events,
    timelineSegments,
    tasks,
    intents,
    memories,
    knowledgeItems,
    displayedSourceRefs,
    tags,
    auditLogs,
    sync,
    autonomy,
    candidateProposalCount,
    devicesById,
    capabilitiesByKey,
    permissionRows,
    eventDraft,
    setEventDraft,
    taskDraft,
    setTaskDraft,
    intentDraft,
    setIntentDraft,
    memoryDraft,
    setMemoryDraft,
    knowledgeDraft,
    setKnowledgeDraft,
    tagDraft,
    setTagDraft,
    correctionDraft,
    setCorrectionDraft,
    sourceTarget,
    memoryVersions,
    searchQuery,
    setSearchQuery,
    searchType,
    setSearchType,
    searchResults,
    loading,
    busy,
    error,
    refresh,
    runAction,
    submitEvent,
    submitTask,
    submitIntent,
    submitMemory,
    submitKnowledge,
    submitTag,
    submitCorrection,
    showSources,
    showMemoryVersions,
    runSearch,
    downloadExport,
  } = useStewardWorkspace();

  if (loading) {
    return (
      <div aria-live="polite" className="steward-loading" role="status">
        正在加载私人管家工作台...
      </div>
    );
  }

  return (
    <div className="steward-workspace">
      {error ? (
        <div aria-live="assertive" className="steward-alert" role="alert">
          {error}
        </div>
      ) : null}

      <section className="steward-overview-band">
        <div className="steward-agent-card">
          <div>
            <p className="steward-eyebrow">Local Steward</p>
            <h2>
              {statusText(overview?.agent.status ?? "stopped")}
              <HelpIcon text={helpText.localSteward} />
            </h2>
            <p>
              {overview?.agent.device_name ?? "local-device"} ·{" "}
              {overview?.agent.platform ?? "windows"} ·{" "}
              {overview?.agent.version ?? "s2-data-foundation"}
            </p>
            {(overview?.agent.background_loops ?? []).length > 0 ? (
              <small className="steward-loop-status">
                {(overview?.agent.background_loops ?? [])
                  .map((loop) => (
                    <span key={loop.name}>
                      {`${loop.name}:${
                      !loop.enabled
                        ? "关闭"
                        : loop.running
                          ? loop.consecutive_failures > 0
                            ? `降级(${loop.consecutive_failures})`
                            : "运行"
                          : "停止"
                    }`}
                    </span>
                  ))}
              </small>
            ) : null}
          </div>
          <div className="steward-agent-actions">
            <button
              className="steward-button"
              disabled={busy !== null}
              onClick={() => runAction("启动 Agent", startStewardAgent)}
              type="button"
            >
              启动
            </button>
            <button
              className="steward-button steward-button-secondary"
              disabled={busy !== null}
              onClick={() => runAction("停止 Agent", stopStewardAgent)}
              type="button"
            >
              停止
            </button>
          </div>
        </div>
        <div className="steward-metrics steward-metrics-s2">
          <Metric label="事件" value={counts.events ?? 0} />
          <Metric label="时间线" value={counts.timeline_segments ?? 0} />
          <Metric label="任务" value={counts.tasks ?? 0} />
          <Metric
            label="意图"
            value={counts.candidate_intents ?? counts.intents ?? 0}
          />
          <Metric label="记忆" value={counts.memories ?? 0} />
          <Metric label="知识" value={counts.knowledge_items ?? 0} />
          <Metric label="来源" value={counts.source_refs ?? 0} />
          <Metric label="审计" value={counts.audit_logs ?? 0} />
        </div>
      </section>

      <ConversationWorkspace onDataChanged={refresh} />

      <ActivityMemoryWorkspace onDataChanged={refresh} />

      <AutomationPolicyWorkspace onDataChanged={refresh} />

      <section className="steward-grid steward-grid-s2">
        <Panel help={helpText.sync} title="三端同步">
          {sync ? (
            <div className="steward-table-list">
              <article className="steward-list-item">
                <div>
                  <strong>
                    {sync.local_device.device_name || "local-device"}
                  </strong>
                  <p>
                    {sync.local_device.platform} ·{" "}
                    {statusText(sync.local_device.trust_status)} ·{" "}
                    {sync.local_device.sync_enabled ? "同步开启" : "同步关闭"}
                  </p>
                  <small>
                    待同步 {sync.pending_changes} · 待补关系{" "}
                    {sync.pending_relations} · 冲突 {sync.conflict_count} · 最近{" "}
                    {formatDate(sync.last_change_at)}
                  </small>
                </div>
              </article>
              {sync.security ? (
                <article className="steward-compact-item">
                  <strong>同步安全链</strong>
                  <small>
                    鉴权
                    {sync.security.auth_required ? "默认要求" : "不安全兼容"} ·
                    HMAC
                    {sync.security.hmac_secret_configured
                      ? "已配置"
                      : "未配置"}{" "}
                    · 设备签名
                    {sync.security.device_signing_ready ? "可用" : "未就绪"} ·
                    传输加密
                    {sync.security.sync_encryption_configured
                      ? "已启用"
                      : "未启用"}{" "}
                    · 本地加密
                    {sync.security.local_encryption_configured
                      ? "已启用"
                      : "未启用"}
                  </small>
                  <small>
                    管理面 {sync.security.management_api_addr} · Peer 面{" "}
                    {sync.security.peer_api_enabled
                      ? sync.security.peer_api_addr
                      : "未启用"}{" "}
                    · 对外地址{" "}
                    {sync.security.peer_api_advertised
                      ? sync.security.public_api_base
                      : "未发布"}
                  </small>
                  {sync.security.insecure_mode_active ? (
                    <small className="steward-error-text">
                      同步接口正在允许未认证请求
                    </small>
                  ) : null}
                  {sync.security.config_errors.length > 0 ? (
                    <small className="steward-error-text">
                      {sync.security.config_errors.join("；")}
                    </small>
                  ) : null}
                </article>
              ) : null}
              {sync.change_contract ? (
                <article className="steward-compact-item">
                  <strong>同步变更契约</strong>
                  <small>
                    {sync.change_contract.healthy ? "健康" : "发现异常"} · 已检查{" "}
                    {sync.change_contract.checked_changes} · 异常{" "}
                    {sync.change_contract.invalid_changes}
                  </small>
                  {sync.change_contract.issues.length > 0 ? (
                    <small className="steward-error-text">
                      {sync.change_contract.issues.join("；")}
                    </small>
                  ) : null}
                </article>
              ) : null}
              {sync.discovery ? (
                <article className="steward-compact-item">
                  <strong>局域网候选发现</strong>
                  <small>
                    {sync.discovery.enabled
                      ? sync.discovery.running
                        ? "运行中"
                        : "已启用但未运行"
                      : "未启用"}{" "}
                    · 候选 {sync.discovery.candidate_count} · 拒绝无效公告{" "}
                    {sync.discovery.rejected_announcements}
                  </small>
                  {sync.discovery.enabled ? (
                    <small>
                      监听 {sync.discovery.listen_addr || "未配置"} · 最近广播{" "}
                      {formatDate(sync.discovery.last_announcement_at)} ·
                      最近发现 {formatDate(sync.discovery.last_discovery_at)}
                    </small>
                  ) : null}
                  {sync.discovery.last_error ? (
                    <small className="steward-error-text">
                      {sync.discovery.last_error}
                    </small>
                  ) : null}
                </article>
              ) : null}
              <div className="steward-compact-list">
                {sync.recent_changes.slice(0, 5).map((change) => (
                  <SyncChangeRow change={change} key={change.id} />
                ))}
                {sync.recent_changes.length === 0 ? (
                  <EmptyState text="暂无同步变更" />
                ) : null}
              </div>
            </div>
          ) : (
            <EmptyState text="同步状态未加载" />
          )}
        </Panel>

        <Panel help={helpText.devices} title="设备权限">
          <div className="steward-table-list">
            {(sync?.discovered_peers ?? []).map((peer) => (
              <DiscoveredPeerRow
                key={`${peer.device_id}:${peer.public_key_fingerprint}`}
                peer={peer}
              />
            ))}
            {(sync?.devices ?? []).map((device) => (
              <DeviceRow
                busy={busy !== null}
                device={device}
                key={device.id}
                onRevoke={(id) =>
                  runAction("撤销设备", () => revokeStewardDevice(id))
                }
                onSync={(id) =>
                  runAction("同步设备", () => syncStewardDevice(id))
                }
                onVerify={(id) =>
                  runAction("验证设备", () => verifyStewardDeviceTrust(id))
                }
              />
            ))}
            {(sync?.devices ?? []).length === 0 ? (
              <EmptyState text="暂无设备" />
            ) : null}
            {permissionRows.map((permission) => (
              <DevicePermissionRow
                busy={busy !== null}
                capability={
                  capabilitiesByKey.get(
                    `${permission.device_id}:${permission.capability}`,
                  ) ?? null
                }
                device={devicesById.get(permission.device_id) ?? null}
                key={permission.id}
                onUpdate={(deviceId, capability, payload) =>
                  runAction("更新设备权限", () =>
                    updateStewardDevicePermission(
                      deviceId,
                      capability,
                      payload,
                    ),
                  )
                }
                permission={permission}
              />
            ))}
            {permissionRows.length === 0 ? (
              <EmptyState text="暂无设备权限策略" />
            ) : null}
            {(sync?.capabilities ?? []).length > 0 ? (
              <div className="steward-compact-list">
                {(sync?.capabilities ?? []).map((capability) => (
                  <article
                    className="steward-compact-item"
                    key={`${capability.device_id}:${capability.capability}`}
                  >
                    <strong>{capability.capability}</strong>
                    <span>
                      {capability.device_id} ·{" "}
                      {capability.target_type || "未声明目标"} ·{" "}
                      {capability.risk_level} · 最高{" "}
                      {capability.max_permission_level}
                    </span>
                    <small>{capability.description || "无能力说明"}</small>
                  </article>
                ))}
              </div>
            ) : (
              <EmptyState text="暂无设备能力声明" />
            )}
          </div>
        </Panel>

        <Panel help={helpText.conflicts} title="同步冲突">
          <div className="steward-table-list">
            {(sync?.conflicts ?? []).map((conflict) => (
              <SyncConflictRow
                busy={busy !== null}
                conflict={conflict}
                key={conflict.id}
                onResolve={(id) =>
                  runAction("处理冲突", () =>
                    resolveStewardSyncConflict(id, "manual review accepted"),
                  )
                }
              />
            ))}
            {(sync?.conflicts ?? []).length === 0 ? (
              <EmptyState text="暂无冲突" />
            ) : null}
          </div>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel
          actions={
            <>
              <div
                aria-label="自主运行模式"
                className="steward-segmented"
                role="group"
              >
                <button
                  className={
                    autonomy?.settings.mode === "suggest_only"
                      ? "is-active"
                      : ""
                  }
                  disabled={busy !== null || !autonomy}
                  onClick={() =>
                    runAction("切换为仅建议模式", () =>
                      updateStewardAutonomySettings({ mode: "suggest_only" }),
                    )
                  }
                  title="只生成候选，不在后台自动执行"
                  type="button"
                >
                  仅建议
                </button>
                <button
                  className={
                    autonomy?.settings.mode === "controlled" ? "is-active" : ""
                  }
                  disabled={busy !== null || !autonomy}
                  onClick={() =>
                    runAction("切换为受控执行模式", () =>
                      updateStewardAutonomySettings({ mode: "controlled" }),
                    )
                  }
                  title="只自动执行已设为低风险自动的启用规则"
                  type="button"
                >
                  受控执行
                </button>
              </div>
              <select
                aria-label="最高自动权限"
                className="steward-inline-select"
                disabled={busy !== null || !autonomy}
                onChange={(event) =>
                  runAction("更新最高自动权限", () =>
                    updateStewardAutonomySettings({
                      max_auto_permission: event.currentTarget.value,
                    }),
                  )
                }
                title="全局自动执行权限上限"
                value={autonomy?.settings.max_auto_permission ?? "A3"}
              >
                {Array.from({ length: 10 }, (_, rank) => `A${rank}`).map(
                  (level) => (
                    <option key={level} value={level}>
                      最高 {level}
                    </option>
                  ),
                )}
              </select>
              <button
                className="steward-button steward-button-secondary"
                disabled={busy !== null}
                onClick={() =>
                  runAction(
                    autonomy?.settings.paused ? "恢复自主能力" : "暂停自主能力",
                    () =>
                      updateStewardAutonomySettings({
                        paused: !(autonomy?.settings.paused ?? false),
                      }),
                  )
                }
                type="button"
              >
                {autonomy?.settings.paused ? "恢复" : "暂停"}
              </button>
              <button
                className="steward-button"
                disabled={busy !== null || autonomy?.settings.paused}
                onClick={() =>
                  runAction("自主扫描", () => runStewardAutonomyCycle(12))
                }
                type="button"
              >
                扫描
              </button>
            </>
          }
          help={helpText.autonomy}
          title="受控自主"
        >
          {autonomy ? (
            <div className="steward-table-list">
              <article className="steward-list-item">
                <div>
                  <strong>
                    {autonomy.settings.paused
                      ? "自主能力已暂停"
                      : "自主能力可生成候选"}
                  </strong>
                  <p>
                    {statusText(autonomy.settings.mode)} · 最高自动权限{" "}
                    {autonomy.settings.max_auto_permission}
                  </p>
                  <p>{advisorStatusText(autonomy.advisor)}</p>
                  <p>
                    策略变更屏障
                    {autonomy.policy_gate?.enabled &&
                    autonomy.policy_gate.current_rule_revalidation
                      ? "已启用"
                      : "未就绪"}
                  </p>
                  <small>
                    执行动作 {autonomy.actions.length} · 规则{" "}
                    {autonomy.rules.length} · 候选 {autonomy.proposals.length} ·
                    审批 {autonomy.approvals.length} · 自动失败最多尝试{" "}
                    {autonomy.retry_policy.max_attempts} 次 · 退避{" "}
                    {autonomy.retry_policy.backoff} 至{" "}
                    {autonomy.retry_policy.max_backoff}
                  </small>
                </div>
              </article>
              <div className="steward-compact-list">
                {autonomy.runs.slice(0, 4).map((run) => (
                  <AutonomousRunRow key={run.id} run={run} />
                ))}
                {autonomy.runs.length === 0 ? (
                  <EmptyState text="暂无自主运行记录" />
                ) : null}
              </div>
            </div>
          ) : (
            <EmptyState text="自主能力状态未加载" />
          )}
        </Panel>

        <Panel help={helpText.autonomyRules} title="自主规则">
          <div className="steward-table-list">
            {(autonomy?.rules ?? []).map((rule) => (
              <AutonomyRuleRow
                busy={busy !== null}
                key={rule.id}
                onUpdate={(id, payload) =>
                  runAction("更新自主规则", () =>
                    updateStewardAutonomyRule(id, payload),
                  )
                }
                rule={rule}
              />
            ))}
            {(autonomy?.rules ?? []).length === 0 ? (
              <EmptyState text="暂无自主规则" />
            ) : null}
          </div>
        </Panel>

        <Panel
          actions={
            <button
              className="steward-button steward-button-secondary"
              disabled={busy !== null || candidateProposalCount === 0}
              onClick={() =>
                runAction("批量清理候选", () =>
                  dismissStewardAutonomyProposals({
                    status: "candidate",
                    limit: 100,
                    reason: "workspace candidate cleanup",
                  }),
                )
              }
              type="button"
            >
              清理候选
            </button>
          }
          help={helpText.autonomyProposals}
          title={`自主候选 ${candidateProposalCount > 0 ? `(${candidateProposalCount})` : ""}`}
        >
          <div className="steward-table-list">
            {(autonomy?.proposals ?? []).map((proposal) => (
              <AutonomyProposalRow
                busy={busy !== null}
                key={proposal.id}
                onAction={runAction}
                proposal={proposal}
              />
            ))}
            {(autonomy?.proposals ?? []).length === 0 ? (
              <EmptyState text="暂无自主候选" />
            ) : null}
          </div>
        </Panel>
      </section>

      <section className="steward-list-layout">
        <Panel help={helpText.approvals} title="审批队列">
          <div className="steward-table-list">
            {(autonomy?.approvals ?? []).map((approval) => (
              <ApprovalRow
                approval={approval}
                busy={busy !== null}
                key={approval.id}
                onApprove={(id) =>
                  runAction("批准审批", () =>
                    approveStewardApprovalRequest(
                      id,
                      "approved in steward workspace",
                    ),
                  )
                }
                onReject={(id) =>
                  runAction("拒绝审批", () =>
                    rejectStewardApprovalRequest(
                      id,
                      "rejected in steward workspace",
                    ),
                  )
                }
              />
            ))}
            {(autonomy?.approvals ?? []).length === 0 ? (
              <EmptyState text="暂无审批请求" />
            ) : null}
          </div>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel help={helpText.collectors} title="采集器">
          <div className="steward-collector-list">
            {collectors.map((collector) => (
              <article className="steward-collector-row" key={collector.id}>
                <label className="steward-switch-row">
                  <input
                    checked={collector.enabled}
                    disabled={busy !== null}
                    onChange={(event) =>
                      runAction("更新采集器", () =>
                        updateStewardCollector(collector.name, {
                          enabled: event.currentTarget.checked,
                        }),
                      )
                    }
                    type="checkbox"
                  />
                  <span>
                    <InfoTerm
                      help={
                        collectorHelp[collector.name] ?? collector.scope_summary
                      }
                      label={collector.name}
                    />
                    <small>{collector.scope_summary}</small>
                    {collector.last_error ? <small className="steward-collector-error">{collector.last_error}</small> : null}
                  </span>
                </label>
                <CollectorSettings
                  busy={busy !== null}
                  collector={collector}
                  key={collector.updated_at}
                  onSave={(settings) => runAction("保存采集范围", () => updateStewardCollector(collector.name, { settings }))}
                />
              </article>
            ))}
          </div>
        </Panel>

        <Panel help={helpText.search} title="统一搜索">
          <form className="steward-form" onSubmit={runSearch}>
            <input
              onChange={(event) => setSearchQuery(event.target.value)}
              placeholder="搜索标题、摘要、来源"
              value={searchQuery}
            />
            <select
              onChange={(event) => setSearchType(event.target.value)}
              value={searchType}
            >
              <option value="">全部实体</option>
              <option value="event">事件</option>
              <option value="timeline_segment">时间线</option>
              <option value="task">任务</option>
              <option value="intent">意图</option>
              <option value="memory">记忆</option>
              <option value="knowledge_item">知识</option>
              <option value="observation">原始观察</option>
              <option value="activity_session">活动会话</option>
              <option value="entity">关系实体</option>
              <option value="habit">习惯</option>
              <option value="insight">洞察</option>
            </select>
            <button
              className="steward-button"
              disabled={busy !== null}
              type="submit"
            >
              搜索
            </button>
          </form>
          <div className="steward-compact-list">
            {searchResults.map((item) => (
              <article
                className="steward-compact-item"
                key={`${item.entity_type}-${item.id}`}
              >
                <strong>{item.title}</strong>
                <span>
                  {entityText(item.entity_type)} · {statusText(item.status)} ·{" "}
                  {item.data_level}
                </span>
              </article>
            ))}
            {searchResults.length === 0 ? (
              <EmptyState text="暂无搜索结果" />
            ) : null}
          </div>
        </Panel>

        <Panel
          actions={
            <button
              className="steward-button steward-button-secondary"
              onClick={downloadExport}
              type="button"
            >
              导出数据
            </button>
          }
          title="数据管理"
          help={helpText.dataManagement}
        >
          <form className="steward-form" onSubmit={submitTag}>
            <input
              onChange={(event) =>
                setTagDraft((draft) => ({ ...draft, name: event.target.value }))
              }
              placeholder="新标签"
              value={tagDraft.name}
            />
            <select
              onChange={(event) =>
                setTagDraft((draft) => ({ ...draft, type: event.target.value }))
              }
              value={tagDraft.type}
            >
              <option value="normal">普通</option>
              <option value="system">系统</option>
              <option value="sensitive">敏感</option>
              <option value="lifecycle">生命周期</option>
            </select>
            <button
              className="steward-button"
              disabled={busy !== null}
              type="submit"
            >
              保存标签
            </button>
          </form>
          <div className="steward-tag-list">
            {tags.map((tag) => (
              <span
                className={`steward-tag steward-tag-${tag.type}`}
                key={tag.id}
              >
                {tag.name}
              </span>
            ))}
            {tags.length === 0 ? <EmptyState text="暂无标签" /> : null}
          </div>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel help={helpText.manualEvent} title="手动事件">
          <form className="steward-form" onSubmit={submitEvent}>
            <input
              onChange={(event) =>
                setEventDraft((draft) => ({
                  ...draft,
                  title: event.target.value,
                }))
              }
              placeholder="事件标题"
              value={eventDraft.title}
            />
            <textarea
              onChange={(event) =>
                setEventDraft((draft) => ({
                  ...draft,
                  summary: event.target.value,
                }))
              }
              placeholder="摘要"
              rows={3}
              value={eventDraft.summary}
            />
            <DataLevelSelect
              value={eventDraft.dataLevel}
              onChange={(value) =>
                setEventDraft((draft) => ({ ...draft, dataLevel: value }))
              }
            />
            <button
              className="steward-button"
              disabled={busy !== null}
              type="submit"
            >
              添加事件
            </button>
          </form>
        </Panel>

        <Panel help={helpText.manualTask} title="手动任务">
          <form className="steward-form" onSubmit={submitTask}>
            <input
              onChange={(event) =>
                setTaskDraft((draft) => ({
                  ...draft,
                  title: event.target.value,
                }))
              }
              placeholder="任务标题"
              value={taskDraft.title}
            />
            <textarea
              onChange={(event) =>
                setTaskDraft((draft) => ({
                  ...draft,
                  description: event.target.value,
                }))
              }
              placeholder="任务说明"
              rows={3}
              value={taskDraft.description}
            />
            <div className="steward-form-row">
              <select
                onChange={(event) =>
                  setTaskDraft((draft) => ({
                    ...draft,
                    priority: event.target.value as TaskDraft["priority"],
                  }))
                }
                value={taskDraft.priority}
              >
                <option value="low">低</option>
                <option value="normal">普通</option>
                <option value="high">高</option>
              </select>
              <input
                onChange={(event) =>
                  setTaskDraft((draft) => ({
                    ...draft,
                    dueAt: event.target.value,
                  }))
                }
                type="datetime-local"
                value={taskDraft.dueAt}
              />
            </div>
            <DataLevelSelect
              value={taskDraft.dataLevel}
              onChange={(value) =>
                setTaskDraft((draft) => ({ ...draft, dataLevel: value }))
              }
            />
            <button
              className="steward-button"
              disabled={busy !== null}
              type="submit"
            >
              创建任务
            </button>
          </form>
        </Panel>

        <Panel help={helpText.candidateIntent} title="候选意图">
          <form className="steward-form" onSubmit={submitIntent}>
            <input
              onChange={(event) =>
                setIntentDraft((draft) => ({
                  ...draft,
                  title: event.target.value,
                }))
              }
              placeholder="候选意图"
              value={intentDraft.title}
            />
            <textarea
              onChange={(event) =>
                setIntentDraft((draft) => ({
                  ...draft,
                  reason: event.target.value,
                }))
              }
              placeholder="推断原因"
              rows={2}
              value={intentDraft.reason}
            />
            <input
              onChange={(event) =>
                setIntentDraft((draft) => ({
                  ...draft,
                  suggestedAction: event.target.value,
                }))
              }
              placeholder="建议动作"
              value={intentDraft.suggestedAction}
            />
            <DataLevelSelect
              value={intentDraft.dataLevel}
              onChange={(value) =>
                setIntentDraft((draft) => ({ ...draft, dataLevel: value }))
              }
            />
            <button
              className="steward-button"
              disabled={busy !== null}
              type="submit"
            >
              加入候选池
            </button>
          </form>
        </Panel>
      </section>

      <section className="steward-grid steward-grid-s2">
        <Panel help={helpText.manualMemory} title="手动记忆">
          <form className="steward-form" onSubmit={submitMemory}>
            <input
              onChange={(event) =>
                setMemoryDraft((draft) => ({
                  ...draft,
                  title: event.target.value,
                }))
              }
              placeholder="记忆标题"
              value={memoryDraft.title}
            />
            <input
              onChange={(event) =>
                setMemoryDraft((draft) => ({
                  ...draft,
                  scope: event.target.value,
                }))
              }
              placeholder="适用范围，例如 global/project/device"
              value={memoryDraft.scope}
            />
            <textarea
              onChange={(event) =>
                setMemoryDraft((draft) => ({
                  ...draft,
                  content: event.target.value,
                }))
              }
              placeholder="记忆内容"
              rows={3}
              value={memoryDraft.content}
            />
            <DataLevelSelect
              value={memoryDraft.dataLevel}
              onChange={(value) =>
                setMemoryDraft((draft) => ({ ...draft, dataLevel: value }))
              }
            />
            <button
              className="steward-button"
              disabled={busy !== null}
              type="submit"
            >
              写入记忆
            </button>
          </form>
        </Panel>

        <Panel help={helpText.knowledgeImport} title="知识导入">
          <form className="steward-form" onSubmit={submitKnowledge}>
            <input
              onChange={(event) =>
                setKnowledgeDraft((draft) => ({
                  ...draft,
                  title: event.target.value,
                }))
              }
              placeholder="知识标题"
              value={knowledgeDraft.title}
            />
            <input
              onChange={(event) =>
                setKnowledgeDraft((draft) => ({
                  ...draft,
                  originalUri: event.target.value,
                }))
              }
              placeholder="文件路径或 URL"
              value={knowledgeDraft.originalUri}
            />
            <textarea
              onChange={(event) =>
                setKnowledgeDraft((draft) => ({
                  ...draft,
                  summary: event.target.value,
                }))
              }
              placeholder="摘要"
              rows={3}
              value={knowledgeDraft.summary}
            />
            <div className="steward-form-row">
              <select
                onChange={(event) =>
                  setKnowledgeDraft((draft) => ({
                    ...draft,
                    type: event.target.value,
                  }))
                }
                value={knowledgeDraft.type}
              >
                <option value="note">笔记</option>
                <option value="document">文档</option>
                <option value="webpage">网页</option>
                <option value="code_snippet">代码片段</option>
                <option value="report">报告</option>
              </select>
              <label className="steward-inline-check">
                <input
                  checked={knowledgeDraft.allowIndex}
                  onChange={(event) =>
                    setKnowledgeDraft((draft) => ({
                      ...draft,
                      allowIndex: event.target.checked,
                    }))
                  }
                  type="checkbox"
                />
                <span>
                  检索
                  <HelpIcon text={helpText.indexing} />
                </span>
              </label>
            </div>
            <DataLevelSelect
              value={knowledgeDraft.dataLevel}
              onChange={(value) =>
                setKnowledgeDraft((draft) => ({ ...draft, dataLevel: value }))
              }
            />
            <button
              className="steward-button"
              disabled={busy !== null}
              type="submit"
            >
              导入知识
            </button>
          </form>
        </Panel>

        <Panel help={helpText.memoryCorrection} title="记忆纠正">
          {correctionDraft ? (
            <form className="steward-form" onSubmit={submitCorrection}>
              <input
                onChange={(event) =>
                  setCorrectionDraft(
                    (draft) => draft && { ...draft, title: event.target.value },
                  )
                }
                value={correctionDraft.title}
              />
              <textarea
                onChange={(event) =>
                  setCorrectionDraft(
                    (draft) =>
                      draft && { ...draft, content: event.target.value },
                  )
                }
                rows={3}
                value={correctionDraft.content}
              />
              <input
                onChange={(event) =>
                  setCorrectionDraft(
                    (draft) =>
                      draft && { ...draft, reason: event.target.value },
                  )
                }
                placeholder="纠正原因"
                value={correctionDraft.reason}
              />
              <button
                className="steward-button"
                disabled={busy !== null}
                type="submit"
              >
                保存纠正
              </button>
            </form>
          ) : (
            <EmptyState text="选择一条记忆后可纠正" />
          )}
          <div className="steward-compact-list">
            {memoryVersions.map((version) => (
              <article className="steward-compact-item" key={version.id}>
                <strong>
                  v{version.version} · {version.title}
                </strong>
                <span>
                  {version.reason || "历史版本"} ·{" "}
                  {formatDate(version.created_at)}
                </span>
              </article>
            ))}
          </div>
        </Panel>
      </section>

      <section className="steward-list-layout">
        <Panel help={helpText.timeline} title="时间线">
          <div className="steward-table-list">
            {timelineSegments.map((segment) => (
              <article className="steward-list-item" key={segment.id}>
                <div>
                  <strong>{segment.title}</strong>
                  <p>{segment.summary || "无摘要"}</p>
                  <small>
                    {segment.event_count} 个事件 · {segment.data_level} · v
                    {segment.version} · {formatDate(segment.start_at)}
                  </small>
                </div>
                <div className="steward-row-actions">
                  <button
                    className="steward-icon-button"
                    disabled={busy !== null}
                    onClick={() =>
                      showSources("timeline_segment", segment.id, segment.title)
                    }
                    type="button"
                  >
                    来源
                  </button>
                  <button
                    className="steward-icon-button steward-danger"
                    disabled={busy !== null}
                    onClick={() =>
                      runAction("删除时间线", () =>
                        deleteStewardTimelineSegment(segment.id),
                      )
                    }
                    type="button"
                  >
                    删除
                  </button>
                </div>
              </article>
            ))}
            {timelineSegments.length === 0 ? (
              <EmptyState text="暂无时间线片段" />
            ) : null}
          </div>
        </Panel>

        <Panel help={helpText.events} title="最近事件">
          <div className="steward-table-list">
            {events.map((event) => (
              <EventRow
                busy={busy !== null}
                event={event}
                key={event.id}
                onAction={runAction}
                onSources={showSources}
              />
            ))}
            {events.length === 0 ? <EmptyState text="暂无事件" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.tasks} title="任务">
          <div className="steward-table-list">
            {tasks.map((task) => (
              <TaskRow
                busy={busy !== null}
                key={task.id}
                onAction={runAction}
                task={task}
              />
            ))}
            {tasks.length === 0 ? <EmptyState text="暂无任务" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.intents} title="意图候选池">
          <div className="steward-table-list">
            {intents.map((intent) => (
              <IntentRow
                busy={busy !== null}
                intent={intent}
                key={intent.id}
                onAction={runAction}
                onSources={showSources}
              />
            ))}
            {intents.length === 0 ? <EmptyState text="暂无候选意图" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.memoryLibrary} title="记忆库">
          <div className="steward-table-list">
            {memories.map((memory) => (
              <MemoryRow
                busy={busy !== null}
                key={memory.id}
                memory={memory}
                onAction={runAction}
                onSources={showSources}
                onVersions={showMemoryVersions}
              />
            ))}
            {memories.length === 0 ? <EmptyState text="暂无记忆" /> : null}
          </div>
        </Panel>

        <Panel help={helpText.knowledgeLibrary} title="知识库">
          <div className="steward-table-list">
            {knowledgeItems.map((item) => (
              <KnowledgeRow
                busy={busy !== null}
                item={item}
                key={item.id}
                onAction={runAction}
                onSources={showSources}
              />
            ))}
            {knowledgeItems.length === 0 ? (
              <EmptyState text="暂无知识条目" />
            ) : null}
          </div>
        </Panel>

        <Panel help={helpText.sourcePanel} title={`来源面板 · ${sourceTarget}`}>
          <div className="steward-table-list">
            {displayedSourceRefs.map((ref) => (
              <article
                className="steward-list-item steward-source-item"
                key={ref.id}
              >
                <div>
                  <strong>
                    {entityText(ref.source_type)} · {ref.source_id || "manual"}
                  </strong>
                  <p>{ref.summary || ref.location || "无摘要"}</p>
                  <small>
                    可信度 {Math.round(ref.confidence * 100)}% ·{" "}
                    {ref.sensitive ? "敏感" : "非敏感"} ·{" "}
                    {formatDate(ref.created_at)}
                  </small>
                </div>
              </article>
            ))}
            {displayedSourceRefs.length === 0 ? (
              <EmptyState text="暂无来源引用" />
            ) : null}
          </div>
        </Panel>

        <Panel help={helpText.audit} title="审计日志">
          <div className="steward-audit-list">
            {auditLogs.map((log) => (
              <AuditRow key={log.id} log={log} />
            ))}
            {auditLogs.length === 0 ? <EmptyState text="暂无审计日志" /> : null}
          </div>
        </Panel>
      </section>
    </div>
  );
}
