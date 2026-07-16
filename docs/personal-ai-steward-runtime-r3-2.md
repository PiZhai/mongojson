# 私人智能管家 R3.2：WebAuthn 交互式审批

R3.2 在 R3.1 独立审批票据上增加 Windows Hello、Touch ID 和安全密钥可承载的 WebAuthn ES256 审批。它缩短日常高权限确认路径，但不改变 Broker 的固定 capability、安全代际和单次授权模型。架构决策见 [ADR-0005](adr/0005-steward-r3-2-webauthn-approval.md)。

## Policy Authority

Broker policy 继续使用 version 2。WebAuthn Authority 必须包含：

```json
{
  "name": "local-webauthn",
  "algorithm": "webauthn-es256",
  "public_key": "<base64 SubjectPublicKeyInfo P-256 public key>",
  "credential_id": "<base64url credential id without padding>",
  "rp_id": "localhost",
  "allowed_origins": ["http://localhost:4174"],
  "enabled": true
}
```

生产 origin 必须使用 HTTPS；只有 `localhost` 允许 HTTP。credential、公钥、RP ID 与 origin 均由 Broker 严格规范化，未知字段、重复 Authority 或跨 RP origin 会导致策略加载失败。

## 使用流程

1. 在管家执行控制面生成 WebAuthn 登记材料；浏览器私钥保留在平台认证器中。
2. 管理员核对材料，通过受保护路径加入 Broker policy 并重启 system service。
3. 为只包含一个 `privilege.execute` 的计划填写审批理由。
4. 选择匹配当前 origin 的 Authority，使用 Windows Hello/Touch ID/安全密钥确认。
5. 控制面把 assertion 作为签名审批 proof 提交；Broker 再独立验证并消费。

如果平台不支持 WebAuthn、用户取消或 Authority origin 不匹配，审批不会降级为普通确认；可显式改用 R3.1 隔离 Ed25519 CLI 并粘贴 proof JSON。

## 安全与证据

- challenge 绑定 subject、plan hash、capability、control generation、granted_by、reason、proof ID 和有效期。
- `userVerification=required`，Broker 同时要求 UP/UV flags。
- assertion 仍受一次性 proof、短期 grant、统一紧急停止代际和 receipt 签名约束。
- 非零 authenticator sign counter 必须递增，并进入签名审计；值为 0 表示认证器未提供克隆检测能力。
- 工作台不得保存公钥之外的登记秘密，也不得修改 Broker policy。

## 验收

```powershell
cd backend
go test ./internal/privilegebroker ./internal/service/steward ./internal/httpapi

cd ../frontend
npm test -- --run
npm run build
```

后端测试必须覆盖有效 assertion，以及 credential、challenge、origin、RP hash、UP、UV、签名、canonical base64url 和 sign counter 回退拒绝。前端测试必须用固定 claims 证明生成 challenge 与 Go 实现一致。

## 尚未覆盖

- Broker system service 安装路径与 DACL、独立恢复授权、签名 checkpoint、Windows restricted token 及 suspended-start Job Object 已由 [R3.3](personal-ai-steward-runtime-r3-3.md) 承接；
- 在线 Authority 撤销分发、企业 attestation 与 A8/A9 凭据代理。
