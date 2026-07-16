# R4.0 单机多 Agent 编排运行说明

R4.0 在 Runtime V2 上增加单机编排层。它不会给 Agent 新增系统权限；每个节点仍是普通 Runtime V2 run，并继续受计划哈希、审批、证据治理、watchdog、全局急停和 Privilege Broker 约束。

## 启用

```env
STEWARD_RUNTIME_V2=true
STEWARD_ORCHESTRATION_R4=true
STEWARD_ORCHESTRATION_SIGNING_KEY=<base64 encoding of exactly 32 random bytes>
STEWARD_ORCHESTRATION_DELEGATION_TTL=15m
```

签名密钥是本机 task-bound delegation 的 Ed25519 私钥种子。它应进入服务私有环境或 secret store，不得写入数据库、前端配置、worker 环境或编排请求。

## API

```text
GET  /api/steward/orchestration/agents
PUT  /api/steward/orchestration/agents
GET  /api/steward/orchestrations
POST /api/steward/orchestrations
GET  /api/steward/orchestrations/{id}
POST /api/steward/orchestrations/{id}/start
POST /api/steward/orchestrations/{id}/cancel
```

Agent 示例：

```json
{
  "id": "researcher",
  "name": "Research Agent",
  "role": "bounded fact collector",
  "permission_ceiling": "A0",
  "data_level_ceiling": "D0",
  "tool_allowlist": ["runtime.echo"],
  "max_concurrency": 1
}
```

编排示例：

```json
{
  "goal": "并行收集并汇总",
  "auto_start": true,
  "permission_ceiling": "A0",
  "data_level": "D0",
  "max_parallel": 2,
  "nodes": [
    {
      "key": "collect_a",
      "agent_id": "researcher",
      "goal": "收集 A",
      "steps": [{"key": "echo", "tool_name": "runtime.echo", "arguments": {"value": "A"}}]
    },
    {
      "key": "collect_b",
      "agent_id": "writer",
      "goal": "收集 B",
      "steps": [{"key": "echo", "tool_name": "runtime.echo", "arguments": {"value": "B"}}]
    },
    {
      "key": "join",
      "agent_id": "writer",
      "goal": "汇总",
      "depends_on": ["collect_a", "collect_b"],
      "steps": [{"key": "echo", "tool_name": "runtime.echo", "arguments": {"value": "A+B"}}]
    }
  ]
}
```

## 调度语义

- daemon 的 `runtime-v2` loop 在每轮 Runtime 执行前后调用编排协调器；
- ready 节点受父级 `max_parallel` 和 Agent `max_concurrency` 双重限制；
- 需要审批的子运行进入 `awaiting_approval`，继续使用既有 run approval API；
- 子运行重试、验证、证据和超时语义不变；
- `fail_fast` 会取消其余未完成子运行；
- 父级取消立即取消未启动子运行，并对运行中的工具设置协作取消标记；
- 急停恢复后，旧 delegation generation 不再有效，调度器会重新 fencing。

## 证据

`GET /api/steward/orchestrations/{id}` 返回：

- 所有节点及对应 `runtime_run_id`；
- Ed25519 委派元数据，不返回签名密钥；
- 编排事件序列；
- `child_run_count`、`artifact_count`、`redacted_count`、数据级别集合；
- 按证据 ID 与 SHA-256 排序生成的 `manifest_sha256`。

证据正文仍只能通过原 Runtime V2 本地 evidence endpoint 读取。

## 验收

数据库集成验收：

```powershell
$env:TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable'
go test ./internal/httpapi -run 'TestStewardR40' -count=1 -v
```

真实服务进程验收：

```powershell
./scripts/verify-r4-orchestration.ps1
```

## 剩余边界

- R4.0 兼容同进程执行；生产式独立 Agent worker 由 R4.1 提供；
- 尚无跨设备派发、mTLS/Broker-to-Broker 委派；
- Saga 补偿由 R4.2 在兼容的编排层上提供；
- 尚无动态工具生成、隔离评测和晋级机制；
- orchestration signing key 仍是本机服务 secret，管理员级密钥回滚问题沿用 R3.3 的 TPM/HSM/远程见证边界。
