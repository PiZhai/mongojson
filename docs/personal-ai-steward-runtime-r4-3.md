# R4.3 跨设备低权限执行运行说明

R4.3 允许 R4 节点在已配对设备的 Runtime V2 上执行。它复用现有设备 Ed25519 身份和受限 Peer 监听器，但远程 outbox/inbox 与同步数据完全分开。

## 配置

每台参与设备都需要原有 S3 配置：

```env
STEWARD_AGENT_ID=windows-main
STEWARD_PEER_HTTP_ADDR=:18081
STEWARD_PUBLIC_API_BASE=https://windows-main.example/api
STEWARD_DEVICE_PUBLIC_KEY=<steward keygen public_key>
STEWARD_DEVICE_PRIVATE_KEY=<steward keygen private_key>
STEWARD_SYNC_REQUIRE_AUTH=true
```

启用远程执行：

```env
STEWARD_RUNTIME_V2=true
STEWARD_ORCHESTRATION_R4=true
STEWARD_ORCHESTRATION_REMOTE=true
STEWARD_REMOTE_EXECUTION_LEASE=30s
STEWARD_REMOTE_EXECUTION_TOOLS=runtime.echo
```

参与设备必须互相登记 ID、公钥和受限 Peer API 地址。生产环境应通过 TLS、WireGuard、Tailscale 或等价私网暴露 Peer 端口，不能暴露本地管理 API。

## 节点选址

```json
{
  "key": "inspect_peer",
  "agent_id": "diagnostics",
  "goal": "在合适设备执行低权限诊断",
  "target_device": "auto",
  "permission_ceiling": "A2",
  "data_level": "D1",
  "steps": [
    {"key": "echo", "tool_name": "runtime.echo", "arguments": {"value": "remote"}}
  ]
}
```

`auto` 只选择最近两分钟内在线、可信、启用同步、具有公钥和 Peer 地址且权限足够的 Peer。使用明确设备 ID 可为离线设备创建持久待发任务；恢复连接后会发送。选址成功后 API 返回 `selected_device_id`。

## 状态和恢复

节点详情的 `remote_dispatch` 返回：

- `status`：`pending/sent/accepted/running/succeeded/failed/cancelled/blocked`；
- `attempt`、`heartbeat_at`、`lease_expires_at`；
- 目标 `remote_run_id`；
- 终态 `result_payload` 和 `result_signature`；
- 连接或验签错误。

来源通过同一个 dispatch ID 重投，目标用 `r43:<origin>:<dispatch>` 作为本地 Runtime idempotency key。已接受任务断线后不会改派其他设备；目标可继续本地执行，重连后来源验签并收敛结果。

## 权限和审批

- 远程硬上限是 A2/D2。
- 默认远程 allowlist 只有 `runtime.echo`。
- 开放 `filesystem.read`、`filesystem.write` 或网络工具时，必须同时在目标设备启用 Runtime R2，并配置其本地根目录、主机白名单和 `STEWARD_REMOTE_EXECUTION_TOOLS`。
- 目标本地策略增加的审批不能被来源设备绕过；运行会保持 `accepted`，直到目标设备完成审批。
- R4.3 低权限路径仍不允许 A3-A9、Shell 或 Privilege Broker；A4-A7 只能走 [R4.4 双 Broker 委派](personal-ai-steward-runtime-r4-4.md)，不能用设备签名绕过。

## Peer API

```text
POST /api/steward/remote-execution/dispatches
GET  /api/steward/remote-execution/dispatches/{id}
POST /api/steward/remote-execution/dispatches/{id}/cancel
```

这些接口只存在于 Peer 监听器，并要求已登记设备签名认证。管理监听器不暴露这些协议接口。

## 验收

PostgreSQL 双节点集成测试：

```powershell
$env:TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable'
go test ./internal/httpapi -run 'TestStewardR43' -count=1 -v
```

真实双进程、双数据库、双设备密钥与断线重启验收：

```powershell
./scripts/verify-r4-3-remote-execution.ps1
```

成功输出应包含不同的 `origin_process_id/target_process_id`、`selected_device_id`、`result_signature_verified: true`、远程 run ID 和父级 evidence manifest。
