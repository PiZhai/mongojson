# ADR-0007: R4.0 单机多 Agent 编排内核

- 状态：Accepted
- 日期：2026-07-17
- 决策范围：R4.0；单机、多 Agent、受控 DAG 编排

## 背景

R0-R3.3 已把自然语言计划、Runtime V2、审批、证据、全局急停、watchdog 和独立 Privilege Broker 落成真实执行链路。下一阶段需要让多个角色协作，但不能让“多 Agent”演化为多套执行器、多份权限或绕过现有安全控制的自由对话网络。

## 决策

### 编排层不执行工具

R4.0 新增持久化的 `orchestration -> node -> Runtime V2 child run` 层。编排器只负责 DAG、Agent 选址、并发预算、委派和状态归并；文件、Shell、浏览器与高权限 capability 仍只由 Runtime V2/Broker 执行。

### Agent 是策略身份

本阶段的 Agent 是本机数据库中的角色定义，不是 OS 账户或独立信任域。每个 Agent 必须声明：

- 稳定 ID、名称和角色；
- `permission_ceiling` 与 `data_level_ceiling`；
- Runtime 工具 allowlist；
- 最大并发数和启用状态。

节点权限不得超过父编排或 Agent 的较小上限。节点数据级别同样不得超过两者。未知工具、停用 Agent 和越权 DAG 在持久化前拒绝。

### DAG 与预算

节点只允许依赖计划中更早的节点，从结构上排除环。父级限制 `max_children`、`max_parallel`、总 Runtime 步骤数和可选 deadline；Agent 另有独立 `max_concurrency`。R4.0 只实现 `fail_fast`，任一节点失败或阻断会取消未完成兄弟节点。

### 任务绑定委派

每个节点物化为子运行时生成 Ed25519 委派 claim，绑定：

- 编排、节点与 Agent；
- 子运行不可变 plan hash；
- 权限与数据级别；
- 全局急停 generation；
- 短期有效期。

Runtime V2 在领取子运行时再次验证 claim、Agent 启用状态、父子状态、有效期和当前 generation。篡改、过期或旧 generation 会将子运行和父编排一起持久化为 `blocked`。服务端私钥种子由 `STEWARD_ORCHESTRATION_SIGNING_KEY` 提供，不写入数据库；R4.1 worker 只接收派生公钥，不能伪造委派。

### 崩溃一致性

子运行先以 `draft` 和确定性 idempotency key 创建，再在持有父编排 advisory lock 的事务中绑定 claim、节点和队列状态。调度器在绑定前崩溃只会留下不可执行的 draft；重试会复用同一子运行。多个服务实例通过 PostgreSQL advisory transaction lock 避免重复调度同一父编排。

### 控制与证据

全局暂停/急停时编排器不派发节点。恢复后，活动委派必须更新到新的 generation 才能重新领取。取消父编排会传播到所有非终态子运行。父编排只保存子证据的计数、数据级别和确定性 manifest SHA-256，不复制受治理的证据 payload。

## 状态机

父编排：

```text
draft -> queued -> running -> succeeded
                    |  |----> failed
                    |-------> blocked
draft/queued/running -------> cancelled
```

节点：

```text
pending -> dispatched -> running -> succeeded
              |            |-----> failed
              |------------------> blocked
pending/dispatched/running -------> cancelled
```

子运行继续使用 Runtime V2 的既有状态机，父节点状态只是其受控投影。

## 非目标

- 跨设备派发和远程 Agent；
- Agent 间自由共享凭据或持久密钥；
- A8/A9 凭据代理；
- 自动生成并立即启用工具；
- 任意动态扩张 DAG；
- 完整 Saga 补偿。R4.0 只定义 fail-fast 取消，补偿进入 R4.2。

## 验收门槛

- 两个不同 Agent 能并行领取独立节点，并在依赖满足后执行汇合节点；
- 所有节点都对应标准 Runtime V2 子运行和可验证委派；
- 父级能够归并子运行状态、事件和证据 manifest；
- 篡改委派必须 fail-closed；
- 父级取消与全局急停必须阻止后续执行；
- 使用真实 PostgreSQL 和真实 HTTP 服务进程完成验收。
