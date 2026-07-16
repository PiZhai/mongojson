# 私人智能管家 R3.3：Privilege Broker 生产加固

R3.3 把独立 Broker 从开发可运行状态提升为生产安全基线。架构规则见 [ADR-0006](adr/0006-steward-r3-3-production-hardening.md)。

## 密钥边界

`steward-broker keygen` 生成三类材料：

- `client_key`：Steward 与 Broker 均持有，可执行 stop，不能 resume；
- `control_key`：只放在 Broker 受保护秘密文件和独立管理员环境；
- Broker Ed25519 signing key：只属于 Broker，公开密钥固定到 Steward。

Steward 服务环境只能包含 client key 与 Broker public key。发现 `STEWARD_BROKER_CONTROL_KEY` 或 signing private key 出现在 Steward 环境时应视为部署失效。

## Windows 安装

在管理员 PowerShell 中准备 policy 和 `keygen` 输出后运行：

```powershell
steward-broker service install --scope system --policy C:\staging\policy.json `
  --client-key '<client key>' --control-key '<control key>' `
  --signing-private-key '<broker signing private key>' --start
```

默认目标：

- `%ProgramFiles%\MongoJSON\StewardBroker`：服务二进制；
- `%ProgramData%\MongoJSON\StewardBroker`：policy、state、audit、checkpoint、`service-secrets.json`；
- 服务：`MongojsonStewardBroker`，启用专用 Service SID；Broker 信任文件只授权 SYSTEM/Administrators，不授权 capability 子进程。

安装器复制并重新校验 policy、初始化签名 checkpoint、收紧 ACL 后才启动。`--dry-run` 只返回脱敏计划，不落盘。

## 旧部署迁移

停止旧 Broker，配置新增 control key 与 checkpoint 路径，然后以 Broker signing key 显式锚定现有完整审计链：

```powershell
steward-broker initialize-checkpoint
```

命令拒绝覆盖现有 checkpoint。初始化前必须人工备份并确认 audit/state 未被截断；缺失或不一致时不得重建锚来绕过告警。

## 紧急停止与恢复

工作台仍可一键 stop。恢复必须按顺序执行：

```powershell
$env:STEWARD_BROKER_CONTROL_KEY = '<control key from protected admin channel>'
steward-broker control resume --generation <current+1> `
  --reason '人工检查完成' --changed-by 'local-admin'
Remove-Item Env:STEWARD_BROKER_CONTROL_KEY
```

然后在工作台恢复统一执行。工作台会验证 Broker 已处于准确的下一代际；未先完成独立恢复时，本地 Runtime V2/S4 保持 stopped。

## 验收

```powershell
./backend/scripts/verify-r3-broker.ps1 `
  -DatabaseUrl 'postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable'
```

脚本验证真实独立进程、一次性审批、固定 capability、receipt、client-key stop、client-key resume 拒绝、control-key 独立恢复、Runtime/S4 第二阶段恢复、checkpoint 与签名审计链。

代码门禁：

```powershell
cd backend
go test ./... -count=1
go vet ./...
```

## 剩余边界

- named pipe 调用方 SID 验证尚未替换 loopback TCP；当前仍使用 loopback、HMAC、nonce 与时间窗纵深防御；
- TPM/HSM 或远程透明日志形式的外部单调 checkpoint 尚未实现；
- capability 尚未按每项资源生成独立 AppContainer；当前使用专用 Service SID、固定 policy、restricted token、最小环境和 Job Object；
- A8/A9 凭据代理、跨设备 A4-A7 委派和不可逆自治仍关闭。
