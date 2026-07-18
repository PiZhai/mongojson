# ADR-0017：持久化多通道通知投递面

状态：已接受
日期：2026-07-18

## 背景

网页消息只能在私人管家页面打开时被看见，无法承担提醒、后台任务完成、主动管家询问和异常告警。Windows 服务还运行在 Session 0，不能直接向当前登录用户可靠显示桌面通知。通知因此必须脱离网页生命周期，并由登录会话 Companion 或外部投递服务完成。

## 决策

新增持久化 Notification Orchestrator。模型、任务扫描器和执行引擎只创建统一通知，不直接调用某个具体供应商：

```text
模型 / 任务 / 执行结果
  → steward_notifications
  → steward_notification_deliveries
  → Windows/macOS Session Companion
     ntfy 跨设备推送
     SMTP 邮件
     Linux notify-send（可选桌面会话）
```

- Windows 使用登录用户会话中的 `steward-companion`，优先调用 Windows App SDK helper 写入系统通知中心；helper 不可用时退回登录会话内的 WinRT Toast。
- macOS Companion 使用系统通知；后续可在不改变服务端协议的情况下替换为签名的原生通知应用。
- Linux 桌面会话可使用 `notify-send`，无桌面或服务器环境优先配置 ntfy 与 SMTP。
- ntfy 是默认推荐的跨设备通道：可自托管，也可连接受信任服务；手机和其他电脑通过对应客户端接收。
- SMTP 是无 Companion、无推送客户端或升级失败时的最终兜底通道。
- Web 只显示历史、投递状态、错误及通道配置，不再作为通知投递通道。

通知、每个通道的投递尝试和用户交互都持久化。投递 worker 使用数据库租约领取消息，后端重启后继续；失败按指数退避重试。`dedupe_key` 防止周期扫描和模型重试制造重复通知。用户可确认、稍后提醒、取消或重新发送。

## 路由与升级

- `low`：优先立即投递系统通知；无桌面会话时立即使用 ntfy，只有邮件可用时立即发邮件。
- `normal`：立即投递系统通知；10 分钟后可升级到 ntfy，1 小时后可升级到邮件。
- `high`：立即投递系统通知和 ntfy；30 分钟后可升级到邮件。
- `urgent`：立即投递全部可用通道。

调用者可显式指定通道覆盖默认路由。某个供应商失败不会抹掉通知；错误、尝试次数、下次尝试时间和供应商消息 ID 都保留在 delivery 记录中。

## 模型接口

循环 Agent 始终可按需调用：

- `notify.send`
- `notify.schedule`
- `notify.list`
- `notify.acknowledge`
- `notify.snooze`
- `notify.cancel`
- `notify.endpoint_test`

是否提醒、提醒内容、时间和优先级由模型结合用户上下文判断。Notification Orchestrator 只负责可靠投递、去重、升级和技术错误恢复，不使用固定业务规则替模型决定应不应该打扰用户。

## 配置与密钥

通道可在网页“通知”弹窗配置，也可由环境变量初始化。网页提交的 ntfy token 和 SMTP password 使用现有 `STEWARD_LOCAL_ENCRYPTION_KEY` 加密保存，读取接口不会返回明文。

主要环境变量：

- `STEWARD_NOTIFICATION_INTERVAL`、`STEWARD_NOTIFICATION_LIMIT`
- `STEWARD_NTFY_URL`、`STEWARD_NTFY_TOPIC`、`STEWARD_NTFY_TOKEN`
- `STEWARD_SMTP_HOST`、`STEWARD_SMTP_PORT`、`STEWARD_SMTP_USERNAME`、`STEWARD_SMTP_PASSWORD`
- `STEWARD_NOTIFICATION_EMAIL_FROM`、`STEWARD_NOTIFICATION_EMAIL_TO`
- `STEWARD_WINDOWS_NOTIFIER_PATH`（仅覆盖自动发现路径时需要）

## 后果

关闭浏览器、切换程序或重启后端不会丢失待投递通知。系统通知依赖登录会话 Companion；Companion 离线会形成可见的重试记录，而不会误报成功。跨设备可靠性依赖至少配置一个 ntfy 或 SMTP endpoint。网页仍可查看全部通知与证据，但不需要保持打开。
