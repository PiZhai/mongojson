# ADR-0006：R3.3 Privilege Broker 生产加固

- 状态：Accepted
- 关联：[ADR-0003](0003-steward-r3-privilege-broker.md)、[ADR-0004](0004-steward-r3-1-independent-approval.md)、[ADR-0005](0005-steward-r3-2-webauthn-approval.md)

## 背景

R3.0-R3.2 已建立独立 Broker、一次性审批证明和 WebAuthn，但生产边界仍有五个缺口：system service 可能从用户可写目录运行；Broker 秘密可能进入 SCM 环境；主 Steward 可使用执行密钥恢复急停；审计文件可被截断；Windows capability 在启动后才加入 Job，且继承完整服务令牌和环境。

## 决策

### Windows 服务边界

Windows system-scope 安装必须采用 hardened 模式：

- Broker 二进制复制到 `%ProgramFiles%\MongoJSON\StewardBroker`；
- policy、state、audit、checkpoint 和秘密文件位于 `%ProgramData%\MongoJSON\StewardBroker`；
- 信任域路径使用 protected DACL，Administrators 为 owner，只授权 SYSTEM 与 Administrators；专用 Service SID 不获得这些文件的 ACE，避免 capability 子进程借服务 SID 回读秘密；
- SCM 注册表环境只保存非敏感最小配置；client key、独立 control key 和 Broker signing private key进入受保护 JSON；
- 服务启用 per-service SID。Service SID 使用 unrestricted 类型以保留固定 A4-A7 capability 的资源 ACL；真正的 capability 子进程另使用 restricted token。

dry-run 不创建目录、不复制文件、不初始化 checkpoint，也不启动服务。正式安装顺序固定为受保护部署、复核目标 policy、显式初始化 checkpoint、再次收紧新文件 ACL，最后才允许启动。

### 独立恢复授权

Broker 使用两个不同的 HMAC 信任域：

- client key：状态、grant、execute 和紧急 stop；
- control key：只用于 resume，绝不进入 Steward 主进程环境。

恢复采用两阶段协议：独立管理员先把 Broker 恢复到 `current_generation + 1`；Steward 工作台随后只验证该签名状态并提交 Runtime V2/S4 的同代际恢复。主进程不能直接让 Broker resume，顺序错误时所有本地执行继续停止。

### 防回滚 checkpoint

签名 checkpoint 精确绑定 audit sequence、audit tail hash、state stopped/generation/changed_at。每次审计追加后原子刷新 checkpoint；启动时 checkpoint 缺失、签名错误、审计截断或 state 回滚一律 fail closed。旧部署必须显式运行 `initialize-checkpoint`，Broker 不在启动时静默创建信任锚。

checkpoint 与数据目录受 OS ACL 保护。能够同时回滚受保护目录及签名密钥的 SYSTEM/Administrators 攻击者超出本阶段本机软件边界；需要防管理员级回滚时应把锚迁入 TPM/HSM 或远程透明日志。

### capability 进程边界

Windows capability 以 `CREATE_SUSPENDED` 和 `CreateRestrictedToken(DISABLE_MAX_PRIVILEGE)` 创建；Administrators SID 被禁用，LocalSystem 服务运行时 SYSTEM user SID也被标记 deny-only。进程先加入 `KILL_ON_JOB_CLOSE` Job Object，再恢复，消除启动到 Job 分配之间的派生逃逸窗口。子进程仅获得系统 API 解析的 `SYSTEMROOT/WINDIR`；不继承 PATH、PATHEXT、TEMP、HOME、USERPROFILE 或 Broker 秘密。Unix 子进程只获得固定 locale。

Windows policy 只接受位于 Windows 或 Program Files 受保护根下、无 reparse component、owner/DACL 不向非受信 SID 开放写权限的 executable 与 working directory。Broker 执行前仍复核 SHA-256；普通用户无法在 hash 与 CreateProcess 之间替换受保护映像。Unix 同样拒绝任一父目录 group/other writable 的 capability。

### 故障语义

- stop 的状态落盘优先于审计，任何后续故障仍保持 stopped；
- resume 只有 state、audit 和 checkpoint 全部成功才对外可见；失败时回滚到原 stopped 状态；
- checkpoint 不一致导致 Broker 不启动，不自动修复或覆盖证据；
- 受保护安装失败留下的服务不得自动启动。

## 结果

Broker 协议升级为 `steward-privilege-broker/v1.2`。R3.1/R3.2 approval proof 格式保持兼容，但 R3.3 部署必须新增 control key 与 checkpoint，旧 Broker 需要显式迁移，不能滚动混用不同控制语义。
