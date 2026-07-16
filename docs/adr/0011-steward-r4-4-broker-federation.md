# ADR-0011：R4.4 Broker-to-Broker 高权限委派与凭据代理

- 状态：已接受并实现
- 日期：2026-07-17
- 关联：[ADR-0006](0006-steward-r3-3-production-hardening.md)、[ADR-0010](0010-steward-r4-3-remote-low-privilege-execution.md)

## 背景

R4.3 的设备签名只证明“哪台 Steward 发出了请求”，不能授权远端 A4-A7。Steward 主服务持有本地 Broker client key，一旦把该密钥、远端 Broker client key、审批私钥或凭据明文放入 Peer 协议，就会破坏 R3.3 的进程隔离和独立审批边界。

## 决策

R4.4 使用两层互不替代的签名：设备 Ed25519 负责传输身份、心跳和结果信封；Broker Ed25519 负责高权限授权和执行 receipt。远端主服务只充当不可信的持久化转运层。

高权限节点必须先调用 preview，固定节点、目标设备、目标 Broker key、capability、credential refs、计划哈希和控制代际。独立 Approval Authority 对该 expectation 签名后，来源 Broker 执行以下检查：

1. 来源本地控制未停止且代际一致；
2. 审批证明绑定 subject、plan、capability 和来源代际且未使用；
3. policy v3 信任目标设备的 Broker 公钥，并显式允许 capability 和 credential refs；
4. 目标 Broker 的签名状态未过期、未停止，实例、策略摘要、代际和 capability 摘要明确。

来源 Broker 随后签发一次性 `steward-broker-delegation/v1`。目标执行前还必须通过 Peer 回查来源 Broker 的新鲜签名状态；目标 Broker 验证来源实例、来源代际和 `stopped=false`，防止来源在签发后急停却由受控失败的主进程继续重放。目标 Broker 再验证来源 Broker policy、委派签名、目标设备、自己的 key/instance/policy/generation、capability digest、凭据 allowlist、有效期和重放状态，然后才运行固定 capability。Broker API 仍只监听 loopback；跨设备网络只承载签名状态、委派和 receipt，不承载 Broker HMAC key。

## 凭据代理

policy v3 可以登记受保护的本机 credential 文件，并将 credential ID 同时绑定到 capability 和对端 Broker。网络、数据库、命令行参数和子进程环境只出现不敏感 ID。目标 Broker 在执行前读取文件，通过匿名 stdin 发送 JSON；凭据不会进入 argv、环境变量、Steward 日志或返回正文。

只要 capability 绑定了凭据，Broker 对 Steward 隐藏 stdout/stderr，只返回大小、SHA-256 和签名 receipt。该能力是固定 capability 的不透明本机凭据代理，不是允许读取、导出或任意使用秘密的 A8/A9 通用凭据权限。

## 状态、取消和证据

来源 outbox 与目标 inbox 继续提供持久派发、心跳和断线恢复。目标执行期间保存可取消 context；父任务取消会中止目标 Broker HTTP 请求，Broker 再终止 Job Object/process group。全局急停仍由各设备本地 Broker 的独立 stop 控制负责。

成功结果必须同时通过目标设备 status 签名和目标 Broker receipt 签名，并精确匹配 delegation ID、实例、capability digest、计划、审批、目标代际、来源 Broker key 和 credential refs。失败若发生在 Broker 接纳前，可以只有目标设备签名；任何声称成功的结果缺少 Broker receipt 都会被来源拒绝。

## 明确边界

- 只开放 policy 固定的 A4-A7 capability，不开放任意 Shell、动态 executable、动态 argv 或环境变量。
- 来源设备和来源 Broker 必须在线且状态证明新鲜；R4.4 不允许像 R4.3 低权限任务一样离线继续启动高权限动作。
- 任一 Broker 重启都会更换 instance ID；未执行委派随之失效，需要新的 preview 和独立审批，不能自动扩权续签。
- credential 文件必须通过所有权、DACL/权限和无 reparse/symlink 检查；安装工具不会替管理员创建真实凭据。
- 跨公网仍需要 TLS/VPN。设备签名和 Broker 签名不提供流量机密性。
- A8/A9 通用凭据读取、导出、长期令牌铸造和不可逆自治仍关闭。
