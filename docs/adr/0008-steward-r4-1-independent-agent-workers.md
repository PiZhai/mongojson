# ADR-0008: R4.1 独立 Agent worker

- 状态：Accepted
- 日期：2026-07-17
- 决策范围：单机独立 worker、消息箱、租约、资源预算和崩溃恢复

## 背景

R4.0 已建立受控 DAG 和 Runtime V2 子运行，但策略 Agent 仍可由主服务进程执行。该结构不能证明 Agent 已形成独立故障域，也无法在进程崩溃后可靠判断任务是否应被重新投递。

## 决策

### Server 与 worker 分工

Server 只负责注册 Agent、验证 DAG、签发委派、调度节点和归并状态。`steward-agent-worker` 是独立可执行文件；每个进程绑定一个 Agent ID，只领取该 Agent 的消息，并通过既有 Runtime V2 工具链执行。启用 worker 模式后，主服务的通用 Runtime claimant 排除编排子运行，不能偷取 Agent 工作。

### 持久消息箱

每个已派发节点产生唯一 `execute` 消息，状态为 `pending -> leased -> acknowledged`，不可恢复或超过投递预算时进入 `dead`。领取使用 PostgreSQL 行锁和 `SKIP LOCKED`，消息绑定 Agent、编排、节点与不可变子运行。ACK 只允许当前租约持有者提交。

### 两层租约与恢复

消息租约确认哪个 worker 拥有任务；Runtime invocation 租约确认哪个工具调用仍在执行。worker 定期续租二者。进程失联后：

1. 消息租约到期，原 worker 被持久化为 `stopped`；
2. 消息重新进入 `pending`，投递次数递增；
3. 替代 worker 验证委派并检查是否仍有活跃 invocation；
4. invocation 已过期时由 Runtime watchdog 恢复；若崩溃发生在两个步骤之间，则把无活跃 invocation 的 `running/verifying` 子运行恢复到 `queued`；
5. 所有迟到 ACK、迟到工具提交和失去租约的续租均被拒绝。

### 不向 worker 下放签名权

委派签名改为 Ed25519。Server 独占 32 字节私钥种子，worker 只持有公钥 `STEWARD_ORCHESTRATION_VERIFY_KEY`。worker 能验证任务，但不能扩大权限、伪造节点或签发新任务。

### 资源配额

Agent 定义独立限制：最大并发、计划总运行秒数、总尝试次数和证据字节数。计划持久化前校验最坏情况预算；领取时执行并发限制；证据写入按 Agent 聚合预算限制，超限时只保留摘要。停用 Agent 会取消其活动子运行。

## 安全边界

- worker 不是新的高权限主体；A4-A7 仍必须通过独立 Privilege Broker。
- R4.1 的 CPU/内存配额是执行计划和证据逻辑配额，不是 Job Object/cgroup 硬隔离。
- 当前 worker 直接访问 PostgreSQL，因此数据库角色仍是信任边界；部署时应使用只允许消息箱、Agent 和 Runtime 必要表的专用角色。数据库级最小权限模板进入后续生产加固。
- worker 不执行 schema migration；迁移由 Server 或部署流程先完成。
- 跨设备身份、mTLS 和远程派发不属于 R4.1。

## 验收门槛

- 至少两个不同 PID 的 worker 分别绑定不同 Agent，完成并行节点和汇合节点；
- 主服务在 worker 模式下不能领取编排子运行；
- 消息、worker 心跳、租约、ACK 和投递次数均可持久查询；
- 强制终止持有消息的 worker 后，替代 worker 必须在租约到期后接管同一子运行；
- 恢复不得重复已成功步骤，且父编排、证据 manifest 和全局暂停语义保持正确。
