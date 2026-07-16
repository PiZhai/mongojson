# R4.2 Saga 补偿运行说明

R4.2 是显式 opt-in 的补偿编排。它不回滚 Runtime 数据库事务，而是在正向执行失败后，通过同一套独立 worker 和 Runtime V2 执行预先声明的反向动作。

## 计划格式

```json
{
  "goal": "发布并在失败时清理",
  "auto_start": true,
  "failure_policy": "compensate",
  "permission_ceiling": "A0",
  "data_level": "D0",
  "nodes": [
    {
      "key": "prepare",
      "agent_id": "researcher",
      "goal": "准备资源",
      "steps": [
        {"key": "do", "tool_name": "runtime.echo", "arguments": {"value": "prepared"}}
      ],
      "compensation_steps": [
        {"key": "undo", "tool_name": "runtime.echo", "arguments": {"value": "unprepared"}}
      ]
    },
    {
      "key": "publish",
      "agent_id": "writer",
      "goal": "发布资源",
      "depends_on": ["prepare"],
      "steps": [
        {"key": "do", "tool_name": "runtime.echo", "arguments": {"value": "published"}}
      ],
      "compensation_steps": [
        {"key": "undo", "tool_name": "runtime.echo", "arguments": {"value": "unpublished"}}
      ]
    }
  ]
}
```

`max_children` 同时覆盖正向节点和可能生成的补偿节点。正向与补偿步骤的最坏运行时间、尝试次数和证据预算一起计入 Agent 配额。

## 状态与执行顺序

发生普通正向失败后：

```text
running -> compensating -> compensated
                      |--> compensation_failed
```

假设 `prepare -> publish` 均成功，后续节点失败，系统生成：

```text
compensate-002 (publish) -> compensate-001 (prepare)
```

API 返回的合成节点包含 `kind: compensation` 和 `compensation_of_id`。补偿节点的消息、worker、Runtime run、事件和证据都可通过原有编排详情查询。

需要审批的补偿会停在既有 `awaiting_approval` 状态。全局暂停/急停期间不派发或领取补偿；恢复后必须使用新的 control generation 委派。

## 安全语义

- 只有 Runtime 普通失败触发自动补偿；安全阻断、deadline 和用户取消不会自动执行新动作。
- `compensated` 只证明声明的补偿步骤及其后置条件成功，不宣称外部系统具有事务级回滚。
- 补偿工具应具备幂等语义或稳定 idempotency key，以承受 worker 崩溃和租约重投递。
- 补偿失败会停止剩余补偿并进入 `compensation_failed`，等待人工检查证据。

## 验收

PostgreSQL 集成测试：

```powershell
$env:TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable'
go test ./internal/httpapi -run 'TestStewardR42' -count=1 -v
```

真实 Server、独立 worker、崩溃恢复和 Saga 联合验收：

```powershell
./scripts/verify-r4-2-compensation.ps1
```

成功输出包含 `saga_status: compensated` 和 `saga_compensation_order: [compensate-002, compensate-001]`。
