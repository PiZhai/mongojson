# 私人智能管家 R4.6：模型优先对话内核

R4.6 将 R4.5 的“执行关键词优先”改为“模型理解优先”。启用 `STEWARD_LLM_*` 后，除确定性控制命令外，每条对话消息首先由模型结合历史、长期记忆、工具、设备、时间和系统位置进行结构化分类与规划。

## 启用

```text
STEWARD_RUNTIME_V2=true
STEWARD_RUNTIME_R2=true
STEWARD_LLM_PROVIDER=openai-compatible
STEWARD_LLM_BASE_URL=https://api.openai.com/v1
STEWARD_LLM_MODEL=<model>
STEWARD_LLM_API_KEY=<secret>
STEWARD_LLM_MAX_DATA_LEVEL=D1
STEWARD_RUNTIME_INCLUDE_KNOWN_FOLDERS=true
```

本地无鉴权模型只允许通过既有 `STEWARD_LLM_ALLOW_NO_API_KEY=true` 连接 loopback 地址。没有配置模型时，服务保留本地规则降级，但不能达到完整自然语言理解效果。

## Web 模型配置

管家对话页提供“模型”入口。一套 OpenAI-compatible 连接同时服务于普通对话和意图理解、真实执行计划生成、信息整理、观察分析以及自主建议。

- `GET /api/steward/model-settings` 返回非敏感配置、Advisor/Planner 生效状态和 API Key 是否已配置，不返回密钥原文。
- `PATCH /api/steward/model-settings` 加密保存并热更新模型连接，不需要重启。未传 `api_key` 时保留当前密钥，传空字符串时移除。
- `POST /api/steward/autonomy/advisor/probe` 使用已保存的连接执行 D0 连通性测试。

API Key 使用 `STEWARD_LOCAL_ENCRYPTION_KEY` 对应的本机 at-rest keyring 加密后写入 `steward_model_settings`。没有本机加密密钥时拒绝从 Web 保存 API Key。无密钥模式只允许 loopback 地址。数据库配置优先于启动环境变量；没有数据库配置时继续使用原有 `STEWARD_LLM_*` 和 `STEWARD_RUNTIME_PLANNER_*` 启动行为。

## 路由契约

模型输出意图为：

- `answer`：普通问答。
- `memory_query`：基于持久化记忆回答。
- `information`：收集、研究或整理信息。
- `task`：创建提醒或持续任务。
- `execution`：生成一个至二十步的结构化执行计划。
- `clarify`：只有无法可靠推断且不同选择显著影响结果时追问。

执行计划只接受当前 ToolSpec 清单中的工具。模型返回后，服务端重新校验工具和参数，并重新计算权限、风险、确认、设备和 Broker 要求。

## 当前新增能力

- `fs.create_directory`：创建目录和缺失父目录，已存在目录视为幂等成功。
- 系统位置：`home`、`desktop`、`downloads`、`documents`、`pictures`、`music`、`videos`，同时支持常用中文别名。
- 明确提醒：直接写入已确认本地任务，支持 RFC3339 到期时间和周期说明。
- 明确记忆：直接保存用户确认记忆；普通推断仍进入候选。
- 执行记忆：成功任务写入带来源引用的 `execution_episode`。

## 验收

```powershell
$env:TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55439/postgres?sslmode=disable'
go test ./internal/httpapi -run 'TestStewardR4(5|6)' -count=1 -v
```

验收覆盖模型先于关键词规则、工具与设备上下文传入、低风险目录创建、真实结果验证、执行记忆回写、长期记忆检索、提醒任务持久化，以及原 R4.5 单 Run/多 Agent 路径兼容。
