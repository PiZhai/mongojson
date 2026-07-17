# 私人智能管家 R4.8 主动内核

R4.8 让管家在没有新对话输入时也能基于真实活动和长期上下文进行模型归纳，并由模型决定静默、沟通或调用工具。

## 默认运行

- Windows 原生前台活动采样：每 15 秒。
- 活动会话聚合：主动归纳前强制聚合，生命周期任务仍每小时补偿执行。
- 每日归纳：本地时间 20:00 后首次 proactive 周期。
- 每周归纳：星期日 20:00 后首次 proactive 周期。
- 主动周期：每 5 分钟检查一次是否到期。

手动验收：

```powershell
Invoke-RestMethod -Method Post `
  -Uri http://127.0.0.1:18080/api/steward/proactive/run `
  -ContentType 'application/json' `
  -Body '{"force":true,"cadence":"daily"}'

Invoke-RestMethod http://127.0.0.1:18080/api/steward/proactive/runs?limit=20
```

手动 `force` 使用独立的 `-manual-HHmmss` period key，不会占用当晚正式日/周归纳的幂等键。

## 活动来源

`windows-activity` 在 Windows 默认启用，只记录：

- 前台应用进程名。
- 当前窗口标题。
- 采样持续时间。

它不记录键盘输入、剪贴板、截图或音频。浏览器窗口标题可用于粗粒度活动归纳；精确网页域名需要 ActivityWatch 浏览器扩展。ActivityWatch bridge 只允许 loopback 地址，例如：

```json
{
  "enabled": true,
  "settings": {
    "endpoint": "http://127.0.0.1:6100",
    "limit": 200
  }
}
```

如果 Windows 动态端口排除范围占用了 ActivityWatch 默认 `5600`，可把 ActivityWatch server/client 改到未排除端口（本机使用 `6100`），再同步修改 collector endpoint。

## 模型输入

日/周归纳最多读取：

- 当前时间范围内的 D0-D2 活动会话。
- 已识别的习惯和洞察。
- 开放任务与近期/活跃事件。
- D0-D2 长期记忆。
- 本机和受信任设备状态。
- 当前注册的 Runtime ToolSpec。

D3-D6 内容不进入 R4.8 主动上下文。模型归纳使用 `proactive:*` 的 D2 redacted 策略；模型工具决策仍由 A6 模型外发策略和工具真实权限共同约束。

## 主动决策结果

- `silent`：模型认为不值得打扰，不产生对话消息。
- `message`：消息进入名为“主动管家”的普通会话。
- `execution`：模型调用工具，低风险自动执行，高风险产生确认卡。
- `blocked`：数据或权限策略禁止模型归纳。
- `failed`：模型端点、解析、数据库或执行创建失败，错误保存在主动运行记录和审计中。

固定 S4 规则不会再自动创建主动候选；安全层只负责验证、权限、风险、审批、证据和急停。

