# 私人智能管家 R4.5：对话式真实执行

R4.5 把普通管家对话接到已有 Runtime V2 和 R4 编排内核。用户在 `/steward` 的对话工作台输入明确指令后，无需再复制到执行控制面。

> R4.6 已将本页的关键词/Planner 优先入口替换为模型优先的统一意图理解；当前运行方式见 [R4.6 模型优先对话内核](personal-ai-steward-runtime-r4-6.md)。本页保留 R4.5 桥接层、确认卡和状态控制契约。

## 交互

```text
创建文件 "C:\Work\done.txt" 内容 "finished"
读取文件 "C:\Work\done.txt"
运行命令 "C:\Program Files\Go\bin\go.exe" version
在 office-pc 上读取文件 "C:\Work\status.txt"
执行高权限能力 tool:restart-service
```

- 低风险计划直接显示“排队中/执行中/已完成”。
- 中高风险计划显示一张确认卡，包含计划摘要、设备、权限和风险。
- 高权限卡点击“确认执行”后调用浏览器 WebAuthn；若没有匹配当前 origin 的 authority，会引导用户先在执行控制面配置。
- 卡片每两秒刷新实时状态，终态显示证据数量、脱敏数量和 manifest 摘要。

上下文指令为 `继续`、`暂停`、`取消` 和 `换到另一台电脑`。它们只作用于当前对话最近一个非终态执行；运行中的任务切换设备前必须先暂停。

## API

发送消息仍使用：

```http
POST /api/steward/conversations/{id}/messages
```

响应消息新增 `executions`。确认卡操作：

```http
POST /api/steward/conversation-executions/{id}/decision
Content-Type: application/json

{
  "decision": "confirm",
  "reason": "在管家对话中确认执行",
  "approval_proof": {}
}
```

`decision` 支持 `confirm`、`pause` 和 `cancel`。A4-A7 必须提供有效独立审批证明。

## 启用条件

- 单 Run：`STEWARD_RUNTIME_V2=true` 与 `STEWARD_RUNTIME_R2=true`。
- 多步骤/远程：同时启用 R4 编排及对应 worker/remote 配置。
- 高权限：同时启用独立 Broker、approval authority 和相应 capability policy。
- 远程设备必须已信任、未撤销、具备签名公钥和 `/api` 地址。

R4.5 没有新增绕过开关；允许根目录、可执行文件、远程工具、设备权限和 Broker capability 沿用 R2-R4.4 的配置。

## 验收

```powershell
$env:TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55439/postgres?sslmode=disable'
go test ./internal/httpapi -run 'TestStewardR45' -count=1 -v
```

专项测试覆盖低风险静默文件创建、证据回流、中风险确认、暂停/继续、模糊任务追问，以及多步骤自动选择多 Agent 编排。
