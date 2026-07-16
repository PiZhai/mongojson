# 私人智能管家 R3.1：独立审批证明

R3.1 关闭 R3.0 的最后一个主进程信任缺口：高权限执行不再接受普通 `approval_ref`，必须携带独立 Approval Authority 签名的短时一次性票据。架构依据见 [ADR-0004](adr/0004-steward-r3-1-independent-approval.md)。

## 组件

- `steward-approval`：独立密钥生成、票据签发和离线校验 CLI；只在审批人的隔离环境使用。
- `steward-broker`：加载 policy v2 中的 Authority 公钥，独立验证并持久化消费票据。
- Steward Runtime/S4：展示精确签发参数、预校验并保存票据，但不持有 Authority 私钥。
- `privilege.execute@3.1.0`：把签名票据绑定到 grant、receipt 和 Evidence。

## 初始化

生成独立审批密钥：

```powershell
steward-approval keygen
```

把输出中的 `public_key` 加入 Broker policy v2。私钥必须只保存在独立审批身份可读的位置：

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
      "name": "tool:whoami",
      "description": "Return the Broker service identity",
      "permission_level": "A4",
      "risk_level": "high",
      "executable": "C:\\Windows\\System32\\whoami.exe",
      "executable_sha256": "<sha256>",
      "arguments": [],
      "working_directory": "C:\\Windows\\System32",
      "timeout_seconds": 15,
      "max_output_bytes": 4096,
      "enabled": true
    }
  ]
}
```

`steward-broker validate-policy --policy <path>` 会同时输出策略摘要、公开能力和可信 Authority key ID。

## 签发与审批

工作台会根据待审批对象给出精确命令。审批理由必须与票据中的理由完全相同：

```powershell
$env:STEWARD_APPROVAL_PRIVATE_KEY = '<private key only visible to approval identity>'
steward-approval issue --approve `
  --subject 'runtime:<run-id>' `
  --plan-hash '<plan sha256>' `
  --capability 'tool:whoami' `
  --generation 0 `
  --granted-by 'local-user' `
  --reason '允许本次固定身份检查'
Remove-Item Env:STEWARD_APPROVAL_PRIVATE_KEY
```

将输出 JSON 粘贴到 Runtime 或 S4 审批区。票据默认 5 分钟到期，`--ttl` 最大为 15 分钟。

可在隔离环境离线复核：

```powershell
steward-approval verify --public-key '<public key>' --proof .\proof.json `
  --subject 'runtime:<run-id>' --plan-hash '<plan sha256>' `
  --capability 'tool:whoami' --generation 0 --reason '允许本次固定身份检查'
```

## 安全规则

- Main/Broker 环境中禁止出现 `STEWARD_APPROVAL_PRIVATE_KEY`。
- policy 必须是 version 2，并至少启用一个 Approval Authority。
- 每张票据只能获得一个 grant；Broker 重启后仍拒绝重放。
- 票据、grant 和 receipt 同时受全局 stop generation fencing。
- 票据签发后若 grant 调用失败或超时，不自动复用，必须重新确认。
- Runtime 当前每个 run 只允许一个高权限步骤；多动作任务拆分后逐项审批。

## 验收

开发环境可运行：

```powershell
.\backend\scripts\verify-r3-broker.ps1
```

脚本分别构建普通服务、Broker 与 Approval CLI，验证缺失/篡改票据拒绝、真实跨进程执行、签名 receipt、停止/恢复代际以及审计链，然后清理临时进程和文件。

## 尚未覆盖

- WebAuthn、Windows Hello、Touch ID 等抗钓鱼交互式审批已由 [R3.2](personal-ai-steward-runtime-r3-2.md) 承接；
- HSM/TPM 私钥后端和在线 Authority 撤销列表；
- 一张审批覆盖多个高权限步骤；
- A8/A9 凭据代理、跨设备高权限委派和不可逆自治。
