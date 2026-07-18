# 私人智能管家三端打包、安装与验证教程

> Windows 的当前生产安装已经升级为 LocalService 主服务、独立 LocalSystem Broker 和登录会话 Companion。Windows 全新机器应先按 [全新 Windows 主机完整生产部署指南](windows-fresh-production-deployment.md) 安装；本文保留的旧 Windows 单服务命令只用于理解历史跨平台流程，不能替代当前生产安装器。

本文给出 Windows、macOS 和 Linux 三台真实设备的完整部署路径，适用于当前仓库中的 `steward` CLI、后台服务和 Web 工作台。命令以仓库根目录为起点；Windows 使用 PowerShell，macOS/Linux 使用 PowerShell 7 (`pwsh`) 运行仓库提供的 `.ps1` 脚本。

> 当前发布物是“可移植目录包 + 原生系统服务注册”，不是带安装向导的 MSI、PKG、DMG、DEB 或 RPM。目录包内同时包含 `steward` 二进制和 `ui/`。真实安装会分别注册 Windows Service、macOS LaunchDaemon 或 Linux systemd system unit。

## 1. 部署结果与安全边界

完成后，每台设备都运行一个独立管家节点：

| 端口/资源 | 默认值 | 暴露范围 | 用途 |
| --- | --- | --- | --- |
| 管理 HTTP | `127.0.0.1:18080` | 仅本机 | CLI、工作台、运行状态、审批与审计 |
| Peer HTTP | `:18081` | 受信局域网或私有 VPN | 三端签名同步协议，不承载工作台 |
| PostgreSQL | `127.0.0.1:5432` | 仅本机 | 每个节点的持久数据 |
| Web 工作台 | `http://127.0.0.1:18080/tools/steward` | 仅本机 | 与管家沟通并查看记忆、任务和审计 |

必须遵守以下边界：

1. 不要把 `18080` 管理端口绑定到 `0.0.0.0` 或直接暴露到局域网/互联网。
2. `18081` 只允许在家庭 LAN、WireGuard、Tailscale 等可信网络中访问，并用主机防火墙限制来源。
3. 三台设备必须使用不同的 `STEWARD_AGENT_ID`、Ed25519 设备密钥和本地加密密钥。
4. 三台设备可以共享同步 HMAC secret 和同步 AES key；它们属于高敏感材料，不能写入 Git、inventory 或 evidence。
5. 最终高权限验收要求三端都安装为 `system` scope。用户级服务只能用于开发或受限部署。

## 2. 准备三台主机

推荐设备命名：

| 平台 | Agent ID | 服务名 | 建议同步 key id | 建议本地 key id |
| --- | --- | --- | --- | --- |
| Windows | `windows-main` | `MongojsonSteward` | `home-sync-v1` | `windows-local-v1` |
| macOS | `macbook-main` | `com.mongojson.steward` | `home-sync-v1` | `macbook-local-v1` |
| Linux | `linux-main` | `mongojson-steward` | `home-sync-v1` | `linux-local-v1` |

### 2.1 构建机

构建机需要：

- Git。
- 仓库当前 `go.mod` 所要求的 Go 版本。
- Node.js 和 npm，用于构建工作台。
- PowerShell 7，命令为 `pwsh`；Windows PowerShell 也可运行当前脚本。
- 足够空间保存五套目录产物。

检查工具：

```powershell
git --version
go version
node --version
npm --version
$PSVersionTable.PSVersion
```

### 2.2 每台目标机

每台目标机需要：

- PostgreSQL，且服务账户能访问本机数据库。
- PowerShell 7，用于推荐的一体化安装与 evidence 脚本。
- Windows 管理员、macOS `root`/`sudo`、Linux `root`/`sudo` 权限。
- 三端之间可达的可信 Peer 网络。
- 可选但推荐：一个 OpenAI-compatible 模型端点。最终 advisor 验收需要真实可用模型。

先确定架构：

```powershell
# Windows
[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture

# macOS / Linux
uname -m
```

`x86_64` 对应 `amd64`，Apple Silicon 和常见 ARM64 Linux 对应 `arm64`。

## 3. 构建三端发布包

### 3.1 构建五个目标

在仓库根目录执行：

```powershell
$version = "0.1.0-s3s4"
pwsh ./deploy/build-steward.ps1 -Version $version -Clean
```

脚本会先构建前端、运行主 CLI 和 companion 测试，然后交叉编译：

```text
backend/dist/steward/
  steward-0.1.0-s3s4-windows-amd64/
    steward.exe
    steward-broker.exe
    steward-companion.exe
    ui/index.html
    ui/assets/...
  steward-0.1.0-s3s4-darwin-amd64/
    steward
    steward-broker
    steward-companion
    ui/...
  steward-0.1.0-s3s4-darwin-arm64/
    steward
    steward-broker
    steward-companion
    ui/...
  steward-0.1.0-s3s4-linux-amd64/
    steward
    steward-broker
    steward-companion
    ui/...
  steward-0.1.0-s3s4-linux-arm64/
    steward
    steward-broker
    steward-companion
    ui/...
  manifest.json
  SHA256SUMS.txt
```

不要在正式发布时使用 `-SkipUI`，否则目标机没有随服务托管的工作台。

### 3.2 校验发布物

```powershell
pwsh ./deploy/verify-steward-dist.ps1 `
  -ExpectedVersion $version `
  -RunCurrentBinary
```

该命令会核对五个 target、主二进制、Privilege Broker、Companion、工作台、manifest、所有文件的 SHA-256 及当前平台主二进制版本。`-RunCurrentBinary` 只会运行当前构建机平台的 `steward version`，其他平台需在对应目标机再次执行。

### 3.3 生成可传输安装包

当前校验清单覆盖完整五目标目录，因此最稳妥的交付包是把整个 `backend/dist/steward` 打成一个归档，再由各目标机选取自己的目录。

Windows ZIP：

```powershell
$archive = "steward-$version-all-platforms.zip"
Compress-Archive -Path ./backend/dist/steward/* -DestinationPath $archive -Force
Get-FileHash -Algorithm SHA256 $archive
```

跨平台 TAR.GZ：

```powershell
tar -C ./backend/dist -czf "steward-$version-all-platforms.tar.gz" steward
Get-FileHash -Algorithm SHA256 "steward-$version-all-platforms.tar.gz"
```

通过受信渠道分发归档文件和归档 SHA-256。不要把服务配置、数据库密码、模型 API key 或配对私钥放进发布包。

### 3.4 在目标机再次核验

解包前先重新计算归档哈希，并与构建机记录逐字符比较：

```powershell
# Windows
Get-FileHash -Algorithm SHA256 ./steward-0.1.0-s3s4-all-platforms.zip

# macOS
shasum -a 256 ./steward-0.1.0-s3s4-all-platforms.tar.gz

# Linux
sha256sum ./steward-0.1.0-s3s4-all-platforms.tar.gz
```

解包后，在 `SHA256SUMS.txt` 中找到当前平台二进制的记录，再计算该文件哈希。随后运行 `steward version`，确认 `version`、`goos`、`goarch` 与目标机一致。归档或二进制任一哈希不一致都应停止安装并重新取得发布物。

## 4. 准备数据库

每台设备推荐使用自己的 PostgreSQL 数据库。示例仅展示本机连接，生产密码必须替换：

```sql
CREATE USER steward WITH PASSWORD '<strong-random-password>';
CREATE DATABASE steward OWNER steward;
```

连接串形式：

```text
postgres://steward:<url-encoded-password>@127.0.0.1:5432/steward?sslmode=disable
```

安装前从目标机验证：

```powershell
psql "<postgres-url>" -c "select current_database(), current_user, now();"
```

如果 PostgreSQL 不在本机，必须使用 TLS，并把数据库防火墙限制到对应设备。三端互通依赖管家的 Peer API，不需要三端共用一个数据库。

## 5. 生成身份与加密材料

在安全、不会录屏或上传终端历史的环境中操作。以下输出必须立即保存到密码管理器或受保护配置文件。

### 5.1 每台设备生成 Ed25519 身份

在各自目标目录中执行：

```powershell
# Windows
./steward.exe keygen --prefix windows-main

# macOS / Linux
chmod 0755 ./steward
./steward keygen --prefix macbook-main
```

Linux 使用 `linux-main`。把输出的 `private_key` 和 `public_key` 分别写入该设备配置；私钥不能跨设备复用。

### 5.2 生成共享同步密钥

只在一台可信设备生成一次，然后安全分发给三台设备：

```powershell
./steward sync-keygen --key-id home-sync-v1
```

三台设备使用相同的 `STEWARD_SYNC_ENCRYPTION_KEY` 和 `STEWARD_SYNC_ENCRYPTION_KEY_ID`。

### 5.3 每台设备生成本地存储密钥

每台设备再执行一次，key id 分别为 `windows-local-v1`、`macbook-local-v1`、`linux-local-v1`：

```powershell
./steward sync-keygen --key-id windows-local-v1
```

这些本地 key 不能跨设备复用。另生成一个至少 24 字符的随机 `STEWARD_SYNC_SECRET`，在三端安全共享，用于 Peer 请求的 HMAC 保护。

## 6. 创建受保护的服务配置

每台设备创建独立的扁平 JSON 文件，所有值都必须是字符串。Windows 示例：

```json
{
  "HTTP_ADDR": "127.0.0.1:18080",
  "STEWARD_PEER_HTTP_ADDR": ":18081",
  "DATABASE_URL": "postgres://steward:<password>@127.0.0.1:5432/steward?sslmode=disable",
  "STORAGE_DIR": "C:\\ProgramData\\MongojsonSteward\\data",
  "STEWARD_UI_DIR": "C:\\Program Files\\MongojsonSteward\\ui",
  "STEWARD_AGENT_ID": "windows-main",
  "STEWARD_PUBLIC_API_BASE": "http://10.10.0.11:18081/api",
  "STEWARD_SYNC_SECRET": "<shared-24-plus-character-secret>",
  "STEWARD_DEVICE_PRIVATE_KEY": "<this-device-ed25519-private-key>",
  "STEWARD_DEVICE_PUBLIC_KEY": "<this-device-ed25519-public-key>",
  "STEWARD_SYNC_ENCRYPTION_KEY": "<shared-sync-aes-key>",
  "STEWARD_SYNC_ENCRYPTION_KEY_ID": "home-sync-v1",
  "STEWARD_LOCAL_ENCRYPTION_KEY": "<this-device-local-aes-key>",
  "STEWARD_LOCAL_ENCRYPTION_KEY_ID": "windows-local-v1",
  "STEWARD_HEARTBEAT_INTERVAL": "1m",
  "STEWARD_COLLECTION_INTERVAL": "5m",
  "STEWARD_SYNC_INTERVAL": "5m",
  "STEWARD_AUTONOMY_INTERVAL": "15m",
  "STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS": "3",
  "STEWARD_AUTONOMY_RETRY_BACKOFF": "30s",
  "STEWARD_AUTONOMY_RETRY_MAX_BACKOFF": "15m",
  "STEWARD_LOG_DIR": "C:\\ProgramData\\MongojsonSteward\\logs",
  "STEWARD_LLM_PROVIDER": "openai-compatible",
  "STEWARD_LLM_BASE_URL": "https://api.openai.com/v1",
  "STEWARD_LLM_MODEL": "<model-name>",
  "STEWARD_LLM_API_KEY": "<api-key>",
  "STEWARD_LLM_TIMEOUT": "60s",
  "STEWARD_LLM_MAX_DATA_LEVEL": "D1",
  "STEWARD_LLM_FAILURE_THRESHOLD": "3",
  "STEWARD_LLM_FAILURE_COOLDOWN": "5m"
}
```

macOS/Linux 只需替换路径、agent id、本地 key id 和该设备的 Peer 地址。例如：

```text
macOS STORAGE_DIR=/Library/Application Support/MongojsonSteward/data
macOS STEWARD_UI_DIR=/usr/local/libexec/mongojson-steward/ui
Linux STORAGE_DIR=/var/lib/mongojson-steward/data
Linux STEWARD_UI_DIR=/opt/mongojson-steward/ui
```

`STEWARD_LLM_PROVIDER` 当前使用 `openai-compatible`。本地无鉴权模型端点需要额外设置 `STEWARD_LLM_ALLOW_NO_API_KEY` 为字符串 `"true"`；公网端点不得使用该选项。建议从 `D0` 或 `D1` 开始，不要在未完成隐私审查前提高数据级别。

### 6.1 保护 Windows 配置

在管理员 PowerShell 中：

```powershell
$config = "C:\ProgramData\MongojsonSteward\service-env.json"
icacls $config /inheritance:r
icacls $config /grant:r "*S-1-5-18:(R)" "*S-1-5-32-544:(R)"
icacls $config /remove:g "*S-1-5-32-545" "*S-1-5-11" "*S-1-1-0"
icacls $config
```

### 6.2 保护 macOS/Linux 配置

```bash
sudo chown root:wheel /Library/Application\ Support/MongojsonSteward/service-env.json
sudo chmod 0600 /Library/Application\ Support/MongojsonSteward/service-env.json
# Linux
sudo chown root:root /etc/mongojson-steward/service-env.json
sudo chmod 0600 /etc/mongojson-steward/service-env.json
```

真实安装脚本会拒绝权限过宽的配置。`-AllowInsecureConfigFile` 只允许配合 `-PlanOnly` 排错，不能绕过真实安装检查。

## 7. Windows 安装

### 7.1 解包到稳定路径

在管理员 PowerShell 中：

```powershell
$release = "C:\Program Files\MongojsonSteward"
$state = "C:\ProgramData\MongojsonSteward"
New-Item -ItemType Directory -Force $release, $state, "$state\data", "$state\logs", "$state\evidence" | Out-Null
Copy-Item -Recurse -Force ".\backend\dist\steward\steward-0.1.0-s3s4-windows-amd64\*" $release
& "$release\steward.exe" version
```

不要让服务指向下载目录、临时目录或会被自动清理的 evidence 目录。

### 7.2 先做无写入安装计划

```powershell
pwsh ./deploy/run-steward-service-install-e2e.ps1 `
  -PlanOnly `
  -BinaryPath "C:\Program Files\MongojsonSteward\steward.exe" `
  -WorkDir "C:\ProgramData\MongojsonSteward" `
  -ConfigFile "C:\ProgramData\MongojsonSteward\service-env.json" `
  -ServiceName MongojsonSteward `
  -ServiceScope system `
  -EvidenceDir "C:\ProgramData\MongojsonSteward\evidence\install-plan"
```

确认输出中的监听地址、agent id、数据库、key id、UI 路径和模型身份均正确；evidence 不应出现密钥明文。

### 7.3 注册并启动 Windows Service

```powershell
pwsh ./deploy/run-steward-service-install-e2e.ps1 `
  -ConfirmInstall `
  -BinaryPath "C:\Program Files\MongojsonSteward\steward.exe" `
  -WorkDir "C:\ProgramData\MongojsonSteward" `
  -ConfigFile "C:\ProgramData\MongojsonSteward\service-env.json" `
  -ServiceName MongojsonSteward `
  -ServiceScope system `
  -WatchDuration 24h `
  -WatchInterval 5m `
  -EvidenceDir "C:\ProgramData\MongojsonSteward\evidence\install"
```

该命令会真实注册、启动并严格验证服务，默认执行 advisor 可用性、不支持数据等级阻断和 24 小时 watch。首次调试可把 `WatchDuration` 临时改为 `10m`，但不能用短验证声称最终验收完成。

### 7.4 更新已安装服务

先构建并校验 Windows 发布目录，再从管理员 PowerShell 执行：

```powershell
pwsh ./deploy/update-steward-windows-service.ps1 `
  -SourceDir ".\backend\dist\steward\steward-0.1.0-s3s4-windows-amd64" `
  -InstallDir "C:\Program Files\MongojsonSteward" `
  -ServiceName MongojsonSteward
```

脚本会先拒绝缺少 `steward-broker.exe` 的不完整 Windows 发布目录，再停止现有服务，将整个程序目录改名为带时间戳的备份，复制新版本，重新启动并检查 `/healthz`。更新会把 Broker 二进制部署到程序目录，但不会擅自生成 policy/密钥或安装 Broker 服务；独立 Broker 的首次安装仍按 R3.3 流程显式完成。更新不会修改 `C:\ProgramData\MongojsonSteward`；新版本启动或健康检查失败时会自动恢复旧程序目录。

## 8. macOS 安装

### 8.1 选择并安装二进制目录

Apple Silicon 选择 `darwin-arm64`，Intel Mac 选择 `darwin-amd64`：

```bash
sudo mkdir -p /usr/local/libexec/mongojson-steward
sudo mkdir -p '/Library/Application Support/MongojsonSteward/'{data,logs,evidence}
sudo cp -R ./steward-0.1.0-s3s4-darwin-arm64/. /usr/local/libexec/mongojson-steward/
sudo chmod 0755 /usr/local/libexec/mongojson-steward/steward
/usr/local/libexec/mongojson-steward/steward version
```

如果 macOS 因下载隔离属性阻止运行，先确认归档 SHA-256 和来源，再检查并按组织策略移除隔离属性；不要在未验证来源时全局关闭 Gatekeeper。

### 8.2 计划并注册 LaunchDaemon

在仓库副本中运行：

```bash
sudo pwsh ./deploy/run-steward-service-install-e2e.ps1 \
  -PlanOnly \
  -BinaryPath /usr/local/libexec/mongojson-steward/steward \
  -WorkDir '/Library/Application Support/MongojsonSteward' \
  -ConfigFile '/Library/Application Support/MongojsonSteward/service-env.json' \
  -ServiceName com.mongojson.steward \
  -ServiceScope system \
  -EvidenceDir '/Library/Application Support/MongojsonSteward/evidence/install-plan'

sudo pwsh ./deploy/run-steward-service-install-e2e.ps1 \
  -ConfirmInstall \
  -BinaryPath /usr/local/libexec/mongojson-steward/steward \
  -WorkDir '/Library/Application Support/MongojsonSteward' \
  -ConfigFile '/Library/Application Support/MongojsonSteward/service-env.json' \
  -ServiceName com.mongojson.steward \
  -ServiceScope system \
  -WatchDuration 24h \
  -WatchInterval 5m \
  -EvidenceDir '/Library/Application Support/MongojsonSteward/evidence/install'
```

`system` scope 会注册 LaunchDaemon；不要把最终部署降级为只在登录后运行的 LaunchAgent。

## 9. Linux 安装

### 9.1 安装目录

amd64 选择 `linux-amd64`，ARM64 选择 `linux-arm64`：

```bash
sudo install -d -m 0755 /opt/mongojson-steward
sudo install -d -m 0700 /etc/mongojson-steward
sudo install -d -m 0750 /var/lib/mongojson-steward/{data,logs,evidence}
sudo cp -R ./steward-0.1.0-s3s4-linux-amd64/. /opt/mongojson-steward/
sudo chmod 0755 /opt/mongojson-steward/steward
/opt/mongojson-steward/steward version
```

### 9.2 计划并注册 systemd system unit

```bash
sudo pwsh ./deploy/run-steward-service-install-e2e.ps1 \
  -PlanOnly \
  -BinaryPath /opt/mongojson-steward/steward \
  -WorkDir /var/lib/mongojson-steward \
  -ConfigFile /etc/mongojson-steward/service-env.json \
  -ServiceName mongojson-steward \
  -ServiceScope system \
  -EvidenceDir /var/lib/mongojson-steward/evidence/install-plan

sudo pwsh ./deploy/run-steward-service-install-e2e.ps1 \
  -ConfirmInstall \
  -BinaryPath /opt/mongojson-steward/steward \
  -WorkDir /var/lib/mongojson-steward \
  -ConfigFile /etc/mongojson-steward/service-env.json \
  -ServiceName mongojson-steward \
  -ServiceScope system \
  -WatchDuration 24h \
  -WatchInterval 5m \
  -EvidenceDir /var/lib/mongojson-steward/evidence/install
```

## 10. 三端设备配对

安装成功不等于已经互信。每个目标设备必须显式登记另外两台设备，形成完整 mesh。

### 10.1 发送方导出签名配对包

三端安装配置已经通过受保护文件分发共享同步材料，因此普通设备登记不应再次把 HMAC/AES key 放入配对包。发送方只导出设备身份，并用本机 Ed25519 私钥签名。

例如 Windows 为 macOS 导出时，临时从受保护配置向当前进程注入四个必要值：

```powershell
$config = Get-Content -Raw "C:\ProgramData\MongojsonSteward\service-env.json" | ConvertFrom-Json
$env:STEWARD_AGENT_ID = $config.STEWARD_AGENT_ID
$env:STEWARD_PUBLIC_API_BASE = $config.STEWARD_PUBLIC_API_BASE
$env:STEWARD_DEVICE_PUBLIC_KEY = $config.STEWARD_DEVICE_PUBLIC_KEY
$env:STEWARD_DEVICE_PRIVATE_KEY = $config.STEWARD_DEVICE_PRIVATE_KEY
try {
  & "C:\Program Files\MongojsonSteward\steward.exe" pairing export `
    --output .\windows-for-mac.pairing.json
} finally {
  Remove-Item Env:STEWARD_AGENT_ID, Env:STEWARD_PUBLIC_API_BASE, Env:STEWARD_DEVICE_PUBLIC_KEY, Env:STEWARD_DEVICE_PRIVATE_KEY -ErrorAction SilentlyContinue
  Remove-Variable config
}
```

`pairing export --output` 会以 `0600` 语义创建文件，并在私钥存在时写入 bundle signature。不要把私钥作为 CLI 参数，也不要使用 shell 重定向保存包含共享材料的配对包。

### 10.2 接收方先 dry-run，再导入

把签名包通过受信渠道送到 macOS，先执行无写入检查：

```bash
/usr/local/libexec/mongojson-steward/steward \
  --api http://127.0.0.1:18080/api pairing import \
  --file ./windows-for-mac.pairing.json \
  --require-signature \
  --dry-run

/usr/local/libexec/mongojson-steward/steward \
  --api http://127.0.0.1:18080/api pairing import \
  --file ./windows-for-mac.pairing.json \
  --require-signature
```

检查 dry-run 中的 peer id、平台、公钥指纹、Peer API 和 A3 权限上限，再执行真实导入。对 Windows↔macOS、Windows↔Linux、macOS↔Linux 的两个方向重复操作，使每台设备都登记另外两台。

### 10.3 仅在密钥初次下发或轮换时传递共享材料

如果接收方尚未获得共享同步 key，先在接收方执行 `pairing keygen` 生成一次性 X25519 keypair。发送方使用 `pairing export --include-sync-encryption-key --encrypt-shared-sync-for <recipient-public-key>` 创建加密包，接收方用 `pairing import --decrypt-shared-sync-key <recipient-private-key> --require-signature --dry-run` 解密检查。为避免 key 出现在进程参数中，应通过受控进程环境提供 `STEWARD_SYNC_ENCRYPTION_KEY` 和 `STEWARD_PAIRING_PRIVATE_KEY`。

配对导入只登记 peer，不会静默更新服务密钥。对 `suggested_env` 先运行 `pairing bootstrap` 或 `service env plan --from-pairing`，再人工确认 `service env apply --strict-security --confirm --restart --verify`。完成后销毁一次性 X25519 私钥和含共享材料的临时配对包。

## 11. 安装后分层验证

以下命令都在各自目标机运行，并使用本机管理 API。

### 11.1 服务和本机 API

Windows：

```powershell
& "C:\Program Files\MongojsonSteward\steward.exe" service status --name MongojsonSteward --scope system
Invoke-RestMethod http://127.0.0.1:18080/healthz
Start-Process http://127.0.0.1:18080/tools/steward
```

macOS/Linux：

```bash
./steward service status --name <service-name> --scope system
curl --fail --silent http://127.0.0.1:18080/healthz
```

### 11.2 严格运行时验证

```powershell
./steward --api http://127.0.0.1:18080/api verify runtime `
  --strict-security `
  --write-probes `
  --advisor-probe `
  --advisor-privacy-probe `
  --evidence-dir ./evidence/runtime
```

成功条件包括：管理面仅 loopback、Peer 安全材料完整、数据库可用、低风险写入探针成功、D0 advisor 调用成功、不支持的 D7 数据在发送到模型前被阻断；D0-D6 按独立策略判断。

### 11.3 系统服务验证

```powershell
./steward --api http://127.0.0.1:18080/api verify service `
  --name <service-name> `
  --scope system `
  --strict-security `
  --write-probes `
  --advisor-probe `
  --advisor-privacy-probe `
  --evidence-dir ./evidence/service
```

### 11.4 Peer 同步验证

```powershell
./steward --api http://127.0.0.1:18080/api verify peers `
  --strict `
  --require-peers `
  --sync `
  --write-probes `
  --evidence-dir ./evidence/peers
```

确认每个 peer 的签名、公钥、agent id、同步 key id 和同步结果均为通过。不要因为“设备能 ping 通”就判定同步已完成。

## 12. 三端 24 小时最终验收

### 12.1 建立安全的管理面本地映射

`run-steward-s3s4-final-host.ps1` 需要从一台主机访问三个管理 API。不要把远端 `18080` 直接开放到 LAN；使用 SSH local forwarding、WireGuard/Tailscale 本机代理或等价隧道，把三端管理面映射为例如：

```text
Windows 本机: http://127.0.0.1:18080/api
macOS 映射:   http://127.0.0.1:28080/api
Linux 映射:   http://127.0.0.1:38080/api
```

### 12.2 每台设备采集 final-host evidence

Windows 示例：

```powershell
pwsh ./deploy/run-steward-s3s4-final-host.ps1 `
  -EvidenceDir C:\ProgramData\MongojsonSteward\evidence\final-host `
  -ServiceName MongojsonSteward `
  -ServiceScope system `
  -APIBase http://127.0.0.1:18080/api `
  -LocalAgentID windows-main `
  -LocalPlatform windows `
  -LocalSyncKeyID home-sync-v1 `
  -LocalLocalKeyID windows-local-v1 `
  -Node http://127.0.0.1:18080/api,http://127.0.0.1:28080/api,http://127.0.0.1:38080/api `
  -ExpectedAgentIDs windows-main,macbook-main,linux-main `
  -ExpectedPlatforms windows,darwin,linux `
  -ExpectedSyncKeyIDs home-sync-v1 `
  -ExpectedLocalKeyIDs windows-local-v1,macbook-local-v1,linux-local-v1 `
  -ExpectedAdvisorProvider openai-compatible `
  -ExpectedAdvisorModel "<model-name>" `
  -ExpectedAdvisorMaxDataLevel D1 `
  -WatchDuration 24h `
  -WatchInterval 5m
```

macOS/Linux 替换本机平台、agent id、本地 key id、服务名和 evidence 路径。最终验证不要使用 `-AllowIncompleteMesh`、`-SkipAdvisorProbe`、`-SkipAdvisorPrivacyProbe` 或用户级 scope。

### 12.3 汇总三台设备 evidence

把三个完整 final-host 运行目录复制到一台协调机，基于示例创建 inventory：

```powershell
Copy-Item ./deploy/steward-s3s4-final-system.example.json ./deploy/steward-s3s4-final-system.json
```

inventory 只填写平台、agent id、服务名、system scope、advisor 身份和 evidence 路径，禁止写入任何 key。先预览，再正式归档：

```powershell
pwsh ./deploy/run-steward-s3s4-final-system.ps1 `
  -InventoryFile ./deploy/steward-s3s4-final-system.json `
  -PlanOnly

pwsh ./deploy/run-steward-s3s4-final-system.ps1 `
  -InventoryFile ./deploy/steward-s3s4-final-system.json `
  -BinaryPath ./backend/dist/steward/steward-0.1.0-s3s4-windows-amd64/steward.exe `
  -EvidenceDir ./evidence/s3s4-final-system
```

最终成功目录应包含：

- `hosts/windows`、`hosts/darwin`、`hosts/linux` 三个来源副本。
- `reports/preflight-<platform>.json`。
- `final-manifest.json`。
- `steward-verify-s3s4-final-system-*-pass.json`。

只有三台真实主机、system service、完整 mesh、指定模型、严格隐私探针和至少 24 小时 watch 全部通过，才能判定三端最终验收完成。

## 13. 升级、回滚与卸载

### 13.1 升级

1. 构建新版本并完成 manifest/SHA 校验。
2. 备份数据库、服务配置和当前二进制目录。
3. 在目标机执行新版本 `steward version`。
4. 停止服务，原子替换整个程序目录，保留配置与数据目录。
5. 启动服务，执行 `verify service` 和 `verify peers --sync`。
6. 重大版本升级重新采集 24 小时 evidence。

```powershell
./steward service stop --name <service-name> --scope system
./steward service start --name <service-name> --scope system
```

回滚时恢复数据库兼容备份和上一版完整目录，不能只替换一个二进制而保留不匹配的 UI。

### 13.2 卸载服务

```powershell
./steward service stop --name <service-name> --scope system
./steward service uninstall --name <service-name> --scope system
```

卸载服务不会等同于安全删除数据库、日志、evidence 和密钥。确认备份和保留策略后再人工清理数据目录；密钥泄露时应执行轮换，而不是只删除本机文件。

## 14. 常见故障

| 现象 | 检查项 | 处理方向 |
| --- | --- | --- |
| `PlanOnly` 拒绝配置 | JSON 不是扁平字符串对象，或缺少 strict 字段 | 修正字段类型、agent id、Peer 地址和密钥材料 |
| 真实安装拒绝权限 | 非管理员/root，或配置文件可被普通用户读取 | 提升终端权限并收紧 ACL/`0600` |
| 服务已注册但启动失败 | PostgreSQL、工作目录、UI 路径、日志目录 | 查看原生服务状态及 `STEWARD_LOG_DIR` |
| 工作台打不开 | `18080` 未监听或 `ui/index.html` 缺失 | 检查服务状态、发布目录完整性和 `STEWARD_UI_DIR` |
| Peer 可达但同步失败 | HMAC secret、Ed25519 公钥、AES key id 不一致 | 重新核对配对包和三端 key id，不要关闭 strict 校验 |
| advisor probe 失败 | base URL、模型名、API key、代理或限流 | 先直接验证 OpenAI-compatible 端点，再检查熔断状态 |
| privacy probe 失败 | 不支持的 D7 数据被实际接受，或来源级采集/外发策略未生效 | 立即停止 advisor，保留 evidence 并排查策略门禁 |
| 24 小时验证中断 | 主机休眠、网络隧道断开、服务重启 | 解决稳定性问题后重新采集完整连续窗口 |

## 15. 最终检查表

- [ ] 五个目标目录、`manifest.json` 和 `SHA256SUMS.txt` 校验通过。
- [ ] 三端都从稳定目录运行正确架构的二进制。
- [ ] 三端 PostgreSQL 可用且不直接暴露公网。
- [ ] 管理面仅绑定 `127.0.0.1:18080`。
- [ ] Peer 面只对可信网络开放，防火墙已限制来源。
- [ ] 三端 agent id、设备密钥、本地 key 均不同。
- [ ] 共享同步 key 和 HMAC secret 已通过安全渠道分发。
- [ ] 服务配置 ACL/`0600` 合格，未进入 Git 或 evidence。
- [ ] 三端均以 system scope 安装、启动并通过 `verify service`。
- [ ] 每台设备均登记另外两台并通过 `verify peers --sync --write-probes`。
- [ ] 工作台可从每台设备本机打开。
- [ ] 指定模型的 D0 probe 成功，不支持的 D7 probe 被本地阻断；D0-D6 按来源级采集/外发策略执行。
- [ ] 三端 final-host 连续 24 小时通过。
- [ ] final-system 生成 `*-pass.json` 和统一 manifest。

更细的协议、权限、同步、模型隐私和 evidence 字段定义见 [S3/S4 运行与验证基线](personal-ai-steward-s3-s4-runtime.md)。
