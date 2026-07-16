# 私人智能管家 R2：自然语言到用户态真实执行

R2 在 [Runtime V2](personal-ai-steward-runtime-v2.md) 的持久化执行内核上增加自然语言 Planner、服务端策略引擎和受限的真实系统工具。架构约束见 [ADR-0002](adr/0002-steward-r2-user-mode-execution.md)。计划预览、实时证据与全局暂停的工作台交互见 [R2.5 执行控制面](personal-ai-steward-runtime-r2-5.md)。

## 能做什么

默认本地 Planner 支持一条明确指令编译为一个步骤：

- `列出目录 "C:\Work"`
- `读取文件 "C:\Work\note.txt"`
- `创建文件 "C:\Work\note.txt" 内容 "完成 R2"`
- `运行命令 "C:\Go\bin\go.exe" version`
- `获取网页 https://example.com`
- `打开网页 https://example.com`（需额外启用浏览器工具）

同义英文形式也可用。模糊、多动作或未支持的本地句式返回 `422`，不会猜测执行。可以配置 OpenAI-compatible fallback 生成结构化计划，但最终工具、路径、参数、权限、审批和重试预算仍由服务端校验。

## 启用与最小配置

R2 默认关闭。Windows PowerShell 示例：

```powershell
$env:STEWARD_RUNTIME_R2 = 'true'
$env:STEWARD_RUNTIME_V2 = 'true'
$env:STEWARD_RUNTIME_ALLOWED_ROOTS = 'C:\Work,C:\Mine\projects'
$env:STEWARD_RUNTIME_EXECUTABLES = 'C:\Program Files\Go\bin\go.exe,C:\Program Files\Git\cmd\git.exe'
$env:STEWARD_RUNTIME_BROWSER_OPEN_ENABLED = 'false'
```

系统服务安装可使用：

```powershell
go run ./cmd/steward service install `
  --runtime-r2 `
  --runtime-allowed-roots 'C:\Work,C:\Mine\projects' `
  --runtime-executables 'C:\Program Files\Go\bin\go.exe' `
  --dry-run
```

`--runtime-r2` 会把服务配置中的 R1 开关一并设为 true。若允许根目录为空，则只允许 `STORAGE_DIR`。可执行文件为空时 `shell.exec` 保持注册但所有命令 preflight 都会拒绝。`browser.open_url` 默认不注册。

## API

查看 Planner 和工具状态：

```http
GET /api/steward/runtime/planner
GET /api/steward/runtime/tools
```

从自然语言创建 Run：

```http
POST /api/steward/runs/plan
Content-Type: application/json

{
  "instruction": "创建文件 \"C:\\Work\\r2.txt\" 内容 \"verified\"",
  "idempotency_key": "r2-demo-20260716-1",
  "data_level": "D0",
  "auto_start": true
}
```

响应包含原始指令、Planner、计划摘要、策略摘要、步骤策略决定、幂等模式和不可变 `plan_hash`。只读文件操作直接进入 `queued`；文件创建、网络、进程和浏览器动作进入 `awaiting_approval`。批准仍使用 R1 接口：

```http
POST /api/steward/runs/{id}/approve
Content-Type: application/json

{
  "plan_hash": "<响应中的 plan_hash>",
  "granted_by": "local-user",
  "reason": "已检查目标路径和动作"
}
```

调用方应为一次用户请求提供稳定且唯一的 `idempotency_key`。相同键和相同计划返回原 Run；相同键对应不同计划返回 `409`。

## 策略与验证

| 工具 | 关键限制 | 成功验证 |
|---|---|---|
| `fs.list` | 单目录、非递归、条数上限、允许根目录内 | 返回结构化条目和证据 |
| `fs.read_text` | 普通文件、文本、最大 1 MiB、允许根目录内 | 内容 SHA-256 与输出一致 |
| `fs.create_text` | 最大 1 MiB、原子创建、不覆盖 | 重新读取目标并核对 SHA-256 |
| `shell.exec` | 直接白名单 executable、参数数组、受控 cwd、净化环境、输出上限 | 退出码为 0，并核对声明的输出条件 |
| `web.fetch_text` | HTTP(S)、文本、重定向/大小限制、SSRF 防护 | 状态码、内容和 SHA-256 证据 |
| `browser.open_url` | 显式开关、HTTP(S)、SSRF 防护 | 只证明 OS 接受启动分派，不声称页面加载成功 |

计划请求中的 `requires_approval=false`、更高重试次数或较低权限声明不能降低 ToolSpec 策略。权限 ceiling 不足时返回 `403`；路径或可执行文件不在白名单也在建 Run 前拒绝。

私网/本机网页默认禁止。如确需访问本机受信任服务，可精确配置主机名：

```powershell
$env:STEWARD_RUNTIME_WEB_ALLOWED_HOSTS = '127.0.0.1,localhost'
```

该配置是 SSRF 例外，不是通用域名白名单；公网 HTTP(S) 默认仍可在审批后访问。

## 可选远程 Planner

```powershell
$env:STEWARD_RUNTIME_PLANNER_PROVIDER = 'openai-compatible'
$env:STEWARD_RUNTIME_PLANNER_BASE_URL = 'https://api.openai.com/v1'
$env:STEWARD_RUNTIME_PLANNER_MODEL = '<model>'
$env:STEWARD_RUNTIME_PLANNER_API_KEY = '<secret>'
$env:STEWARD_RUNTIME_PLANNER_MAX_DATA_LEVEL = 'D1'
$env:STEWARD_RUNTIME_PLANNER_TIMEOUT = '30s'
```

本地规则始终先尝试，只有未支持的句式才发送到 fallback。超过 `STEWARD_RUNTIME_PLANNER_MAX_DATA_LEVEL` 的指令不会发给远程模型。Planner API key 只用于 Planner HTTP 客户端，不会进入 `shell.exec` 子进程环境。`STEWARD_RUNTIME_PLANNER_ALLOW_NO_API_KEY=true` 只允许 loopback Planner 地址。

## 崩溃恢复

- 读取和网页获取可以重新入队。
- 文件创建使用 keyed reconcile：若目标已存在且内容哈希相同，记录为已协调；不同内容绝不覆盖。
- 命令和浏览器启动是非幂等动作。若 worker 在调用期间中断，Run 进入 `blocked`，旧批准被撤销，并产生 `run.recovery_blocked`。`resume` 后必须再次批准，系统不会自行猜测动作是否已发生。

## R2 明确不做

- 不以管理员/root 身份运行，不弹提权，不持有系统凭据。
- 不提供任意 PowerShell/cmd/sh 字符串解释；只有显式白名单 executable。
- 不覆盖、移动或删除文件。
- 不修改服务、注册表、启动项、系统设置或防火墙。
- 不自动提交/推送代码，不发送邮件/消息，不付款或购买。
- 不读取浏览器当前页面、不点击、不填表；R2 仅可请求默认浏览器打开 URL。
- 不进行跨设备执行、子 Agent 编排和自动生成工具。

这些能力需要 R3 Broker 或 R4 编排层，不应通过扩大 R2 主进程权限实现。

## 验收证据

自动化测试覆盖：

- 中文指令编译和 Windows 引号路径；
- 策略不可被调用方降级；
- 允许根目录与可执行文件拒绝；
- 文件原子创建、相同内容协调和禁止覆盖；
- 私网 SSRF 默认拒绝与精确例外；
- PostgreSQL 持久化审批、实际文件读写、真实白名单进程、HTTP 获取和证据；
- 非幂等 invocation 中断后阻断、撤销批准和禁止自动重放。
