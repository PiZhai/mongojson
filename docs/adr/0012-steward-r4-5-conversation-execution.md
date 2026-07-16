# ADR-0012：R4.5 对话执行入口

- 状态：已实施
- 日期：2026-07-17
- 决策范围：自然语言对话、Runtime V2、R4 编排、设备选址与审批交互

## 背景

R2 已能把自然语言编译为 Runtime V2 计划，R4.0-R4.4 已提供多 Agent、远程执行与 Broker 联邦，但普通对话仍只产生任务、意图和记忆候选。用户必须离开对话工作台，手工选择另一套执行入口，底层能力没有形成“一句话交代、在原对话看到结果”的产品闭环。

## 决策

新增持久化的 `steward_conversation_executions` 桥接记录。每个可执行对话请求绑定一个不可变 Runtime Run、R4 Orchestration，或一个需要补充信息的问题。桥接层只负责意图路由、目标选择、交互状态和结果投影，不执行系统工具，也不改变 Runtime、Broker 或远程协议的信任边界。

### 路由

- 明确的执行句式交给 R2 Planner；普通聊天继续进入 Conversation Advisor。
- 单步骤本机计划使用 Runtime Run。
- 多步骤计划或任意远程计划使用 R4 Orchestration；每个步骤按文件、网络、进程、权限或通用角色分配最小权限 Agent。
- 未能安全编译的执行请求返回 `needs_input`，不得猜测命令、路径、设备或 capability。

### 设备选择

- 默认本机。
- 指令明确包含已登记设备 ID/名称时选择该设备。
- “另一台电脑”或“远程设备”选择最近在线、已信任、未撤销且具备签名身份和 API 地址的 peer。
- R4.3 普通远程执行仍限制为 A0-A2/D0-D2；A4-A7 只能通过 R4.4 的 `privilege.execute` 与 Broker-to-Broker 委派。

### 自动执行与确认

- 所有工具均以服务端 ToolSpec 重新计算权限和风险，模型不能自行降低等级。
- `risk=low` 且最高 A3 的明确对话指令可静默授权并进入队列。
- medium/high/critical、进程启动、Broker capability 和凭据绑定操作显示一张确认卡。
- A4-A7 的确认卡必须取得与 subject、plan hash、capability 和 control generation 绑定的独立审批证明；普通“继续”文本不能代替强认证。
- 已签名且通过设备策略校验的 R4.3 dispatch 是目标端 A0-A2 child run 的授权，不在目标设备重复弹出第二次确认。

### 状态与控制

- 卡片实时投影底层 Run/Orchestration 的 `queued/running/succeeded/failed/cancelled/blocked` 状态。
- 最终卡片只显示治理后的证据数量、脱敏数量、数据等级和 manifest digest，不绕过 Evidence API 暴露正文。
- “暂停”使用底层协作取消并把对话桥接状态置为 paused；单 Run 可恢复，多 Agent 任务重新规划后继续。
- “取消”传播到当前 Run 或所有编排子任务。
- “换到另一台电脑”只允许在尚未开始或已暂停时重建计划；运行中必须先暂停，避免同一计划在两台设备并发执行。

## 结果

普通用户只需要对话和一张例外确认卡；执行控制面继续作为高级诊断、审批与审计入口。R4.5 不承诺任意 Shell、任意凭据或无限权限，所有动作仍受工具注册、路径白名单、设备信任、权限 ceiling、全局急停、watchdog 和 Broker policy 约束。
