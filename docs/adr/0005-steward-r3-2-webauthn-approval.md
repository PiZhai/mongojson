# ADR-0005：R3.2 WebAuthn 交互式高权限审批

- 状态：Accepted
- 关联：[ADR-0003](0003-steward-r3-privilege-broker.md)、[ADR-0004](0004-steward-r3-1-independent-approval.md)

## 背景

R3.1 用独立 Ed25519 Authority 关闭了主进程伪造审批的边界，但人工签发、复制 JSON 的流程不适合日常使用，私钥文件也容易被钓鱼或误复制。高权限执行需要一个不把私钥交给 Steward、同时要求本地用户验证的交互式入口。

## 决策

Broker policy v2 的 Approval Authority 新增 `webauthn-es256` 算法。每个 Authority 固定绑定 ES256/P-256 公钥、credential ID、RP ID 和允许的 origin。浏览器以当前不可变执行主体、计划哈希、唯一 capability、控制代际、审批人、理由和最长 15 分钟有效期构造 challenge，并调用 WebAuthn `get()`，且强制 `userVerification=required`。

Broker 不信任浏览器的“已成功”结论，仍独立验证：

- credential ID、RP ID hash、origin 与 `webauthn.get` 类型；
- challenge 对完整审批 claims 的 SHA-256 绑定；
- User Presence 与 User Verification 标志；
- ES256 签名、Authority key ID、有效期和执行绑定；
- proof 单次消费及非零 authenticator sign counter 单调递增。

认证器不提供计数器（值为 0）时仍可使用，但控制面不得宣称具备克隆检测。非零计数器会写入 Broker 签名审计链并在重启后恢复；相同或回退计数被拒绝。

WebAuthn 登记只在浏览器生成策略材料，主服务不自动写入 root-owned Broker policy。管理员必须核对 origin/RP ID 后，通过受保护的部署流程更新策略并重启 Broker。

## 兼容与失败语义

- R3.2 Broker 协议为 `steward-privilege-broker/v1.1`，现已由 R3.3 v1.2 取代；R3.1 Ed25519 proof 保持兼容。
- 无 WebAuthn、origin 不匹配、用户取消、UV 缺失或签名失败时一律 fail closed；用户仍可使用隔离的 Ed25519 Authority。
- 主服务只接收公开 Authority 元数据和最终 assertion，不接收认证器私钥。
- WebAuthn 不扩大 A4-A7 范围，不开放 A8/A9、动态参数或任意 shell。

## 后续安全工作

R3.2 不解决 system service 安装目录 ACL、独立 resume Authority、审计 checkpoint 防回滚、Windows suspended process/job 原子隔离和 restricted token。这些属于下一阶段 Broker 部署与控制信任域加固，在完成前生产环境不得从普通用户可写目录以 LocalSystem 运行 Broker。
