# ADR-0004：R3.1 独立审批证明

- 状态：已接受并实现
- 日期：2026-07-17
- 关联：[ADR-0003：R3 独立 Privilege Broker](0003-steward-r3-privilege-broker.md)

## 背景

R3.0 已把 A4-A7 能力移入独立 Broker，但 Broker 仍相信 Steward 主进程提供的 `approval_ref`。一旦主进程被完全控制，攻击者可以伪造审批引用并申请能力令牌。这不符合“高权限执行必须由另一个信任域确认”的目标。

## 决策

引入独立 Ed25519 Approval Authority。其私钥不得进入 Steward 主进程、Broker 服务环境或两者可读取的目录。Broker policy v2 只登记启用的 Authority 公钥；签发由独立的 `steward-approval` CLI 完成，后续可以替换为 HSM、WebAuthn 或独立桌面确认器而不改变 Broker 协议。

签名票据固定绑定：

- 票据版本与 256 位随机 `proof_id`；
- 唯一执行主体，例如 `runtime:<run-id>` 或 `s4:<proposal-id>`；
- 不可变计划 SHA-256；
- 唯一 Broker capability；
- 当前全局控制代际；
- 审批人、审批理由、签发时间和到期时间。

票据最长有效 15 分钟。一个 Runtime run 目前只允许一个 `privilege.execute` 步骤；多个高权限动作必须拆分为多个 run 并逐项审批。

## 验证与消费

Steward 从 Broker 的签名状态取得可信 Authority 公钥，先做用户反馈所需的预校验并持久化原始票据。该预校验不是最终授权。

Broker 在发 grant 前重新独立完成签名、主体、计划、能力、理由以外的执行绑定、控制代际和有效期验证。`approval_ref` 必须等于签名 `proof_id`。Broker 在 `grant.issued` 审计记录成功落盘后消费票据；同一 proof 不能再次获得 grant。启动时 Broker 从已验证的签名审计链恢复已消费 proof 集合，因此重启不会恢复重放能力。

grant、执行 receipt 和审计链都记录 `approval_proof_id`、Authority key ID 与审批到期时间。票据在 grant 后、执行前到期时，执行仍被拒绝。

## 故障语义

- 缺失、过期、篡改、绑定不一致或不受信任的票据一律 fail closed；
- grant 已签发但调用方未执行时，票据仍视为已消费，必须重新人工审批；
- 紧急停止/恢复提升控制代际，之前签发的审批票据和能力令牌均失效；
- S4 自动策略无法绕过独立票据，缺失票据时只会安全失败。

## 运维边界

`steward-approval` 私钥必须由不同 OS 身份、离线终端或硬件密钥保护。若私钥文件对 Steward 服务账号可读，独立审批属性即失效。CLI 的 `issue` 命令要求显式 `--approve`，且不会被安装到 Broker 服务环境。

R3.1 尚未提供 WebAuthn/Windows Hello/macOS LocalAuthentication 交互确认，也没有 Authority 撤销列表在线分发。Authority 轮换通过更新 root-owned Broker policy、重启 Broker，并由签名状态向主进程公布新 key ID 完成。
