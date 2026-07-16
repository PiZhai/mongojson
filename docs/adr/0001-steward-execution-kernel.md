# ADR-0001：建立可持久化的执行内核，而不是继续扩展任务记录模型

- 状态：已接受
- 日期：2026-07-16
- 决策范围：R0 架构基线、R1 执行内核

## 背景

现有 steward 已具备事件、任务、意图、记忆、同步、自治候选、审批记录和固定配置工具等能力，但它们围绕“记录一件事”和“执行一个预先写死的动作”组织。`StewardTask` 不是执行实例，`StewardAutonomousRun` 也没有可恢复的多步骤计划、逐步工具调用、后置验证和证据链。继续在这些表和接口上追加字段，会把业务任务、模型建议、工具执行和系统权限混成一个无法可靠恢复的状态机。

目标形态要求自然语言任务最终能够落到操作系统。进入文件、Shell、GUI、浏览器和凭据代理之前，必须先有一个与具体系统工具无关、可持久化、可审批、可取消、可恢复、可验证的执行内核。

## 决策

### 1. 新建独立的 Runtime V2 有界上下文

执行内核使用独立实体和表：

- `AgentRun`：一次不可变计划对应的执行实例。
- `RunStep`：有序、有依赖、可重试的执行步骤。
- `ToolSpec`：工具契约、版本、输入输出 schema、权限与风险声明。
- `ToolInvocation`：某一步的某次真实调用尝试。
- `ApprovalGrant`：绑定 `run_id + plan_hash` 的批准凭证。
- `EvidenceArtifact`：验证结果、工具输出摘要和后续可扩展的文件/截图/日志证据。
- `RunEvent`：只追加的状态事件日志，也是 SSE 的数据源。

旧的 `StewardTask`、`StewardAutonomyProposal`、`StewardApprovalRequest` 和 `StewardAutonomousRun` 在迁移期保持原语义和 API，不与 Runtime V2 共表。未来由适配器把“任务/建议”编译成 `AgentRun`，而不是让 `AgentRun` 反向承担任务管理职责。

### 2. 计划先于执行，计划哈希不可变

R1 只接收显式的结构化手工计划。规范化后的目标、执行模式、目标设备、数据级别、权限上限和步骤图计算 SHA-256 `plan_hash`。审批、幂等冲突判断和步骤幂等键都引用这个哈希。

调度元数据（请求者、是否立即入队、HTTP 幂等键）不改变计划含义，因此不进入 `plan_hash`。计划内容改变必须创建新的 Run；不允许批准后原地改步骤。

### 3. 执行状态必须持久化

Run 主路径：

```text
draft -> planning -> awaiting_approval -> queued -> running -> verifying -> succeeded
```

异常状态：`failed`、`cancelled`、`compensating`、`blocked`。

Step 状态：`pending -> running -> verifying -> succeeded`，异常状态为 `failed`、`cancelled`、`blocked`。

状态切换、调用记录和事件写入尽量在同一数据库事务中完成。进程重启时，将中断的 `running/verifying` Run 恢复到 `queued`，保留失败 invocation，并使用新的 attempt 继续执行。

### 4. “执行成功”必须包含验证

工具返回成功不等于任务成功。每个 `RuntimeTool` 同时提供 `Execute` 和 `Verify`。Step 只有在后置条件通过后才进入 `succeeded`，Run 只有在所有步骤成功且最终不变量检查通过后才进入 `succeeded`。输出和验证证据写入 `EvidenceArtifact`。

R1 内置的 `runtime.echo` 是唯一生产注册工具，用于证明内核，而不是提供操作系统能力。它是确定性的、A0、低风险、支持取消。真实文件、Shell、GUI、浏览器和凭据工具属于 R2/R3。

### 5. 执行器不直接获得系统权限

核心只依赖 `RuntimeTool` 契约，不直接调用 PowerShell、Shell、Windows API 或桌面自动化。R2 可以增加沙箱内用户态工具；R3 必须通过独立 Privilege Broker、短期能力令牌和策略引擎处理提权。主 Web/API 进程不得因为“管家需要高权限”而长期以管理员/root 身份运行。

### 6. 并行迁移并由功能开关控制

`STEWARD_RUNTIME_V2=false` 是默认值。关闭时：

- V2 表可以迁移并保留数据；
- V1 API 和后台循环不变；
- Runtime V2 API 返回 503；
- Runtime V2 循环不执行任务。

开启时由 `runtime-v2` daemon loop 恢复中断 Run 并领取 `queued` Run。旧接口不自动双写，避免尚未验证的迁移影响已有工作流。

## 并发、幂等与恢复规则

- `AgentRun.idempotency_key` 非空时全局唯一。同一键、同一计划返回原 Run；同一键、不同计划返回 409。
- worker 使用 `FOR UPDATE SKIP LOCKED` 领取 Run，并在事务中把它改为 `running`，避免多实例重复领取。
- 每一步有稳定幂等键，每次 attempt 有唯一 invocation 键；调用历史不覆盖。
- 自动重试受 `max_attempts` 限制，单次调用受 `timeout_seconds` 限制。
- `cancel` 对未执行 Run 立即生效；对执行中 Run 设置 `cancel_requested`，工具通过 context 协作取消，并在步骤边界再次检查。
- R1 重启恢复只允许确定性工具。R2 引入非幂等写工具之前，必须增加工具级 reconcile/probe，不能盲目重放未知结果的调用。

## API 决策

管理面新增：

- `GET /api/steward/runtime/tools`
- `POST /api/steward/runs`
- `GET /api/steward/runs/{id}`
- `GET /api/steward/runs/{id}/events`（SSE，支持 `Last-Event-ID`）
- `POST /api/steward/runs/{id}/start`
- `POST /api/steward/runs/{id}/approve`
- `POST /api/steward/runs/{id}/cancel`
- `POST /api/steward/runs/{id}/resume`

这些接口不加入跨设备 Peer 协议面。跨设备调度属于后续阶段，必须先解决目标设备能力声明、身份、授权转移和远程取消语义。

## 数据库迁移边界

- 只新增 `steward_runtime_*`、`steward_agent_runs`、`steward_run_steps`、`steward_tool_invocations`、`steward_approval_grants` 和 `steward_evidence_artifacts`，不改写旧表数据。
- 迁移可重复执行；关闭功能开关不需要回滚 schema。
- 回退应用版本前应保留新表，不应删除执行证据。
- R2 若改变 ToolSpec 契约，新增版本而不是原地改变已被 Run 引用的执行语义。

## 阶段边界

### R0 已确立

- 有界上下文、状态机、实体、API、迁移和权限边界形成书面决策。
- 旧 V1 继续运行，Runtime V2 通过开关并行引入。

### R1 本决策允许实现

- 手工结构化计划。
- PostgreSQL 持久化状态机。
- 工具注册表和一个确定性测试工具。
- 幂等、超时、重试、取消、恢复、审批绑定、验证、证据和 SSE。

### R1 明确不实现

- 自然语言规划器和模型自主改计划。
- 文件、Shell、GUI、浏览器、邮件、凭据等真实工具。
- 管理员/root 提权、Privilege Broker、长期能力授权。
- 自动补偿、跨设备执行和并行 DAG 调度。
- 前端操作台重构。

## 结果与代价

正面结果：任务管理与执行语义解耦；执行过程可恢复和审计；审批不能被计划变更复用；R2/R3 可以围绕稳定工具契约演进。

代价：短期存在 V1/V2 两套 Run 概念；查询一次 Run 需要组合步骤、调用、证据和审批；R1 的重启恢复策略仅适用于确定性工具。接受这些代价，是为了避免在真实系统权限接入后再返工核心状态机。
