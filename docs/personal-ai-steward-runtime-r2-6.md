# 私人智能管家 R2.6：执行安全层

R2.6 不扩大执行权限，而是把 R2.5 的单域暂停升级为跨 Runtime V2 与 S4 的统一安全边界，并补齐证据最小披露、执行租约、watchdog fencing 和操作系统进程树回收。后续管理员/root 隔离已由 [R3.0 独立 Privilege Broker](personal-ai-steward-runtime-r3.md) 承接。

## 统一紧急停止

新的权威入口是：

```http
GET  /api/steward/execution/control
POST /api/steward/execution/control/stop
POST /api/steward/execution/control/resume
```

`/api/steward/runtime/control/*` 继续作为 R2.5 兼容入口。控制状态保存在 `steward_runtime_execution_control`，每次真实 stop/resume 都递增 `generation` 并写入控制事件。代际是 fencing token：即使用户在旧工具尚未观察停止时快速恢复，旧执行看到代际变化后仍必须取消，不能穿过一次已经发生的紧急停止。

停止顺序如下：

1. 先持久化 `stopped=true` 和新代际，所有进程立即拒绝领取新 Runtime Run。
2. 取消当前进程内 Runtime/S4 执行 context；Runtime 工具以 200ms 周期观察持久化控制状态。
3. 获取 S4 policy write barrier，等待持有 read barrier 的扫描或真实执行退出。
4. 等待 Runtime invocation 协作退出；未及时退出的 invocation 保持可见，由租约和 watchdog 收口。

停止只门控真实执行：任务记录、对话、记忆、同步、审批查看和 S4 模拟仍可工作。恢复只开放新执行和安全队列；已被阻断的未知非幂等结果不会自动重放。

## 证据治理

Run 详情不再内联 EvidenceArtifact 正文，只返回：

- `data_level`、`content_type`、`payload_state`；
- `payload_available`、`size_bytes`、`sha256`、`redacted`；
- 人类可读摘要、类型和时间。

正文必须逐项通过本机管理端显式读取：

```http
GET /api/steward/runs/{run_id}/evidence/{evidence_id}
```

该入口拒绝非 loopback 请求。工具 invocation 只保存安全预览和指向 canonical `tool_result` evidence 的治理元数据；`content`、`stdout`、`stderr`、`body`、凭据字段等不在默认详情中展开。

持久化规则：

- D0-D3 且正文不超过配置上限：以 JSON 保存，但默认详情不返回正文。
- D4-D6 且配置 `STEWARD_LOCAL_ENCRYPTION_KEY`：AES-256-GCM 本机加密保存，显式读取时解密。
- D4-D6 缺少本机加密密钥：只保存摘要、大小和哈希，不落正文。
- 任意级别超过 `STEWARD_RUNTIME_EVIDENCE_MAX_BYTES`：只保存摘要、大小和哈希。默认上限 65536 bytes，硬上限 1 MiB。
- 递归凭据字段和已识别的 credential assignment 在加密或保存前先脱敏；哈希对应治理后的 JSON 正文。

每次成功调用都会生成 canonical `tool_result` evidence，工具自行声明的文件哈希、HTTP 状态、进程退出状态等证据继续独立保存。

## 执行租约与 watchdog

每个 running invocation 持久化：

- `lease_owner`；
- `control_generation`；
- `heartbeat_at`；
- `lease_expires_at`。

worker 默认取得 10 秒租约，并在执行和验证期间续租。daemon 的 `runtime-watchdog` 默认每 2 秒扫描一次，只处理已经过期或旧版本没有租约的 invocation。daemon 启动不再无条件恢复所有 `running` Run，因此新启动的第二个 daemon 不会夺走另一个健康 worker 的任务。

watchdog 的恢复规则：

- replay-safe：旧 invocation 失败并被 fencing，Step 回到 `pending`，Run 回到 `queued`，写入 `run.watchdog_recovered`。
- non-idempotent：结果视为未知，Step/Run 进入 `blocked`，活动审批撤销，写入 `run.watchdog_blocked`。
- 迟到 worker 的 success/fail/cancel 提交必须先把仍为 `running` 的 invocation 原子更新；watchdog 已处理后更新行数为 0，旧 worker 不能覆盖恢复结果。

## 进程树回收

`shell.exec` 不再只依赖 `exec.CommandContext` 杀父进程：

- Windows 为命令创建带 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 的 Job Object，取消、超时或紧急停止时终止整个 Job。
- Unix 为命令创建独立 process group，取消时终止整个进程组。

这样工具启动的子进程不能在父进程被取消后继续脱离运行。

## 配置

```dotenv
STEWARD_RUNTIME_LEASE_TTL=10s
STEWARD_RUNTIME_WATCHDOG_INTERVAL=2s
STEWARD_RUNTIME_WATCHDOG_LIMIT=20
STEWARD_RUNTIME_EVIDENCE_MAX_BYTES=65536
```

敏感证据加密继续使用现有本机 keyring：`STEWARD_LOCAL_ENCRYPTION_KEY`、`STEWARD_LOCAL_ENCRYPTION_KEY_ID` 和 `STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS`。

## 验收门槛

- PostgreSQL 集成测试证明统一 stop 同时阻断 Runtime V2 和 S4，并持久化控制代际。
- 活跃租约不能被 watchdog 接管；过期 replay-safe invocation 被重排，过期 non-idempotent invocation 被阻断且审批撤销。
- Run 默认详情不含 evidence 正文；D4 正文以加密态保存并可经本机显式接口读取；超限正文只保留摘要、大小和哈希。
- Windows 实际父子测试进程在 context 取消后均不再存活。
- R1/R2/R2.5 的重试、审批、SSE、恢复、真实文件、白名单进程和 HTTP 工具回归继续通过。

## R2.6 明确不做

- 不授予管理员/root 权限，不读取或代理明文凭据。
- 不允许任意 Shell、任意路径、任意网络目标或绕过 ToolSpec。
- 不把 watchdog 当成补偿事务；未知非幂等结果只阻断并请求人工复核。
- 不把紧急停止解释为停止记录、记忆、同步或关闭 Agent 进程。
- 本阶段本身不包含独立 Broker；该能力现由 R3.0 承接。仍不实现 R4 多 Agent 与跨设备执行编排。
