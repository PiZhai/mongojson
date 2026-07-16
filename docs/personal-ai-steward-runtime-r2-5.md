# 私人智能管家 R2.5：执行控制面

> R2.6 已把本页的 Runtime V2 单域暂停升级为覆盖 Runtime V2 与 S4 的统一紧急停止，并加入证据治理和执行 watchdog。当前语义见 [R2.6 执行安全层](personal-ai-steward-runtime-r2-6.md)。本页保留为 R2.5 历史基线。

R2.5 把 R1/R2 的持久化执行能力交付到私人管家工作台，使真实系统操作具备可见、可审、可停的用户控制路径。它不扩大工具权限；管理员/root 仍属于 R3 Privilege Broker。

## 用户闭环

1. 用户输入自然语言任务并选择数据级别、权限上限。
2. 系统创建 `auto_start=false` 的持久化计划草稿，不立即操作系统。
3. 工作台展示不可变计划哈希、Planner、逐步工具、参数、预期输出、权限策略、幂等性和审批要求。
4. 对需审批计划，用户填写理由后批准当前 `plan_hash`。
5. 用户显式启动计划；工作台通过 SSE 展示队列、执行、验证和终态。
6. 每一步保留 invocation、结构化输出、错误、idempotency key 和 EvidenceArtifact。
7. 用户可取消单个 Run，或通过全局暂停阻止整个 Runtime V2 领取和继续执行任务。

## 控制面 API

```http
GET  /api/steward/runs?status=<status>&limit=40
GET  /api/steward/runs/{id}
GET  /api/steward/runs/{id}/events
POST /api/steward/runs/plan
POST /api/steward/runs/{id}/approve
POST /api/steward/runs/{id}/start
POST /api/steward/runs/{id}/cancel
POST /api/steward/runs/{id}/resume

GET  /api/steward/runtime/control
POST /api/steward/runtime/control/pause
POST /api/steward/runtime/control/resume
```

Run 列表返回轻量摘要，包括总步骤、已完成步骤、审批要求、失败摘要和更新时间。详情接口仍是计划、审批、调用和证据的完整权威视图。

全局控制请求示例：

```json
{
  "reason": "发现异常输出，暂停检查",
  "changed_by": "local-user"
}
```

暂停状态和最近 20 条 pause/resume 操作记录在 PostgreSQL 中，不依赖进程内存；服务重启后仍然生效。

## 全局暂停语义

全局暂停只控制 Runtime V2 执行器：

- 停止 worker 领取新的 `queued` Run。
- 已领取但尚未调用工具的步骤回到持久队列。
- 正在运行的工具通过 context 协作取消。
- 可安全重放的工作回到 `queued`，保持等待，直到全局恢复。
- 已完成并通过 postcondition 的步骤先持久化成功证据，再暂停后续步骤，避免重复执行。
- 结果未知的非幂等操作进入 `blocked`，旧批准被撤销，并记录 `run.pause_blocked`；全局恢复不会自动重放它。

全局恢复只重新开放队列。对未知结果的非幂等 Run，用户必须先执行单 Run `resume`，再对原计划哈希重新审批。

全局暂停不停止任务记录、对话、记忆、同步或 S4 自主候选模块。它不是终止整个 Agent 的开关，也不等同于关闭进程。

## 工作台交互约束

- 危险的全局暂停采用二次确认并要求填写审计原因。
- 状态同时使用中文文本和视觉样式，不依赖颜色表达含义。
- 审批区域明确展示哈希绑定语义；旧批准、撤销批准和其他计划的批准均不视为有效。
- 参数与证据使用渐进展开，默认优先展示目标、状态、策略和下一可用操作。
- 运行列表与详情在窄屏改为上下布局，主要按钮满足至少 44px 触控高度。
- SSE 断开会显示可见错误；终态流正常关闭不会伪装成仍在实时连接。

## 验收证据

- 前端 TypeScript 生产构建通过。
- 前端 Runtime 状态与 plan-hash 审批判断有单元测试。
- PostgreSQL 集成测试证明：暂停状态持久化且有审计事件；暂停时 worker 不领取队列；恢复后队列继续执行。
- PostgreSQL 集成测试证明：执行中全局暂停会协作取消工具；未知结果的非幂等步骤被阻断、旧审批撤销，并产生 `run.pause_blocked`。
- R1/R2 原有的重试、SSE、崩溃恢复、真实文件、白名单进程和 HTTP 工具集成测试继续通过。

## R2.5 明确不做

- 不增加管理员/root 权限或凭据读取能力。
- 不绕过现有 ToolSpec、路径、命令、网络和数据级别策略。
- 不把全局恢复解释为批准所有阻断任务。
- 不提供跳过验证、删除审计记录或修改已创建计划的入口。
- 不替代 R3 的独立 Privilege Broker，也不实现 R4 多 Agent 编排。
