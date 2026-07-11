# 私人智能管家 S3/S4 运行与验证基线

本文档承接 [多阶段目标文档](personal-ai-steward-multi-stage-goals.md)、[S0 边界与安全基线](personal-ai-steward-s0-boundary-baseline.md)、[S1 单机本地管家原型](personal-ai-steward-s1-local-prototype.md) 和 [S2 个人数据底座](personal-ai-steward-s2-data-foundation.md)，记录当前 S3/S4 的运行入口、同步模型、自主执行控制面和验证方式。

## 当前实现边界

面向实际操作者的 Windows、macOS、Linux 完整发布流程见 [三端打包、安装与验证教程](personal-ai-steward-three-platform-deployment-guide.md)。本文继续作为服务模型、同步协议、安全策略和 evidence 门禁的实现基线。

当前实现已经具备：

- Go 后端 API 服务和可复用运行器，管理面与跨设备 Peer 面使用独立路由和独立监听器。
- `steward` CLI 入口。
- 构建工具链基线为 Go `1.26.5` 或同系列更高补丁版本。Windows 多进程同步压力验证曾在 Go `1.26.1` 的标准库 overlapped I/O 路径触发进程级 access violation；升级到 `1.26.5` 后同一三节点 mesh 连续三轮及完整 readiness 均通过，因此不再接受 1.26.1 作为 S3/S4 发布工具链。
- Windows Service、macOS LaunchAgent/LaunchDaemon、Linux systemd user/system unit 的安装、卸载、启动、停止和状态查询命令；Windows 固定为 system scope，macOS/Linux 默认使用 user scope，显式 `--scope system` 才进入系统级服务路径。
- 服务进程内的后台守护循环：心跳默认开启，可信 peer 同步和自主扫描可通过 interval 显式开启。
- 后台循环故障域状态：heartbeat、sync、autonomy 分别持久化启用/运行状态、间隔、最近尝试/成功、有界错误摘要和连续失败次数。单个 peer 的同步故障只让 sync 循环显示降级，不写入或覆盖 Agent 全局错误，也不让本地管理 API、心跳、自主扫描或其他健康 peer 停止工作；修复后成功一轮会清零该循环失败状态。
- 本地设备身份和设备权限表；权限策略已接入出站读取与入站导入，不是仅展示配置。
- S3 设备身份登记契约：管理 API 只能登记 `peer`，必须提供非本机 agent id，并严格校验 Windows/darwin/Linux/unknown 平台、A0-A9 权限和无凭据、无 query/fragment、以 `/api` 结尾的 Peer 地址；任何请求都不能覆盖本机 `local` 角色、同步开关或权限。同步心跳仍只能刷新已认证 peer 的描述性字段。`verify runtime` 的 `s3.device.policy_contract` 会反向检查数据库中恰好一个本机角色、其他设备均为 peer、吊销设备已关闭同步，以及所有设备权限记录使用已定义 capability/policy/权限等级。
- S3 同步变更契约：本地 outbox 只允许本机来源和已注册实体，远端导入必须携带 UUID change/entity id、`create/update/delete` 操作、正版本、D0-D6 数据级别和已登记可信来源；payload 中显式携带的权限必须为 A0-A9，数据级别必须与 envelope 一致。同一 change id 只能幂等重放完全相同的不可变内容。单批中的畸形 change 会被逐条拒绝、脱敏审计且不落库，批次仍返回成功以允许同步水位越过永久坏数据；签名设备不能通过省略 payload device 绕过设备权限。`sync-status` 和工作台显示全量历史扫描结果，`verify runtime` 的 `s3.sync.change_contract` 会把任何遗留脏记录变成失败门禁。
- 默认关闭的签名局域网候选发现：`STEWARD_DISCOVERY_ENABLED=true` 后通过 UDP multicast 或显式 UDP targets 广播 Ed25519 自签名设备公告；接收端验证协议版本、时间窗、公钥和签名，只在内存中保存最多 256 个带 TTL 的候选。过期时间由签名内的签发时间计算，重复播放旧公告不能延长候选寿命。公告验签只证明“公告未被篡改且发送者持有公告内公钥”，不等于设备已受信任；发现层不会自动登记、信任、同步、配对或修改服务环境。工作台和 `sync-status` 会明确显示候选尚未信任，并展示公钥指纹、过期时间、拒绝公告计数和运行状态。
- 核心 S2 实体、来源引用、标签、实体标签和时间线片段的同步变更队列。
- 同步变更导入和自动应用接口。
- 已登记 peer 设备的 HTTP pull/import/push 同步入口；Peer 路由只暴露增量读取、导入、签名实体探针和配对挑战，不暴露任务、记忆、自主设置、设备吊销等管理 API。
- `STEWARD_SYNC_SECRET` HMAC 请求签名，以及 Ed25519 设备私钥签名，用于保护 peer 同步读取和导入接口。
- `STEWARD_SYNC_ENCRYPTION_KEY` AES-256-GCM payload envelope，用于 peer 同步传输时加密每条变更的 `payload`；`STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS` 支持保留旧 key 做解密，方便人工轮换。
- `STEWARD_LOCAL_ENCRYPTION_KEY` AES-256-GCM local-at-rest envelope，用于把 `steward_sync_changes.payload` 加密后再写入本地数据库；API、应用逻辑和传输层读取时透明解密。
- 配对包共享同步材料加密：`pairing keygen` 生成接收端 X25519 密钥，`pairing export --encrypt-shared-sync-for` 可把显式 include 的同步 secret/key 封装为 NaCl sealed box，导入端用 `--decrypt-shared-sync-key` 解密后只输出 `suggested_env`。
- 配对包来源签名：`pairing export` 在有 `STEWARD_DEVICE_PRIVATE_KEY` 或显式 `--private-key` 时自动用设备 Ed25519 私钥签名设备身份、API 地址和共享同步材料封包；导入端遇到签名会自动验签，也可用 `--require-signature` 拒绝无签名 bundle。
- 配对落地 bootstrap 计划：`pairing bootstrap` 可在不登记 peer、不写服务环境、不重启服务的前提下，把配对包导入载荷、解密后的共享同步环境键、脱敏 `service env plan`、验收建议和下一步命令一次性输出，适合作为人工复核门槛；显式 `--service-scope system` 时，生成的 service env 建议命令会带上系统级服务作用域。
- 同步安全诊断状态：工作台和 `sync-status` 会显示管理监听、Peer 监听、对外 Peer 地址、鉴权是否要求、HMAC 是否配置、设备签名是否就绪、传输加密/本地加密是否启用、旧 key 数量、候选发现状态和配置错误摘要；不会返回任何私钥或共享密钥原文。
- `service install --strict-security` 安装前预检：在写入系统服务前校验管理面仅绑定回环、Peer 面独立端口、对外地址不指向管理端口，以及设备 ID、HMAC secret、Ed25519 公私钥、同步 AES key、本地 AES key、旧 key 格式、启用后的发现参数和 S4 advisor 安全配置。发现开关和时长环境变量使用严格解析，非法值不会静默回退为默认值。
- `service env plan/apply` 服务环境更新入口：可把配对包解密得到的 `suggested_env`、显式 `--set/--remove` 环境变更或 `--rotate-sync-key-id` / `--rotate-local-key-id` 生成的 AES key 轮换写入 Windows Service、macOS LaunchAgent/LaunchDaemon 或 Linux systemd user/system unit；`plan` 只预览，`apply` 必须显式 `--confirm`，输出会脱敏，并额外给出基于目标环境生成的 `verification` 验收命令建议。`--strict-security` 会在预览或写入前校验写入后的完整目标环境；默认不会自动重启服务，显式加 `--restart` 才会请求平台服务重启并读取重启后状态。
- `service env plan --current-env-file` 离线服务环境计划入口：可从显式 JSON 当前环境计算 key 轮换和目标环境，不读取或写入系统服务管理器，适合三端部署前验证密钥轮换、strict-security、脱敏输出和验收命令建议。
- `service plan --current-env-file` 离线三端安装计划入口：可用同一份当前服务环境渲染 Windows Service、macOS LaunchAgent/LaunchDaemon 和 Linux systemd user/system unit 的脱敏安装 artifact，并按目标平台生成 `verification_by_platform` 验收命令建议。
- `--log-dir` / `STEWARD_LOG_DIR` 统一服务进程日志：`steward run` 启动时会把 Go 运行日志追加到 `<service-name>.log`，Windows Service、macOS LaunchAgent/LaunchDaemon 和 Linux systemd user/system unit 都能通过同一目录保留长时间后台运行证据；macOS 仍额外配置 launchd stdout/stderr 文件。
- 工作台托管：正式发布目录默认在二进制同级包含 `ui/index.html`，`steward run` 会自动识别并托管，不需要额外参数；显式 `--ui-dir` / `STEWARD_UI_DIR` 仍可覆盖默认目录。`/api`、`/healthz` 和 `/readyz` 继续走管理 API，`/tools/steward` 等 SPA 路由返回 `index.html`，Peer 协议面不会托管 UI。
- 设备信任挑战验证：本机管理面可通过 `POST /api/steward/devices/{id}/verify` 远程挑战对端 Peer 面的 `POST /api/steward/pairing/challenge`，验证对端持有已登记公钥对应的 Ed25519 私钥。
- 每台 peer 的远端拉取序号、最近同步时间和最近同步错误记录。
- 设备吊销会写入 `device_revoke` 同步变更，其他设备接收后只会禁用该设备，不允许通过同步恢复信任。
- HMAC 验签成功后仍必须通过本机设备登记、信任状态和 `sync_enabled` 检查；被吊销设备即使仍持有旧共享 secret 也不能继续读取、导入或探测 Peer 数据。
- Peer 心跳和导入只能刷新设备名称、平台、回连地址和在线时间，不能修改本机保存的信任状态、同步开关或权限上限，也不能把 peer 自报为本地设备。
- 冲突队列和人工解决接口：旧版本变更以及等版本但业务内容不同的分叉都会进入冲突队列，不会静默覆盖本地实体。
- 脱敏审计摘要同步：仅同步 D0/D1 且 `syncable=true` 的动作摘要，排除输入摘要、前后快照和高等级数据；远端审计强制标记为不可再次同步，防止回声循环。
- 自主规则、候选建议、审批请求、模拟执行和受控执行接口。
- 本地候选意图评分：根据规则命中、来源可追溯性、触发原因、建议动作和摘要完整度生成 `0-1` 分数及可读解释；工作台按分数展示，但分数不能提升权限或绕过审批。
- 可注册的自主动作执行器：规则动作会作为提案快照持久化，模拟和执行通过同一执行器注册表调度；每个执行器声明目标类型、风险和最大权限，未知动作或越权动作默认阻断。
- 默认关闭、可插拔的 S4 大模型建议器：通过 OpenAI-compatible HTTP 接口增强候选建议文本，但不能提升权限、风险、策略或直接执行动作；显式 `advisor-probe` 可用 D0 探针验证配置是否真的可调用，`advisor-privacy-probe` 可验证 D2 数据会在模型提交前被拦截。
- S4 大模型建议器失败隔离：供应商请求、超时或响应解析连续失败会打开短暂熔断；熔断状态、连续失败次数、下次重试时间和脱敏最近错误会出现在 `advisor` 状态和工作台中。隐私等级拦截属于本地策略拒绝，不计入供应商失败。
- S4 模型输出 guardrail：如果模型建议文本包含外发、删除、付款、凭据、系统配置、提交/发布等高风险动作，系统会拒收模型文本并回退到本地规则生成的候选。
- S4 提案状态机：`dismissed` 和 `executed` 为终态；高风险、A4 及以上或 `never` 策略提案不能被批准为自主执行；审批通过只会推进低风险执行审批，不会解锁高风险自动化。
- S4 严格策略契约：设置更新只接受 `suggest_only` / `controlled`，自动权限上限只能是 A0-A3；规则只接受已定义 policy 和 A0-A9；提案只接受已定义风险、权限和 D0-D6 数据级别。未知值直接返回 400 且不会写库，中风险及以上按高风险路径阻断自动执行。审批状态机和执行入口会重新校验持久化提案，因此升级前遗留或数据库外部写入的非法提案即使被标成 `approved` 也只能转为 `blocked`，不能进入执行器；`verify runtime` 的 `s4.autonomy.policy_contract` 同时检查历史设置和规则。
- S4 策略变更屏障：自主扫描和真实执行持有 PostgreSQL advisory shared gate，暂停、模式/自动权限更新和规则更新持有 exclusive gate。暂停或策略收紧请求必须等待在途扫描/执行结束后才返回，因此返回后不会再出现使用旧设置启动的动作。提案仍保留创建时规则快照用于解释，但执行入口会复核当前规则；规则已禁用、改为 `never`、动作不再匹配或权限上限降低时，旧 `auto` 提案不能复用历史授权。工作台显示屏障状态，`verify runtime` 和最终 evidence 通过 `s4.autonomy.policy_gate` 强制确认扫描、执行、设置、规则和当前规则复核五项契约。
- S4 执行失败恢复：自动策略失败后按运行记录执行指数退避，默认最多尝试 3 次，达到上限后把 proposal 置为 `blocked` 并停止后台重试；失败次数、下次自动重试时间和耗尽状态随 proposal 返回。用户只能对确有失败执行记录的 proposal 显式调用 `retry`，每次人工恢复都会单独写入审计。
- 工作台中的同步、自主、设备、冲突、审批和运行记录视图。
- `steward verify runtime` 运行时验收命令，通过本机管理 API 检查 health/ready、Agent、S3 同步安全、设备身份/权限契约、同步变更历史契约、S4 自主规则、严格策略契约和已启用 advisor 的可观测安全配置，并可选写入低风险探针验证同步队列和自主候选生成。启用发现时还会要求发现循环运行、至少成功发送过一次公告、最近没有发送错误、候选计数一致且所有暴露候选均已验签。
- `steward verify service` 服务级验收命令，会同时检查系统服务管理器状态和 S3/S4 runtime 验收结果。
- `steward verify peers` peer 验收命令，可遍历已登记 peer 做设备信任挑战，并可选执行一次 pull/import/push 同步；显式加 `--write-probes` 时会创建一组低风险本地关系探针，包括任务、来源引用、标签、实体标签、事件和时间线片段，同步后由本机服务使用 HMAC/Ed25519 签名调用对端只读实体探针，验证“本机写入的数据及其核心关系已在对端可见”，不再远程访问通用搜索 API。
- `steward verify mesh` 多节点验收命令，可传入多个本地或通过 SSH/VPN 安全隧道映射到回环地址的管理 API，对每个节点复用 runtime 和 peer 验收，形成 Windows/macOS/Linux 三端矩阵结果；不要求把管理 API 暴露到局域网。
- `verify --evidence-dir` 验收证据落盘：runtime、service、peers 和 mesh 验收都可以把完整 JSON 结果保存为 timestamped evidence 文件；失败结果也会先落盘，命令输出会返回 `evidence_path`，便于收集 24 小时常驻、密钥轮换和三端 mesh 验收材料。`verify evidence` 可汇总 evidence 目录并检查 kind、平台、agent、kind+platform、platform+agent、kind+platform+agent、关键 check、check+platform、kind+check+platform、全通过、全局最小 watch 时长和逐平台最小 watch 时长覆盖。
- 本机真实三进程 mesh 验证脚本：`deploy/run-steward-local-mesh.ps1` 会启动三个真实 `steward run` 进程、三个临时 Postgres 数据库、三组独立管理/Peer API 和三组独立 UDP 发现监听；在人工登记前要求每个节点发现另外两个签名候选，再执行双向设备注册、`verify mesh --sync --write-probes` 和一次 S4 低风险 autonomy 模拟/执行。脚本还会停止一个 peer，在其他节点本地创建任务，再重启该 peer、显式同步并检查任务在恢复节点可见，以验证离线期间本地写入和重新上线后的增量追赶；最终生成 `local-mesh` 与嵌套 `mesh` evidence。该脚本只能证明当前宿主机上的多进程、多数据库、后台循环和 CLI 验收链路，不等同于三台物理 Windows/macOS/Linux 设备验收。
- 配对 bootstrap 预检脚本：`deploy/run-steward-pairing-bootstrap-preflight.ps1` 会生成签名且加密共享同步材料的配对包、接收端当前服务环境、`pairing bootstrap --require-signature --strict-security` 输出和 `pairing-bootstrap-preflight` evidence，验证配对材料解密、脱敏、service env plan、scope-aware 验收建议和下一步命令建议，不登记 peer、不写服务、不访问管理 API、不调用模型端点。
- 本机真实进程 S4 advisor 验证脚本：`deploy/run-steward-advisor-e2e.ps1` 会启动一个真实 `steward run` 进程、临时 Postgres 数据库和可控的本地 OpenAI-compatible loopback 模型端点。除运行 `verify runtime --advisor-probe --advisor-privacy-probe` 并检查 D0 到达模型、D2 在本地阻断外，默认 mock 模式还会注入请求超时和 HTTP 失败，验证连续失败打开熔断、熔断期间不再访问上游、本地规则 proposal 仍然生成、fallback 失败审计可查，以及冷却后成功探针会关闭熔断并清零失败状态。该脚本也支持显式切到外部 OpenAI-compatible 端点；外部模式只做 live/privacy probe，不对真实服务注入确定性故障。
- 本机真实进程 watch 验证脚本：`deploy/run-steward-runtime-watch.ps1` 会启动一个真实 `steward run` 进程和临时 Postgres 数据库，运行 `verify runtime --watch-duration ... --watch-interval ... --write-probes`，生成 `runtime-watch` 与嵌套 `runtime` evidence，并证明后台心跳在 watch 窗口内推进。默认窗口较短，适合本机回归；最终 24 小时验收仍要显式放大参数并在目标设备上运行。
- 三端发布预检脚本：`deploy/run-steward-dist-preflight.ps1` 会构建 Windows amd64、macOS amd64/arm64、Linux amd64/arm64 五个自包含目录，要求每个目标都带有工作台，逐文件验证 manifest/SHA-256，并运行当前平台二进制版本 smoke，生成 `dist-preflight` evidence。
- 本机 S3/S4 readiness 汇总脚本：`deploy/run-steward-local-readiness.ps1` 会在一次新 run 目录内串联三端发布预检、Postgres E2E、服务安装预检、服务环境预检、配对 bootstrap 预检、本机 mesh、advisor E2E 和 runtime watch，并用 `verify evidence` 生成统一 manifest 和 `local-readiness` evidence。该脚本用于实机三端前的本机总门禁，不替代真实 Windows/macOS/Linux 服务安装、24 小时常驻和跨设备同步验收。
- 目标主机系统服务安装 E2E 脚本：`deploy/run-steward-service-install-e2e.ps1` 默认只在 `-PlanOnly` 下执行 strict dry-run；真实安装必须显式 `-ConfirmInstall`，并要求稳定发布二进制、稳定工作目录和管理员/root 权限。脚本从受保护 JSON 或当前进程环境读取服务配置，不把密钥放入记录的命令，安装后启动原生服务并执行 strict service verification、advisor 探针和默认 24 小时 watch，生成脱敏 `service-install-e2e` wrapper evidence 与嵌套 `service` evidence。
- 实机最终证据采集脚本：`deploy/run-steward-s3s4-final-host.ps1` 用同一套参数在每台真实设备上调用 `verify service`、`verify mesh` 和本机 `verify evidence`，默认执行 24 小时 watch、S4 D0 live advisor probe、D2 privacy probe、S3 peer relation write probes，并生成 `s3s4-final-host` wrapper evidence。该脚本不安装服务、不改服务环境、不登记 peer，只把最终验收命令、结果和 evidence 写入指定目录；三台设备的输出合并后再用 `verify evidence --preset s3s4-final-system` 作为高权限完成门禁。

当前仍未完成：

- Windows、macOS、Linux 三端真实机器上的系统服务安装和长时间运行验收。
- 真正跨三台物理设备的端到端同步验证。当前 CLI 已能在已登记 peer 上做显式写入探针和远端可见性检查，但尚未在 Windows、macOS、Linux 三台真实设备上跑完整长时间验收。
- 完全自动的端到端密钥握手和多设备服务环境轮换执行。当前已有人工确认配对包、配对包签名、共享同步材料加密交付、配对 bootstrap 脱敏落地计划、共享同步加密 key 的 AES-GCM payload envelope、本地同步 payload 静态加密、显式 AES key 轮换命令、旧 key 解密配置、共享密钥 HMAC、Ed25519 设备请求签名、设备信任挑战验证、设备吊销传播和显式服务环境更新/重启/验收命令；导入端仍不会静默改系统服务环境，真实三端信任链仍需实机验证。
- 复杂关系实体的真实三端合并验收 evidence，例如同名标签、跨设备时间线片段和来源引用顺序到达。当前已自动应用任务、事件、意图、记忆、知识条目、来源引用、标签、实体标签和时间线段，`verify peers --sync --write-probes` 也会验证任务、来源引用、标签、实体标签、事件和时间线片段在 peer 端可见；尚未在三台物理设备上收集完整 evidence。

## 后端模块边界

S3/S4 领域实现按变化原因拆分，新增能力时不得把传输、安全策略和业务编排重新堆入单个文件：

- `s3.go` 只聚合同步总览；`s3_devices.go`、`s3_changes.go`、`s3_sync.go`、`s3_conflict_service.go` 分别负责设备生命周期、outbox 变更仓储、pull/import/push 编排和冲突队列。`s3_change_policy.go` 是本地/远端 change 规范化、不可变 ID、payload 等级和历史状态校验的唯一归属；`s3_contracts.go` 保存协议常量和公开输入输出契约；`s3_security.go` 集中安全状态计算与入站认证；`s3_transport.go` 只负责 Peer HTTP、请求签名、端点校验和同步窗口；`s3_encryption.go`、`s3_device_keys.go` 和 `s3_permissions.go` 分别负责加密、设备密钥与权限判定。
- S3 实体应用通过 `SyncEntityAdapter` 注册表分发，适配器必须同时声明实体类型、设备同步权限类别和默认权限级别，权限校验与应用逻辑不再维护两份实体映射。`s3_entity_content.go`、`s3_entity_relations.go`、`s3_entity_timeline.go`、`s3_entity_system.go` 分别拥有内容实体、标签/来源关系、时间线关系和审计/设备实体；`s3_entities.go` 只保留共享常量、payload 解析和通用完成逻辑。
- `internal/platform/peerdiscovery` 独立负责发现协议签名/验签、UDP 监听与广播、TTL 和有界候选内存目录；`app.Server` 只管理其生命周期和就绪状态，Steward 领域层通过 `PeerDiscoveryCatalog` 只读接口获取快照。发现包不依赖数据库，也不能调用设备登记或权限接口；ready 检查会识别发现循环退出、尚未成功广播或持续发送错误。
- `daemon.go` 只负责编排三个独立定时循环和生命周期，`daemon_status.go` 负责持久化循环级运行状态；同步或自主错误不得再借用 Agent 全局 `last_error` 传递。新增后台循环必须显式配置状态、记录每轮结果，并保持与其他循环并发隔离。
- `s4.go` 只聚合自主总览并编排一轮扫描；`s4_defaults.go`、`s4_settings.go`、`s4_proposals.go`、`s4_proposal_execution.go`、`s4_approvals.go` 和 `s4_runs.go` 分别负责默认规则、设置/规则、提案、执行、审批和运行记录。`s4_contracts.go` 保存公开输入契约，`s4_policy.go` 是审批状态机、风险、权限级别和当前规则复核的唯一判定位置，`s4_policy_gate.go` 只负责跨进程读写屏障；`s4_repository.go` 保存领域读取与扫描，执行器和模型建议器由 `s4_execution*.go`、`s4_advisor*.go` 隔离。
- S4 候选来源通过 `AutonomyProposalDiscoverer` 注册表按稳定顺序运行；同名注册会替换实现但不改变顺序。新增候选来源应注册独立发现器，新增低风险动作应注册独立执行器，新增同步实体应注册带权限声明的适配器；任何新增动作仍必须复用 `s4_policy.go` 和执行器 guardrail，不得在调用点复制或绕过安全判定。
- CLI 顶层 `cmd/steward/main.go` 只负责进程入口、顶层命令路由、共享 HTTP 客户端和环境读取；`commands_service.go`、`commands_devices.go`、`commands_autonomy.go` 分别拥有服务生命周期、设备权限和自主控制命令的参数解析、校验、请求构造及帮助文本。配对继续由 `pairing*.go` 负责。
- 验收入口 `verify.go` 只分发子命令；`verify_runtime.go`、`verify_service.go`、`verify_peers.go`、`verify_mesh.go` 分别拥有对应验收编排，`verify_types.go` 固定 JSON 契约，`verify_support.go` 保存共享只读解析。Evidence manifest 再按 `types`、`coverage`、`checks`、`requirements` 拆分，新增最终门禁时必须选择唯一归属，不得把平台、Agent、服务作用域和 advisor 规则复制到多个验证模块。

## 双监听安全边界

后台进程使用两个互不包含的 HTTP 路由面：

| 监听面 | 默认地址 | 调用者 | 可用能力 |
| --- | --- | --- | --- |
| 本地管理面 | `127.0.0.1:18080` | 本机工作台和 CLI | 数据管理、设备权限、同步控制、自主设置、审计和验证 |
| Peer 协议面 | `:18081` | 已配对设备 | 健康探针、同步增量读取/导入、签名实体存在性探针、配对挑战 |

约束：

- `HTTP_ADDR` 默认仅绑定回环。非回环地址必须显式设置 `STEWARD_ALLOW_REMOTE_MANAGEMENT=true`，系统服务安装器不提供这个放宽入口。
- `STEWARD_PEER_HTTP_ADDR` 为空时不启动 Peer 监听；三端系统服务安装默认使用 `:18081`。
- 管理面和 Peer 面必须使用不同端口，避免路由隔离被部署配置抵消。
- `STEWARD_UI_DIR` 只接入本地管理面；即使配置了前端工作台，Peer 面也只保留同步协议路由。
- `STEWARD_PUBLIC_API_BASE` 必须指向其他设备可达的 Peer 面，例如 `http://192.168.1.10:18081/api`，不能指向管理端口。
- Peer 同步、实体探针继续默认拒绝匿名请求；配对挑战只返回挑战签名，不提供管理能力。
- Peer 请求体统一限制为 `16 MiB`，并使用更短的读取、写入和空闲连接超时；管理面保留适合本地文件上传的独立超时，不与 Peer 暴露面共享限额。
- Docker Compose 中后端管理监听虽然是容器内的 `:8080`，但 `STEWARD_ALLOW_REMOTE_MANAGEMENT=true` 只用于容器网络代理，宿主机发布端口仍强制绑定 `127.0.0.1`。

## CLI 入口

后端提供两个入口：

```powershell
go run ./cmd/server
go run ./cmd/steward run
```

两者都复用同一个 API 运行器。`cmd/server` 保留原有开发入口，`cmd/steward` 是面向管家的 CLI 控制面。

常用命令：

```powershell
go run ./cmd/steward --help
go run ./cmd/steward help service
go run ./cmd/steward help service env
go run ./cmd/steward help verify
go run ./cmd/steward help pairing
go run ./cmd/steward keygen --prefix windows-main
go run ./cmd/steward sync-keygen --key-id home-sync-v1
go run ./cmd/steward version
go run ./cmd/steward --api http://127.0.0.1:18080/api doctor
go run ./cmd/steward --api http://127.0.0.1:18080/api status
go run ./cmd/steward --api http://127.0.0.1:18080/api start
go run ./cmd/steward --api http://127.0.0.1:18080/api stop
go run ./cmd/steward --api http://127.0.0.1:18080/api sync-status
go run ./cmd/steward --api http://127.0.0.1:18080/api sync-device <peer-device-id>
go run ./cmd/steward --api http://127.0.0.1:18080/api devices list
go run ./cmd/steward --api http://127.0.0.1:18080/api devices register --id macbook-main --name "MacBook Main" --platform darwin --api-base-url http://192.168.1.12:18081/api --public-key "<peer public_key>"
go run ./cmd/steward --api http://127.0.0.1:18080/api devices permissions macbook-main
go run ./cmd/steward --api http://127.0.0.1:18080/api devices permission-set macbook-main sync.memory deny A2
go run ./cmd/steward --api http://127.0.0.1:18080/api devices verify <peer-device-id>
go run ./cmd/steward --api http://127.0.0.1:18080/api devices revoke <peer-device-id>
go run ./cmd/steward pairing keygen --label macbook-main
go run ./cmd/steward --api http://127.0.0.1:18080/api pairing export --api-base-url http://192.168.1.10:18081/api
go run ./cmd/steward --api http://127.0.0.1:18080/api pairing export --api-base-url http://192.168.1.10:18081/api --include-sync-encryption-key --encrypt-shared-sync-for "<recipient pairing public_key>"
go run ./cmd/steward --api http://127.0.0.1:18080/api pairing import --file .\peer-pairing.json --require-signature
go run ./cmd/steward --api http://127.0.0.1:18080/api pairing import --file .\peer-pairing.encrypted.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature
go run ./cmd/steward pairing bootstrap --file .\peer-pairing.encrypted.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --current-env-file .\current-service-env.json --strict-security
go run ./cmd/steward --api http://127.0.0.1:18080/api pairing verify <peer-device-id>
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --strict-security --write-probes
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --advisor-probe
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --advisor-privacy-probe
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --expect-agent-id windows-main --expect-agent-version version-smoke --expect-agent-platform windows --expect-advisor-provider openai-compatible --expect-advisor-model "<model-name>" --expect-advisor-max-data-level D1 --expect-sync-key-id home-sync-v2 --expect-local-key-id windows-local-v1 --expect-sync-previous-key-count 1
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --strict-security --watch-duration 24h --watch-interval 5m --evidence-dir .\evidence\s3s4
go run ./cmd/steward --api http://127.0.0.1:18080/api verify service --strict-security --write-probes
go run ./cmd/steward --api http://127.0.0.1:18080/api verify service --advisor-probe
go run ./cmd/steward --api http://127.0.0.1:18080/api verify service --advisor-privacy-probe
go run ./cmd/steward --api http://127.0.0.1:18080/api verify service --expect-agent-id windows-main --expect-agent-version version-smoke --expect-agent-platform windows --expect-advisor-provider openai-compatible --expect-advisor-model "<model-name>" --expect-advisor-max-data-level D1 --expect-sync-key-id home-sync-v2 --expect-local-key-id windows-local-v1 --expect-sync-previous-key-count 1
go run ./cmd/steward --api http://127.0.0.1:18080/api verify service --strict-security --watch-duration 24h --watch-interval 5m --evidence-dir .\evidence\s3s4
go run ./cmd/steward --api http://127.0.0.1:18080/api verify peers --strict --require-peers
go run ./cmd/steward --api http://127.0.0.1:18080/api verify peers --strict --require-peers --sync
go run ./cmd/steward --api http://127.0.0.1:18080/api verify peers --strict --require-peers --sync --write-probes
go run ./cmd/steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --node http://127.0.0.1:38080/api --expect-agent-id windows-main --expect-agent-id macbook-main --expect-agent-id linux-main --expect-agent-platform windows --expect-agent-platform darwin --expect-agent-platform linux --expect-sync-key-id home-sync-v2 --strict-security --strict --require-peers --sync --write-probes
go run ./cmd/steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --node http://127.0.0.1:38080/api --expect-agent-id windows-main --expect-agent-id macbook-main --expect-agent-id linux-main --expect-agent-platform windows --expect-agent-platform darwin --expect-agent-platform linux --strict-security --strict --require-peers --watch-duration 24h --watch-interval 5m --evidence-dir .\evidence\s3s4
go run ./cmd/steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --node http://127.0.0.1:38080/api --expect-advisor-provider openai-compatible --expect-advisor-model "<model-name>" --expect-advisor-max-data-level D1
go run ./cmd/steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --node http://127.0.0.1:38080/api --advisor-probe
go run ./cmd/steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --node http://127.0.0.1:38080/api --advisor-privacy-probe
.\deploy\run-steward-s3s4-final-host.ps1 -EvidenceDir .\evidence\s3s4\windows -LocalAgentID windows-main -LocalSyncKeyID home-sync-v2 -LocalLocalKeyID windows-local-v1 -Node http://127.0.0.1:18080/api,http://127.0.0.1:28080/api,http://127.0.0.1:38080/api -ExpectedAgentIDs windows-main,macbook-main,linux-main -ExpectedSyncKeyIDs home-sync-v2 -ExpectedLocalKeyIDs windows-local-v1,macbook-local-v1,linux-local-v1 -ExpectedAdvisorProvider openai-compatible -ExpectedAdvisorModel "<model-name>" -ExpectedAdvisorMaxDataLevel D1
go run ./cmd/steward verify evidence --dir .\evidence\s3s4 --preset s3s4-final-system --require-agent-id windows-main --require-agent-id macbook-main --require-agent-id linux-main --require-kind-platform-agent service:windows:windows-main --require-kind-platform-agent service:darwin:macbook-main --require-kind-platform-agent service:linux:linux-main --require-kind-platform-service-name service:windows:MongojsonSteward --require-kind-platform-service-name service:darwin:com.mongojson.steward --require-kind-platform-service-name service:linux:mongojson-steward --require-kind-platform-advisor-provider service:windows:openai-compatible --require-kind-platform-advisor-provider service:darwin:openai-compatible --require-kind-platform-advisor-provider service:linux:openai-compatible --require-kind-platform-advisor-model service:windows:<model-name> --require-kind-platform-advisor-model service:darwin:<model-name> --require-kind-platform-advisor-model service:linux:<model-name> --require-kind-platform-advisor-max-data-level service:windows:D1 --require-kind-platform-advisor-max-data-level service:darwin:D1 --require-kind-platform-advisor-max-data-level service:linux:D1 --require-kind-platform-agent mesh:windows:windows-main --require-kind-platform-agent mesh:darwin:macbook-main --require-kind-platform-agent mesh:linux:linux-main --require-kind-platform-agent s3s4-final-host:windows:windows-main --require-kind-platform-agent s3s4-final-host:darwin:macbook-main --require-kind-platform-agent s3s4-final-host:linux:linux-main --require-kind-platform-advisor-provider mesh:windows:openai-compatible --require-kind-platform-advisor-provider mesh:darwin:openai-compatible --require-kind-platform-advisor-provider mesh:linux:openai-compatible --require-kind-platform-advisor-model mesh:windows:<model-name> --require-kind-platform-advisor-model mesh:darwin:<model-name> --require-kind-platform-advisor-model mesh:linux:<model-name> --require-kind-platform-advisor-max-data-level mesh:windows:D1 --require-kind-platform-advisor-max-data-level mesh:darwin:D1 --require-kind-platform-advisor-max-data-level mesh:linux:D1 --output .\evidence\s3s4\manifest.json
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy status
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy pause
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy resume
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy run
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy mode controlled
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy rules
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy rule-policy event-knowledge-summary auto
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy rule-disable event-knowledge-summary
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy dismiss-candidates --limit 100
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy bulk-dismiss --status blocked --limit 50
```

CLI 管理请求默认超时为 `20s`，大于服务端单次 peer 同步的 `12s` HTTP 预算，避免多节点并发同步时由客户端提前误判失败；可通过 `STEWARD_API_TIMEOUT` 显式调整。

系统服务命令：

```powershell
go run ./cmd/steward service install --dry-run
go run ./cmd/steward service install --dry-run --strict-security
go run ./cmd/steward service plan --current-env-file .\current-service-env.json --target windows,darwin,linux --strict-security
go run ./cmd/steward service install --strict-security --start --verify
go run ./cmd/steward service install --strict-security --start --verify --verify-startup-timeout 2m --verify-watch-duration 24h --verify-watch-interval 5m --verify-evidence-dir .\evidence\s3s4
go run ./cmd/steward service install --strict-security --start --verify --verify-advisor-probe
go run ./cmd/steward service env plan --from-pairing .\peer-pairing.encrypted.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --strict-security
go run ./cmd/steward service env apply --from-pairing .\peer-pairing.encrypted.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --strict-security --confirm
go run ./cmd/steward service env apply --from-pairing .\peer-pairing.encrypted.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --strict-security --confirm --restart
go run ./cmd/steward service env apply --from-pairing .\peer-pairing.encrypted.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --strict-security --confirm --restart --verify --verify-startup-timeout 2m --verify-evidence-dir .\evidence\s3s4
go run ./cmd/steward service env apply --from-pairing .\peer-pairing.encrypted.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --strict-security --confirm --restart --verify --verify-advisor-probe
go run ./cmd/steward service env plan --set STEWARD_SYNC_INTERVAL=5m --remove STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS
go run ./cmd/steward service status
go run ./cmd/steward service start
go run ./cmd/steward service stop
go run ./cmd/steward service restart
go run ./cmd/steward service uninstall
```

`service install` 的 JSON 输出包含 `service` 和 `verification`。`service.environment` 是脱敏后的安装目标环境；`verification` 会基于同一份目标环境生成安装后应执行的 `verify runtime`、`verify service` 和长时间心跳观察命令。显式加 `--start` 时，CLI 会在安装成功后请求启动服务并输出 `start` 与启动后的 `status`；显式加 `--verify` 时，CLI 会复用 `verify service` 的内部验收逻辑，使用目标环境里的非敏感期望值做严格校验。自动验收默认先等待最多 `30s` 直到服务和 runtime 可用，可用 `--verify-startup-timeout` 调整。`--verify-watch-duration` 和 `--verify-watch-interval` 会在启动验收通过后进入长时间观察模式。`--verify-evidence-dir` 会把这次 post-install 验收结果写成 evidence JSON，并在输出中返回 `verification_evidence_path`；失败验收也会先落盘。若还需要在安装验收中证明 S4 模型端点可调用，可追加 `--verify-advisor-probe`；长时间模型调用验收可再加 `--verify-advisor-probe-each-sample`。

`service plan --current-env-file` 的 JSON 输出包含 `plans`、兼容字段 `verification` 和按平台划分的 `verification_by_platform`。`plans[].environment` 与 `plans[].artifacts` 都只返回脱敏内容；Windows plan 会描述服务类型、bin path 和注册表环境；macOS 默认渲染 LaunchAgent，显式 `--scope system` 时渲染 LaunchDaemon；Linux 默认渲染 systemd user unit，显式 `--scope system` 时渲染 systemd system unit。该命令不读取服务管理器、不写文件、不启动服务，适合在三端安装前确认同一份目标环境是否能生成可复核的安装计划；真实写入仍必须使用目标设备上的 `service install`。

`service env plan/apply` 的 JSON 输出包含 `service_env` 和 `verification`。`service_env.environment` 是脱敏后的目标环境；`verification` 会按目标 `HTTP_ADDR` 生成 `verify runtime`、`verify service` 和 24 小时 `verify service --watch-duration` 命令，并自动带上可公开比较的期望值，例如 `STEWARD_AGENT_ID`、当前平台、同步 key ID、本地 key ID 和已启用 advisor 的 provider/model/max data level。它不会把任何 secret、private key、AES key 或 API key 写入验收命令。`service env apply --restart --verify` 会在环境写入、服务重启和状态查询后运行同一套服务级验收；`--verify` 必须配合 `--restart`，避免验证仍在使用旧环境的运行中进程。自动验收同样默认等待最多 `30s`，可用 `--verify-startup-timeout` 调整。`--verify-evidence-dir` 会把这次 post-apply 验收结果写成 evidence JSON，并在输出中返回 `verification_evidence_path`。若环境变更启用了或调整了 S4 advisor，可追加 `--verify-advisor-probe` 或 `--verify-advisor-privacy-probe` 验证模型可达性和 D2 本地阻断。

所有 `verify runtime`、`verify service`、`verify peers` 和 `verify mesh` 命令都支持 `--evidence-dir <dir>`；`service install --verify` 和 `service env apply --verify` 支持 `--verify-evidence-dir <dir>`。CLI 会在该目录创建 `steward-verify-<kind>-<timestamp>-pass|fail.json`，内容包含验收类型、脱敏命令行、创建时间和完整 stdout payload；失败验收也会先写 evidence，再返回非零退出。`--evidence-dir` 和 `--verify-evidence-dir` 不会写入 secret、private key、AES key 或 API key 命令参数值。三端实机验收时建议把每台设备的 `--log-dir` 和本机 evidence 目录一起归档，形成“服务日志 + verifier JSON + 命令输出”的证据链。

`verify evidence` 用于汇总 evidence 目录并生成 manifest：

```powershell
.\bin\steward.exe verify evidence `
  --dir .\evidence\s3s4 `
  --preset s3s4-final-system `
  --require-agent-id windows-main `
  --require-agent-id macbook-main `
  --require-agent-id linux-main `
  --require-kind-platform-agent service-install-e2e:windows:windows-main `
  --require-kind-platform-agent service-install-e2e:darwin:macbook-main `
  --require-kind-platform-agent service-install-e2e:linux:linux-main `
  --require-kind-platform-agent service:windows:windows-main `
  --require-kind-platform-agent service:darwin:macbook-main `
  --require-kind-platform-agent service:linux:linux-main `
  --require-kind-platform-service-name service:windows:MongojsonSteward `
  --require-kind-platform-service-name service:darwin:com.mongojson.steward `
  --require-kind-platform-service-name service:linux:mongojson-steward `
  --require-kind-platform-advisor-provider service:windows:openai-compatible `
  --require-kind-platform-advisor-provider service:darwin:openai-compatible `
  --require-kind-platform-advisor-provider service:linux:openai-compatible `
  --require-kind-platform-advisor-model service:windows:<model-name> `
  --require-kind-platform-advisor-model service:darwin:<model-name> `
  --require-kind-platform-advisor-model service:linux:<model-name> `
  --require-kind-platform-advisor-max-data-level service:windows:D1 `
  --require-kind-platform-advisor-max-data-level service:darwin:D1 `
  --require-kind-platform-advisor-max-data-level service:linux:D1 `
  --require-kind-platform-agent mesh:windows:windows-main `
  --require-kind-platform-agent mesh:darwin:macbook-main `
  --require-kind-platform-agent mesh:linux:linux-main `
  --require-kind-platform-advisor-provider mesh:windows:openai-compatible `
  --require-kind-platform-advisor-provider mesh:darwin:openai-compatible `
  --require-kind-platform-advisor-provider mesh:linux:openai-compatible `
  --require-kind-platform-advisor-model mesh:windows:<model-name> `
  --require-kind-platform-advisor-model mesh:darwin:<model-name> `
  --require-kind-platform-advisor-model mesh:linux:<model-name> `
  --require-kind-platform-advisor-max-data-level mesh:windows:D1 `
  --require-kind-platform-advisor-max-data-level mesh:darwin:D1 `
  --require-kind-platform-advisor-max-data-level mesh:linux:D1 `
  --output .\evidence\s3s4\manifest.json
```

该命令会读取 `steward-verify-*.json`，汇总通过/失败数量、已覆盖 kind、已覆盖平台、已覆盖 agent、已覆盖 service scope、已覆盖 service name、已覆盖 advisor provider/model/max data level、已覆盖 kind+platform、已覆盖 platform+agent、已覆盖 kind+platform+agent、已覆盖 platform+service scope、已覆盖 kind+platform+service scope、已覆盖 platform+service name、已覆盖 kind+platform+service name、已覆盖 platform+advisor provider/model/max data level、已覆盖 kind+platform+advisor provider/model/max data level、已覆盖 check、已覆盖 check+platform、已覆盖 kind+check+platform、最大 watch 样本数、最大 watch 时间跨度和每个平台的最大 watch 时间跨度。`--preset s3s4-final-system` 是高权限真实三端完成门槛，会自动要求：

- 所有 evidence 通过。
- `service-install-e2e`、`service`、`mesh` 和 `s3s4-final-host` 四类 evidence 都存在。
- Windows、macOS、Linux 三个平台都存在 `service-install-e2e`、`service`、`mesh` 与 `s3s4-final-host` evidence。
- 每个平台至少覆盖 `24h` watch span，并启用逐平台 watch 检查。
- Windows、macOS、Linux 三个平台的 `service-install-e2e` 与 `service` evidence 都来自 `system` 服务作用域。
- `service-install-e2e` evidence 里每个平台都有 `service_install_e2e.binary`、`service_install_e2e.redaction`、`service_install_e2e.command` 和 `service_install_e2e.install`；`-PlanOnly` 只生成 `service_install_e2e.plan`，不能满足最终预设。
- `service` evidence 里每个平台都有 `service.status`、`service.runtime`、`service.watch`、`service.watch.heartbeat`、`daemon.loops.status`、`s3.sync.security.strict`、`s3.sync.security.expected_sync_key`、`s3.sync.security.expected_local_key`、`s4.autonomy.status`、`s4.autonomy.retry_policy`、`s4.advisor.probe` 和 `s4.advisor.privacy_probe`。
- `mesh` evidence 里每个平台都有 `mesh.watch`、`mesh.watch.heartbeat`、`daemon.loops.status`、`s3.peers.present`、`s3.peers.status`、`s3.sync.security.strict`、`s3.sync.security.expected_sync_key`、`s3.sync.security.expected_local_key`、`s3.peer_probe.task/source_ref/data_tag/entity_tag/event/timeline_segment/relations`、`s4.autonomy.status`、`s4.autonomy.retry_policy`、`s4.advisor.probe` 和 `s4.advisor.privacy_probe`。
- `s3s4-final-host` wrapper evidence 里每个平台都有 `agent_id`、`s3s4_final_host.binary`、`s3s4_final_host.service`、`s3s4_final_host.mesh` 和 `s3s4_final_host.local_manifest`，证明每台真实设备都跑过最终采集脚本并通过本机 manifest 门禁；`-PlanOnly` 只会生成 `s3s4_final_host.plan`，不能满足最终预设。

`--require-agent-id` 用于确认目标设备 ID 出现在 evidence 中，`--require-kind-platform-agent KIND:PLATFORM:AGENT_ID` 用于确认指定类型的 evidence 来自指定平台上的指定设备，例如 `service-install-e2e:darwin:macbook-main`、`mesh:darwin:macbook-main` 或 `s3s4-final-host:darwin:macbook-main`。`--require-kind-platform-service-scope KIND:PLATFORM:SCOPE` 用于确认指定类型的 evidence 来自目标服务作用域，例如 `service-install-e2e:linux:system` 或 `service:linux:system`；`s3s4-final-system` 已内置两类三端 system scope 要求。`--require-kind-platform-service-name KIND:PLATFORM:NAME` 用于确认指定类型的 evidence 检查的是预期平台服务名，例如 `service:windows:MongojsonSteward`。服务名是部署命名，不由 `s3s4-final-system` 预设自动决定；最终归档建议显式传入 Windows、macOS、Linux 三端的目标服务名。`--require-kind-platform-advisor-provider/model/max-data-level` 用于确认通过的 S4 advisor 检查来自目标 provider、模型和数据级别上限，而不是任意可用模型。`s3s4-final-system` 会要求 advisor probe 和 privacy probe 存在，但不会替你决定模型名称；最终归档应显式传入目标模型。 如果某台 macOS/Linux 设备刻意采用用户级服务，应改用 `--preset s3s4-final` 并显式传入对应 `service-install-e2e:<platform>:user` 与 `service:<platform>:user` 要求，最终 manifest 会明确记录这个选择。`--latest-per-kind` 只纳入每种 evidence kind 的最新文件，适合对持续追加的本机回归目录做当前状态门禁；最终归档审计建议去掉它，让历史失败也参与判断。生成的 `manifest.json` 是最终人工复核入口；它不能替代原始 evidence 和服务日志，但能避免只凭文件数量误判 S3/S4 已完成。

## 实机系统服务安装 E2E

`deploy/run-steward-service-install-e2e.ps1` 负责单台目标设备的原生服务安装、启动和安装后验收。它与 `run-steward-s3s4-final-host.ps1` 分工明确：前者改变本机 service manager 状态，后者只验证已经安装的服务和三端 mesh。

服务环境文件必须是“键和值均为字符串”的扁平 JSON 对象，例如：

```json
{
  "HTTP_ADDR": "127.0.0.1:18080",
  "STEWARD_PEER_HTTP_ADDR": ":18081",
  "DATABASE_URL": "<postgres-url>",
  "STORAGE_DIR": "<stable-data-dir>",
  "STEWARD_AGENT_ID": "windows-main",
  "STEWARD_PUBLIC_API_BASE": "http://192.168.1.10:18081/api",
  "STEWARD_SYNC_SECRET": "<24-plus-character-secret>",
  "STEWARD_DEVICE_PRIVATE_KEY": "<ed25519-private-key>",
  "STEWARD_DEVICE_PUBLIC_KEY": "<ed25519-public-key>",
  "STEWARD_SYNC_ENCRYPTION_KEY": "<aes-256-gcm-key>",
  "STEWARD_SYNC_ENCRYPTION_KEY_ID": "home-sync-v2",
  "STEWARD_LOCAL_ENCRYPTION_KEY": "<local-aes-256-gcm-key>",
  "STEWARD_LOCAL_ENCRYPTION_KEY_ID": "windows-local-v1",
  "STEWARD_HEARTBEAT_INTERVAL": "1m",
  "STEWARD_SYNC_INTERVAL": "5m",
  "STEWARD_AUTONOMY_INTERVAL": "15m",
  "STEWARD_LOG_DIR": "<stable-log-dir>",
  "STEWARD_LLM_PROVIDER": "openai-compatible",
  "STEWARD_LLM_BASE_URL": "<model-base-url>",
  "STEWARD_LLM_MODEL": "<model-name>",
  "STEWARD_LLM_API_KEY": "<model-api-key>",
  "STEWARD_LLM_MAX_DATA_LEVEL": "D1"
}
```

Windows 上应移除 `Everyone`、`BUILTIN\\Users` 和 `Authenticated Users` 的读取权限；macOS/Linux 使用 owner-only `0600`。真实安装不会接受 `-AllowInsecureConfigFile`，该开关只允许在 `-PlanOnly` 下排查配置。也可以不落配置文件，先在受控终端中设置同名进程环境变量，再显式传入 `-UseProcessEnvironment`。

先执行不触碰服务管理器的计划：

```powershell
.\deploy\run-steward-service-install-e2e.ps1 `
  -PlanOnly `
  -ConfigFile C:\secure\steward-service-env.json `
  -ServiceScope system `
  -EvidenceDir .\evidence\s3s4\install-plan
```

确认发布目录、监听地址、设备 ID、key ID、数据库和模型配置后，在管理员/root 终端执行真实安装。`-BinaryPath` 和 `-WorkDir` 在真实安装时必须是不会随临时 evidence 清理而消失的稳定路径：

```powershell
.\deploy\run-steward-service-install-e2e.ps1 `
  -ConfirmInstall `
  -BinaryPath "C:\Program Files\MongojsonSteward\steward.exe" `
  -WorkDir "C:\ProgramData\MongojsonSteward" `
  -ConfigFile "C:\ProgramData\MongojsonSteward\service-env.json" `
  -ServiceName MongojsonSteward `
  -ServiceScope system `
  -WatchDuration 24h `
  -WatchInterval 5m `
  -EvidenceDir "C:\ProgramData\MongojsonSteward\evidence"
```

macOS/Linux 使用 PowerShell 7 (`pwsh`) 运行同一脚本，替换二进制和工作目录路径，并在 system scope 下以 root 执行。默认会调用 `service install --strict-security --start --verify`，同时启用 D0 advisor live probe、D2 privacy probe 和 24 小时 service watch；只有非最终调试才应使用 `-SkipAdvisorProbe` 或 `-SkipAdvisorPrivacyProbe`。`-AdvisorProbeEachSample` 会在每个 watch 样本再次调用模型，可能产生真实调用成本。

安装或安装后验证失败时，脚本会保留已经创建的服务和日志供排查，不会静默卸载。人工确认需要清理时运行：

```powershell
steward service stop --name <service-name> --scope system
steward service uninstall --name <service-name> --scope system
```

`service-install-e2e` evidence 只记录配置来源、配置键名、非敏感设备/key ID、脱敏命令结果和检查状态；若下层 CLI 意外输出敏感值，wrapper 会先替换为 `<redacted>` 并把 redaction check 标记为失败。该 evidence 证明“该目标主机执行过真实安装入口”，但最终完成仍要求继续采集下面的三端 service/mesh/final-host evidence。

## 实机最终 Evidence

每台真实设备安装并启动系统服务后，在该设备上运行：

```powershell
.\deploy\run-steward-s3s4-final-host.ps1 `
  -EvidenceDir .\evidence\s3s4\windows `
  -ServiceName MongojsonSteward `
  -ServiceScope system `
  -APIBase http://127.0.0.1:18080/api `
  -LocalAgentID windows-main `
  -LocalPlatform windows `
  -LocalSyncKeyID home-sync-v2 `
  -LocalLocalKeyID windows-local-v1 `
  -Node http://127.0.0.1:18080/api,http://127.0.0.1:28080/api,http://127.0.0.1:38080/api `
  -ExpectedAgentIDs windows-main,macbook-main,linux-main `
  -ExpectedPlatforms windows,darwin,linux `
  -ExpectedSyncKeyIDs home-sync-v2 `
  -ExpectedLocalKeyIDs windows-local-v1,macbook-local-v1,linux-local-v1 `
  -ExpectedAdvisorProvider openai-compatible `
  -ExpectedAdvisorModel "<model-name>" `
  -ExpectedAdvisorMaxDataLevel D1
```

macOS 和 Linux 设备只替换 `-EvidenceDir`、`-LocalAgentID`、`-LocalPlatform`、`-LocalLocalKeyID`、本机 `-APIBase`，以及任何自定义的 `-ServiceName`。未显式传入 `-ServiceName` 时，脚本会按平台使用默认服务名：Windows `MongojsonSteward`、macOS `com.mongojson.steward`、Linux `mongojson-steward`。高权限最终门槛使用 `s3s4-final-system`，因此 final-host service 验收默认要求 `-ServiceScope system`；macOS/Linux 如果刻意采集用户级服务 evidence，必须显式传入 `-AllowUserServiceScope`，并在最终归档时改用 `s3s4-final` 加对应 `service:<platform>:user` 要求，不能声称满足高权限 system 门槛。最终 service 验收必须显式传入当前设备的 `-LocalAgentID`、`-LocalSyncKeyID` 和 `-LocalLocalKeyID`；最终 mesh 验收必须传入三个非空 `-ExpectedAgentIDs`，并让 `-ExpectedPlatforms` 精确覆盖 `windows,darwin,linux`，否则脚本会在创建 evidence 目录前失败。如果三端共享同一个同步加密 key id，`-LocalSyncKeyID` 可以保持一致，`-ExpectedSyncKeyIDs` 可以只传一个共享 key id；`-ExpectedLocalKeyIDs` 可以传一个所有节点相同的 local key id，或按 `-ExpectedPlatforms` 顺序传三个设备各自的 local key id。`-Node` 应该是三台设备的管理 API 通过 SSH local forwarding、WireGuard/Tailscale 本机代理或等价安全隧道映射到当前设备后的地址；不要为了验收把管理面直接绑定到局域网。脚本默认使用 `-WatchDuration 24h -WatchInterval 5m`，默认会运行：

- `verify service --strict-security --watch-duration 24h --advisor-probe --advisor-privacy-probe`，生成本机 `service` evidence。
- `verify mesh --strict-security --strict --require-peers --sync --write-probes --watch-duration 24h --advisor-probe --advisor-privacy-probe`，生成三端 `mesh` evidence。
- 本机 `verify evidence`，检查当前设备 run 目录内的 service/mesh evidence 是否满足本机最终采集要求，并要求本机 `service` evidence 带有当前 `-ServiceScope` 对应的 `--require-kind-platform-service-scope service:<platform>:<scope>`、当前 `-ServiceName` 对应的 `--require-kind-platform-service-name service:<platform>:<name>`，以及 sync/local key id 期望值检查。
- `s3s4-final-host` wrapper evidence，记录命令、结果、运行目录和失败摘要，便于人工审计。

该脚本不安装服务、不改服务环境、不登记 peer、不写业务数据之外的验证探针。`--write-probes` 只创建 S3/S4 低风险验证实体，用于证明任务、来源引用、标签、实体标签、事件和时间线片段关系可以同步并被 peer 签名探针确认。默认启用 advisor live probe 与 privacy probe，因此必须显式传入 `-ExpectedAdvisorProvider`、`-ExpectedAdvisorModel` 和 `-ExpectedAdvisorMaxDataLevel`，让最终 evidence 证明目标模型身份和数据级别上限，而不是任意可用模型；非最终 smoke 可同时传入 `-SkipAdvisorProbe -SkipAdvisorPrivacyProbe` 跳过这项要求。`-AllowIncompleteMesh` 只适合未建立完整三端隧道的调试场景，会放宽三平台和三 agent id 的早期校验，不能用于最终完成验收。`-AllowUserServiceScope` 只适合刻意验证用户级服务的非 system 路线；不传该开关时，脚本会在 evidence 目录创建前拒绝 `-ServiceScope user`。`-PlanOnly` 输出的 `commands.local_manifest` 会展示本机 manifest 的完整门禁参数，包括 service scope、service name、agent identity 和 advisor provider/model/max data level，适合部署前复核。

三台设备各自完成后，把三个 `run-steward-s3s4-final-host.ps1` 运行目录传到同一台协调机。复制 `deploy/steward-s3s4-final-system.example.json` 为本机 inventory，填写三个本地 evidence 路径、真实 agent id 和目标 advisor 身份；inventory 只保存标识和路径，不能写 API key、同步密钥或本地加密密钥。

先检查将执行的命令，再运行最终归档：

```powershell
.\deploy\run-steward-s3s4-final-system.ps1 `
  -InventoryFile .\deploy\steward-s3s4-final-system.json `
  -PlanOnly

.\deploy\run-steward-s3s4-final-system.ps1 `
  -InventoryFile .\deploy\steward-s3s4-final-system.json `
  -BinaryPath .\bin\steward.exe `
  -EvidenceDir .\evidence\s3s4-final-system
```

协调脚本不会连接远程设备、安装服务或读取服务配置。它会先对每个来源目录独立要求对应平台的 `service-install-e2e`、`service`、`mesh`、`s3s4-final-host`、agent id、system scope、服务名、advisor provider/model/max data level 和至少 24 小时 watch；只有三个来源都通过，才复制文件名为 `steward-verify-*.json` 的 evidence。复制后的每个文件都会记录相对路径、字节数和 SHA-256，然后由现有 `verify evidence --preset s3s4-final-system` 做统一门禁。输出目录包含：

- `hosts/<platform>/`：按平台隔离的 evidence 副本。
- `reports/preflight-<platform>.json`：每个来源包的独立 manifest。
- `final-manifest.json`：三端统一最终 manifest。
- `steward-verify-s3s4-final-system-*-pass|fail.json`：inventory、命令、文件哈希和最终结果的 wrapper evidence。

仍可直接调用 `steward verify evidence --preset s3s4-final-system` 做底层排障，但正式归档应使用协调脚本，避免手工参数遗漏、错误混入其他主机目录或丢失来源文件哈希。

`-PlanOnly` 可用于三端部署前打印将要执行的命令并生成 `s3s4-final-host` plan evidence；它不会访问 API，也不会构建二进制。`-AllowIncompleteMesh` 只适合本机 smoke 或隧道未完全建立时调试，不能用于最终完成验收。`-AllowUserServiceScope` 只能用于明确不走 `s3s4-final-system` 的用户级服务验收。

### 三端二进制构建

S3/S4 的后台服务安装应使用可重复构建的固定发布目录。仓库提供 `deploy/build-steward.ps1` 构建 `cmd/steward` 和前端工作台，不会安装服务、不会写入系统环境、不会读取或打印密钥：

```powershell
.\deploy\build-steward.ps1
```

默认产物：

```text
backend/dist/steward/
  steward-<version>-windows-amd64/
    steward.exe
    ui/index.html
    ui/assets/...
  steward-<version>-darwin-amd64/
    steward
    ui/...
  steward-<version>-darwin-arm64/
    steward
    ui/...
  steward-<version>-linux-amd64/
    steward
    ui/...
  steward-<version>-linux-arm64/
    steward
    ui/...
  SHA256SUMS.txt
  manifest.json
```

默认目标覆盖 Windows amd64、macOS Intel/Apple Silicon、Linux amd64/arm64。脚本会先执行前端生产构建和 `go test ./cmd/steward`，再用 `CGO_ENABLED=0`、`-trimpath` 和精简 ldflags 构建各平台二进制，把同一份工作台复制到每个目标目录，并为二进制和全部 UI 文件生成 SHA-256 清单。每个目标目录都是可独立复制的运行单元。构建脚本会把 `Version`、git commit 和 UTC 构建时间注入二进制，`manifest.json` 使用同一组值。需要只构建某个目标时：

```powershell
.\deploy\build-steward.ps1 -Targets windows/amd64 -Version local-test
.\deploy\build-steward.ps1 -Targets linux/amd64,linux/arm64 -SkipTests
.\deploy\build-steward.ps1 -SkipFrontendBuild
.\deploy\build-steward.ps1 -SkipUI
```

`-SkipFrontendBuild` 只复用已存在且包含 `index.html` 的 `frontend/dist`；`-SkipUI` 才会生成仅含 CLI/服务二进制的兼容产物。默认发布不得使用 `-SkipUI`，因为那会移除本地沟通与数据查看界面。

构建完成后用只读校验脚本确认发行物完整性：

```powershell
.\deploy\verify-steward-dist.ps1 -ExpectedVersion local-test -RunCurrentBinary
```

`verify-steward-dist.ps1` 会读取 `manifest.json` 和 `SHA256SUMS.txt`，确认必需 target 覆盖 Windows amd64、macOS amd64/arm64、Linux amd64/arm64，逐个重新计算二进制和 UI 文件的 SHA-256，并要求每个声明了 `ui_dir` 的目标都包含已登记的 `ui/index.html`。它会拒绝 manifest 与 checksum 不一致、缺失或被篡改文件、重复 target/path、额外 checksum 项或 artifact path 越界。`-RunCurrentBinary` 只运行当前平台的 `steward version`，校验二进制上报的 name、version、commit 和 GOOS/GOARCH 与 manifest 一致；它不会安装服务、不会读取服务环境、不会访问管理 API，也不会接触密钥。

`backend/dist/` 和 `backend/bin/` 是生成产物目录，已加入 `.gitignore`。正式安装时应把对应平台的整个目标目录复制到该设备的稳定路径，macOS/Linux 确认 `steward` 具有可执行权限，再执行 `service install --binary <path>`。如果二进制同级存在 `ui/index.html`，`steward run` 会自动托管工作台；`service install` 也会把检测到的 UI 绝对路径写入服务环境。

## 安装前严格预检 Evidence

正式写入 Windows Service、macOS LaunchAgent/LaunchDaemon 或 Linux systemd user/system unit 前，可以先在目标设备执行非侵入式预检：

```powershell
.\deploy\run-steward-service-preflight.ps1
```

该脚本会在 `backend/dist/steward-service-preflight/bin/` 构建或使用当前平台 `steward` 二进制，生成一次性设备 Ed25519 密钥、同步 AES key、本地 AES key、旧 key 和 HMAC secret，然后执行：

```powershell
steward service install --dry-run --strict-security ...
```

默认还会加入一个只指向 `http://127.0.0.1:11434/v1` 的 OpenAI-compatible advisor 配置，使用 `--llm-allow-no-api-key=true` 验证“无 API key 只允许 loopback 模型网关”的 S4 strict-security 路径；如果目标设备不需要验证 advisor 配置，可追加 `-SkipAdvisorConfig`。脚本只做 dry-run，不会写系统服务、不启动服务、不访问管理 API、不调用模型端点。

脚本输出：

- Evidence：`backend/dist/steward-service-preflight/steward-verify-service-preflight-*-pass|fail.json`。
- 临时二进制：`backend/dist/steward-service-preflight/bin/steward-preflight-<platform>-<arch>`。

可用现有 evidence 汇总器验证预检证据：

```powershell
cd backend
go run ./cmd/steward verify evidence --dir .\dist\steward-service-preflight --require-passing --require-kind service-preflight --require-platform windows --require-check service_preflight.strict_dry_run --require-check service_preflight.redaction --require-check service_preflight.verification_advice --require-check service_preflight.advisor_config
```

这类 evidence 能证明当前平台 CLI、strict-security 配置校验、服务 dry-run 输出脱敏和安装后验收建议生成链路有效；它不能替代真实系统服务安装、重启、24 小时常驻和三端网络同步验收。

## 配对 Bootstrap 预检 Evidence

配对包交给另一台设备后，可以先生成一份不改变系统状态的落地计划 evidence：

```powershell
.\deploy\run-steward-pairing-bootstrap-preflight.ps1
```

该脚本会构建或使用当前平台 `steward` 二进制，并在临时目录中生成：

- 发送端 Ed25519 设备密钥。
- 接收端 Ed25519 设备密钥。
- 接收端 X25519 pairing key。
- 共享 `STEWARD_SYNC_SECRET`、当前同步 AES key 和 previous sync key。
- 接收端当前服务环境 JSON。
- 一个已签名、且用接收端 pairing public key 加密共享同步材料的配对包。

随后脚本运行：

```powershell
steward pairing bootstrap `
  --file <encrypted-signed-pairing.json> `
  --service-scope system `
  --decrypt-shared-sync-key <recipient pairing private_key> `
  --require-signature `
  --current-env-file <recipient-current-env.json> `
  --strict-security
```

脚本验证：

- 配对包有 Ed25519 来源签名，且共享同步材料只以 sealed box 形式出现。
- `pairing bootstrap` 能解密配对包并校验签名。
- `suggested_env_redacted` 包含 `STEWARD_SYNC_SECRET`、`STEWARD_SYNC_ENCRYPTION_KEY`、`STEWARD_SYNC_ENCRYPTION_KEY_ID` 和 `STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS`，其中 secret、当前 key 和 previous keys 都必须脱敏。
- `service_env_plan` 使用接收端显式当前环境生成，且目标环境中的设备私钥、同步 key 和 previous keys 不泄露。
- `verification.runtime_args` 带有接收端 agent、当前 sync key ID 和 local key ID 断言。
- 下一步命令建议包含 `pairing import`、`service env plan --scope <scope>` 和 `service env apply --scope <scope> --confirm --restart --verify`，加密配对包的私钥参数只能显示为 `<recipient pairing private_key>` 占位。
- 输出不包含发送端设备私钥、接收端设备私钥、接收端 pairing private key、同步 secret、当前同步 AES key、previous sync keys 或本地 AES key。
- 输出明确声明 bootstrap 不登记 peer、不写服务环境、不重启服务、不访问管理 API、不调用模型端点。

默认输出：

- Evidence：`backend/dist/steward-pairing-bootstrap-preflight/steward-verify-pairing-bootstrap-preflight-*-pass|fail.json`，kind 为 `pairing-bootstrap-preflight`。
- 临时二进制：`backend/dist/steward-pairing-bootstrap-preflight/bin/steward-pairing-bootstrap-preflight-<platform>-<arch>`。

可用 evidence 汇总器检查配对 bootstrap 预检证据：

```powershell
cd backend
go run ./cmd/steward verify evidence `
  --dir .\dist\steward-pairing-bootstrap-preflight `
  --latest-per-kind `
  --require-passing `
  --require-kind pairing-bootstrap-preflight `
  --require-platform windows `
  --require-check pairing_bootstrap_preflight.bundle `
  --require-check pairing_bootstrap_preflight.bootstrap `
  --require-check pairing_bootstrap_preflight.suggested_env `
  --require-check pairing_bootstrap_preflight.service_env_plan `
  --require-check pairing_bootstrap_preflight.verification_advice `
  --require-check pairing_bootstrap_preflight.command_advice `
  --require-check pairing_bootstrap_preflight.redaction `
  --require-check pairing_bootstrap_preflight.no_mutation
```

这类 evidence 能证明“签名配对包、加密同步材料、bootstrap 解密验签、脱敏输出、服务环境落地计划、验收命令建议和非突变边界”链路有效；它不能替代真实接收端执行 `pairing import`、`service env apply --confirm --restart --verify`、服务重启、跨设备同步和 24 小时常驻验收。

## 服务环境轮换预检 Evidence

配对和密钥轮换落地前，可以先用显式当前环境做非侵入式计划，不读取也不写入系统服务管理器：

```powershell
.\deploy\run-steward-service-env-preflight.ps1
```

该脚本会构建或使用当前平台 `steward` 二进制，生成一次性设备 Ed25519 密钥、同步 AES key、本地 AES key、旧 key、HMAC secret 和可选 OpenAI-compatible advisor 配置，然后把完整当前服务环境写入临时 JSON，只用于调用：

```powershell
steward service env plan --current-env-file <temp-json> --rotate-sync-key-id env-preflight-sync-v2 --rotate-local-key-id env-preflight-local-v2 --strict-security
steward service plan --current-env-file <temp-json> --target windows,darwin,linux --strict-security
```

脚本结束会删除临时 `current-env.json`，默认不会留下密钥原文。它验证：

- 计划命令不读取服务管理器状态，不写 Windows Service、LaunchAgent/LaunchDaemon 或 systemd unit。
- 轮换计划生成新的 sync/local key ID，并把当前 key 移入 previous decrypt-only 列表。
- 输出中的 secret、private key、AES key、previous key 和 API key 都被脱敏。
- `verification.runtime_args` 包含新的 agent、sync key、local key 和 advisor 期望值。
- 三端离线安装计划覆盖 Windows、macOS 和 Linux，且 artifact 与 `verification_by_platform` 不泄露密钥原文。

默认输出：

- Evidence：`backend/dist/steward-service-env-preflight/steward-verify-service-env-preflight-*-pass|fail.json`，kind 为 `service-env-preflight`。
- 临时二进制：`backend/dist/steward-service-env-preflight/bin/steward-service-env-preflight-<platform>-<arch>`。

可用 evidence 汇总器检查环境轮换预检证据：

```powershell
cd backend
go run ./cmd/steward verify evidence --dir .\dist\steward-service-env-preflight --require-passing --require-kind service-env-preflight --require-platform windows --require-check service_env_preflight.plan --require-check service_env_preflight.redaction --require-check service_env_preflight.rotation --require-check service_env_preflight.verification_advice --require-check service_env_preflight.no_service_manager --require-check service_env_preflight.install_plan
```

这类 evidence 能证明“服务环境更新前的目标环境计算、key rotation、strict-security、脱敏输出、验收命令建议和三端离线安装计划渲染”链路有效；它不能替代 `service env apply --confirm --restart --verify` 对真实已安装服务的写入、重启和运行时验收。

安装前可以在目标设备上确认二进制身份：

```powershell
.\steward.exe version
```

该命令只读取本地二进制构建信息，不访问管理 API，也不读取服务环境。后台服务启动后，Agent 心跳会把同一个 `version` 和当前平台写入 `/api/steward/agent`，工作台顶部和 `steward status` 可用于确认当前运行服务是否已经切到目标版本；安装验收时也可以用 `verify runtime --expect-agent-version <version> --expect-agent-platform <windows|darwin|linux>` 或 `verify service --expect-agent-version <version> --expect-agent-platform <windows|darwin|linux>` 让 CLI 直接失败退出。

如果只在当前设备上手工构建，也可以使用单平台 `go build`。正式安装时仍应使用已构建的稳定二进制，不应直接用 `go run` 的临时产物作为服务路径：

```powershell
cd backend
go build -o .\bin\steward.exe .\cmd\steward
.\bin\steward.exe service install `
  --name MongojsonSteward `
  --scope system `
  --binary C:\Mine\projects\custom_tools\mongojson\backend\bin\steward.exe `
  --workdir C:\Mine\projects\custom_tools\mongojson\backend `
  --agent-id windows-main `
  --http-addr 127.0.0.1:18080 `
  --peer-http-addr :18081 `
  --public-api-base http://192.168.1.10:18081/api `
  --database-url "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable" `
  --storage-dir C:\Mine\projects\custom_tools\mongojson\backend\data `
  --sync-secret "replace-with-long-random-shared-secret" `
  --device-private-key "<steward keygen 输出的 private_key>" `
  --device-public-key "<steward keygen 输出的 public_key>" `
  --sync-encryption-key "<steward sync-keygen 输出的 key>" `
  --sync-encryption-key-id "home-sync-v1" `
  --sync-encryption-previous-keys "home-sync-v0:<old-sync-key>" `
  --local-encryption-key "<本机 local-at-rest key>" `
  --local-encryption-key-id "windows-local-v1" `
  --sync-interval 5m `
  --autonomy-interval 15m `
  --strict-security
```

如果希望安装时同时启用 S4 大模型建议器，可以在同一个 `service install` 命令中追加以下可选参数。它们只会写入后台服务环境，干跑输出会对 API key 脱敏；未传入时建议器保持关闭：

```powershell
  --llm-provider openai-compatible `
  --llm-base-url https://api.openai.com/v1 `
  --llm-model "<model-name>" `
  --llm-api-key "<api-key>" `
  --llm-timeout 20s `
  --llm-max-data-level D1 `
  --llm-failure-threshold 3 `
  --llm-failure-cooldown 1m
```

本地 OpenAI-compatible 网关如果不需要 key，必须显式使用 `--llm-allow-no-api-key=true`，并配合较低的 `--llm-max-data-level`。

默认 API 地址来自：

```text
STEWARD_API_BASE=http://127.0.0.1:18080/api
```

本机设备 ID 默认是 `local-s1`，三端真实运行时必须为每台机器显式指定不同 ID：

```text
STEWARD_AGENT_ID=windows-main
STEWARD_AGENT_ID=macbook-main
STEWARD_AGENT_ID=linux-lab
```

每台设备必须发布其他设备可达的受限 Peer 地址。这个地址不是本机管理 API：

```text
STEWARD_PUBLIC_API_BASE=http://<this-device-ip>:18081/api
```

本地直接运行时，如果 `backend/.env` 仍指向 Docker Compose 内部主机名 `postgres`，需要覆盖数据库地址：

```powershell
$env:HTTP_ADDR='127.0.0.1:18080'
$env:STEWARD_PEER_HTTP_ADDR=':18081'
$env:STEWARD_PUBLIC_API_BASE='http://<this-device-ip>:18081/api'
$env:DATABASE_URL='postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable'
$env:STORAGE_DIR='./data'
$env:STEWARD_SYNC_SECRET='replace-with-long-random-shared-secret'
$env:STEWARD_DEVICE_PRIVATE_KEY='<steward keygen 输出的 private_key>'
$env:STEWARD_DEVICE_PUBLIC_KEY='<steward keygen 输出的 public_key>'
$env:STEWARD_SYNC_ENCRYPTION_KEY='<steward sync-keygen 输出的 key>'
$env:STEWARD_SYNC_ENCRYPTION_KEY_ID='home-sync-v1'
$env:STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS='home-sync-v0:<old-sync-key>'
$env:STEWARD_LOCAL_ENCRYPTION_KEY='<本机 local-at-rest key>'
$env:STEWARD_LOCAL_ENCRYPTION_KEY_ID='windows-local-v1'
$env:STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS='windows-local-v0:<old-local-key>'
$env:STEWARD_SYNC_INTERVAL='5m'
$env:STEWARD_AUTONOMY_INTERVAL='15m'
go run ./cmd/steward run
```

## 系统服务模型

`steward run` 是唯一长期运行入口。系统服务安装命令只负责把这个入口交给不同平台的服务管理器：

- Windows：安装为 Windows Service，服务进程实现 SCM stop/shutdown 协议。
- macOS：默认安装为当前用户的 LaunchAgent，plist 写入 `~/Library/LaunchAgents/<name>.plist`；显式 `--scope system` 时安装为 LaunchDaemon，plist 写入 `/Library/LaunchDaemons/<name>.plist`，需要管理员权限。
- Linux：默认安装为当前用户的 systemd user unit，unit 写入 `~/.config/systemd/user/<name>.service`；显式 `--scope system` 时安装为 systemd system unit，unit 写入 `/etc/systemd/system/<name>.service`，需要 root 或 sudo 权限。

`--scope` 是服务管理器作用域，不是自动授权策略。默认值如下：

- Windows 固定为 `system`，不支持 `user`。
- macOS 默认 `user`，可显式选择 `system`。
- Linux 默认 `user`，可显式选择 `system`。

凡是会读取或修改服务管理器状态的命令都支持相同的作用域参数：`service install --scope`、`service plan --scope`、`service env plan/apply --scope`、`service start/stop/restart/status --scope` 和 `verify service --scope`。`service install`、`service env` 和 `service plan` 输出的 `verification.service_args` / `verification.watch_args` 会自动带上同一 `--scope`，避免系统级服务安装后验收命令误查用户级服务。

系统级服务只解决“更早启动、更强常驻、更高 OS 服务管理权限”的部署问题，不会绕过 S0/S3/S4 的能力边界：管理 API 仍应绑定回环，高风险动作仍默认阻断或要求人工确认，Peer 面仍只暴露受限同步协议。

安装命令会为后台进程固定以下环境变量：

- `HTTP_ADDR`
- `STEWARD_PEER_HTTP_ADDR`
- `DATABASE_URL`
- `STORAGE_DIR`
- `STEWARD_UI_DIR`，显式 `--ui-dir` 或当前环境已有值优先；未提供时，如果安装目标二进制同级存在 `ui/index.html`，安装器会自动写入该目录。它用于让后台服务直接提供 `/tools/steward` 等本地界面入口。
- `STEWARD_AGENT_ID`
- `STEWARD_PUBLIC_API_BASE`，仅在传入 `--public-api-base` 时写入。
- `STEWARD_SYNC_SECRET`，仅在传入 `--sync-secret` 时写入，干跑输出会脱敏。
- `STEWARD_DEVICE_PRIVATE_KEY`，仅在传入 `--device-private-key` 时写入，干跑输出会脱敏。
- `STEWARD_DEVICE_PUBLIC_KEY`，仅在传入 `--device-public-key` 时写入。
- `STEWARD_SYNC_ENCRYPTION_KEY`，仅在传入 `--sync-encryption-key` 时写入，干跑输出会脱敏。
- `STEWARD_SYNC_ENCRYPTION_KEY_ID`，仅在传入 `--sync-encryption-key-id` 时写入。
- `STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS`，仅在传入 `--sync-encryption-previous-keys` 时写入，格式是逗号分隔的 `key_id:base64`，只用于解密旧 payload，干跑输出会脱敏。
- `STEWARD_LOCAL_ENCRYPTION_KEY`，仅在传入 `--local-encryption-key` 时写入，用于本机数据库内 sync payload 静态加密，干跑输出会脱敏。
- `STEWARD_LOCAL_ENCRYPTION_KEY_ID`，仅在传入 `--local-encryption-key-id` 时写入。
- `STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS`，仅在传入 `--local-encryption-previous-keys` 时写入，格式是逗号分隔的 `key_id:base64`，只用于解密本机旧 payload，干跑输出会脱敏。
- `STEWARD_HEARTBEAT_INTERVAL`，默认 `1m`。
- `STEWARD_SYNC_INTERVAL`，默认不写入，`0` 表示不自动同步。
- `STEWARD_AUTONOMY_INTERVAL`，默认不写入，`0` 表示不自动扫描自主候选。
- `STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS`、`STEWARD_AUTONOMY_RETRY_BACKOFF` 和 `STEWARD_AUTONOMY_RETRY_MAX_BACKOFF`，默认分别为 `3`、`5m` 和 `1h`；自动执行失败后使用指数退避，达到尝试上限后停止自动重试。可通过 `--autonomy-retry-max-attempts`、`--autonomy-retry-backoff` 和 `--autonomy-retry-max-backoff` 写入系统服务环境。
- `STEWARD_DISCOVERY_ENABLED`、`STEWARD_DEVICE_NAME`、`STEWARD_DISCOVERY_LISTEN_ADDR`、`STEWARD_DISCOVERY_TARGETS`、`STEWARD_DISCOVERY_INTERVAL` 和 `STEWARD_DISCOVERY_TTL`，默认不写入且发现关闭；传入 `--discovery-enabled` 后由安装器写入。默认监听/广播组是 `239.255.77.77:18777`，显式 targets 可用于 multicast 不可用的受控网络或本机多进程验证。服务安装和运行进程都会严格拒绝无法解析的开关、非正时长和不满足 `TTL >= 2 * interval` 的配置。
- `STEWARD_LLM_PROVIDER`、`STEWARD_LLM_BASE_URL`、`STEWARD_LLM_MODEL`、`STEWARD_LLM_API_KEY`、`STEWARD_LLM_ALLOW_NO_API_KEY` 等大模型建议器配置，默认不写入，只有传入 `--llm-*` 或当前环境已有对应值时才写入；未显式启用时 S4 只使用本地规则生成候选。
- `STEWARD_LLM_TIMEOUT`、`STEWARD_LLM_MAX_DATA_LEVEL`、`STEWARD_LLM_FAILURE_THRESHOLD` 和 `STEWARD_LLM_FAILURE_COOLDOWN` 等建议器控制配置，默认不写入；服务未设置时运行时使用本地默认值，通常只有接入外部模型或不稳定本地模型网关时才需要调整。

正式安装前建议使用 `--strict-security`。该预检会在写入系统服务前拒绝以下配置：

- `STEWARD_AGENT_ID` 仍是默认占位或未显式设备化。
- `STEWARD_SYNC_SECRET` 缺失或过短。
- S4 advisor 启用后缺少 `STEWARD_LLM_MODEL`，缺少 API key 且不是显式 loopback no-key 模式，或 `STEWARD_LLM_MAX_DATA_LEVEL` 高于 `D1`。
- S4 advisor 的 provider、base URL、timeout、failure threshold 或 failure cooldown 格式不合法。
- S4 自动重试次数不在 `1-20`，退避时长不在 `(0,24h]`，或最大退避小于初始退避。
- `STEWARD_DEVICE_PRIVATE_KEY` / `STEWARD_DEVICE_PUBLIC_KEY` 不是有效 Ed25519 key，或公私钥不匹配。
- `STEWARD_SYNC_ENCRYPTION_KEY` / `STEWARD_LOCAL_ENCRYPTION_KEY` 不是 32 字节 AES key，或缺少对应 key id。
- `STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS` / `STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS` 不是逗号分隔的 `key_id:base64` 格式。
- `HTTP_ADDR` 不是回环监听、`STEWARD_PEER_HTTP_ADDR` 缺失或与管理面复用同一端口。
- `STEWARD_PUBLIC_API_BASE` 缺失、格式无效、路径不以 `/api` 结尾或指向管理端口。
- 启用发现后缺少匹配的 Ed25519 公私钥、Peer API 地址，UDP 地址无效，或候选 TTL 小于广播间隔的两倍。

严格预检只校验本机安装材料，不打印密钥原文；`--dry-run` 输出仍会对敏感环境变量脱敏。

确认安装材料后，可以让 CLI 在安装成功后立即启动服务并执行服务级验收：

```powershell
.\bin\steward.exe service install `
  --name MongojsonSteward `
  --binary C:\Mine\projects\custom_tools\mongojson\backend\bin\steward.exe `
  --workdir C:\Mine\projects\custom_tools\mongojson\backend `
  --agent-id windows-main `
  --http-addr 127.0.0.1:18080 `
  --peer-http-addr :18081 `
  --public-api-base http://192.168.1.10:18081/api `
  --sync-secret "<24+ chars>" `
  --device-private-key "<steward keygen private_key>" `
  --device-public-key "<steward keygen public_key>" `
  --sync-encryption-key "<steward sync-keygen key>" `
  --sync-encryption-key-id home-sync-v1 `
  --local-encryption-key "<local 32-byte base64 key>" `
  --local-encryption-key-id windows-local-v1 `
  --log-dir C:\Mine\projects\custom_tools\mongojson\backend\logs\steward `
  --strict-security `
  --start `
  --verify
```

`--start` 会在安装成功后请求平台服务启动，并输出 `start` 与启动后的 `status`。`--verify` 会使用刚写入的目标环境生成严格 `verify service` 选项，先在 `--verify-startup-timeout` 窗口内等待服务和 runtime 可用，再执行最终验收；如果验收失败，CLI 会先输出 `service`、`verification`、可用的 `start/status` 和 `verification_result`，再以失败状态退出。长时间安装验收可以追加 `--verify-watch-duration 24h --verify-watch-interval 5m`，这会在启动验收通过后复用 `verify service --watch-duration` 的心跳推进检查。`--log-dir` 会写入 `STEWARD_LOG_DIR`，运行进程会在该目录追加 `<service-name>.log`，用于排查启动失败、后台循环错误和 24 小时验收期间的异常。若安装时同时配置了 S4 advisor，可以加 `--verify-advisor-probe` 做一次 D0 live 模型探针，加 `--verify-advisor-privacy-probe` 证明 D2 数据会在模型提交前被拒绝；只有在明确接受模型调用成本时，才把 `--verify-advisor-probe --verify-advisor-probe-each-sample` 和 `--verify-watch-duration` 组合用于每个 watch 样本的长跑模型验收。

上面的 Windows 示例显式写了 `--scope system`，因为 Windows Service 只有系统服务作用域。macOS/Linux 如果不传 `--scope`，仍保持用户级后台服务；需要开机级或更高权限常驻时再显式传 `--scope system`，并用对应平台的管理员权限执行安装、环境写入和重启命令。

已安装服务的环境变量后续可通过 `service env` 显式更新。这个入口用于配对后的同步密钥落地和 key 轮换，不会隐藏在 `pairing import` 里自动执行：

```powershell
.\bin\steward.exe service env plan `
  --name MongojsonSteward `
  --scope system `
  --from-pairing .\peer-pairing.encrypted.json `
  --decrypt-shared-sync-key "<recipient pairing private_key>" `
  --require-signature `
  --strict-security

.\bin\steward.exe service env apply `
  --name MongojsonSteward `
  --scope system `
  --from-pairing .\peer-pairing.encrypted.json `
  --decrypt-shared-sync-key "<recipient pairing private_key>" `
  --require-signature `
  --strict-security `
  --confirm
```

也可以直接传入明确的环境变量变更：

```powershell
.\bin\steward.exe service env plan `
  --name MongojsonSteward `
  --set STEWARD_SYNC_ENCRYPTION_KEY="<new sync key>" `
  --set STEWARD_SYNC_ENCRYPTION_KEY_ID="home-sync-v2" `
  --set STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS="home-sync-v1:<old sync key>"
```

更推荐的 key 轮换方式是让 CLI 生成新 AES-256-GCM key，并把当前 key 自动移入 previous decrypt-only 列表：

```powershell
.\bin\steward.exe service env plan `
  --name MongojsonSteward `
  --rotate-sync-key-id home-sync-v2 `
  --rotate-local-key-id windows-local-v2 `
  --strict-security

.\bin\steward.exe service env apply `
  --name MongojsonSteward `
  --rotate-sync-key-id home-sync-v2 `
  --rotate-local-key-id windows-local-v2 `
  --strict-security `
  --confirm `
  --restart `
  --verify
```

`--rotate-sync-key-id` 会生成新的 `STEWARD_SYNC_ENCRYPTION_KEY`，设置新的 `STEWARD_SYNC_ENCRYPTION_KEY_ID`，并把当前 `STEWARD_SYNC_ENCRYPTION_KEY_ID:STEWARD_SYNC_ENCRYPTION_KEY` 追加到 `STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS` 前面；`--rotate-local-key-id` 对本机 at-rest key 做同样处理。轮换要求当前 key 和 key id 都已经存在且 key 是有效的 32 字节 base64；新 key id 不能等于当前 key id。轮换参数不能与同一组 key 的手工 `--set/--remove` 混用，避免把 previous 列表拼错。`plan` 会生成一次临时 key 用于严格校验和脱敏预览，但不会输出密钥原文；真正写入时 `apply --confirm` 会重新生成并写入实际新 key。

`service env plan` 默认只读取当前服务配置并输出脱敏后的目标环境；加 `--current-env-file <json>` 时，会从显式 JSON 当前环境计算目标环境，不读取系统服务管理器，且仅支持 `plan`。加 `--strict-security` 时会对补丁后的完整目标环境执行与 `service install --strict-security` 相同的 S3/S4 校验，但不会写入。`service env apply` 必须带 `--confirm` 才会写入平台服务配置。默认写入后当前进程不会自动重启，输出中会给出对应平台和 scope 的重启命令：Windows 使用 `sc.exe stop/start`，macOS 使用 `launchctl kickstart -k`，Linux user scope 使用 `systemctl --user daemon-reload/restart`，Linux system scope 使用 `systemctl daemon-reload/restart`。

同一份输出还会包含 `verification` 对象，方便把环境写入、重启和验收串成固定 runbook：

```json
{
  "verification": {
    "api_base": "http://127.0.0.1:18080/api",
    "service_scope": "system",
    "runtime_args": ["steward", "--api", "http://127.0.0.1:18080/api", "verify", "runtime", "--strict-security"],
    "service_args": ["steward", "--api", "http://127.0.0.1:18080/api", "verify", "service", "--name", "MongojsonSteward", "--scope", "system", "--strict-security"],
    "watch_args": ["steward", "--api", "http://127.0.0.1:18080/api", "verify", "service", "--name", "MongojsonSteward", "--scope", "system", "--strict-security", "--watch-duration", "24h", "--watch-interval", "5m"]
  }
}
```

当目标环境包含非敏感期望值时，CLI 会把它们附加到三条验收命令，例如 `--expect-agent-id`、`--expect-agent-platform`、`--expect-sync-key-id`、`--expect-local-key-id`、`--expect-advisor-provider`、`--expect-advisor-model` 和 `--expect-advisor-max-data-level`。这些命令只比较运行时公开状态、平台和 key ID，不包含任何密钥原文。

如果希望在确认写入后立即让后台进程加载新环境，可以显式加 `--restart`：

```powershell
.\bin\steward.exe service env apply `
  --name MongojsonSteward `
  --scope system `
  --from-pairing .\peer-pairing.encrypted.json `
  --decrypt-shared-sync-key "<recipient pairing private_key>" `
  --require-signature `
  --strict-security `
  --confirm `
  --restart `
  --verify
```

`--restart` 只支持 `apply`，不支持 `plan`。它会在服务环境写入成功后请求平台服务重启，并把 `service_env`、`verification`、`restart` 和重启后的 `status` 一起输出；如果环境已经写入但重启或状态查询失败，CLI 会先输出已完成步骤、验收建议和错误字段，再以失败状态退出。`--verify` 也只支持 `apply`，且必须配合 `--restart`，避免服务进程仍在使用旧环境时误判成功；自动验收会先等待 `--verify-startup-timeout`，再执行最终验收，可追加 `--verify-watch-duration 24h --verify-watch-interval 5m` 做密钥轮换后的长时间心跳验收。

服务环境轮换后，可以直接使用输出中的 `verification.service_args` 或 `verification.watch_args` 验证重启后的运行时已经加载目标配置。该检查只比较非密钥标识和数量，不输出密钥原文：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify service `
  --scope system `
  --strict-security `
  --expect-agent-id windows-main `
  --expect-agent-version version-smoke `
  --expect-agent-platform windows `
  --expect-advisor-provider openai-compatible `
  --expect-advisor-model "<model-name>" `
  --expect-advisor-max-data-level D1 `
  --expect-sync-key-id home-sync-v2 `
  --expect-local-key-id windows-local-v1 `
  --expect-sync-previous-key-count 1 `
  --expect-local-previous-key-count 0
```

其中 `--expect-agent-platform` 对应运行中进程暴露的 Agent 平台字段，Windows 为 `windows`，macOS 为 `darwin`，Linux 为 `linux`；`--expect-sync-key-id` 对应 `sync.security.sync_encryption_key_id`，`--expect-local-key-id` 对应 `local_encryption_key_id`；previous key count 用于确认轮换窗口内旧 key 是否仍被加载用于解密。`--expect-advisor-*` 只检查 `/api/steward/autonomy` 暴露的 provider/model/max data level 是否已由服务进程加载，不会调用模型端点。

三端建议命名：

```text
Windows: --agent-id windows-main --name MongojsonSteward
macOS:   --agent-id macbook-main --name com.mongojson.steward
Linux:   --agent-id linux-lab --name mongojson-steward
```

## 后台守护循环

`steward run` 启动后会创建 `steward.Daemon`，所有后台动作都通过同一个 `Service` 聚合入口执行，避免 HTTP handler、CLI 和定时任务各自实现一套业务逻辑。

Daemon 启动时会为 `heartbeat`、`sync`、`autonomy` 三个循环写入独立状态；关闭的循环也会以 `enabled=false` 保留配置可见性。每轮执行完成后更新自己的 `last_success_at`、`last_error` 和 `consecutive_failures`，进程停止或父 context 结束时将 `running` 置为 `false`。`GET /api/steward/agent` 的 `background_loops` 返回这些状态，工作台会把运行、关闭、停止和降级次数显示在 Agent 区域。`verify runtime` 的 `daemon.loops.status` 要求 heartbeat 存在且所有已启用循环仍在运行；循环处于运行中但某个 peer 暂时失败时仍通过该就绪检查，同时在 detail 中报告 degraded 数量。

当前守护循环：

- 心跳：默认每 1 分钟更新本机 `steward_agent_status.last_heartbeat_at`，`/readyz` 会检查 `steward_daemon=ok`。如果 Agent 已被 `steward stop` 标记为 `stopped`，心跳只更新存活时间，不会把状态恢复成 `running`。
- 同步：设置 `STEWARD_SYNC_INTERVAL` 后，只有当本机 Agent 状态为 `running` 时，才周期性扫描已登记、可信、未撤销、启用同步且配置了 `api_base_url` 的 peer，并调用同一套 `SyncDevice` 逻辑。
- 自主扫描：设置 `STEWARD_AUTONOMY_INTERVAL` 后，只有当本机 Agent 状态为 `running` 时，才周期性调用 `RunAutonomyCycle` 生成候选建议；它不会绕过暂停、规则策略、权限上限和审批边界。

默认只启用心跳。自动同步和自主扫描必须显式配置 interval，原因是二者会产生后台网络请求、候选建议和审计记录，不能在用户未明确启用时静默发生。

`steward stop` 和 `steward start` 控制的是管家 Agent 的后台工作开关：停止后会暂停自动同步和自主扫描，但系统服务进程仍可常驻，以便工作台、CLI 和审计查询继续可用。`steward service stop --scope <user|system>` 才会请求 Windows Service、macOS LaunchAgent/LaunchDaemon 或 Linux systemd unit 停止整个进程。

## 运行时验收命令

`steward verify runtime` 是 S3/S4 的统一本机验收入口，适合每台设备安装服务后先跑一遍：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime
```

默认只做只读检查：

- `GET /healthz`
- `GET /readyz`
- `GET /api/steward/agent`
- `GET /api/steward/sync/status`
- `GET /api/steward/autonomy`

严格安全模式会把以下缺失视为失败：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime --strict-security
```

- peer 同步请求鉴权开启。
- 设备私钥签名就绪。
- 设备公钥身份可登记。
- peer 同步 payload 传输加密开启。
- 本机 sync payload 静态加密开启。
- 安全配置没有解析错误。
- 如果签名候选发现已启用，发现循环必须运行、至少完成一次广播、最近错误为空、API 候选计数一致且候选全部通过公告签名验证。
- 如果 S4 advisor 已启用，provider、model、base URL 和 `max_data_level` 必须满足运行时安全约束；`max_data_level` 只能是 `D0` 或 `D1`。

环境轮换或服务重启后，可加 expected flags 验证运行时配置已经切到目标设备 ID、平台和 key ID：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime `
  --expect-agent-id windows-main `
  --expect-agent-version version-smoke `
  --expect-agent-platform windows `
  --expect-advisor-provider openai-compatible `
  --expect-advisor-model "<model-name>" `
  --expect-advisor-max-data-level D1 `
  --expect-sync-key-id home-sync-v2 `
  --expect-local-key-id windows-local-v1 `
  --expect-sync-previous-key-count 1 `
  --expect-local-previous-key-count 0
```

这些检查只读取 `/api/steward/agent`、`/api/steward/sync/status` 和 `/api/steward/autonomy` 暴露的脱敏状态，不读取或打印 `STEWARD_SYNC_SECRET`、AES key、私钥或 `STEWARD_LLM_API_KEY`。

写入探针模式会创建 D0/A3/low 的低风险任务和事件，用于验证业务写入、同步变更队列和自主候选生成：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime --write-probes
```

写入探针会：

1. 创建一个 `source=verification` 的任务。
2. 检查该任务是否进入 `recent_changes` 同步队列。
3. 创建一个 `source=verification` 的事件。
4. 调用 `POST /api/steward/autonomy/run`。
5. 检查是否为该事件生成自主候选。
6. 自动忽略本次探针生成的候选，避免候选列表堆积。

推荐实机验收命令：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime --strict-security --write-probes
```

该命令仍不能代替三台真实设备之间的长时间同步验收；它证明的是单台设备的 S3/S4 运行控制面、写入路径、同步 outbox 和自主候选路径可用。

如果当前进程不是系统服务，例如前台运行、Docker、CI 或临时端口映射，可以直接对 runtime 做长时间观察：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime --strict-security --watch-duration 24h --watch-interval 5m
```

`verify runtime --watch-duration` 会重复采样 health、ready、Agent、同步安全和自主控制面，并检查 `steward.agent.last_heartbeat_at` 是否推进。少于两个样本会失败，因为无法证明心跳推进。若同时传入 `--write-probes`、`--advisor-probe` 或 `--advisor-privacy-probe`，默认只在第一个样本运行这些主动探针，后续样本改为只读检查。

模型建议器 live 探针默认不运行，避免普通健康检查意外调用外部服务。需要验证 `STEWARD_LLM_*` 配置真的能访问 OpenAI-compatible 服务时，显式传入：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime --advisor-probe
```

该探针会调用 `POST /api/steward/autonomy/advisor/probe`，只发送一条 D0 低风险验证文本。若 provider 未启用、请求失败、返回格式无法解析，验收会失败并写入 `autonomy.advisor.probe` 审计记录；成功时只输出 provider、model、D0 数据级别、耗时和建议标题摘要，不输出 API key。

如果要把真实外部或本地 OpenAI-compatible 服务纳入长时间稳定性验收，可以显式要求每个 watch 样本都调用一次 D0 live 探针：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime --advisor-probe --advisor-probe-each-sample --watch-duration 24h --watch-interval 30m
```

`--advisor-probe-each-sample` 必须同时配合 `--advisor-probe` 和 `--watch-duration` 使用。它不会重复运行写入探针或 D2 privacy probe，只会在每个样本调用 D0 advisor probe；适合明确接受模型调用成本和外部请求频率时使用。

隐私分级拦截探针同样默认不运行。需要确认 `STEWARD_LLM_MAX_DATA_LEVEL` 会在模型提交前阻断 D2 数据时，显式传入：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify runtime --advisor-privacy-probe
```

该探针会向同一 API 发送 D2 验证文本，并要求返回 `data level D2 exceeds advisor max ...` 之类的阻断结果。它的成功条件是“模型没有被调用且探针被拒绝”；如果 D2 被接受，验收会失败。

服务安装后建议再跑服务级验收：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify service --strict-security --write-probes
```

`verify service` 会先按 `--scope` 读取当前平台的服务管理器状态：Windows Service 需要 `running`，Linux systemd unit 需要 `active`，macOS LaunchAgent/LaunchDaemon 需要 `loaded`；随后复用 `verify runtime` 的 health、ready、Agent、同步安全、自主规则和可选写入探针检查。它不能替代 24 小时常驻验收，但能把“系统服务已装好”和“API 运行时真的可用”合并到同一个安装后门槛。

长时间观察模式：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify service --strict-security --watch-duration 24h --watch-interval 5m
```

`--watch-duration` 会在指定时长内重复采样服务状态和 runtime 验收结果，输出每次样本。CLI 会检查 `steward.agent.last_heartbeat_at` 是否推进，用于证明后台 daemon 心跳确实在运行；少于两个样本会失败，观察窗口和 `--watch-interval` 应长于当前 `STEWARD_HEARTBEAT_INTERVAL`。若同时传入 `--write-probes`、`--advisor-probe` 或 `--advisor-privacy-probe`，默认只在第一个样本运行这些主动探针；后续样本改为只读检查，避免长时间验收期间堆积验证任务、事件或重复调用模型端点。若需要服务级长跑模型验收，可追加 `--advisor-probe --advisor-probe-each-sample`，让每个 watch 样本都调用一次 D0 advisor probe。

peer 验收用于每台设备登记其他设备后检查跨端可达性：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify peers --strict --require-peers
```

它会读取 `GET /api/steward/sync/status` 中的 `devices`，跳过本机设备，对每个 peer 检查：

- 未吊销。
- `sync_enabled=true`。
- 已配置 `api_base_url`。
- 已配置 `public_key`。
- `POST /api/steward/devices/{id}/verify` 能完成设备信任挑战。

如果希望把“可信且可同步”也纳入验收，可以加 `--sync`：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify peers --strict --require-peers --sync
```

`--sync` 会在每个 peer 信任验证成功后调用 `POST /api/steward/devices/{id}/sync`，返回 pull/import/apply/push 摘要。它会真实推进该 peer 的同步水位，因此只应在确认 peer API 和密钥配置正确后使用。

如果还要证明“本机新写入的数据已经出现在 peer 端”，可以继续加 `--write-probes`：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api verify peers --strict --require-peers --sync --write-probes
```

`--write-probes` 必须搭配 `--sync` 使用。它会先在本机创建 `D0`、`A3`、`low` 的验证任务，以及指向该任务的来源引用、标签、实体标签，再创建一个验证事件并转换出时间线片段；随后对每个 peer 执行信任挑战和同步，最后由本机服务签名调用 `POST /api/steward/devices/{id}/probe`，让本机服务再访问对端只读同步探针。探针只返回指定同步实体是否存在，以及少量关系计数/匹配细节，不提供搜索或内容读取。该命令会产生真实低风险关系数据并推进同步水位，适合作为三端安装后的手动验收门槛，不适合作为默认后台健康检查。

`verify peers --write-probes` 的输出会包含稳定 check id：`s3.peer_probe.task`、`s3.peer_probe.source_ref`、`s3.peer_probe.data_tag`、`s3.peer_probe.entity_tag`、`s3.peer_probe.event`、`s3.peer_probe.timeline_segment` 和聚合项 `s3.peer_probe.relations`。每个 check 的 detail 会记录本机 `agent_id/platform`、预期 peer 数、已可见 peer 和缺失项，因此后续 `verify evidence` 可以用 `--require-kind-check-platform mesh:s3.peer_probe.relations:<platform>` 把关系同步覆盖纳入三端 evidence 门禁。

三端都安装并互相登记后，可以用 mesh 验收一次性检查整组设备：

```powershell
.\bin\steward.exe verify mesh `
  --node http://127.0.0.1:18080/api `
  --node http://127.0.0.1:28080/api `
  --node http://127.0.0.1:38080/api `
  --expect-agent-id windows-main `
  --expect-agent-id macbook-main `
  --expect-agent-id linux-main `
  --expect-agent-platform windows `
  --expect-agent-platform darwin `
  --expect-agent-platform linux `
  --expect-sync-key-id home-sync-v2 `
  --expect-local-key-id windows-local-v1 `
  --expect-local-key-id macbook-local-v1 `
  --expect-local-key-id linux-local-v1 `
  --strict-security `
  --strict `
  --require-peers `
  --sync `
  --write-probes `
  --expect-advisor-provider openai-compatible `
  --expect-advisor-model "<model-name>" `
  --expect-advisor-max-data-level D1
```

`verify mesh` 对每个 `--node` 分别执行 `verify runtime` 和 `verify peers` 的内部验收逻辑。第二、第三个示例端口应通过 SSH local forwarding、WireGuard/Tailscale 本机代理或等价安全隧道映射到对应设备的 `127.0.0.1:18080`，不能直接把管理监听改成局域网地址。`--strict-security` 约束每个节点的 S3/S4 密钥、双监听、加密状态和已启用 advisor 的可观测安全配置，`--strict --require-peers` 约束 peer 配置完整性，`--sync --write-probes` 要求每个节点都能创建低风险任务、来源引用、标签、实体标签、事件和时间线片段探针，同步到已登记 peer，并通过签名 Peer 探针确认这些关系实体 ID。

节点身份和 key ID 可以在 mesh 层直接断言：`--expect-agent-id`、`--expect-agent-version`、`--expect-agent-platform`、`--expect-sync-key-id` 和 `--expect-local-key-id` 都可以只传一次用于所有节点，也可以按 `--node` 顺序重复传入。三端实机验收建议至少重复传入三次 `--expect-agent-id` 和三次 `--expect-agent-platform`，确保验收到的不是“任意三个可用 API”，而是目标 Windows/macOS/Linux 设备本身。`--expect-advisor-*` 会对每个节点复用运行时 advisor 配置断言，但不调用模型端点。没有传 `--node` 时，它只验当前 `--api`，适合本机安装后快速 smoke test。

mesh 也支持长时间观察：

```powershell
.\bin\steward.exe verify mesh `
  --node http://127.0.0.1:18080/api `
  --node http://127.0.0.1:28080/api `
  --node http://127.0.0.1:38080/api `
  --expect-agent-id windows-main `
  --expect-agent-id macbook-main `
  --expect-agent-id linux-main `
  --expect-agent-platform windows `
  --expect-agent-platform darwin `
  --expect-agent-platform linux `
  --strict-security `
  --strict `
  --require-peers `
  --watch-duration 24h `
  --watch-interval 5m
```

该模式会重复采样每个节点的 runtime 和 peer 验收结果，并检查每个节点的 `steward.agent.last_heartbeat_at` 是否推进。少于两个样本会失败，因为无法证明任一节点的后台心跳推进。若带上 `--write-probes`、`--advisor-probe` 或 `--advisor-privacy-probe`，默认只会在第一轮运行这些主动探针，后续样本不再制造验证任务，也不会重复调用模型端点；若同时保留 `--sync`，后续样本仍会执行同步检查并推进同步水位。若显式追加 `--advisor-probe --advisor-probe-each-sample`，每个节点会在每轮样本中调用一次 D0 advisor probe，用于验证三端模型配置的长时间可用性。

macOS 示例：

```bash
cd backend
go build -o ./bin/steward ./cmd/steward
./bin/steward service install \
  --name com.mongojson.steward \
  --binary "$PWD/bin/steward" \
  --workdir "$PWD" \
  --agent-id macbook-main \
  --http-addr 127.0.0.1:18080 \
  --peer-http-addr :18081 \
  --public-api-base http://<macbook-ip>:18081/api \
  --database-url "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable" \
  --storage-dir "$PWD/data" \
  --sync-secret "replace-with-long-random-shared-secret" \
  --device-private-key "<steward keygen 输出的 private_key>" \
  --device-public-key "<steward keygen 输出的 public_key>" \
  --sync-encryption-key "<steward sync-keygen 输出的 key>" \
  --sync-encryption-key-id "home-sync-v1" \
  --sync-encryption-previous-keys "home-sync-v0:<old-sync-key>" \
  --local-encryption-key "<本机 local-at-rest key>" \
  --local-encryption-key-id "macbook-local-v1" \
  --sync-interval 5m \
  --autonomy-interval 15m
```

Linux 示例：

```bash
cd backend
go build -o ./bin/steward ./cmd/steward
./bin/steward service install \
  --name mongojson-steward \
  --binary "$PWD/bin/steward" \
  --workdir "$PWD" \
  --agent-id linux-lab \
  --http-addr 127.0.0.1:18080 \
  --peer-http-addr :18081 \
  --public-api-base http://<linux-ip>:18081/api \
  --database-url "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable" \
  --storage-dir "$PWD/data" \
  --sync-secret "replace-with-long-random-shared-secret" \
  --device-private-key "<steward keygen 输出的 private_key>" \
  --device-public-key "<steward keygen 输出的 public_key>" \
  --sync-encryption-key "<steward sync-keygen 输出的 key>" \
  --sync-encryption-key-id "home-sync-v1" \
  --sync-encryption-previous-keys "home-sync-v0:<old-sync-key>" \
  --local-encryption-key "<本机 local-at-rest key>" \
  --local-encryption-key-id "linux-local-v1" \
  --sync-interval 5m \
  --autonomy-interval 15m
```

服务安装后，用同一个二进制管理：

```bash
./bin/steward service status
./bin/steward service start
./bin/steward service stop
./bin/steward service uninstall
```

## 设备身份模型

设备记录保存在 `steward_devices`。

核心字段：

- `id`：设备 ID。当前本机默认是 `local-s1`。
- `device_name`：设备名。
- `platform`：`windows`、`darwin`、`linux` 或未知平台。
- `role`：`local` 或 `peer`。
- `trust_status`：`trusted` 或 `revoked`。
- `sync_enabled`：是否允许参与同步。
- `permission_level`：设备默认权限上限。
- `public_key`：Ed25519 设备公钥，用于验证该设备发起的 peer 同步请求签名。它不是数据加密密钥。
- `api_base_url`：手动登记的受限 peer API 地址，例如 `http://192.168.1.12:18081/api`。
- `last_sync_sequence`：本机已从该 peer 成功拉取到的远端最大同步序号。
- `last_sent_sequence`：本机已成功推送给该 peer 的本地最大同步序号。
- `last_seen_at`：最近心跳或同步可见时间。
- `last_sync_at`：最近一次同步尝试时间。
- `last_sync_error`：最近一次同步失败或部分失败原因。

后台同步即使没有业务变更，也会通过现有鉴权同步接口发送空变更心跳，使对端刷新本机的 `last_seen_at`。本机成功联系 peer 后也会刷新该 peer 的 `last_seen_at`；连接失败只记录同步错误，不会伪造在线时间。

设备权限保存在 `steward_device_permissions`。

管理接口：

```http
GET /api/steward/devices/{id}/permissions
PUT /api/steward/devices/{id}/permissions/{capability}
```

工作台的“设备权限”面板会直接读取 `GET /api/steward/sync/status` 返回的 `permissions`，按设备和 capability 展示当前策略、范围说明和最高权限。用户可以在面板中把策略调整为 `allow`、`confirm` 或 `deny`，也可以降低或提高 `max_permission_level`；每次修改都会调用对应 `PUT` 接口并刷新总览。已撤销设备的权限行在前端禁用，避免误以为撤销设备仍能恢复参与同步。

CLI 也提供同一控制面，适合通过 SSH 或无头系统服务环境降权某个 peer：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api devices permissions macbook-main
.\bin\steward.exe --api http://127.0.0.1:18080/api devices permission-set macbook-main sync.memory deny A2
```

`permission-set` 必须显式给出 `allow`、`confirm` 或 `deny`。如果省略最后的 A 级权限，CLI 会先读取当前策略并保留当前 `max_permission_level`，避免因为 `PUT` 默认值意外放宽权限。

默认权限：

- `sync.metadata`：允许，最高 A1。
- `sync.tasks`：允许，最高 A3。
- `sync.timeline`：允许，最高 A3。
- `sync.memory`：允许，最高 A3。
- `sync.knowledge`：允许，最高 A3。
- `sync.tags`：允许，最高 A3。
- `sync.audit`：允许，最高 A3。
- `sync.devices`：允许，最高 A3。
- `remote.execute`：拒绝，远端默认不能触发本机执行。
- `autonomy.execute`：确认，默认需要用户确认。

权限执行语义：

- 每条同步实体映射到一个固定 `sync.*` capability，同时受 capability 的 `max_permission_level` 和设备总 `permission_level` 双重上限约束。
- `allow` 才允许无人值守同步；`confirm` 和 `deny` 都不会在后台放行，避免同步循环替用户完成确认。
- 出站增量读取会过滤请求设备无权读取的实体；入站批次会跳过无权写入的单条变更，继续应用同批允许内容，并返回 `denied` 计数。
- 每条入站拒绝都会写入 `sync.change.denied` 本地审计，且审计本身不可再次同步。
- 权限过滤不会卡住水位：Peer 响应返回扫描后的 `next_sequence` 和 `has_more`，即使本窗口所有实体均被过滤，调用端也会推进到已扫描水位。

设备可执行能力声明保存在 `steward_device_capabilities`。它与权限策略分离：能力声明回答“该设备能执行什么”，设备权限回答“是否允许以及允许到什么级别”。

当前行为：

- Agent 启动时从 S4 `AutonomyActionExecutor` 注册表发布本机能力，包括动作名、说明、目标类型、风险等级和最高权限等级。
- 能力元数据未变化时保持原版本，并复用稳定的 outbox 变更 ID，不会因重启重复制造同步记录。
- 能力元数据变化时版本递增，并生成新的 `device_capability` 同步变更。
- 远端能力的所有者始终取同步变更的 `origin_device_id`；载荷中的 `device_id` 仅作兼容字段，不能声明其他设备的能力。
- 能力实体 ID 由“来源设备 + 能力名”稳定派生。实体 ID 与来源不匹配时拒绝应用。
- 旧版本或同版本不同内容的能力声明进入同步冲突队列，不覆盖本地已知声明。
- 当前版本只同步可用能力的 upsert 声明，不支持远端删除能力声明。

## 同步协议草案

同步变更记录保存在 `steward_sync_changes`。

核心字段：

- `sequence`：本地递增序号。
- `entity_type`：实体类型，例如 `task`。
- `entity_id`：实体 ID。
- `operation`：`create`、`update`、`delete`。
- `origin_device_id`：来源设备。
- `version`：实体版本。
- `data_level`：数据级别。
- `payload`：同步载荷。
- `payload_hash`：载荷哈希。
- `sync_status`：`pending`、`applied`、`stored`、`conflict`。

增量读取响应包含：

- `changes`：经过设备权限过滤后的可见变更。
- `next_sequence`：服务端本窗口实际扫描到的最大序号，与过滤后的数组是否为空无关。
- `has_more`：是否需要继续读取下一窗口。

当前核心实体同步策略：

- 本机创建任务会写入 `create` 变更。
- 本机更新、完成、取消任务会写入 `update` 变更。
- 本机删除任务会写入 `delete` 变更。
- 本机创建、更新或删除事件、意图、记忆、知识条目时，也会写入对应同步变更。
- 本机 D0/D1、允许同步的审计记录会写入 `audit_summary` 变更；载荷只包含动作、目标、来源、权限、结果、截断输出/原因和错误摘要，不包含 `input_summary`、`before_summary`、`after_summary`。
- 导入远端任务、事件、意图、记忆、知识条目、来源引用、标签、实体标签和时间线段时会自动应用到本地。
- 导入远端 `audit_summary` 后会保存为 `syncable=false` 的审计记录；应用其他同步变更产生的 `sync.change.apply` 审计同样不可同步，因此不会在设备间形成审计回声。
- 时间线先于关联事件到达时，缺失关联会写入 `steward_timeline_pending_events`；事件后续到达后自动补链并清理积压，不要求人工重放整条时间线变更。
- 本机吊销设备会写入 `device_revoke` 变更；远端导入该变更时会把同一设备标记为 `revoked` 且关闭 `sync_enabled`。远端 revocation 不能禁用本机，也不能把已吊销设备恢复为 trusted。
- 本机执行器能力会写入 `device_capability` 变更；远端按来源设备保存能力声明，并执行版本和等版本内容冲突检测。
- 导入远端任务、事件、意图、记忆、知识条目和时间线段时，如果远端版本旧于本地版本，则进入冲突队列。
- 如果远端版本与本地版本相同，系统会比较规范化业务内容指纹；标题、正文、状态、权限、确认状态、时间字段或时间线关联事件等内容不同，也会进入冲突队列并保留本地值。
- 业务内容指纹不包含 `origin_device_id`、本机保存的 `source=sync`、创建时间和更新时间等传输元数据，避免同一内容在不同设备上被误判为分叉。
- 任务同步会保留 `due_at`、`completed_at`、`canceled_at` 和 `user_confirmed`，这些字段也参与等版本冲突检测。
- 来源引用按 ID 幂等 upsert；标签按名称匹配规范标签，同名且元数据一致时写入 `steward_data_tag_aliases`，后续实体标签新增和删除都会解析到本地规范 ID；同名但类型、颜色或说明不同则进入冲突队列，不做 last-write-wins 覆盖。
- 实体标签按 `entity_type + entity_id + tag_id` 派生稳定同步 ID；时间线段按版本检测冲突，关联事件已存在时立即链接，缺失时进入待解析队列。

`GET /api/steward/sync/status` 的 `pending_relations` 表示尚待关联事件到达的时间线关系数量；`permissions` 返回设备权限策略，`capabilities` 返回本机和已同步 peer 的工具能力声明。工作台会在设备权限面板展示并修改权限策略，同时展示能力声明；CLI 同步摘要会显示能力总数。

同步导入接口：

```http
POST /api/steward/sync/changes/import
```

登记 peer 设备示例：

```powershell
$body = @{
  id = 'macbook-main'
  device_name = 'MacBook Main'
  platform = 'darwin'
  role = 'peer'
  sync_enabled = $true
  permission_level = 'A3'
  api_base_url = 'http://192.168.1.12:18081/api'
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri http://127.0.0.1:18080/api/steward/devices -ContentType 'application/json' -Body $body
```

本地管理面接口：

```http
GET /api/steward/sync/status
GET /api/steward/sync/conflicts
POST /api/steward/sync/conflicts/{id}/resolve
POST /api/steward/devices/{id}/sync
POST /api/steward/devices/{id}/verify
POST /api/steward/devices/{id}/probe
```

受限 Peer 面接口：

```http
GET /api/steward/sync/changes
POST /api/steward/sync/changes/import
GET /api/steward/sync/probe
POST /api/steward/pairing/challenge
```

`GET /api/steward/sync/changes?since_sequence=<n>&limit=<m>` 是同步协议的增量重放接口，不是“最近记录”接口。返回顺序固定为 `sequence asc`，即从旧到新返回水位之后的最早一批变更，保证离线积压超过单批窗口时不会先取到最新变更并跳过中间历史。工作台中的 `recent_changes` 由服务端单独按 `sequence desc` 查询，只用于展示最近活动。

`GET /api/steward/sync/status` 的 `security` 字段只暴露安全状态，不暴露密钥：

- `management_api_addr` / `management_remote_access`：管理监听地址以及它是否离开回环边界。
- `peer_api_addr` / `peer_api_enabled`：独立 Peer 监听地址以及是否启用。
- `public_api_base` / `peer_api_advertised`：是否配置了其他设备可达的 Peer 基地址。
- `auth_required`：当前 peer 同步接口是否要求签名。
- `insecure_mode_active`：是否显式启用了未认证同步兼容；生产和真实三端验收必须为 `false`。
- `hmac_secret_configured`：是否配置共享 HMAC secret。
- `device_signing_ready`：是否有有效 Ed25519 私钥可用于出站请求签名。
- `device_identity_advertisable`：是否有可登记给其他设备的公钥，或可从私钥派生公钥。
- `sync_encryption_configured`：是否配置 peer 传输 payload 加密。
- `local_encryption_configured`：是否配置本机 sync payload 静态加密。
- `config_errors`：只记录配置项名称和解析错误，不包含密钥值。

以下 peer 同步接口支持请求级认证：

```http
GET /api/steward/sync/changes
POST /api/steward/sync/changes/import
GET /api/steward/sync/probe
```

认证策略：

- 如果设置了 `STEWARD_SYNC_SECRET`，请求必须带有效 HMAC 签名，或带有效 Ed25519 设备签名。
- HMAC 和 Ed25519 两条认证路径都必须映射到本机已登记、可信且启用同步的设备；设备吊销立即阻断后续 Peer 请求，不依赖共享 secret 轮换完成。
- 默认拒绝未签名请求。没有配置共享密钥或已登记设备公钥时，peer 同步接口保持关闭，而不是降级为匿名访问。
- `STEWARD_SYNC_REQUIRE_AUTH=true` 显式保持强制鉴权。
- 仅本机隔离开发环境可以显式设置 `STEWARD_SYNC_ALLOW_INSECURE=true`。只有共享密钥为空且未强制鉴权时，该开关才会激活未认证兼容；单独设置 `STEWARD_SYNC_REQUIRE_AUTH=false` 不会放行。
- Docker Compose 默认设置 `STEWARD_SYNC_REQUIRE_AUTH=true`、`STEWARD_SYNC_ALLOW_INSECURE=false`，并把 PostgreSQL、backend 和 nginx 的宿主机端口绑定到 `127.0.0.1`。不要通过修改 `HOST_BIND_ADDRESS` 暴露容器内管理 API；真实跨设备运行应使用双监听系统服务，或单独增加只映射 Peer 端口的部署覆盖文件。
- `steward sync-device` 和后台同步循环会自动读取本机环境变量并签名出站 peer 请求。

通用签名头：

- `X-Steward-Device-ID`
- `X-Steward-Timestamp`
- `X-Steward-Body-SHA256`

HMAC 签名头：

- `X-Steward-Signature`

Ed25519 设备签名头：

- `X-Steward-Key-Algorithm: ed25519`
- `X-Steward-Key-Signature`

签名串由 `method`、`path`、`raw_query`、`timestamp`、`body_hash`、`device_id` 逐行组成。HMAC 使用 `STEWARD_SYNC_SECRET` 做 HMAC-SHA256，Ed25519 使用 `STEWARD_DEVICE_PRIVATE_KEY` 对同一签名串签名。时间戳允许 5 分钟窗口，body hash 必须等于请求体 SHA-256。

设备签名依赖 `steward_devices.public_key`。每台设备先运行：

```powershell
.\bin\steward.exe keygen --prefix windows-main
```

将本机 `private_key` 配为 `STEWARD_DEVICE_PRIVATE_KEY`，将本机 `public_key` 配为 `STEWARD_DEVICE_PUBLIC_KEY` 并登记到其他 peer 的 `public_key` 字段。对端验签时还会检查设备未被撤销、`sync_enabled=true` 且存在已登记公钥。

人工确认配对包：

```powershell
.\bin\steward.exe pairing export `
  --id windows-main `
  --name "Windows Main" `
  --api-base-url http://192.168.1.10:18081/api `
  --public-key "<windows public_key>" `
  --output .\windows-main.pairing.json

.\bin\steward.exe --api http://127.0.0.1:18080/api pairing import --file .\windows-main.pairing.json
```

默认配对包只包含设备 ID、平台、可达 API 地址和 Ed25519 公钥；导入时只登记 peer 设备，不会自动改本机服务环境或静默授权高风险能力。如果需要把共享 `STEWARD_SYNC_SECRET`、当前 `STEWARD_SYNC_ENCRYPTION_KEY` 或解密旧 envelope 用的 `STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS` 一并带给另一台设备，必须在导出时显式加 `--include-sync-secret`、`--include-sync-encryption-key` 或 `--include-sync-encryption-previous-keys`。导入端会把这些值放在 `suggested_env` 中提示后续配置；真正写入系统服务必须单独执行 `service env plan/apply`。

如果导出端配置了 `STEWARD_DEVICE_PRIVATE_KEY` 或显式传入 `--private-key`，配对包会自动带 `signature`。签名覆盖 schema、版本、创建时间、设备 ID、设备名称、平台、API 地址、Ed25519 公钥、权限上限，以及明文或加密的共享同步材料封包。导入端遇到 `signature` 会自动验签；如果希望拒绝旧的无签名配对包，导入时加 `--require-signature`：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api pairing import `
  --file .\windows-main.pairing.json `
  --require-signature
```

共享同步材料建议使用加密配对包传递。接收设备先生成只用于配对包解密的 X25519 密钥：

```powershell
.\bin\steward.exe pairing keygen --label macbook-main
```

发送设备导出配对包时，把接收设备的 `public_key` 传给 `--encrypt-shared-sync-for`：

```powershell
.\bin\steward.exe pairing export `
  --id windows-main `
  --name "Windows Main" `
  --api-base-url http://192.168.1.10:18081/api `
  --public-key "<windows ed25519 public_key>" `
  --include-sync-secret `
  --sync-secret "<shared hmac secret>" `
  --include-sync-encryption-key `
  --sync-encryption-key "<home-sync-v1 key>" `
  --sync-encryption-key-id home-sync-v1 `
  --encrypt-shared-sync-for "<macbook pairing public_key>" `
  --output .\windows-main.encrypted.pairing.json
```

该 bundle 不包含明文 `shared_sync` 或 `suggested_env`，只包含 `shared_sync_encrypted`。接收设备导入时使用自己的配对私钥解密：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api pairing import `
  --file .\windows-main.encrypted.pairing.json `
  --decrypt-shared-sync-key "<macbook pairing private_key>" `
  --require-signature
```

解密成功后 CLI 仍只返回 `suggested_env`，需要人工写入该设备的服务环境并重启服务；这样避免传输包泄露明文同步 secret/key，同时保留 S0/S3 的人工确认边界。

写入前可先用 `pairing bootstrap` 生成一个不变更系统状态的复核包：

```powershell
.\bin\steward.exe pairing bootstrap `
  --file .\windows-main.encrypted.pairing.json `
  --decrypt-shared-sync-key "<macbook pairing private_key>" `
  --require-signature `
  --current-env-file .\current-service-env.json `
  --strict-security
```

输出包含：

- `device`：后续 `pairing import` 将登记的 peer 设备载荷。
- `suggested_env_keys` 和 `suggested_env_redacted`：解密得到的服务环境键和值的脱敏视图，不输出 secret、private key、AES key 或 API key 原文。
- `service_env_plan`：从显式 `--current-env-file` 计算出的脱敏目标环境；不会读取服务管理器，也不会写系统服务配置。
- `verification`：基于目标环境生成的 `verify runtime`、`verify service` 和长时间 watch 参数建议。
- `commands`：下一步 `pairing import`、`service env plan` 和 `service env apply --confirm --restart --verify` 的建议命令；加密配对包中的私钥参数只用 `<recipient pairing private_key>` 占位。

`pairing bootstrap` 不会登记设备、不写服务环境、不重启服务、不访问管理 API，也不会调用模型端点。它只是把配对导入和环境落地拆成一份可复核计划，避免人工在多个命令输出之间漏看密钥、签名或 strict-security 问题。

复核通过后，再用 `service env plan` 查看脱敏后的目标服务环境，并用 `service env apply --confirm` 写入：

```powershell
.\bin\steward.exe service env plan `
  --name MongojsonSteward `
  --from-pairing .\windows-main.encrypted.pairing.json `
  --decrypt-shared-sync-key "<macbook pairing private_key>" `
  --require-signature `
  --strict-security

.\bin\steward.exe service env apply `
  --name MongojsonSteward `
  --from-pairing .\windows-main.encrypted.pairing.json `
  --decrypt-shared-sync-key "<macbook pairing private_key>" `
  --require-signature `
  --strict-security `
  --confirm
```

`pairing import` 和 `service env apply` 是两个独立动作。前者登记 peer 设备，后者只更新本机已安装服务的环境变量。建议配对材料落地时始终加 `--strict-security`，先验证写入后的完整服务环境仍满足 S3/S4 安全基线。写入后需要按命令输出重启服务，新的同步密钥才会进入运行中的后台进程；也可以在人工确认后使用 `service env apply --strict-security --confirm --restart` 让 CLI 请求服务重启并返回重启后状态。若这次环境更新包含 `STEWARD_LLM_*` advisor 配置或密钥轮换，继续加 `--verify`、`--verify-advisor-probe` 和按需的 `--verify-advisor-privacy-probe`，让 CLI 在重启后直接验证模型端点可用且隐私分级仍生效。长时间 post-apply 验收与安装验收一致：`--verify-watch-duration` 只重复采样服务和 runtime，只有显式追加 `--verify-advisor-probe-each-sample` 才会在每个样本重复调用 D0 advisor probe。

同步 key 轮换期间的配对包示例：

先在发起端用服务环境轮换命令生成并加载新的当前 key，同时保留旧 key 做解密窗口：

```powershell
.\bin\steward.exe service env apply `
  --name MongojsonSteward `
  --rotate-sync-key-id home-sync-v2 `
  --strict-security `
  --confirm `
  --restart `
  --verify
```

再把新的当前 sync key 和 previous key 列表放入配对包，交给其他已信任设备落地：

```powershell
.\bin\steward.exe pairing export `
  --id windows-main `
  --name "Windows Main" `
  --api-base-url http://192.168.1.10:18081/api `
  --public-key "<windows public_key>" `
  --include-sync-encryption-key `
  --sync-encryption-key "<home-sync-v2 key>" `
  --sync-encryption-key-id home-sync-v2 `
  --include-sync-encryption-previous-keys `
  --sync-encryption-previous-keys "home-sync-v1:<old-key>,home-sync-v0:<older-key>" `
  --output .\windows-main.rotation.pairing.json
```

这个 bundle 含有敏感同步密钥材料，只应通过你认可的安全渠道传递；导入端仍只返回 `suggested_env`。需要落地到已安装服务时，先运行 `service env plan --from-pairing ... --strict-security` 预览并校验目标环境，再运行 `service env apply --from-pairing ... --strict-security --confirm` 写入并按输出重启服务。

轮换包也可以加 `--encrypt-shared-sync-for "<recipient pairing public_key>"`，这样旧 key 和新 key 都只以 `shared_sync_encrypted` 形式出现在配对包中。

配对后可做一次设备信任挑战：

```powershell
.\bin\steward.exe --api http://127.0.0.1:18080/api devices verify macbook-main
.\bin\steward.exe --api http://127.0.0.1:18080/api pairing verify macbook-main
```

验证流程：

1. 本机读取已登记 peer 的 `api_base_url` 和 `public_key`。
2. 本机生成一次性随机 challenge，请求对端 `POST /api/steward/pairing/challenge`。
3. 对端用 `STEWARD_DEVICE_PRIVATE_KEY` 签名 challenge，并返回设备 ID、公钥、算法、签名和签名时间。
4. 本机用已登记的 `public_key` 验签，并检查设备 ID、challenge、时间窗口和公钥一致性。
5. 验证成功后更新 peer 的 `last_seen_at`，清空 `last_sync_error`，并写入 `device.trust.verify` 审计。

这个流程只能证明“当前 `api_base_url` 后面的对端持有已登记公钥对应的私钥”，不会分发同步密钥，也不会自动授予额外权限。

同步 payload 加密：

```powershell
.\bin\steward.exe sync-keygen --key-id home-sync-v1
```

将同一个 `STEWARD_SYNC_ENCRYPTION_KEY` 和 `STEWARD_SYNC_ENCRYPTION_KEY_ID` 配置到可信设备。启用后，`steward sync-device` 和后台同步循环会在 pull 请求中加入 `encrypted=true`，并在 push/import 请求中把每条变更的 `payload` 替换为 AES-256-GCM envelope：

- `_encrypted=true`
- `algorithm=aes-256-gcm`
- `key_id`
- `nonce`
- `ciphertext`
- `payload_hash`

加密附加认证数据会绑定 change ID、实体类型、实体 ID、操作、来源设备、版本和数据级别。接收端在 `ImportSyncChanges` 入库和应用前自动解密。未配置 `STEWARD_LOCAL_ENCRYPTION_KEY` 时，数据库内仍保存本机可读 payload；配置本地 key 后，新写入的 sync outbox payload 会以本地静态加密 envelope 保存。

本地 sync payload 静态加密：

```powershell
.\bin\steward.exe sync-keygen --key-id windows-local-v1
```

将输出的 `key` 配为 `STEWARD_LOCAL_ENCRYPTION_KEY`，将 `key_id` 配为 `STEWARD_LOCAL_ENCRYPTION_KEY_ID`。启用后，新写入 `steward_sync_changes.payload` 的内容会被替换为 `scope=local-at-rest` 的 AES-256-GCM envelope；业务层、同步接口和工作台读取时会透明解密。这个 key 是本机本地存储 key，不需要和其他设备共享。

本地 key 轮换时，将旧 key 追加到 `STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS`，例如 `windows-local-v0:<old-key>`。只有在确认数据库里不再需要读取旧 envelope 后，才移除旧 key。若数据库中已有加密 payload 但启动时未配置对应 key，读取相关同步记录会失败；这是预期的安全失败。

人工轮换同步加密 key 时：

1. 先用 `steward sync-keygen --key-id home-sync-v2` 生成新 key。
2. 将新 key 配到 `STEWARD_SYNC_ENCRYPTION_KEY`，新 key ID 配到 `STEWARD_SYNC_ENCRYPTION_KEY_ID`。
3. 将仍可能收到的旧 key 追加到 `STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS`，例如 `home-sync-v1:<old-key>,home-sync-v0:<older-key>`。
4. 需要把轮换配置传给其他设备时，使用 `pairing export --include-sync-encryption-key --include-sync-encryption-previous-keys --encrypt-shared-sync-for <recipient pairing public_key>` 生成加密配对包；接收端用 `pairing import --decrypt-shared-sync-key` 验证并查看 `suggested_env`，先用 `service env plan --from-pairing ... --strict-security` 预览，再用 `service env apply --from-pairing ... --strict-security --confirm` 写入已安装服务。
5. 所有可信设备完成配置并确认不再传输旧 envelope 后，再移除旧 key。

`POST /api/steward/devices/{id}/sync` 当前执行一次保守的手动 peer 同步：

1. 使用设备的 `api_base_url` 读取对端 `GET /api/steward/sync/changes?since_sequence=<last_sync_sequence>&limit=200`。
2. 对端按远端 `sequence asc` 返回增量窗口，本机按远端序号从旧到新导入；重复 change ID 会跳过，避免离线恢复后重复应用。入站 change 写入使用数据库原子 `ON CONFLICT (id) DO NOTHING`，因此后台 Daemon 与人工 `verify mesh --sync` 并发导入同一 change 时也只保留一条记录，不依赖存在竞态的“先查再插”。
3. 更新该设备的 `last_sync_sequence`、`last_sync_at` 和 `last_sync_error`。
4. 使用该设备的 `last_sent_sequence` 读取本地未发送变更，推送到对端 `POST /api/steward/sync/changes/import`。
5. 只有在 push 成功后推进 `last_sent_sequence`；如果本轮只有来自该 peer、无需回推的变更，也会推进水位以避免重复扫描。

拉取和推送都使用从旧到新的窗口语义。每个窗口最多 200 条变更；一次 `syncDevice` 最多连续处理 25 个窗口，尽量在单次手动同步或单次后台同步中追平离线积压。若积压超过保护上限，下一轮继续从上次成功推进的水位后读取下一批。同步失败时不会推进失败窗口对应的水位，下一轮会重试同一批窗口。

当前 push 同时依赖 per-peer 发送水位和对端 change ID 幂等去重。后续应补自动密钥协商、轮换分发、全量业务表静态加密和跨三台真实设备的信任链验收。

## 自主执行控制面

自主设置保存在 `steward_autonomy_settings`。

默认策略：

- `paused=false`
- `mode=suggest_only`
- `max_auto_permission=A3`

默认规则：

- `event-follow-up-candidate`：从事件生成跟进任务候选，默认需要确认。
- `stale-open-task-review`：为长时间未更新任务生成复盘建议，默认仅建议。
- `event-knowledge-summary`：把 D0/D1 事件整理为本地知识摘要，默认仅建议。
- `due-task-reminder`：为已到期或 24 小时内到期的任务生成提醒，默认仅建议。
- `sync-conflict-diagnostics`：同步冲突出现时建议运行只读诊断，默认仅建议。
- `high-risk-guardrail`：高风险操作只生成计划和审批请求，默认禁止执行。

运行模式：

- `suggest_only`：后台周期只生成候选，即使某条规则为 `auto` 也不自动执行。
- `controlled`：后台周期可以执行当前仍启用且当前策略仍为 `auto` 的低风险规则候选。
- 自动执行需要全局 `controlled` 和规则 `auto` 双重授权。没有关联规则的手工 `policy=auto` 提案不会被后台周期拾取；规则被禁用、改回确认/建议或动作发生变化后，旧候选也不会获得后台执行资格。
- 工作台“受控自主”面板提供模式分段控件，并保留独立的一键暂停。暂停优先级最高。

每个自主候选会由本地 `AutonomyProposalScorer` 生成 `score` 和 `score_reason`。默认评分器只使用本地确定性证据：规则命中、关联来源实体、触发原因、建议动作、摘要和来源类型。候选列表按分数降序排列，工作台同时展示百分比分数和评分依据。

评分用于降噪和排序，不是执行授权：模型不能修改评分，分数再高也不能改变 `risk_level`、`permission_level`、`policy`，不能绕过暂停、审批、高风险或 A4+ 阻断。评分器通过独立接口注入，后续可以替换为用户反馈学习实现，而不改变提案持久化和执行状态机。

自主动作通过 `AutonomyActionExecutor` 注册。当前默认执行器：

- `create_local_task`：创建普通本地低风险任务。
- `create_follow_up_task`：从事件候选创建本地跟进任务。
- `create_review_checklist`：从过期任务候选创建本地复盘检查清单。
- `create_reminder_task`：创建本地提醒任务；来源为任务时继承真实 `due_at`。
- `create_knowledge_summary`：把低风险事件整理为本地可索引知识摘要，不修改来源。
- `run_readonly_diagnostics`：只读统计同步积压、待补关系、冲突、失败自主运行和未完成任务，并保存一份本地诊断报告。

`GET /api/steward/autonomy` 的 `actions` 会返回每个执行器的动作名、说明、目标类型、风险等级和最大权限。提案创建时把 `action` 写入提案快照，之后即使规则被修改，该提案也不会静默切换执行语义。模拟和真实执行都先解析同一个执行器并检查其权限声明；成功后会记录 `execution_target_type`、`execution_target_id`，创建任务的兼容字段 `created_task_id` 也会保留。

当前本地任务执行器使用 `proposal_id + action` 派生确定性任务 UUID；知识摘要和诊断报告使用同样原则派生确定性知识条目 UUID。若进程在“目标已创建、提案尚未写回 executed”之间退出，恢复后再次执行会读取同一目标，不会创建重复待办、摘要、报告或同步变更。未来增加外部工具执行器时，执行器必须自行提供等价的幂等键、结果查询和恢复语义。

规则自动执行生成的任务或知识条目标记为 `user_confirmed=false`；用户明确批准后执行的目标标记为 `user_confirmed=true`，便于审计区分“规则预授权”和“本次人工确认”。

大模型建议器默认关闭。启用后，它只参与 `RunAutonomyCycle` 的候选文本生成：规则、来源实体、风险等级、权限等级、数据等级和策略仍由本地规则决定，模型返回值不能提升这些安全字段，也不能触发执行。

```text
STEWARD_LLM_PROVIDER=openai-compatible
STEWARD_LLM_BASE_URL=https://api.openai.com/v1
STEWARD_LLM_MODEL=<model-name>
STEWARD_LLM_API_KEY=<api-key>
STEWARD_LLM_ALLOW_NO_API_KEY=false
STEWARD_LLM_TIMEOUT=20s
STEWARD_LLM_MAX_DATA_LEVEL=D1
STEWARD_LLM_FAILURE_THRESHOLD=3
STEWARD_LLM_FAILURE_COOLDOWN=1m
```

配置说明：

- `STEWARD_LLM_PROVIDER` 为空、`off`、`disabled` 或 `none` 时完全关闭。
- 当前实现支持 OpenAI-compatible `/chat/completions` HTTP 接口；本地兼容服务如需无 key 调用，必须显式设置 `STEWARD_LLM_ALLOW_NO_API_KEY=true`。
- `STEWARD_LLM_MAX_DATA_LEVEL` 默认 `D1`，超过该等级的数据不会发送给模型，只回退到本地规则候选。
- `STEWARD_LLM_FAILURE_THRESHOLD` 默认 `3`，`STEWARD_LLM_FAILURE_COOLDOWN` 默认 `1m`。供应商请求、超时、HTTP 错误和响应解析错误会累计失败；达到阈值后，冷却窗口内的候选增强会直接回退到本地规则，不再请求模型端点。
- D2/D3/D4 等超过 `STEWARD_LLM_MAX_DATA_LEVEL` 的输入会在本地被拒绝，拒绝不会增加连续失败次数，也不会打开熔断。
- 候选增强失败时，服务最多每 5 分钟写入一条 `autonomy.advisor.fallback` 审计，标记已使用本地规则回退；该审计不可同步，且只保存错误摘要，不保存模型输入正文。
- `GET /api/steward/autonomy` 会返回 `advisor` 状态，包含 provider、model、max data level、熔断状态、连续失败次数、下次重试时间和脱敏最近错误；`steward verify runtime` 会检查 `s4.advisor.status` 是否可见。
- `GET /api/steward/autonomy` 同时返回 `retry_policy`；每个 proposal 返回 `failed_attempts`、`retry_eligible`、`retry_exhausted` 和可选 `auto_retry_at`。自动扫描只选择退避已到期且未耗尽的 `auto` proposal，状态由持久化运行记录派生，服务重启不会清空失败次数。
- `POST /api/steward/autonomy/advisor/probe` 会执行一次 advisor 探针并写审计；D0 live 探针通过 `verify runtime --advisor-probe`、`verify service --advisor-probe` 或 `verify mesh --advisor-probe` 调用，watch 模式下可显式追加 `--advisor-probe-each-sample` 做每样本模型长跑验收；D2 隐私阻断探针通过 `--advisor-privacy-probe` 调用。
- 即使模型输出了外发、删除、付款、凭据读取、提交/发布或系统配置建议，S4 会拒收该模型文本并回退到本地规则候选；执行路径仍只允许低风险本地候选进入审批和模拟，高风险/A4+ 仍会被阻断。

自主执行接口：

```http
GET /api/steward/autonomy
POST /api/steward/autonomy/advisor/probe
PATCH /api/steward/autonomy/settings
POST /api/steward/autonomy/run
PATCH /api/steward/autonomy/rules/{id}
POST /api/steward/autonomy/proposals/{id}/simulate
POST /api/steward/autonomy/proposals/{id}/approve
POST /api/steward/autonomy/proposals/{id}/execute
POST /api/steward/autonomy/proposals/{id}/retry
POST /api/steward/autonomy/proposals/{id}/dismiss
POST /api/steward/autonomy/proposals/bulk-dismiss
POST /api/steward/autonomy/approvals/{id}/approve
POST /api/steward/autonomy/approvals/{id}/reject
```

执行边界：

- 低风险候选必须满足规则策略、权限上限和暂停状态。
- 提案动作必须存在已注册执行器，且提案权限不得超过执行器声明的 `max_permission_level`。
- 模拟和真实执行使用同一个动作执行器，模拟不会写入执行目标，真实执行成功后必须记录目标类型和目标 ID。
- 当前本地任务动作必须使用确定性幂等目标；崩溃恢复或人工重试不能创建第二条相同自主任务。
- 本地知识摘要和诊断报告同样必须使用确定性幂等目标。
- 后台自动执行只接受 `controlled` 模式下、来自当前启用且当前仍为 `auto` 的同动作规则候选。
- 需要确认的候选必须先批准。
- 高风险或 A4 及以上候选会被阻断，只生成审批请求和计划摘要。
- 暂停后，自主扫描不会创建或执行新任务，只记录阻断运行日志。
- `candidate` 可以进入 `approved`、`dismissed` 或 `blocked`；`approved` 可以进入 `executed`、`dismissed` 或 `blocked`；`blocked` 只能被忽略清理或保留为人工复核材料。
- `executed` 和 `dismissed` 是终态，重复执行只会写入阻断运行日志，不会创建重复任务。
- `approve autonomous execution` 审批通过会把低风险候选推进到 `approved`；审批拒绝会把该候选推进到 `dismissed`。
- `manual high-risk review` 和 `review blocked autonomous proposal` 只表示人工复核，不会把高风险、未知动作或越权提案改成可自主执行。
- 批量清理只接受 `candidate`、`approved`、`blocked` 三类未执行候选；拒绝 `executed`、`dismissed` 和未知状态。每条被清理的候选都会写入单条审计记录，批量操作还会写入汇总审计。
- 同一提案的执行与状态迁移使用 PostgreSQL session advisory lease；同数据库上的多个 API/Daemon 进程只能有一个进入执行器，其余请求在重新读取状态后记录为重复阻断。
- 同一提案、同一请求动作最多保留一条 `pending` 审批记录；数据库部分唯一索引负责跨进程去重。
- 自主运行 `failed` 会写入失败审计和恢复提示，不再被误记为成功。

## 工作台入口

前端入口：

```text
/tools/steward
```

开发模式下通常由 Vite 提供该路由；`deploy/build-steward.ps1` 生成的后台软件目录会自动托管同级 `ui/`。自定义布局仍可通过 `steward run --ui-dir <frontend-dist>` 或 `steward service install --ui-dir <frontend-dist>` 覆盖。工作台目录必须包含 `index.html`；缺失的静态资源返回 404，普通 SPA 路由回退到 `index.html`。

工作台前端按职责拆分：`StewardWorkspace.tsx` 只编排页面区块，`steward/useStewardWorkspace.ts` 管理加载、表单状态和 API 副作用，`steward/rows.tsx` 收拢各实体行及其局部交互，`steward/model.ts` 维护状态文案与数据级别等纯规则，`steward/presentation.tsx` 维护通用展示控件。后续增加同步实体或自主动作时，应优先扩展对应模块，避免重新把 API、领域状态和大段 JSX 堆回页面容器。

工作台包含：

- Agent 状态。
- S2 数据底座指标。
- S3 同步状态、设备权限策略查看和修改、冲突队列。
- S3 同步安全链：鉴权、设备签名、传输加密、本地加密和配置错误。
- S4 自主模式切换、一键暂停、可用执行动作、规则、候选建议、审批队列、运行日志。
- S4 候选建议单条模拟、批准、执行、忽略，以及候选批量清理。
- 事件、任务、意图、记忆、知识、来源、审计等 S2 数据管理能力。

## 验证命令

后端测试：

```powershell
cd backend
go test ./...
```

可选的真实 Postgres-backed HTTP 同步与自主能力集成测试：

```powershell
.\deploy\run-steward-postgres-e2e.ps1 -StartPostgres
```

`run-steward-postgres-e2e.ps1` 默认使用 `postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable`，可用 `-DatabaseURL` 覆盖，也可先设置 `TEST_DATABASE_URL`。`-StartPostgres` 只会执行本仓库 `docker compose up -d postgres` 并等待 `pg_isready`，不会安装系统服务、不会读取服务环境、不会调用真实管理 API、不会接触同步密钥或模型 API key。脚本默认写入：

- 测试日志：`backend/dist/steward-e2e/steward-postgres-e2e-*.log`。
- Evidence：`backend/dist/steward-e2e/steward-verify-postgres-e2e-*-pass|fail.json`，kind 为 `postgres-e2e`，包含脱敏数据库 URL、当前平台、测试命令、日志路径和 `postgres_e2e.go_test` 检查项。

可用现有 evidence 汇总器检查这类本机 E2E 证据：

```powershell
cd backend
go run ./cmd/steward verify evidence --dir .\dist\steward-e2e --require-passing --require-kind postgres-e2e --require-check postgres_e2e.go_test
```

底层等价测试命令：

```powershell
cd backend
$env:TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable'
go test ./internal/httpapi -run TestSteward -count=1 -timeout 300s -v
```

这些测试会按场景创建两个或三个临时数据库，分别启动独立管理 API 和 Peer API，启用 `STEWARD_SYNC_REQUIRE_AUTH=true` 和共享 HMAC secret，覆盖：

- 手动同步：一端创建任务后通过真实 HTTP pull/import/push 同步到另一端，另一端离线积压的任务能在下一次同步中补齐到本端。
- 后台同步：启动 `steward.Daemon` 的短间隔同步循环，不直接调用 `SyncDevice`，验证常驻进程能自动把可信 peer 的本地任务推送到对端，也能自动拉取 peer 新增任务。
- 后台同步故障隔离：同时登记健康 peer 和不可达 peer，验证不可达设备保留 `last_sync_error` 和 sync 循环连续失败状态时，健康 peer 仍能收到任务，本地事件写入、heartbeat 和 autonomy 循环继续；修复 peer 地址后自动补齐积压并清零失败状态。
- 三节点 mesh：Windows、macOS、Linux 三个逻辑节点完成任务中继收敛、离线重返补齐和第三方设备撤销传播。
- 设备权限：双向 `sync.memory=deny` 过滤、允许类型继续同步、拒绝审计以及过滤游标推进。
- S3 复杂关系：时间线段乱序补链、同名标签 alias 合并、来源引用和实体标签先于目标实体到达，以及 source_ref/entity_tag 删除重放。
- S4 并发：同一提案八路并发只能成功执行一次，高风险并发提案只能生成一条待审批，暂停状态阻断扫描与执行。
- S4 模型建议器 resilience：连续供应商失败会打开熔断，熔断期间不会继续请求模型端点；冷却后成功调用会清零失败状态；隐私分级拒绝不计入供应商失败。

测试结束会关闭节点并删除临时数据库；未设置 `TEST_DATABASE_URL` 时默认跳过，不影响普通单元测试。
这些 evidence 只能证明本机多临时数据库、多 HTTP 节点和后台 daemon 的行为，不能替代 Windows、macOS、Linux 三台真实机器的安装、常驻、网络和 service manager 验收。

可选的本机真实三进程 mesh 验证：

```powershell
.\deploy\run-steward-local-mesh.ps1
```

该脚本会构建或使用当前平台的 `steward` 二进制，启动 `docker compose` 中的 Postgres，创建三个临时数据库，并在当前宿主机上启动三个真实后台进程：

- `windows-main`：管理 API `127.0.0.1:19180`，Peer API `127.0.0.1:19181`。
- `macbook-main`：管理 API `127.0.0.1:19190`，Peer API `127.0.0.1:19191`。
- `linux-lab`：管理 API `127.0.0.1:19200`，Peer API `127.0.0.1:19201`。

每个节点都有独立的设备密钥、本地加密 key、数据库和日志目录；三节点共享同步 HMAC secret 与同步 payload AES key。脚本会先等待三个进程 ready，再双向登记 peer，随后运行：

```powershell
steward verify mesh --strict-security --strict --require-peers --sync --write-probes ...
```

最后它会通过真实管理 API 创建并执行一条 S4 低风险本地任务提案，验证自主执行器不是只停留在接口层。默认输出：

- Evidence：`backend/dist/steward-local-mesh/steward-verify-local-mesh-*-pass|fail.json`，kind 为 `local-mesh`。
- 嵌套 mesh evidence：`backend/dist/steward-local-mesh/mesh-evidence/steward-verify-mesh-*-pass|fail.json`。
- 节点日志和临时二进制：`backend/dist/steward-local-mesh/nodes/` 与 `backend/dist/steward-local-mesh/bin/`。

可用 evidence 汇总器检查本机 mesh 证据：

```powershell
cd backend
go run ./cmd/steward verify evidence `
  --dir .\dist\steward-local-mesh `
  --latest-per-kind `
  --require-passing `
  --require-kind local-mesh `
  --require-kind mesh `
  --require-platform windows `
  --require-check local_mesh.verify_mesh `
  --require-check local_mesh.autonomy_execute `
  --require-check s3.sync.security.strict `
  --require-check s4.autonomy.status `
  --require-kind-check-platform mesh:s3.peer_probe.task:windows `
  --require-kind-check-platform mesh:s3.peer_probe.source_ref:windows `
  --require-kind-check-platform mesh:s3.peer_probe.data_tag:windows `
  --require-kind-check-platform mesh:s3.peer_probe.entity_tag:windows `
  --require-kind-check-platform mesh:s3.peer_probe.event:windows `
  --require-kind-check-platform mesh:s3.peer_probe.timeline_segment:windows `
  --require-kind-check-platform mesh:s3.peer_probe.relations:windows
```

这类 evidence 覆盖真实进程、真实 Postgres、真实 HTTP 同步、后台心跳、后台同步配置、设备信任挑战、关系写入探针和 S4 低风险执行链路；其中 `s3.peer_probe.*` check 会把任务、来源引用、标签、实体标签、事件和时间线片段是否出现在每个可同步 peer 端作为 manifest 可强制要求的门禁。`--latest-per-kind` 只评估目录中每种 evidence kind 的最新文件，适合忽略同一目录里的历史失败回归记录；如果要做归档目录的全量审计，可以去掉它。但三个进程仍运行在同一宿主 OS 上，`platform` evidence 只能代表当前机器，不能替代三台真实 Windows/macOS/Linux 设备的安装、24 小时常驻、网络和服务管理器验收。

可选的本机真实进程 S4 advisor E2E 验证：

```powershell
.\deploy\run-steward-advisor-e2e.ps1
```

该脚本会构建或使用当前平台的 `steward` 二进制，启动 `docker compose` 中的 Postgres，创建一个临时数据库，启动本地 OpenAI-compatible mock 端点 `http://127.0.0.1:19382/v1`，再启动一个真实 `steward run` 后台进程。运行时环境会启用：

- `STEWARD_LLM_PROVIDER=openai-compatible`
- `STEWARD_LLM_BASE_URL=http://127.0.0.1:19382/v1`
- `STEWARD_LLM_MODEL=advisor-e2e-model`
- `STEWARD_LLM_ALLOW_NO_API_KEY=true`
- `STEWARD_LLM_MAX_DATA_LEVEL=D1`

随后脚本运行：

```powershell
steward --api http://127.0.0.1:19380/api verify runtime --strict-security --advisor-probe --advisor-privacy-probe ...
```

默认输出：

- Evidence：`backend/dist/steward-advisor-e2e/steward-verify-advisor-e2e-*-pass|fail.json`，kind 为 `advisor-e2e`。
- 嵌套 runtime evidence：`backend/dist/steward-advisor-e2e/runtime-evidence/steward-verify-runtime-*-pass|fail.json`。
- 本地 mock 模型请求日志：`backend/dist/steward-advisor-e2e/mock-advisor/requests.jsonl`。

可用 evidence 汇总器检查本机 advisor 证据：

```powershell
cd backend
go run ./cmd/steward verify evidence --dir .\dist\steward-advisor-e2e --require-passing --require-kind advisor-e2e --require-kind runtime --require-platform windows --require-check advisor_e2e.verify_runtime --require-check advisor_e2e.advisor_request_count --require-check advisor_e2e.timeout --require-check advisor_e2e.circuit_open --require-check advisor_e2e.circuit_short_circuit --require-check advisor_e2e.local_fallback --require-check advisor_e2e.failure_audit --require-check advisor_e2e.recovery --require-kind-check-platform runtime:s4.advisor.probe:windows --require-kind-check-platform runtime:s4.advisor.privacy_probe:windows
```

默认 mock 模式先验证一次 D0 模型调用和 D2 本地阻断，再注入一次超时和一次 HTTP 失败；达到阈值后，第三次失败探针必须被本地熔断且不增加模型请求数。熔断期间脚本还会创建事件并运行自主扫描，要求本地规则 proposal 与 fallback 审计仍然产生；冷却后恢复成功调用并清零失败状态。若要用真实外部或本地模型网关验收，可显式传入 `-UseExternalAdvisor -AdvisorBaseURL <base-url> -AdvisorModel <model> -AdvisorAPIKey <key>`；外部模式不注入故障，这会产生真实模型调用成本和外部依赖，仍建议只发送 D0/D1 探针数据。

这类 evidence 能证明 S4 advisor 的进程级配置加载、OpenAI-compatible HTTP 调用、strict runtime 可观测状态、D0 live 探针和 D2 隐私拦截链路；它不能替代真实外部或本地模型服务的长时间稳定性验收，也不能替代系统服务安装后的模型调用验收。

可选的本机真实进程 runtime watch 验证：

```powershell
.\deploy\run-steward-runtime-watch.ps1
```

该脚本会构建或使用当前平台的 `steward` 二进制，启动 `docker compose` 中的 Postgres，创建一个临时数据库，启动一个真实 `steward run` 后台进程，并运行：

```powershell
steward --api http://127.0.0.1:19480/api verify runtime --strict-security --watch-duration 8s --watch-interval 2s --write-probes ...
```

默认输出：

- Evidence：`backend/dist/steward-runtime-watch/steward-verify-runtime-watch-*-pass|fail.json`，kind 为 `runtime-watch`。
- 嵌套 runtime evidence：`backend/dist/steward-runtime-watch/runtime-evidence/steward-verify-runtime-*-pass|fail.json`。
- 运行日志：`backend/dist/steward-runtime-watch/node/logs/runtime-watch-node.log`。

可用 evidence 汇总器检查本机 watch 证据：

```powershell
cd backend
go run ./cmd/steward verify evidence --dir .\dist\steward-runtime-watch --require-passing --require-kind runtime-watch --require-kind runtime --require-platform windows --require-check runtime_watch.verify_runtime --require-check runtime_watch.heartbeat --require-check runtime.watch.heartbeat --require-check runtime.watch --require-check s3.sync.security.strict --require-check s4.write.autonomy_run --min-watch-duration 8s
```

默认 `8s/2s` 窗口用于快速证明“真实后台进程的心跳会推进、runtime watch 至少收集两个样本、首轮低风险写入探针有效”。最终 24 小时门槛可以使用同一脚本放大参数：

```powershell
.\deploy\run-steward-runtime-watch.ps1 -WatchDuration 24h -WatchInterval 5m
```

这类 evidence 比普通单次 health check 更接近“后台长时间运行”，但它仍不是系统服务管理器 evidence：它不证明 Windows Service、macOS LaunchAgent/LaunchDaemon 或 Linux systemd unit 已安装、重启后可恢复，也不证明三台真实设备各自完成 24 小时常驻。

可选的本机 S3/S4 readiness 汇总验证：

```powershell
.\deploy\run-steward-local-readiness.ps1
```

该脚本会为每次运行创建独立目录 `backend/dist/steward-local-readiness/run-*`，构建或使用当前平台 `steward` 二进制，依次调用本机 evidence 脚本，然后在同一 run 目录上执行：

```powershell
steward verify evidence --dir <run-root> --require-passing ...
```

默认会覆盖以下本机门禁：

- `dist-preflight`：五个 Windows/macOS/Linux 发布目标、每目标内置工作台、逐文件 SHA-256 和当前平台二进制 smoke。
- `postgres-e2e`：Postgres 集成测试。
- `service-preflight`：三端 service install strict dry-run、脱敏、验收建议和本地 advisor 配置预检。
- `service-env-preflight`：服务环境计划、密钥轮换、脱敏、离线 install plan 和不触碰 service manager 的预检。
- `pairing-bootstrap-preflight`：签名加密配对包、bootstrap、脱敏 service env plan 和下一步命令建议。
- `local-mesh` / `mesh`：本机三进程、三数据库、每节点发现另外两个签名候选、peer 同步、离线重新上线追赶、关系写入探针和 S4 低风险执行。
- `advisor-e2e` / `runtime`：真实 `steward run` 进程、本地 OpenAI-compatible mock、D0 live 探针和 D2 隐私阻断。
- `runtime-watch` / `runtime`：真实后台进程心跳推进、watch 样本和低风险写入探针。

默认输出：

- 汇总 evidence：`backend/dist/steward-local-readiness/run-*/steward-verify-local-readiness-*-pass|fail.json`，kind 为 `local-readiness`。
- 汇总 manifest：`backend/dist/steward-local-readiness/run-*/manifest.json`。
- 子脚本 evidence：同一 run 目录下的 `dist-preflight/`、`postgres-e2e/`、`service-preflight/`、`service-env-preflight/`、`pairing-bootstrap-preflight/`、`local-mesh/`、`advisor-e2e/`、`runtime-watch/`。

2026-07-11 当前工作树使用 Go `1.26.5` 的完整本机门禁已通过，最新证据位于 `backend/dist/steward-local-readiness/run-20260711T141852.4191853Z/`：8 个阶段全部通过，统一 manifest 收录 11 个 evidence 文件，执行 72 个 manifest 检查，并强制要求 10 个 kind、49 个 check 和 9 个 kind/check/platform 组合；wrapper 的 `local_readiness.manifest_contract` 会反向确认动态要求未在 PowerShell 参数构造中丢失。

门禁覆盖后台循环、S3 设备身份/权限与同步变更历史契约、S4 严格策略、有界重试和策略变更屏障，以及 advisor 的 D0/D2 探针、超时、熔断、回退、失败审计和恢复。Postgres E2E 已验证同步故障隔离与恢复、畸形 change 逐条拒绝且水位不卡住、非法设备/自主策略输入不污染持久化状态、高风险与历史脏提案无法进入执行器、失败退避与人工恢复；并连续验证暂停/规则更新等待在途扫描和执行结束，更新返回后旧授权不再生效，当前 `never`、禁用或降权规则能撤销既有 `auto` 提案的执行资格。dist preflight、服务安装/环境预检、配对 bootstrap、三进程 mesh、advisor E2E 和 runtime watch 均通过。该证据仍只代表 Windows 宿主机上的本地多进程门禁，不能代替三台真实设备和 24 小时 system service evidence。

轻量 smoke 可跳过耗时较高的 Postgres、mesh 和 advisor 进程链路：

```powershell
.\deploy\run-steward-local-readiness.ps1 -SkipDistPreflight -SkipPostgresE2E -SkipLocalMesh -SkipAdvisorE2E -WatchDuration 6s -WatchInterval 2s
```

如果显式加 `-SkipRuntimeWatch`，汇总 manifest 不会要求 `--min-watch-duration`，因为该模式只适合验证安装、环境和配对预检链路。最终验收仍必须在真实 Windows、macOS、Linux 设备上分别收集 service、mesh、advisor 和 24 小时 watch evidence。

前端构建和测试：

```powershell
cd frontend
pnpm build
pnpm test
pnpm exec eslint src/components/tooling/StewardWorkspace.tsx src/lib/api/client.ts src/types/tooling.ts

cd ..
.\deploy\build-steward.ps1 -Version s3s4-local-verify -Clean
.\deploy\verify-steward-dist.ps1 -ExpectedVersion s3s4-local-verify -RunCurrentBinary
.\deploy\run-steward-postgres-e2e.ps1 -StartPostgres
.\deploy\run-steward-service-preflight.ps1
.\deploy\run-steward-service-env-preflight.ps1
.\deploy\run-steward-pairing-bootstrap-preflight.ps1
.\deploy\run-steward-local-mesh.ps1
.\deploy\run-steward-advisor-e2e.ps1
.\deploy\run-steward-runtime-watch.ps1
.\deploy\run-steward-local-readiness.ps1
```

运行时探针：

```powershell
go run ./cmd/steward --api http://127.0.0.1:18080/api doctor
go run ./cmd/steward version
go run ./cmd/steward --api http://127.0.0.1:18080/api status
go run ./cmd/steward --api http://127.0.0.1:18080/api sync-status
go run ./cmd/steward --api http://127.0.0.1:18080/api sync-device <peer-device-id>
go run ./cmd/steward --api http://127.0.0.1:18080/api devices list
go run ./cmd/steward --api http://127.0.0.1:18080/api devices register --id macbook-main --platform darwin --api-base-url http://192.168.1.12:18081/api --public-key "<peer public_key>"
go run ./cmd/steward --api http://127.0.0.1:18080/api devices permissions macbook-main
go run ./cmd/steward --api http://127.0.0.1:18080/api devices permission-set macbook-main sync.memory deny A2
go run ./cmd/steward --api http://127.0.0.1:18080/api devices verify macbook-main
go run ./cmd/steward --api http://127.0.0.1:18080/api devices revoke macbook-main
go run ./cmd/steward pairing keygen --label macbook-main
go run ./cmd/steward pairing export --id windows-main --api-base-url http://192.168.1.10:18081/api --public-key "<local public_key>"
go run ./cmd/steward pairing export --id windows-main --api-base-url http://192.168.1.10:18081/api --public-key "<local public_key>" --include-sync-encryption-key --sync-encryption-key "<home-sync-v2 key>" --sync-encryption-key-id home-sync-v2 --include-sync-encryption-previous-keys --sync-encryption-previous-keys "home-sync-v1:<old-key>" --encrypt-shared-sync-for "<recipient pairing public_key>"
go run ./cmd/steward --api http://127.0.0.1:18080/api pairing import --file .\windows-main.pairing.json --dry-run --require-signature
go run ./cmd/steward --api http://127.0.0.1:18080/api pairing import --file .\windows-main.encrypted.pairing.json --decrypt-shared-sync-key "<recipient pairing private_key>" --dry-run --require-signature
go run ./cmd/steward service env plan --from-pairing .\windows-main.encrypted.pairing.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --strict-security
go run ./cmd/steward service env apply --from-pairing .\windows-main.encrypted.pairing.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --strict-security --confirm
go run ./cmd/steward service env apply --from-pairing .\windows-main.encrypted.pairing.json --decrypt-shared-sync-key "<recipient pairing private_key>" --require-signature --strict-security --confirm --restart
go run ./cmd/steward service env plan --rotate-sync-key-id home-sync-v2 --rotate-local-key-id windows-local-v2 --strict-security
go run ./cmd/steward service env apply --rotate-sync-key-id home-sync-v2 --rotate-local-key-id windows-local-v2 --strict-security --confirm --restart --verify --verify-evidence-dir .\evidence\s3s4
go run ./cmd/steward service env apply --rotate-sync-key-id home-sync-v2 --rotate-local-key-id windows-local-v2 --strict-security --confirm --restart --verify --verify-advisor-probe --verify-evidence-dir .\evidence\s3s4
go run ./cmd/steward --api http://127.0.0.1:18080/api pairing verify macbook-main
go run ./cmd/steward keygen --prefix windows-main
go run ./cmd/steward sync-keygen --key-id home-sync-v1
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --strict-security --write-probes
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --advisor-probe
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --advisor-privacy-probe
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --strict-security --watch-duration 24h --watch-interval 5m --evidence-dir .\evidence\s3s4
go run ./cmd/steward --api http://127.0.0.1:18080/api verify runtime --advisor-probe --advisor-probe-each-sample --watch-duration 24h --watch-interval 30m --evidence-dir .\evidence\s3s4
go run ./cmd/steward --api http://127.0.0.1:18080/api verify service --strict-security --write-probes
go run ./cmd/steward --api http://127.0.0.1:18080/api verify service --advisor-probe
go run ./cmd/steward --api http://127.0.0.1:18080/api verify service --advisor-privacy-probe --evidence-dir .\evidence\s3s4
go run ./cmd/steward --api http://127.0.0.1:18080/api verify peers --strict --require-peers --sync
go run ./cmd/steward --api http://127.0.0.1:18080/api verify peers --strict --require-peers --sync --write-probes
go run ./cmd/steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --node http://127.0.0.1:38080/api --advisor-probe
go run ./cmd/steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --node http://127.0.0.1:38080/api --advisor-privacy-probe
go run ./cmd/steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --node http://127.0.0.1:38080/api --expect-advisor-provider openai-compatible --expect-advisor-model "<model-name>" --expect-advisor-max-data-level D1 --evidence-dir .\evidence\s3s4
go run ./cmd/steward verify evidence --dir .\evidence\s3s4 --preset s3s4-final-system --require-agent-id windows-main --require-agent-id macbook-main --require-agent-id linux-main --require-kind-platform-agent service:windows:windows-main --require-kind-platform-agent service:darwin:macbook-main --require-kind-platform-agent service:linux:linux-main --require-kind-platform-service-name service:windows:MongojsonSteward --require-kind-platform-service-name service:darwin:com.mongojson.steward --require-kind-platform-service-name service:linux:mongojson-steward --require-kind-platform-advisor-provider service:windows:openai-compatible --require-kind-platform-advisor-provider service:darwin:openai-compatible --require-kind-platform-advisor-provider service:linux:openai-compatible --require-kind-platform-advisor-model service:windows:<model-name> --require-kind-platform-advisor-model service:darwin:<model-name> --require-kind-platform-advisor-model service:linux:<model-name> --require-kind-platform-advisor-max-data-level service:windows:D1 --require-kind-platform-advisor-max-data-level service:darwin:D1 --require-kind-platform-advisor-max-data-level service:linux:D1 --require-kind-platform-agent mesh:windows:windows-main --require-kind-platform-agent mesh:darwin:macbook-main --require-kind-platform-agent mesh:linux:linux-main --require-kind-platform-agent s3s4-final-host:windows:windows-main --require-kind-platform-agent s3s4-final-host:darwin:macbook-main --require-kind-platform-agent s3s4-final-host:linux:linux-main --require-kind-platform-advisor-provider mesh:windows:openai-compatible --require-kind-platform-advisor-provider mesh:darwin:openai-compatible --require-kind-platform-advisor-provider mesh:linux:openai-compatible --require-kind-platform-advisor-model mesh:windows:<model-name> --require-kind-platform-advisor-model mesh:darwin:<model-name> --require-kind-platform-advisor-model mesh:linux:<model-name> --require-kind-platform-advisor-max-data-level mesh:windows:D1 --require-kind-platform-advisor-max-data-level mesh:darwin:D1 --require-kind-platform-advisor-max-data-level mesh:linux:D1 --output .\evidence\s3s4\manifest.json
go run ./cmd/steward --api http://127.0.0.1:18080/api autonomy dismiss-candidates --limit 100
go run ./cmd/steward service install --dry-run --agent-id windows-main --http-addr 127.0.0.1:18080 --peer-http-addr :18081 --public-api-base http://192.168.1.10:18081/api --sync-secret probe-secret --device-private-key probe-private-key --device-public-key probe-public-key --sync-encryption-key probe-sync-key --sync-encryption-key-id home-sync-v1 --sync-encryption-previous-keys "home-sync-v0:probe-old-key" --local-encryption-key probe-local-key --local-encryption-key-id windows-local-v1 --local-encryption-previous-keys "windows-local-v0:probe-old-local-key" --sync-interval 5m --autonomy-interval 15m --log-dir .\logs\steward
go run ./cmd/steward service install --dry-run --strict-security --agent-id windows-main --public-api-base http://192.168.1.10:18081/api --sync-secret "<24+ chars>" --device-private-key "<steward keygen private_key>" --device-public-key "<steward keygen public_key>" --sync-encryption-key "<steward sync-keygen key>" --sync-encryption-key-id home-sync-v1 --local-encryption-key "<local 32-byte base64 key>" --local-encryption-key-id windows-local-v1
go run ./cmd/steward service install --strict-security --agent-id windows-main --public-api-base http://192.168.1.10:18081/api --sync-secret "<24+ chars>" --device-private-key "<steward keygen private_key>" --device-public-key "<steward keygen public_key>" --sync-encryption-key "<steward sync-keygen key>" --sync-encryption-key-id home-sync-v1 --local-encryption-key "<local 32-byte base64 key>" --local-encryption-key-id windows-local-v1 --log-dir .\logs\steward --llm-provider openai-compatible --llm-base-url https://api.openai.com/v1 --llm-model "<model-name>" --llm-api-key "<api-key>" --start --verify --verify-advisor-probe --verify-advisor-privacy-probe
```

任务同步队列探针：

```powershell
$body = @{
  title = 'S3 sync runtime probe'
  description = 'created by S3/S4 verification'
  source = 'manual'
  data_level = 'D0'
  permission_level = 'A3'
  risk_level = 'low'
  user_confirmed = $true
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri http://127.0.0.1:18080/api/steward/tasks -ContentType 'application/json' -Body $body
Invoke-RestMethod http://127.0.0.1:18080/api/steward/sync/status
```

自主候选探针：

```powershell
$body = @{
  title = 'S4 autonomy runtime probe'
  summary = 'event should become a follow-up proposal'
  source = 'manual'
  data_level = 'D0'
  permission_level = 'A3'
  user_confirmed = $true
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri http://127.0.0.1:18080/api/steward/events -ContentType 'application/json' -Body $body
Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:18080/api/steward/autonomy/run?limit=5'
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:18080/api/steward/autonomy/proposals/bulk-dismiss -ContentType 'application/json' -Body (@{ status = 'candidate'; limit = 100; reason = 'verification cleanup' } | ConvertTo-Json)
```

## S3/S4 未完成验收

当前还不能把 S3/S4 标记为完整完成，原因：

- 尚未在 Windows、macOS、Linux 三端分别真实安装并验证长时间后台常驻。
- 尚未在三端真实服务管理器上验证 `service env plan/apply --strict-security` 写入环境、重启服务和密钥轮换后的长时间运行。
- 已有本机多临时数据库的真实 HTTP 同步集成测试、本机三进程 `steward run` mesh 验证，以及本机单进程 runtime watch evidence，覆盖签名候选发现、手动同步、后台 Daemon 自动同步、对端可见性、复杂关系写入探针、离线积压补齐、设备信任挑战、后台心跳推进和 S4 低风险执行；尚未在 Windows、macOS、Linux 三台真实设备上重复该验收。
- 后台同步故障域已由三数据库 E2E 验证：不可达 peer 不会阻断健康 peer、本地写入、heartbeat 或 autonomy，恢复后会自动清除错误并追平；尚未在真实三端网络断连、系统休眠和服务重启组合下收集长时间 evidence。
- 已有 OpenAI-compatible adapter 单元测试、模型输出 guardrail 单元测试、模型建议器失败熔断单元测试、D0 live 探针 API、D2 privacy probe API、`verify --advisor-probe` / `verify --advisor-privacy-probe` 验收入口、`--advisor-probe-each-sample` 长时间模型调用验收入口，以及本机真实 `steward run` + loopback OpenAI-compatible mock 端点的 advisor E2E evidence；尚未用真实外部或本地模型服务跑通实机 live 调用、超时、失败回退和长期稳定性。
- S4 业务执行失败已具备持久化失败计数、指数退避、自动尝试上限、耗尽阻断、显式人工重试和独立审计，并由 Postgres HTTP E2E 覆盖；尚未在三端真实后台服务中制造业务执行器故障并收集长时间恢复 evidence。
- 时间线段乱序补链、同名标签别名合并、来源引用与实体标签乱序重放和关系删除已有 Postgres 集成测试；仍尚未在真实三端下验证长时间离线重放和复杂删除顺序。
