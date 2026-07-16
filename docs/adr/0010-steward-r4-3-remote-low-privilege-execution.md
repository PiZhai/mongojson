# ADR-0010: R4.3 跨设备低权限执行

- 状态：Accepted
- 日期：2026-07-17
- 决策范围：受信设备间最高 A2/D2 的签名派发、心跳、恢复和结果证明

## 背景

R4.0-R4.2 的编排、独立 worker 和 Saga 都只在单机内运行。项目已有 S3 双监听 Peer API、设备登记、Ed25519 身份和请求认证，但同步接口只允许数据复制，不能被扩张为通用远程管理面。R4.3 需要建立独立、低权限、可恢复的执行协议。

## 决策

### 协议面隔离

管理 API 继续只绑定本机。Peer API 新增独立的 `/api/steward/remote-execution/dispatches` 协议，和同步接口共享设备认证中间件，但使用独立 outbox/inbox、载荷和状态机。Peer 面仍不暴露通用任务、审批、权限或设备管理 API。

### 双层签名

每次 HTTP 请求继续由来源设备 Ed25519 私钥签名，接收端按 `steward_devices.public_key` 验证。派发正文还包含可持久化的独立签名，绑定：

- 来源和目标设备；
- 编排、节点与 Agent；
- 标准化步骤及远程 plan hash；
- A2/D2 权限包络；
- 来源 control generation；
- 签发和过期时间。

目标设备必须同时验证传输身份和派发正文签名。这样 outbox 重投不依赖原 HTTP 连接，也不能被中间数据库修改后静默执行。

### 设备选址

节点新增 `target_device`：

- `local`：保持既有本机执行；
- 明确设备 ID：固定到已登记、可信、启用、具有公钥和 Peer 地址且权限足够的设备；
- `auto`：从两分钟内见过的合格 Peer 中选择最近在线者，并以设备 ID 稳定打破平局。

选址结果持久化为 `selected_device_id`。派发后不因临时断线迁移到其他设备，避免同一副作用在两台设备重复执行。Saga 补偿固定回原执行设备。

### 最小权限

远程节点硬限制为最高 A2/D2。目标设备再次检查工具存在、版本、权限和本机 `STEWARD_REMOTE_EXECUTION_TOOLS` allowlist；默认只开放 `runtime.echo`。文件和网络工具必须由管理员在每台设备显式启用，并继续受目标设备 Runtime R2 根目录、主机白名单和本地审批策略约束。远程协议不能调用 Privilege Broker 或 A3-A9 工具。

### 持久化恢复

来源设备保存 `steward_remote_dispatches`，目标设备保存 `steward_remote_inbox`。dispatch ID 和目标本地 Runtime idempotency key 保证重复 POST 复用同一运行。目标设备周期性返回由自身密钥签名的状态、心跳和租约；来源断线时保留原状态并退避轮询，恢复连接后继续查询原 dispatch，不重新选址。

### 结果证明

目标设备对终态状态签名，签名覆盖远程 run ID、状态、远程计划哈希、失败摘要、证据数量、数据级别和 evidence manifest SHA-256。来源只有验签、设备身份和 plan hash 全部匹配后才持久化结果并推进父节点。父级 evidence manifest 纳入远程 manifest，不复制远程证据正文。

### 控制传播

父级取消、fail-fast、deadline 和来源急停会把活动 dispatch 标记为 `cancel_requested`，来源在 Peer 恢复后持续发送签名取消。目标将取消写入本地 Runtime V2。目标离线时无法实现瞬时远程停止；因此 R4.3 只允许低权限动作，真实跨设备紧急停止的物理边界必须明确展示。

## 非目标和边界

- 不开放跨设备 A3-A9、Privilege Broker 或凭据代理。
- 不提供跨设备 exactly-once；通过固定设备、幂等 dispatch 和 Runtime 后置条件降低重复风险。
- 不在网络分区时自动迁移活动任务。
- 不复制远程 evidence payload，只验签并聚合 manifest。
- 不把来源 control generation 等同于目标设备本地 generation；目标本机急停始终独立生效。
- mTLS、设备证书轮换和跨公网中继不属于 R4.3，生产网络仍应使用 TLS/VPN。

## 验收门槛

- 两个不同 Server 进程、数据库和设备密钥完成自动选址和远程 Runtime 执行；
- A3 或不在远程 allowlist 的节点必须在派发前拒绝；
- 来源验证目标签名心跳和终态结果；
- 目标接受后断线，重启并重连时不得创建第二个本地运行；
- 父取消必须传播到目标本地 Runtime；
- 远程证据计数和 manifest 必须进入父编排摘要。
