import { useState } from "react";
import {
  acceptStewardIntent,
  approveStewardAutonomyProposal,
  archiveStewardMemory,
  cancelStewardTask,
  completeStewardTask,
  convertStewardEvent,
  deleteStewardEvent,
  deleteStewardIntent,
  deleteStewardKnowledgeItem,
  deleteStewardMemory,
  deleteStewardTask,
  dismissStewardAutonomyProposal,
  dismissStewardIntent,
  executeStewardAutonomyProposal,
  retryStewardAutonomyProposal,
  hideStewardEvent,
  muteStewardIntent,
  simulateStewardAutonomyProposal,
} from "../../../lib/api/client";
import type {
  StewardApprovalRequest,
  StewardSignedApprovalProof,
  StewardAuditLog,
  StewardAutonomousRun,
  StewardAutonomyProposal,
  StewardAutonomyRule,
  StewardDevice,
  StewardDeviceCapability,
  StewardDevicePermission,
  StewardDiscoveredPeer,
  StewardEvent,
  StewardIntent,
  StewardKnowledgeItem,
  StewardMemory,
  StewardSyncChange,
  StewardSyncConflict,
  StewardTask,
} from "../../../types/tooling";
import { entityText, formatDate, priorityText, statusText } from "./model";

export type StewardActionRunner = (
  label: string,
  action: () => Promise<unknown>,
) => Promise<void>;
export type StewardSourceLoader = (
  entityType: string,
  id: string,
  title: string,
) => Promise<void>;

export function SyncChangeRow({ change }: { change: StewardSyncChange }) {
  return (
    <article className="steward-compact-item">
      <strong>
        #{change.sequence} · {entityText(change.entity_type)} ·{" "}
        {statusText(change.operation)}
      </strong>
      <span>
        {statusText(change.sync_status)} · {change.origin_device_id} ·{" "}
        {change.data_level} · v{change.version}
      </span>
    </article>
  );
}

export function DeviceRow({
  device,
  busy,
  onRevoke,
  onSync,
  onVerify,
}: {
  device: StewardDevice;
  busy: boolean;
  onRevoke: (id: string) => Promise<void>;
  onSync: (id: string) => Promise<void>;
  onVerify: (id: string) => Promise<void>;
}) {
  const isLocal = device.role === "local";
  const canVerify =
    !isLocal &&
    device.trust_status !== "revoked" &&
    Boolean(device.api_base_url && device.public_key);
  return (
    <article className="steward-list-item">
      <div>
        <strong>{device.device_name || device.id}</strong>
        <p>
          {device.platform} · {device.role} · {statusText(device.trust_status)}
        </p>
        <small>
          {device.permission_level} ·{" "}
          {device.sync_enabled ? "同步开启" : "同步关闭"} · 最近{" "}
          {formatDate(device.last_seen_at)}
        </small>
        <small>
          {device.api_base_url || "未配置 peer API"} · 拉取序号{" "}
          {device.last_sync_sequence ?? 0} · 发送序号{" "}
          {device.last_sent_sequence ?? 0} · 同步{" "}
          {formatDate(device.last_sync_at)}
        </small>
        {device.last_sync_error ? (
          <small className="steward-error-text">{device.last_sync_error}</small>
        ) : null}
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={
            busy ||
            isLocal ||
            device.trust_status === "revoked" ||
            !device.api_base_url
          }
          onClick={() => onSync(device.id)}
          type="button"
        >
          同步
        </button>
        <button
          className="steward-icon-button"
          disabled={busy || !canVerify}
          onClick={() => onVerify(device.id)}
          type="button"
        >
          验证
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy || isLocal || device.trust_status === "revoked"}
          onClick={() => onRevoke(device.id)}
          type="button"
        >
          撤销
        </button>
      </div>
    </article>
  );
}

export function DiscoveredPeerRow({ peer }: { peer: StewardDiscoveredPeer }) {
  const fingerprint = peer.public_key_fingerprint.slice(0, 16);
  return (
    <article className="steward-list-item">
      <div>
        <strong>{peer.device_name || peer.device_id}</strong>
        <p>
          {peer.platform} · 公告签名{peer.signature_verified ? "有效" : "无效"}·
          尚未信任
        </p>
        <small>{peer.peer_api_base}</small>
        <small>
          指纹 {fingerprint} · 最近 {formatDate(peer.last_seen_at)} · 过期{" "}
          {formatDate(peer.expires_at)}
        </small>
      </div>
    </article>
  );
}

export function DevicePermissionRow({
  permission,
  device,
  capability,
  busy,
  onUpdate,
}: {
  permission: StewardDevicePermission;
  device: StewardDevice | null;
  capability: StewardDeviceCapability | null;
  busy: boolean;
  onUpdate: (
    deviceId: string,
    capability: string,
    payload: {
      policy?: string;
      max_permission_level?: string;
      scope_summary?: string;
    },
  ) => Promise<void>;
}) {
  const deviceName = device?.device_name || device?.id || permission.device_id;
  const disabled = busy || device?.trust_status === "revoked";
  const updatePayload = (patch: {
    policy?: string;
    max_permission_level?: string;
  }) => ({
    policy: patch.policy ?? permission.policy,
    max_permission_level:
      patch.max_permission_level ?? permission.max_permission_level,
    scope_summary: permission.scope_summary,
  });
  return (
    <article className="steward-list-item">
      <div>
        <strong>{permission.capability}</strong>
        <p>
          {permission.scope_summary ||
            capability?.description ||
            "未设置权限范围说明"}
        </p>
        <small>
          {deviceName} · {capability?.target_type || "设备策略"} · 当前
          {statusText(permission.policy)} · 最高{" "}
          {permission.max_permission_level}
        </small>
      </div>
      <div className="steward-row-actions">
        <select
          className="steward-inline-select"
          disabled={disabled}
          onChange={(event) =>
            onUpdate(
              permission.device_id,
              permission.capability,
              updatePayload({ policy: event.currentTarget.value }),
            )
          }
          value={permission.policy}
        >
          <option value="allow">允许</option>
          <option value="confirm">需确认</option>
          <option value="deny">拒绝</option>
        </select>
        <select
          className="steward-inline-select steward-inline-select-narrow"
          disabled={disabled}
          onChange={(event) =>
            onUpdate(
              permission.device_id,
              permission.capability,
              updatePayload({
                max_permission_level: event.currentTarget.value,
              }),
            )
          }
          value={permission.max_permission_level}
        >
          {["A0", "A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9"].map(
            (level) => (
              <option key={level} value={level}>
                最高 {level}
              </option>
            ),
          )}
        </select>
      </div>
    </article>
  );
}

export function SyncConflictRow({
  conflict,
  busy,
  onResolve,
}: {
  conflict: StewardSyncConflict;
  busy: boolean;
  onResolve: (id: string) => Promise<void>;
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>
          {entityText(conflict.entity_type)} · {statusText(conflict.status)}
        </strong>
        <p>{conflict.reason}</p>
        <small>
          {conflict.entity_id} · {formatDate(conflict.created_at)}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy || conflict.status === "resolved"}
          onClick={() => onResolve(conflict.id)}
          type="button"
        >
          已人工处理
        </button>
      </div>
    </article>
  );
}

export function AutonomyRuleRow({
  rule,
  busy,
  onUpdate,
}: {
  rule: StewardAutonomyRule;
  busy: boolean;
  onUpdate: (
    id: string,
    payload: {
      policy?: string;
      enabled?: boolean;
      max_permission_level?: string;
      scope_summary?: string;
    },
  ) => Promise<void>;
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{rule.name}</strong>
        <p>{rule.scope_summary}</p>
        <small>
          {rule.trigger_type} · {rule.risk_level} · {rule.max_permission_level}
        </small>
      </div>
      <div className="steward-row-actions">
        <label className="steward-inline-check">
          <input
            checked={rule.enabled}
            disabled={busy}
            onChange={(event) =>
              onUpdate(rule.id, { enabled: event.currentTarget.checked })
            }
            type="checkbox"
          />
          <span>启用</span>
        </label>
        <select
          className="steward-inline-select"
          disabled={busy}
          onChange={(event) =>
            onUpdate(rule.id, { policy: event.currentTarget.value })
          }
          value={rule.policy}
        >
          <option value="suggest">仅建议</option>
          <option value="confirm">需确认</option>
          <option value="auto">低风险自动</option>
          <option value="never">禁止</option>
        </select>
        <select
          aria-label={`${rule.name} 最高权限`}
          className="steward-inline-select"
          disabled={busy}
          onChange={(event) =>
            onUpdate(rule.id, {
              max_permission_level: event.currentTarget.value,
            })
          }
          value={rule.max_permission_level}
        >
          {Array.from({ length: 10 }, (_, rank) => `A${rank}`).map((level) => (
            <option key={level} value={level}>最高 {level}</option>
          ))}
        </select>
      </div>
    </article>
  );
}

export function AutonomyProposalRow({
  proposal,
  busy,
  onAction,
}: {
  proposal: StewardAutonomyProposal;
  busy: boolean;
  onAction: StewardActionRunner;
}) {
  const executable =
    proposal.status === "approved" ||
    (proposal.status === "candidate" && proposal.policy === "auto");
  return (
    <article className="steward-list-item">
      <div>
        <strong>{proposal.title}</strong>
        <p>{proposal.trigger_reason || proposal.summary}</p>
        {proposal.score_reason ? (
          <p>评分依据：{proposal.score_reason}</p>
        ) : null}
        {proposal.failed_attempts > 0 ? (
          <p>
            执行失败 {proposal.failed_attempts} 次 · {" "}
            {proposal.retry_exhausted
              ? "自动重试已停止"
              : proposal.auto_retry_at
                ? `下次自动重试 ${formatDate(proposal.auto_retry_at)}`
                : "可人工重试"}
          </p>
        ) : null}
        <small>
          候选分 {Math.round(proposal.score * 100)}% ·{" "}
          {statusText(proposal.status)} · {statusText(proposal.policy)} ·{" "}
          {proposal.action} · {proposal.risk_level} ·{" "}
          {proposal.permission_level}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("模拟自主候选", () =>
              simulateStewardAutonomyProposal(proposal.id),
            )
          }
          type="button"
        >
          模拟
        </button>
        <button
          className="steward-icon-button"
          disabled={busy || proposal.status !== "candidate"}
          onClick={() =>
            onAction("批准自主候选", () =>
              approveStewardAutonomyProposal(proposal.id),
            )
          }
          type="button"
        >
          批准
        </button>
        <button
          className="steward-icon-button"
          disabled={busy || !executable}
          onClick={() =>
            onAction("执行自主候选", () =>
              executeStewardAutonomyProposal(proposal.id),
            )
          }
          type="button"
        >
          执行
        </button>
        {proposal.retry_eligible ? (
          <button
            className="steward-icon-button"
            disabled={busy}
            onClick={() =>
              onAction("重试自主候选", () =>
                retryStewardAutonomyProposal(proposal.id),
              )
            }
            type="button"
          >
            重试
          </button>
        ) : null}
        <button
          className="steward-icon-button steward-danger"
          disabled={busy || proposal.status === "dismissed"}
          onClick={() =>
            onAction("忽略自主候选", () =>
              dismissStewardAutonomyProposal(proposal.id),
            )
          }
          type="button"
        >
          忽略
        </button>
      </div>
    </article>
  );
}

export function ApprovalRow({
  approval,
  busy,
  onApprove,
  onReject,
}: {
  approval: StewardApprovalRequest;
  busy: boolean;
  onApprove: (id: string, proof?: StewardSignedApprovalProof) => Promise<void>;
  onReject: (id: string) => Promise<void>;
}) {
  const [proofText, setProofText] = useState("");
  const [proofError, setProofError] = useState("");
  const expectation = approval.approval_proof_expectation;
  async function approve() {
    let proof: StewardSignedApprovalProof | undefined;
    if (approval.approval_proof_required) {
      try {
        proof = JSON.parse(proofText) as StewardSignedApprovalProof;
      } catch {
        setProofError("签名审批票据不是有效 JSON");
        return;
      }
    }
    setProofError("");
    await onApprove(approval.id, proof);
  }
  return (
    <article className="steward-list-item">
      <div>
        <strong>{approval.requested_action}</strong>
        <p>{approval.plan_summary || approval.risk_summary}</p>
        <small>
          {statusText(approval.status)} · {formatDate(approval.created_at)}
        </small>
        {approval.approval_proof_required && expectation ? (
          <details>
            <summary>独立审批票据</summary>
            <p>在隔离终端签发，理由固定为 approved in steward workspace：</p>
            <pre>{`steward-approval issue --approve --subject "${expectation.subject}" --plan-hash "${expectation.plan_hash}" --capability "${expectation.capability}" --generation ${expectation.control_generation} --granted-by "local-user" --reason "approved in steward workspace"`}</pre>
            <textarea onChange={(event) => setProofText(event.target.value)} placeholder="粘贴签名审批票据 JSON" rows={6} value={proofText} />
            {proofError ? <small className="steward-danger">{proofError}</small> : null}
          </details>
        ) : null}
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          onClick={() => void approve()}
          disabled={busy || approval.status !== "pending" || (approval.approval_proof_required && !proofText.trim())}
          type="button"
        >
          批准
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy || approval.status !== "pending"}
          onClick={() => onReject(approval.id)}
          type="button"
        >
          拒绝
        </button>
      </div>
    </article>
  );
}

export function AutonomousRunRow({ run }: { run: StewardAutonomousRun }) {
  return (
    <article className="steward-compact-item">
      <strong>
        {statusText(run.mode)} · {statusText(run.status)}
      </strong>
      <span>
        {run.impact_summary || run.trigger_reason}
      </span>
      {run.recovery_hint ? <small>恢复建议：{run.recovery_hint}</small> : null}
    </article>
  );
}

export function EventRow({
  event,
  busy,
  onAction,
  onSources,
}: {
  event: StewardEvent;
  busy: boolean;
  onAction: StewardActionRunner;
  onSources: StewardSourceLoader;
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{event.title}</strong>
        <p>{event.summary || "无摘要"}</p>
        <small>
          {event.type} · {event.source} · {event.data_level} · v{event.version}{" "}
          · {formatDate(event.created_at)}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("事件转任务", () => convertStewardEvent(event.id, "task"))
          }
          type="button"
        >
          转任务
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("事件转意图", () =>
              convertStewardEvent(event.id, "intent"),
            )
          }
          type="button"
        >
          转意图
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("事件转记忆", () =>
              convertStewardEvent(event.id, "memory"),
            )
          }
          type="button"
        >
          转记忆
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("事件转知识", () =>
              convertStewardEvent(event.id, "knowledge"),
            )
          }
          type="button"
        >
          转知识
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("事件入时间线", () =>
              convertStewardEvent(event.id, "timeline"),
            )
          }
          type="button"
        >
          入时间线
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() => onSources("event", event.id, event.title)}
          type="button"
        >
          来源
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() => onAction("隐藏事件", () => hideStewardEvent(event.id))}
          type="button"
        >
          隐藏
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy}
          onClick={() =>
            onAction("删除事件", () => deleteStewardEvent(event.id))
          }
          type="button"
        >
          删除
        </button>
      </div>
    </article>
  );
}

export function TaskRow({
  task,
  busy,
  onAction,
}: {
  task: StewardTask;
  busy: boolean;
  onAction: StewardActionRunner;
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{task.title}</strong>
        <p>{task.description || "无说明"}</p>
        <small>
          {statusText(task.status)} · {priorityText(task.priority)} ·{" "}
          {task.data_level} · v{task.version} · 截止 {formatDate(task.due_at)}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy || task.status === "done"}
          onClick={() =>
            onAction("完成任务", () => completeStewardTask(task.id))
          }
          type="button"
        >
          完成
        </button>
        <button
          className="steward-icon-button"
          disabled={busy || task.status === "canceled"}
          onClick={() => onAction("取消任务", () => cancelStewardTask(task.id))}
          type="button"
        >
          取消
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy}
          onClick={() => onAction("删除任务", () => deleteStewardTask(task.id))}
          type="button"
        >
          删除
        </button>
      </div>
    </article>
  );
}

export function IntentRow({
  intent,
  busy,
  onAction,
  onSources,
}: {
  intent: StewardIntent;
  busy: boolean;
  onAction: StewardActionRunner;
  onSources: StewardSourceLoader;
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{intent.title}</strong>
        <p>{intent.reason || intent.summary || "无原因"}</p>
        <small>
          {statusText(intent.status)} · 可信度{" "}
          {Math.round(intent.confidence * 100)}% · {intent.data_level} · v
          {intent.version}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy || intent.status === "accepted"}
          onClick={() =>
            onAction("接受意图", () => acceptStewardIntent(intent.id))
          }
          type="button"
        >
          接受
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("忽略意图", () => dismissStewardIntent(intent.id))
          }
          type="button"
        >
          忽略
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("静音意图", () => muteStewardIntent(intent.id))
          }
          type="button"
        >
          静音
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() => onSources("intent", intent.id, intent.title)}
          type="button"
        >
          来源
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy}
          onClick={() =>
            onAction("删除意图", () => deleteStewardIntent(intent.id))
          }
          type="button"
        >
          删除
        </button>
      </div>
    </article>
  );
}

export function MemoryRow({
  memory,
  busy,
  onAction,
  onSources,
  onVersions,
}: {
  memory: StewardMemory;
  busy: boolean;
  onAction: StewardActionRunner;
  onSources: StewardSourceLoader;
  onVersions: (memory: StewardMemory) => Promise<void>;
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{memory.title}</strong>
        <p>{memory.summary || memory.content || "无内容"}</p>
        <small>
          {statusText(memory.status)} · {memory.scope} · {memory.data_level} · v
          {memory.version} · {memory.user_confirmed ? "已确认" : "未确认"}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() => onVersions(memory)}
          type="button"
        >
          纠正
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() => onSources("memory", memory.id, memory.title)}
          type="button"
        >
          来源
        </button>
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() =>
            onAction("归档记忆", () => archiveStewardMemory(memory.id))
          }
          type="button"
        >
          归档
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy}
          onClick={() =>
            onAction("删除记忆", () => deleteStewardMemory(memory.id))
          }
          type="button"
        >
          删除
        </button>
      </div>
    </article>
  );
}

export function KnowledgeRow({
  item,
  busy,
  onAction,
  onSources,
}: {
  item: StewardKnowledgeItem;
  busy: boolean;
  onAction: StewardActionRunner;
  onSources: StewardSourceLoader;
}) {
  return (
    <article className="steward-list-item">
      <div>
        <strong>{item.title}</strong>
        <p>{item.summary || item.original_uri || "无摘要"}</p>
        <small>
          {item.type} · {item.data_level} · v{item.version} ·{" "}
          {item.allow_index ? "可检索" : "不进检索"}
        </small>
      </div>
      <div className="steward-row-actions">
        <button
          className="steward-icon-button"
          disabled={busy}
          onClick={() => onSources("knowledge_item", item.id, item.title)}
          type="button"
        >
          来源
        </button>
        <button
          className="steward-icon-button steward-danger"
          disabled={busy}
          onClick={() =>
            onAction("删除知识", () => deleteStewardKnowledgeItem(item.id))
          }
          type="button"
        >
          删除
        </button>
      </div>
    </article>
  );
}

export function AuditRow({ log }: { log: StewardAuditLog }) {
  return (
    <article className="steward-audit-item">
      <span>{log.action}</span>
      <strong>{entityText(log.target_type)}</strong>
      <small>
        {log.permission_level} · {log.data_level} · v{log.version} ·{" "}
        {formatDate(log.occurred_at)}
      </small>
      <p>{log.after_summary || log.output_summary || log.input_summary}</p>
    </article>
  );
}
