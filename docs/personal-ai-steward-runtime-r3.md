# 私人智能管家 R3.0：独立 Privilege Broker

> R3.1 已在本基线上增加独立签名审批票据。新部署应继续阅读 [R3.1 独立审批证明](personal-ai-steward-runtime-r3-1.md)，并使用 policy version 2 与 `privilege.execute@3.1.0`。

R3.0 将 A4-A7 高权限动作从 Steward 主进程迁移到独立的 `steward-broker` system service。主进程只按能力名申请一次性短时令牌，不能向 Broker 传入可执行路径、参数、工作目录或环境变量。完整架构决策见 [ADR-0003](adr/0003-steward-r3-privilege-broker.md)。

## 已交付能力

- 独立 `steward-broker` 二进制和 Windows Service、systemd、launchd system scope 管理入口。
- 回环限定、HMAC 请求认证、时间戳与 nonce 重放防护。
- 固定 Ed25519 Broker 身份，以及签名状态、能力令牌和执行 receipt。
- 5 秒至 2 分钟的短时 grant、单次消费、Broker 实例绑定和控制代际 fencing。
- Broker 自有的 A4-A7 固定能力策略、可执行文件 SHA-256 固定与启动前复检。
- 与 stop 线性化的执行准入，以及启动前审计 fail-closed；finished 审计异常仍返回签名故障 receipt。
- shell interpreter 拒绝、固定参数、固定工作目录、超时和输出上限。
- Windows Job Object / Unix process group 进程树取消。
- 签名 append-only 审计哈希链，启动时完整防篡改校验。
- Runtime 工具已升级为 `privilege.execute@3.1.0`，强制计划哈希与独立签名审批票据绑定。
- S4 configured tool 执行迁移到 Broker；主进程不再直接 `exec` 配置的高权限路径。
- 与 R2.6 统一 stop/resume、状态面板、代际和 Evidence 治理联动。

## Broker 策略

策略是 Broker 的执行授权源。下面是结构示例，其中路径和哈希必须替换为目标机器上真实、由管理员控制的二进制：

```json
{
  "version": 2,
  "approval_authorities": [
    {
      "name": "local-operator",
      "public_key": "<base64 Ed25519 public key>",
      "enabled": true
    }
  ],
  "capabilities": [
    {
      "name": "tool:restart-approved-service",
      "description": "Restart one pre-approved local service",
      "permission_level": "A7",
      "risk_level": "critical",
      "executable": "C:\\Program Files\\StewardTools\\restart-approved-service.exe",
      "executable_sha256": "<64 lowercase hex characters>",
      "arguments": ["MongojsonWorker"],
      "working_directory": "C:\\Program Files\\StewardTools",
      "timeout_seconds": 60,
      "max_output_bytes": 65536,
      "enabled": true
    }
  ]
}
```

Linux/macOS 同样要求绝对路径。能力数为 1-128；单能力输出上限为 1 KiB-1 MiB，超时为 1-3600 秒。不要把能够解释用户输入的通用脚本包装器登记为能力，否则会绕过“固定命令”边界。

计算和校验哈希：

```powershell
(Get-FileHash -Algorithm SHA256 'C:\Program Files\StewardTools\restart-approved-service.exe').Hash.ToLowerInvariant()
steward-broker validate-policy --policy 'C:\ProgramData\Mongojson\StewardBroker\policy.json'
```

```bash
sha256sum /usr/local/libexec/steward/restart-approved-service
steward-broker validate-policy --policy /etc/mongojson-steward-broker/policy.json
```

## 密钥和服务部署

先由管理员生成一次密钥材料：

```powershell
steward-broker keygen
```

输出分成两组：

- `broker_env`：HMAC client key 与 Ed25519 private key，只写入 Broker system service。
- `steward_env`：相同 client key 与 Ed25519 public key，只写入普通 Steward service。

私钥不得进入主服务、`.env`、数据库、前端或日志。生产路径必须使用管理员/root ACL，至少保护 Broker 二进制、policy、state、audit 和 private key；策略文件不得允许普通 Steward 身份写入。

设置 Broker 环境或使用对应 flags 后，以 system scope 安装：

```powershell
steward-broker service install `
  --scope system `
  --policy 'C:\ProgramData\Mongojson\StewardBroker\policy.json' `
  --state 'C:\ProgramData\Mongojson\StewardBroker\state.json' `
  --audit 'C:\ProgramData\Mongojson\StewardBroker\audit.jsonl' `
  --client-key '<base64-client-key>' `
  --signing-private-key '<base64-private-key>' `
  --dry-run
```

检查 dry-run 后移除 `--dry-run` 并加 `--start`。Linux/macOS 使用相同命令，服务名和服务管理器由平台实现选择。Broker 不依赖 PostgreSQL，也不加载主 Steward 的模型、同步或数据库凭据。

普通 Steward 服务启用 R3：

```powershell
steward service install `
  --runtime-r3 `
  --broker-url 'http://127.0.0.1:18100' `
  --broker-client-key '<base64-client-key>' `
  --broker-public-key '<base64-public-key>' `
  --dry-run
```

`--runtime-r3` 自动启用 Runtime V2。安装命令会在修改服务前校验回环 URL、HMAC key 和固定公钥；dry-run 和服务计划会脱敏 client key。

## 运行与诊断

Broker CLI 使用主服务侧的 client/public key 环境读取签名状态：

```powershell
steward-broker status
steward-broker service status
```

主 API 的统一视图：

```http
GET /api/steward/execution/control
```

`broker` 字段包含 configured、reachable、stopped、generation、instance ID、policy digest、key ID、capability count、active executions 和错误。工作台会把不可达、签名失败或控制代际不一致显示为危险状态。

管理员可以直接执行带审计原因的控制命令，但正常运行应通过主 API 统一 stop/resume，以同时 fencing Runtime V2、S4 与 Broker：

```powershell
steward-broker control stop --generation 5 --reason 'incident isolation' --changed-by 'local-admin'
steward-broker control resume --generation 6 --reason 'inspection complete' --changed-by 'local-admin'
```

## Runtime 与 S4 接入

启用 R2 Planner 与 R3 后，本地规则只接受显式、精确的能力标识：

```text
执行高权限能力 tool:restart-approved-service
```

Planner 不会从“帮我重启一下”之类模糊表述猜测高权限能力。工作台从 Broker 签名状态列出可用 capability，并在 Broker 已配置时开放 A4-A7 计划权限上限；生成的计划仍只包含能力名，必须预览并审批后才会申请 grant。

Runtime 计划中的高权限步骤使用：

```json
{
  "tool_name": "privilege.execute",
  "tool_version": "3.1.0",
  "arguments": { "capability": "tool:restart-approved-service" }
}
```

该工具固定为 A7/critical/always approval/non-idempotent。执行器必须从运行上下文获得当前 Run ID、不可变 plan hash、active approval ID 和 control generation；缺任一字段都不发 grant。

S4 configured tool 的 `action` 必须与 Broker capability 同名，且管理元数据与 Broker 签名元数据一致。执行结果记录 Broker receipt ID 及 stdout/stderr SHA-256；敏感正文继续遵循 R2.6 D0-D6 Evidence 规则。

## 验收门槛

Windows 开发机可在临时 PostgreSQL 上重复执行真实跨进程验收：

```powershell
./backend/scripts/verify-r3-broker.ps1 -DatabaseUrl 'postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable'
```

脚本动态构建普通主进程和独立 Broker，生成临时密钥与固定 `whoami.exe` 策略，验证审批绑定 receipt、Evidence 最小披露、统一 stop/resume 和独立审计链，随后按已校验的临时绝对路径清理进程和文件。它是开发验收工具，不替代生产 system-scope 安装和 ACL 检查。

- 真实独立 Broker 进程只监听 loopback，签名状态可由固定公钥验证。
- 策略外能力、A8/A9、shell interpreter、错误二进制哈希全部被拒绝。
- grant 绑定计划/审批/代际，过期、重复、重启前和旧代际令牌不能执行。
- 统一 stop 能取消 Broker 活跃进程树，停止期间不发 grant，恢复后旧代际仍被 fencing。
- receipt 签名、输出哈希和独立 audit hash chain 可验证；篡改 audit 后 Broker 拒绝启动。
- 主进程高权限执行路径不再调用数据库中的 executable/arguments。
- Broker 离线时普通记录、查询和审批查看可用，高权限执行安全失败且控制面明确告警。

## R3.0 明确不做

- 不开放 A8/A9，不代理密码、令牌、银行卡、签名密钥或任意用户凭据。
- 不支持动态参数、任意 Shell、任意可执行文件或用户可写脚本。
- R3.0 基线本身不抵抗主进程伪造审批引用；当前实现已由 R3.1 独立签名票据补齐该边界。
- 不实现 R4 多 Agent、跨设备高权限委派、分布式补偿或自动生成高权限工具。
