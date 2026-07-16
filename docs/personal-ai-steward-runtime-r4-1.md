# R4.1 独立 Agent worker 运行说明

R4.1 把策略 Agent 从 Server 进程拆为 `steward-agent-worker`。Server 签发任务绑定委派并写入持久消息箱；worker 只有验证公钥，按 Agent 身份领取消息并调用 Runtime V2。

## 配置

Server：

```env
STEWARD_RUNTIME_V2=true
STEWARD_ORCHESTRATION_R4=true
STEWARD_ORCHESTRATION_WORKERS=true
STEWARD_ORCHESTRATION_SIGNING_KEY=<base64 32-byte Ed25519 seed>
STEWARD_ORCHESTRATION_MESSAGE_LEASE=15s
```

生成 worker 公钥配置：

```powershell
go run ./cmd/steward-agent-worker --print-verify-key
```

worker 环境不能包含 `STEWARD_ORCHESTRATION_SIGNING_KEY`，只配置：

```env
DATABASE_URL=postgres://...
STEWARD_RUNTIME_V2=true
STEWARD_ORCHESTRATION_R4=true
STEWARD_ORCHESTRATION_WORKERS=true
STEWARD_ORCHESTRATION_VERIFY_KEY=<base64 Ed25519 public key>
STEWARD_ORCHESTRATION_MESSAGE_LEASE=15s
```

启动一个绑定 Agent 的进程：

```powershell
go run ./cmd/steward-agent-worker --agent researcher --worker-id researcher-local-1 --poll 250ms
```

数据库 schema 必须先由 Server 或部署迁移完成。worker 不执行迁移，以避免多个进程并发 DDL。

## Agent 配额

注册 Agent 时可配置：

```json
{
  "id": "researcher",
  "name": "Research Agent",
  "role": "bounded collector",
  "permission_ceiling": "A0",
  "data_level_ceiling": "D0",
  "tool_allowlist": ["runtime.echo"],
  "max_concurrency": 1,
  "max_runtime_seconds": 900,
  "max_attempts": 20,
  "max_evidence_bytes": 262144
}
```

运行时间和尝试次数按节点计划的最坏情况计算；证据配额按 Agent 的全部委派运行聚合计算。CPU、内存和进程级 Job Object 硬配额尚未包含在 R4.1。

## 可观测状态

`GET /api/steward/orchestrations/{id}` 同时返回：

- `messages`：状态、投递次数、租约持有者、租约到期时间、最后错误和 ACK 时间；
- `workers`：worker ID、Agent ID、PID、心跳、当前消息和停止时间；
- 原有节点、事件和证据 manifest。

全局暂停时 worker 不领取新消息。父取消、Agent 停用或委派校验失败继续 fail-closed。

## 验收

数据库集成测试：

```powershell
$env:TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable'
go test ./internal/httpapi -run 'TestStewardR41' -count=1 -v
```

真实跨进程与强杀恢复：

```powershell
./scripts/verify-r4-1-workers.ps1
```

脚本构建真实 Server/worker 二进制，启动两个 Agent worker，完成扇出与汇合；随后强杀持有长任务的 worker，等待消息租约过期并启动替代 worker。成功输出应包含不同 `worker_processes`、`crash_recovery_status: succeeded` 和 `crash_message_attempts: 2`。
