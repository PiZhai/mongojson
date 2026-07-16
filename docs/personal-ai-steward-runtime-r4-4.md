# R4.4 跨设备高权限委派运行说明

R4.4 在 R4.3 outbox/inbox 上增加 Broker-to-Broker 授权。每台参与设备仍运行普通权限 Steward 和独立 system-scope Broker。

## Broker policy v3

来源 Broker 和目标 Broker 必须互相固定对方的设备 ID、Broker 公钥及允许范围。目标设备另外登记本机 credential 和 capability 绑定：

```json
{
  "version": 3,
  "broker_peers": [{
    "device_id": "windows-main",
    "name": "Windows Broker",
    "public_key": "<broker Ed25519 public key>",
    "allowed_capabilities": ["tool:deploy"],
    "allowed_credentials": ["credential:deploy"] ,
    "enabled": true
  }],
  "credentials": [{
    "id": "credential:deploy",
    "path": "C:\\ProgramData\\MongoJSON\\StewardBroker\\credentials\\deploy.token",
    "max_bytes": 16384,
    "enabled": true
  }],
  "capabilities": [{
    "name": "tool:deploy",
    "permission_level": "A6",
    "credential_ids": ["credential:deploy"]
  }]
}
```

完整 capability 仍必须包含固定 executable、SHA-256、argv、工作目录、超时和输出上限。Broker 服务还应配置：

```env
STEWARD_BROKER_DEVICE_ID=windows-main
```

设备登记需要同时固定设备身份和 Broker 身份：`public_key`、`broker_public_key`、`broker_key_id` 和 Peer `api_base_url`。

## 创建与审批

高权限远程节点只接受一个 `privilege.execute` step，不接受同一节点内的补偿步骤：

```json
{
  "target_device": "windows-main",
  "permission_ceiling": "A7",
  "data_level": "D4",
  "credential_refs": ["credential:deploy"],
  "steps": [{
    "key": "deploy",
    "tool_name": "privilege.execute",
    "arguments": {"capability": "tool:deploy"}
  }]
}
```

调用顺序：

```text
POST /api/steward/orchestrations/{id}/nodes/{nodeID}/remote-privilege/preview
steward-approval issue --approve --subject ... --plan-hash ... --capability ... --generation ...
POST /api/steward/orchestrations/{id}/nodes/{nodeID}/remote-privilege/approve
```

审批完成前节点保持 `pending`，详情的 `remote_privilege.status` 为 `awaiting_approval`；来源 Broker 签发委派后变为 `delegated`，随后才允许进入远程 outbox。目标执行前必须在线回查来源 Broker 的新鲜签名控制状态；来源离线、急停、代际变化或 Broker 重启时安全失败，不会离线启动 A4-A7。

## 验收

```powershell
$env:TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable'
go test ./internal/privilegebroker ./internal/httpapi -run 'TestBrokerToBroker|TestStewardR44' -count=1 -v
./scripts/verify-r4-4-broker-federation.ps1
```

真实验收会启动两个 Steward、两个 Broker、两个 PostgreSQL 数据库和四套独立 Ed25519 身份，检查独立审批、委派、A4 capability、opaque credential refs、Broker audit、双重结果验签及正文不泄露。
