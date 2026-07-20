# 全新 Windows 主机完整生产部署指南

本文用于在一台全新的 Windows 电脑上部署完整 Personal Tooling Platform 与私人智能管家。它以当前 `main` 分支的实际发布脚本为准，目标是完成一次可重启、可更新、可验收、可回滚的本机生产安装。

## 先看最终形态

推荐部署不是“全部放进 Docker”，而是把适合容器化的数据库放进 Docker，把必须接触 Windows 权限和登录桌面的管家原生安装：

```mermaid
flowchart LR
    Browser["浏览器<br/>完整 Web 工作台"] -->|"127.0.0.1:18080"| Main["MongojsonSteward<br/>LocalService + Restricted Service SID"]
    Main -->|"127.0.0.1:55439"| PG["PostgreSQL 17<br/>Docker named volume"]
    Main -->|"签名请求 127.0.0.1:18100"| Broker["MongojsonStewardBroker<br/>LocalSystem"]
    Broker --> Host["受限 System Tool Host<br/>Session 0 + Job Object"]
    Main -->|"认证 Named Pipe"| Companion["MongojsonStewardCompanion<br/>当前登录用户 + Limited"]
    Companion --> Desktop["窗口 / UIA / 键鼠 / 剪贴板 / 截图 / 系统通知"]
```

| 组件 | 部署方式 | 原因 |
|---|---|---|
| PostgreSQL 17 | Docker | 版本固定、数据卷清晰、备份和迁移简单 |
| 完整 Go 后端 | Windows Service | 它同时承载工具平台、Agent、后台采集和 Web UI，需要稳定的本机身份 |
| Privilege Broker | Windows Service / LocalSystem | 高权限工具必须经过独立密钥、策略、审计、受限 Token 和 Job Object |
| Session Companion | 当前用户登录任务 | 窗口、剪贴板、键鼠、截图、UI Automation 和通知只能在交互会话中可靠工作 |
| 前端 | 打包进原生发布物的 `ui/` | `steward.exe` 直接托管完整工作台，不需要额外 Nginx 或前端容器 |

### 为什么智能管家不能放进 Docker

Docker Desktop 默认运行 Linux 容器。Linux 容器不能可靠提供下列 Windows 原生边界：

- Windows Service Control Manager、Service SID 和 DACL。
- 独立 LocalSystem Broker、Session 0 和 Windows Restricted Token。
- Windows Job Object、进程树急停和受限能力进程。
- 当前登录用户的桌面、HKCU、窗口、UI Automation、键鼠、剪贴板、截图和系统通知。
- 只允许主服务、SYSTEM 与当前用户访问的认证 Named Pipe。

所以这里遵循“能容器化的基础设施优先 Docker，必须拥有 Windows 原生权限与会话能力的控制面原生安装”。这不是放弃隔离，而是把隔离建立在正确的操作系统边界上。

> **不要在原生管家安装后运行 `docker compose up` 或 `deploy-docker.ps1`。** 根 Compose 会再启动一个 Linux `backend` 并占用 `18080`；如果它连接同一数据库，还会同时运行第二套 Agent、Daemon 和后台任务。Windows 生产模式只启动 PostgreSQL 容器。

## 1. 支持范围与完成标准

推荐环境：

- Windows 11 x64；Windows 10 至少 Build 19041。
- x86-64 处理器，PowerShell 中架构应显示 `X64`。
- BIOS/UEFI 已开启硬件虚拟化。
- 至少 8 GB 内存，推荐 16 GB；至少预留 20 GB 可用磁盘。
- 当前登录用户可通过 UAC 执行管理员操作。
- 所有管理端口只绑定回环地址，不对局域网或互联网开放。

检查系统：

```powershell
[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
[System.Environment]::OSVersion.Version
Get-ComputerInfo -Property HyperVisorPresent,HyperVRequirementVirtualizationFirmwareEnabled
```

一次完整安装必须同时满足：

- `MongojsonSteward` 正在运行，账户为 `NT AUTHORITY\LocalService`，Service SID 为 `RESTRICTED`。
- `MongojsonStewardBroker` 正在运行，账户为 `LocalSystem`。
- `MongojsonStewardCompanion` 以当前登录用户、`RunLevel=Limited` 运行。
- PostgreSQL 17 容器为 `healthy`，且只监听 `127.0.0.1:55439`。
- `/healthz` 和 `/readyz` 返回成功。
- Broker 真实执行 `system.uptime`，返回工具结果与已审计的签名 receipt。
- 浏览器可访问完整工作台和私人管家。
- Windows 重启并重新登录后，再次通过上述检查。

## 2. 安装 Windows 基础软件

### 2.1 安装 PowerShell 7

在普通 Windows 终端中执行：

```powershell
winget install --exact --id Microsoft.PowerShell `
  --accept-package-agreements --accept-source-agreements
```

重新打开终端并检查：

```powershell
pwsh --version
```

为什么：仓库的安装、更新、卸载、发布和验收入口都是 PowerShell 脚本。PowerShell 7 的错误处理和 JSON 行为也比旧 Windows PowerShell 更一致。

### 2.2 启用 WSL 2

Docker Desktop 的 Linux 容器后端需要 WSL 2。管理员终端执行：

```powershell
wsl --install --no-distribution
wsl --update
```

如果系统提示需要重启，先重启，再检查：

```powershell
wsl --version
```

Docker 官方当前要求 WSL 2.1.5 或更高版本，并建议使用 WSL 2 后端。以官方要求为准：[Docker Desktop for Windows](https://docs.docker.com/desktop/setup/install/windows-install/)。

### 2.3 安装并初始化 Docker Desktop

```powershell
winget install --exact --id Docker.DockerDesktop `
  --accept-package-agreements --accept-source-agreements
```

安装后：

1. 启动 Docker Desktop。
2. 接受许可协议。
3. 选择 WSL 2/Linux containers 后端。
4. 在 Settings > General 中启用登录后自动启动。
5. 等待 Docker Desktop 显示 Engine running。

检查：

```powershell
docker version
docker compose version
docker info --format '{{.OSType}}'
```

最后一条应输出 `linux`。

> Docker Desktop 通常在用户登录后才完全启动，而 Windows 主服务在开机阶段启动。后文会为主服务配置失败恢复，并要求重启后验证数据库与服务的启动顺序。如果要求“无人登录时数据库也必须始终可用”，应把 PostgreSQL 放到独立服务器或改用原生 Windows PostgreSQL 服务；智能管家本身仍保持原生安装。

### 2.4 安装 .NET 8 Desktop Runtime（推荐）

```powershell
winget install --exact --id Microsoft.DotNet.DesktopRuntime.8 `
  --accept-package-agreements --accept-source-agreements
```

检查：

```powershell
dotnet --list-runtimes
```

为什么：发布包中的 Windows App SDK 通知 helper 目标为 `net8.0-windows`。没有该 Runtime 时核心服务仍可运行，并可回退到 PowerShell/WinRT 通知，但安装 Runtime 能让正式通知 helper 按预期工作。Microsoft 对 Runtime 与 SDK 的区别见 [.NET on Windows](https://learn.microsoft.com/dotnet/core/install/windows)。

### 2.5 管理员执行方式

安装 Windows 服务、写入 `Program Files`/`ProgramData` 和配置 DACL 必须通过 UAC 获取真实管理员 Token。

推荐做法是右键“PowerShell 7”，选择“以管理员身份运行”，然后检查：

```powershell
$principal = [Security.Principal.WindowsPrincipal]::new(
  [Security.Principal.WindowsIdentity]::GetCurrent()
)
$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
```

结果必须为 `True`。

Windows 11 24H2 或更高版本也可启用 Windows Sudo 后执行 `sudo pwsh -NoProfile`，UAC 确认后在管理员子进程中完成后续操作。官方说明见 [Sudo for Windows](https://learn.microsoft.com/windows/advanced-settings/sudo/)。

## 3. 预检端口和旧安装

后续固定使用以下端口：

| 端口 | 用途 | 绑定地址 |
|---:|---|---|
| `55439` | Docker PostgreSQL | `127.0.0.1` |
| `18080` | 完整工作台与管理 API | `127.0.0.1` |
| `18081` | 本机/受信设备 Peer API | `127.0.0.1` |
| `18100` | Privilege Broker | `127.0.0.1` |

检查占用：

```powershell
$ports = 55439,18080,18081,18100
Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
  Where-Object LocalPort -In $ports |
  Select-Object LocalAddress,LocalPort,OwningProcess
```

在真正的全新安装前，以下命令应没有结果：

```powershell
Get-Service MongojsonSteward,MongojsonStewardBroker -ErrorAction SilentlyContinue
Get-ScheduledTask MongojsonStewardCompanion -ErrorAction SilentlyContinue
```

如果已经存在 `MongojsonSteward`：

- 账户是 `NT AUTHORITY\LocalService`：不要重新安装，使用 `update-steward-production.ps1`。
- 账户是 `LocalSystem` 且没有新 Broker：这是旧版安装，使用 `migrate-steward-production.ps1`。
- 不确定来源：先备份数据库和 `C:\ProgramData\MongojsonSteward`，不要直接删除。

## 4. 使用 Docker 部署 PostgreSQL 17

本节只创建数据库容器，不启动仓库 Compose 中的 `backend`、`frontend` 或 `nginx`。

### 4.1 在同一个管理员 PowerShell 中生成数据库密码

```powershell
$bytes = [byte[]]::new(32)
[Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$pgAdminPassword = [Convert]::ToHexString($bytes).ToLowerInvariant()

[Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$pgAppPassword = [Convert]::ToHexString($bytes).ToLowerInvariant()

$pgContainer = 'mongojson-postgres'
$pgVolume = 'mongojson-postgres-data'
$pgPort = 55439
$pgDatabase = 'mongojson'
$pgUser = 'steward_app'
```

为什么使用十六进制随机密码：强度足够，并且可以直接安全放入 PostgreSQL URL，不需要再处理 `@`、`:`、`/` 等 URL 保留字符。

把两个密码保存到受信密码管理器。不要写进 Git、README、仓库 `.env` 或普通日志。Docker 容器环境对本机管理员可见，这符合本项目的单设备所有者模型，但不能防御已经取得本机管理员权限的攻击者。

### 4.2 创建数据库容器和持久卷

```powershell
docker pull postgres:17
docker volume create $pgVolume

docker run -d `
  --name $pgContainer `
  --restart unless-stopped `
  -e 'POSTGRES_DB=postgres' `
  -e 'POSTGRES_USER=postgres' `
  -e "POSTGRES_PASSWORD=$pgAdminPassword" `
  --publish "127.0.0.1:${pgPort}:5432" `
  --mount "source=${pgVolume},target=/var/lib/postgresql/data" `
  --health-cmd 'pg_isready -U postgres -d postgres' `
  --health-interval 5s `
  --health-timeout 5s `
  --health-retries 20 `
  postgres:17
```

为什么：

- `postgres:17` 与仓库当前版本一致，并固定主版本。
- named volume 在重新创建容器后仍保留数据。
- `--restart unless-stopped` 让 Docker Engine 恢复后自动恢复容器。
- `127.0.0.1:55439` 不把数据库暴露到局域网，同时避开常见的本机 `5432` 冲突。

### 4.3 等待数据库健康

```powershell
$deadline = (Get-Date).AddMinutes(2)
do {
  $health = docker inspect --format '{{.State.Health.Status}}' $pgContainer
  if ($health -eq 'healthy') { break }
  Start-Sleep -Seconds 2
} while ((Get-Date) -lt $deadline)

if ($health -ne 'healthy') {
  docker logs --tail 100 $pgContainer
  throw "PostgreSQL did not become healthy: $health"
}

docker exec $pgContainer pg_isready -U postgres -d postgres
Test-NetConnection 127.0.0.1 -Port $pgPort
```

### 4.4 创建应用专用角色和数据库

```powershell
docker exec $pgContainer psql -U postgres -d postgres -v ON_ERROR_STOP=1 `
  -c "CREATE ROLE $pgUser WITH LOGIN PASSWORD '$pgAppPassword';"

docker exec $pgContainer psql -U postgres -d postgres -v ON_ERROR_STOP=1 `
  -c "CREATE DATABASE $pgDatabase OWNER $pgUser;"

docker exec $pgContainer psql -U postgres -d $pgDatabase -v ON_ERROR_STOP=1 `
  -c 'CREATE EXTENSION IF NOT EXISTS pgcrypto;'

$databaseURL = "postgres://${pgUser}:${pgAppPassword}@127.0.0.1:${pgPort}/${pgDatabase}?sslmode=disable"
```

这里把 PostgreSQL 管理员与应用角色分开。`steward_app` 拥有自己的数据库，应用启动时会自动执行 Schema 迁移，但不需要成为整个 PostgreSQL 实例的管理员。

验证应用账号：

```powershell
docker exec -e "PGPASSWORD=$pgAppPassword" $pgContainer `
  psql -h 127.0.0.1 -U $pgUser -d $pgDatabase `
  -c 'select current_user, current_database(), version();'
```

## 5. 获取 Windows 发布物

生产机优先使用已经由受信构建机或 CI 生成并校验的 Windows 发布包。这样无需在高权限目标机长期安装编译器和前端工具链。

发布目录必须至少包含：

```text
steward.exe
steward-broker.exe
steward-approval.exe
steward-companion.exe
steward-system-tool-host.exe
windows-notifier\
ui\index.html
install-steward-production.ps1
update-steward-production.ps1
uninstall-steward-production.ps1
test-steward-production.ps1
test-steward-broker-session0.ps1
install-steward-companion.ps1
uninstall-steward-companion.ps1
rotate-steward-broker-keys.ps1
migrate-steward-production.ps1
verify-steward-dist.ps1
release-manifest.json
release-manifest.p7s
release-catalog.cat
SHA256SUMS.txt
```

发布者证书的 40 位 SHA-1 Thumbprint 必须通过独立可信渠道取得，例如组织发布页、管理员密码库或线下交付记录。不能从尚未验证的包内 Manifest 自己读取后再信任它。

假设下载并解压后的平台目录为：

```powershell
$downloadedRelease = 'C:\Deploy\MongojsonSteward\steward-<version>-windows-amd64'
$signingThumbprint = '<通过独立可信渠道取得的 40 位发布证书 Thumbprint>'
```

不要直接运行下载目录中的 EXE 或脚本。先定义下面的引导函数：它把整包复制到仅 SYSTEM 与 Administrators 可写的随机目录，检查 ACL 命令确实成功，先用 Windows 自带 Authenticode 校验固定发布者，再运行包内 verifier 完成 package-local Manifest、签名和全部 SHA-256 校验。

```powershell
function New-VerifiedStewardBootstrap {
  param(
    [Parameter(Mandatory)][string]$SourceDir,
    [Parameter(Mandatory)][string]$TrustedSignerThumbprint
  )

  $pin = ($TrustedSignerThumbprint -replace '\s','').ToUpperInvariant()
  if ($pin -notmatch '^[A-F0-9]{40}$') {
    throw '发布者 Thumbprint 必须是 40 位十六进制字符串。'
  }
  $source = (Resolve-Path -LiteralPath $SourceDir).Path
  $bootstrap = Join-Path $env:ProgramData ('MongojsonStewardBootstrap\' + [guid]::NewGuid().ToString('N'))
  try {
    New-Item -ItemType Directory -Force -Path $bootstrap | Out-Null
    & icacls.exe $bootstrap /inheritance:r | Out-Null
    if ($LASTEXITCODE -ne 0) { throw '无法关闭 bootstrap 目录继承。' }
    & icacls.exe $bootstrap /grant:r `
      '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw '无法保护 bootstrap 目录 ACL。' }

    Get-ChildItem -LiteralPath $source -Force |
      Copy-Item -Destination $bootstrap -Recurse -Force

    $required = @(
      'steward.exe','steward-broker.exe','steward-approval.exe',
      'steward-companion.exe','steward-system-tool-host.exe',
      'ui\index.html','install-steward-production.ps1',
      'test-steward-production.ps1','test-steward-broker-session0.ps1',
      'verify-steward-dist.ps1','release-manifest.json',
      'release-manifest.p7s','release-catalog.cat','SHA256SUMS.txt'
    )
    $missing = $required | Where-Object { -not (Test-Path -LiteralPath (Join-Path $bootstrap $_) -PathType Leaf) }
    if ($missing) { throw "Release is incomplete: $($missing -join ', ')" }

    $verifier = Join-Path $bootstrap 'verify-steward-dist.ps1'
    $signature = Get-AuthenticodeSignature -LiteralPath $verifier
    $actualSigner = if ($signature.SignerCertificate) {
      ($signature.SignerCertificate.Thumbprint -replace '\s','').ToUpperInvariant()
    } else { '' }
    if ($signature.Status -ne 'Valid' -or $actualSigner -ne $pin) {
      throw '包内 verifier 没有由预期的 Steward 发布证书签名。'
    }

    & $verifier `
      -DistDir $bootstrap `
      -TrustedSignerThumbprint $pin `
      -RequirePackageMode `
      -RunCurrentBinary | Out-Host
    if ($LASTEXITCODE -ne 0) { throw 'Windows 发布包完整性验证失败。' }
    return $bootstrap
  } catch {
    if (Test-Path -LiteralPath $bootstrap) {
      Remove-Item -LiteralPath $bootstrap -Recurse -Force
    }
    throw
  }
}

$release = New-VerifiedStewardBootstrap `
  -SourceDir $downloadedRelease `
  -TrustedSignerThumbprint $signingThumbprint
```

此后 `$release` 指向受保护且已验证的 bootstrap，而不是原始下载目录。在安装完成或失败后删除它。Windows 平台包的信任源是 `release-manifest.json`、`release-manifest.p7s`、带可信时间戳的 `release-catalog.cat`、包内 `SHA256SUMS.txt` 和固定发布者；Catalog 覆盖 Manifest、detached signature 及全部载荷，使发布证书到期后仍能按签名时刻验证。聚合构建根目录的 `manifest.json` 只用于多平台构建清单，不能替代生产包校验。

## 6. 可选：在这台机器从源码构建发布物

如果已经取得可信的预构建发布包，可跳过本节。

### 6.1 安装构建工具

```powershell
winget install --exact --id Git.Git --accept-package-agreements --accept-source-agreements
winget install --exact --id GoLang.Go --accept-package-agreements --accept-source-agreements
winget install --exact --id OpenJS.NodeJS.LTS --accept-package-agreements --accept-source-agreements
winget install --exact --id Microsoft.DotNet.SDK.8 --accept-package-agreements --accept-source-agreements
```

当前源码要求：

- `backend/go.mod` 指定 Go `1.26.5`。
- 前端正式构建以 Node 24 为基线。
- Windows 通知程序目标为 .NET 8。

检查：

```powershell
git --version
go version
node --version
npm --version
dotnet --version
```

Git、Go 和 .NET 的官方安装入口分别见 [Git for Windows](https://git-scm.com/install/windows)、[Go install](https://go.dev/doc/install) 和 [.NET on Windows](https://learn.microsoft.com/dotnet/core/install/windows)。

### 6.2 获取并锁定源码

```powershell
New-Item -ItemType Directory -Force C:\Projects | Out-Null
git clone https://github.com/PiZhai/mongojson.git C:\Projects\mongojson
Set-Location C:\Projects\mongojson
git checkout main
git pull --ff-only origin main
git status --short --branch
git rev-parse HEAD
```

构建前记录完整提交哈希，发布后可以证明二进制来自哪个版本。

### 6.3 安装依赖并测试

```powershell
npm --prefix frontend ci
npm --prefix frontend test

Push-Location backend
go mod download
go test ./... -count=1
Pop-Location
```

为什么先测试：生产安装器会把二进制放入高权限执行链。只有源代码测试通过后才应生成可安装发布物。

### 6.4 构建并验证 Windows 发布包

```powershell
$version = 'windows-' + (git rev-parse --short HEAD)
$distRoot = Join-Path $PWD "backend\dist\steward-$version"
$signingThumbprint = '<组织代码签名证书 SHA-1 Thumbprint>'

.\deploy\build-steward.ps1 `
  -Targets @('windows/amd64') `
  -Version $version `
  -OutputDir $distRoot `
  -SigningCertificateThumbprint $signingThumbprint `
  -TimestampServer 'http://timestamp.digicert.com' `
  -RequireSignedPackage `
  -Clean

.\deploy\verify-steward-dist.ps1 `
  -DistDir $distRoot `
  -RequiredTargets @('windows/amd64') `
  -ExpectedVersion $version `
  -RunCurrentBinary

$manifest = Get-Content (Join-Path $distRoot 'manifest.json') -Raw | ConvertFrom-Json
$artifact = $manifest.artifacts | Where-Object target -eq 'windows/amd64'
$builtRelease = Join-Path $distRoot (Split-Path $artifact.path -Parent)
$builtRelease

.\deploy\verify-steward-dist.ps1 `
  -DistDir $builtRelease `
  -ExpectedVersion $version `
  -TrustedSignerThumbprint $signingThumbprint `
  -RequirePackageMode `
  -RunCurrentBinary

$release = New-VerifiedStewardBootstrap `
  -SourceDir $builtRelease `
  -TrustedSignerThumbprint $signingThumbprint
```

构建脚本默认拒绝包含已修改或未跟踪文件的 Git worktree。生产签名模式不能跳过完整 Go 测试、前端测试、lint 或全新构建。每个平台目录会生成独立的 `release-manifest.json` 与 `SHA256SUMS.txt`，并对 Manifest 生成 CMS detached signature；Windows 包还会生成覆盖整包的 Catalog，并用同一证书做带可信时间戳的 Authenticode 签名。Steward 自有的 EXE 和安装脚本也使用同一证书和时间戳。验证脚本会检查可信发布者、时间戳、Catalog、Manifest 签名、每个文件的 SHA-256、目标架构、版本、Git commit、Go 版本以及当前 Windows 二进制的真实启动结果。

这里必须使用 `http://` 时间戳 URL。`Set-AuthenticodeSignature` 的底层 Windows API 不支持 HTTPS 时间戳端点；即使 PowerShell 接受 `https://` 参数，也会返回错误并留下没有时间戳的签名。时间戳响应本身由时间戳机构签名，发布验证仍会校验其证书链和签名。

没有代码签名证书的本地开发只能显式使用：

```powershell
$devDist = Join-Path $PWD 'backend\dist\steward-local-dev'
.\deploy\build-steward.ps1 -Targets @('windows/amd64') -OutputDir $devDist -Version local-dev -AllowDirtyWorktree
$devRelease = (Get-ChildItem $devDist -Directory -Filter '*windows-amd64' | Select-Object -First 1).FullName
.\deploy\verify-steward-dist.ps1 -DistDir $devRelease -AllowUnsignedPackage -AllowDirtyPackage -RunCurrentBinary
```

这种发布物会被标记为 `development_unsigned` 或 `development_dirty`，生产安装和更新默认拒绝。`-AllowUnsignedPackage` 与 `-AllowDirtyPackage` 只能用于本机开发，不能写入生产自动化。

## 7. 原生安装智能管家

### 7.1 先验收 Session 0 受限进程启动链

在管理员 PowerShell 7 中执行：

```powershell
& "$release\test-steward-broker-session0.ps1" -SourceDir $release
```

脚本会创建临时 Windows 服务，在真实 Session 0 中验证默认受限 Token、仅移除 privileges 的 Token 和 SYSTEM restricting SID 三条路径，然后自动清理。预期三个 case 都是 `ok: true`。

为什么安装前先测：这能在写入正式服务、策略和密钥之前发现操作系统 Token、DLL 加载、TEMP、PowerShell 路径或安全软件拦截问题。

### 7.2 执行全新生产安装

确认当前 PowerShell 仍然是管理员，并且第 4 节生成的 `$databaseURL` 与第 5/6 节生成的 `$release` 变量仍然存在：

```powershell
$installer = Join-Path $release 'install-steward-production.ps1'
$pin = ($signingThumbprint -replace '\s','').ToUpperInvariant()
$signature = Get-AuthenticodeSignature -LiteralPath $installer
$actualSigner = if ($signature.SignerCertificate) {
  ($signature.SignerCertificate.Thumbprint -replace '\s','').ToUpperInvariant()
} else { '' }
if ($signature.Status -ne 'Valid' -or $actualSigner -ne $pin) {
  throw '安装器没有由预期的 Steward 发布证书签名。'
}

try {
  & $installer `
    -SourceDir $release `
    -TrustedSignerThumbprint $pin `
    -DatabaseURL $databaseURL `
    -InstallCompanion `
    -Start `
    -Verify
} finally {
  if (Test-Path -LiteralPath $release) {
    Remove-Item -LiteralPath $release -Recurse -Force
  }
}
```

第 5 节已经把发布包复制到仅 SYSTEM 和 Administrators 可写的 bootstrap，并验证固定发布者和整包内容。安装器内部还会再次复制到独立管理员 staging、拒绝 reparse point、验证 package-local `release-manifest.json` 与所有哈希，随后只从已经验证的 staging/安装目录执行文件。生产模式不接受旧的发布根 `manifest.json`，也不接受未固定发布者的“系统信任证书”。首次安装会把固定发布者写入受 ACL 保护的 `C:\Program Files\MongojsonSteward\release-trust.json`。

> 全新机器只使用 `install-steward-production.ps1`。不要使用 `migrate-steward-production.ps1`；`migrate` 只用于把已有的旧 LocalSystem 单服务安装迁移到新隔离架构。

标准安装不需要手工提供 Broker policy、Broker 密钥、local encryption key 或 sync secret。安装器会自动：

1. 生成参数化 Broker policy。
2. 生成请求密钥、独立 stop/resume 控制密钥和 Ed25519 签名身份。
3. 生成主服务本地 AES key 与 sync secret。
4. 安装 `MongojsonStewardBroker` 为 LocalSystem 服务。
5. 安装 `MongojsonSteward` 为 LocalService，并启用 Restricted Service SID。
6. 把私密环境写入受 DACL 保护的 JSON，而不是 SCM 公开环境。
7. 给 System Tool Host 建立固定哈希策略和最小执行 ACL。
8. 为当前登录用户安装 Limited Companion 登录任务。
9. 启动服务并执行完整生产验收。

安装失败时，脚本会回滚本次创建的服务和 Companion，不会假装完成。

### 7.3 获取管理密钥并解锁浏览器

生产安装会为主服务生成独立高熵管理密钥，并把给当前登录用户使用的副本写入：

```text
%LOCALAPPDATA%\MongojsonSteward\management-access-token.txt
```

不要把密钥放进 URL、截图、日志或 PowerShell 历史。下面把它直接复制到剪贴板，不在终端明文显示：

```powershell
$managementTokenFile = Join-Path $env:LOCALAPPDATA 'MongojsonSteward\management-access-token.txt'
$managementToken = (Get-Content -Raw -LiteralPath $managementTokenFile).Trim()
if ($managementToken.Length -lt 32) { throw '管理密钥不存在或无效。' }
$managementToken | Set-Clipboard
$managementHeaders = @{ Authorization = "Bearer $managementToken" }
```

打开 `http://127.0.0.1:18080/`，把剪贴板内容粘贴到“解锁本机管理界面”。浏览器只用它换取随机的短期 Bearer 会话令牌，并只保存在当前页面内存中；不写 Cookie、`localStorage` 或 `sessionStorage`，刷新或关闭页面后需要重新解锁。这样可避免明文 loopback 上的 Cookie 被其他本机端口接收。会话过期或 API 返回 401 时，界面也会重新锁定。

PowerShell、CLI 或自动化调用 `/api/...` 时必须使用 `$managementHeaders`。`/healthz` 与 `/readyz` 保持匿名可读，但业务和管家 API 不允许匿名访问。

### 7.4 配置数据库晚启动恢复

Docker Desktop 可能在登录后才启动 PostgreSQL，而 `MongojsonSteward` 是开机服务。正式安装器和更新器会自动设置并由生产验收脚本检查以下策略；这些命令也可用于人工审计或修复旧安装：

```powershell
& sc.exe config MongojsonSteward start= delayed-auto
& sc.exe failure MongojsonSteward reset= 86400 `
  actions= restart/15000/restart/30000/restart/60000
& sc.exe failureflag MongojsonSteward 1
```

给 Broker 配置进程异常后的恢复：

```powershell
& sc.exe failure MongojsonStewardBroker reset= 86400 `
  actions= restart/5000/restart/15000/restart/30000
& sc.exe failureflag MongojsonStewardBroker 1
```

这些设置不会降低服务账户隔离，只负责在 Docker Engine 或进程暂时不可用后重新启动服务。

## 8. 安装后配置模型

打开：

```text
http://127.0.0.1:18080/tools/steward
```

点击“模型”，填写：

- 模型服务类型：OpenAI-compatible。
- Base URL：只填基础地址，不要附加 `/chat/completions`。
- Model：服务端真实支持的模型名。
- API Key。
- 请求超时、最大模型轮次、最大工具调用和最长运行时间。

先点击“测试连接”，成功后再“保存并生效”。

为什么安装后从 Web 配置：API Key 不会出现在 PowerShell 历史或 SCM 环境中；它会使用安装器生成的本机 encryption key 加密后持久化。不要把真实 API Key 写入本文、Git 或普通 `.env`。

模型连接测试只证明端点和凭据有效。还必须通过下一节的 Agent、Broker 和 Companion 执行验收。

## 9. 分层验收

### 9.1 数据库和 HTTP

```powershell
docker inspect --format '{{.State.Health.Status}}' mongojson-postgres
Invoke-RestMethod http://127.0.0.1:18080/healthz
Invoke-RestMethod http://127.0.0.1:18080/readyz
```

预期：

- Docker health 为 `healthy`。
- `/healthz` 返回 `status=ok`。
- `/readyz` 返回 `status=ready`，其中 database、storage、worker 和 steward daemon 均正常。

### 9.2 服务身份、端口与 Companion

```powershell
Get-CimInstance Win32_Service |
  Where-Object Name -In 'MongojsonSteward','MongojsonStewardBroker' |
  Select-Object Name,State,StartMode,StartName,ExitCode

& sc.exe qsidtype MongojsonSteward

Get-NetTCPConnection -State Listen |
  Where-Object LocalPort -In 18080,18081,18100 |
  Select-Object LocalAddress,LocalPort,OwningProcess

Get-ScheduledTask MongojsonStewardCompanion |
  Select-Object TaskName,State,@{n='RunLevel';e={$_.Principal.RunLevel}}

Get-Process steward-companion -IncludeUserName
```

任何 `18080/18081/18100` 监听都不应出现在 `0.0.0.0` 或外部网卡地址。

### 9.3 运行正式验收脚本

管理员 PowerShell：

```powershell
$installedRoot = 'C:\Program Files\MongojsonSteward'
& "$installedRoot\test-steward-production.ps1" -RequireCompanion
```

该脚本不仅看服务是否存在，还会核验：

- 主服务 LocalService 与 Restricted Service SID。
- Broker LocalSystem。
- 主/Broker 私密环境文件和 SCM 无密钥泄漏。
- 主服务和 Broker 私密环境文件启用继承保护，只允许 SYSTEM、Administrators 和对应 Service SID。
- System Tool Host 目录和参数化 policy。
- 真实执行 `system.uptime`。
- 工具输出、审计持久化和 Broker 签名 receipt 同时有效。

最终 JSON 必须是 `"ok": true`。不要只看某一项输出包含“signed”。

### 9.4 浏览器与真实对话

完整工作台：

```text
http://127.0.0.1:18080/
```

私人管家：

```text
http://127.0.0.1:18080/tools/steward
```

至少完成三次真实对话：

1. “读取当前 Windows 系统运行时间，并告诉我证据。”——验证模型到 Broker/System Tool Host。
2. “发送一条 Windows 系统通知，内容是‘管家部署验收成功’。”——验证 Companion 与通知。
3. “读取当前剪贴板文字并复述。”——在剪贴板放入无敏感测试文本后验证交互会话工具。

检查 Episode 卡片中的工具名、结果、证据和最终回答；“计划已创建”不能当作“任务已完成”。

### 9.5 重启 Windows 后再次验收

重启并登录同一用户。等待 Docker Desktop Engine running 后执行：

```powershell
docker inspect --format '{{.State.Health.Status}}' mongojson-postgres
Get-Service MongojsonSteward,MongojsonStewardBroker
Get-ScheduledTask MongojsonStewardCompanion
Invoke-RestMethod http://127.0.0.1:18080/readyz
```

如果数据库已健康但主服务仍未运行：

```powershell
sudo pwsh -NoProfile -Command 'Start-Service MongojsonStewardBroker; Start-Service MongojsonSteward'
```

然后再次运行 `test-steward-production.ps1 -RequireCompanion`。只有重启后仍通过，才能认为整机部署完成。

## 10. 活动采集与主动管家

Windows 原生 `windows-activity` 采集器默认启用，每 15 秒记录前台进程、窗口标题和持续时间。它不记录按键内容、剪贴板、截图或音频。每日与每周归纳会在模型已配置时由主动内核执行。

强制做一次每日归纳验收：

```powershell
Invoke-RestMethod -Method Post `
  -Uri http://127.0.0.1:18080/api/steward/proactive/run `
  -Headers $managementHeaders `
  -ContentType 'application/json' `
  -Body '{"force":true,"cadence":"daily"}'

Invoke-RestMethod `
  -Uri 'http://127.0.0.1:18080/api/steward/proactive/runs?limit=20' `
  -Headers $managementHeaders
```

### 可选 ActivityWatch

内置采集已经能提供应用和窗口活动。只有需要更准确的 AFK 状态或浏览器 URL 时才安装 ActivityWatch。

ActivityWatch 必须原生安装在当前用户会话中，而不是 Docker：它需要系统托盘、窗口 watcher 和浏览器扩展。Windows 官方安装器会配置登录自启动；官方指南见 [ActivityWatch Getting Started](https://docs.activitywatch.net/en/latest/getting-started.html)。

安装后只允许管家读取回环 API，并在管家采集器设置中启用 `activitywatch-bridge`。默认端口通常是 `5600`；如果 Windows 排除端口与其冲突，可统一把 ActivityWatch 与 bridge 改到未占用的回环端口，例如 `6100`。

## 11. 日常维护

### 11.1 查看状态和日志

```powershell
Get-Service MongojsonSteward,MongojsonStewardBroker
docker ps --filter 'name=mongojson-postgres'

Get-Content 'C:\ProgramData\MongojsonSteward\logs\MongojsonSteward.log' -Tail 100
Get-Content "$env:LOCALAPPDATA\MongojsonSteward\companion.log" -Tail 100
docker logs --tail 100 mongojson-postgres
```

`C:\ProgramData\MongoJSON\StewardBroker` 内的 policy、密钥、state、audit 和 checkpoint 属于 Broker 信任域。不要手工修改、截断或放宽 ACL。

### 11.2 备份 PostgreSQL

```powershell
$backup = Join-Path $env:USERPROFILE (
  'Documents\mongojson-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.dump'
)

docker exec mongojson-postgres `
  pg_dump -U steward_app -d mongojson -Fc -f /tmp/mongojson.dump

docker cp mongojson-postgres:/tmp/mongojson.dump $backup
docker exec mongojson-postgres rm -f /tmp/mongojson.dump
Get-FileHash -Algorithm SHA256 $backup
```

同时备份：

- `C:\ProgramData\MongojsonSteward`。
- `C:\ProgramData\MongoJSON\StewardBroker`。
- `C:\Program Files\MongojsonSteward\release-manifest.json`、`SHA256SUMS.txt` 与 `release-trust.json`。
- `%LOCALAPPDATA%\MongojsonSteward\management-access-token.txt`；必须单独加密保存，不能与普通日志放在一起。

这些目录含密钥和审计，不应上传公共网盘或 Git；备份介质必须加密。

### 11.3 更新到新版本

先备份数据库和 ProgramData，再验证新的 Windows 发布目录：

```powershell
$newRelease = 'C:\Deploy\MongojsonSteward\steward-<new-version>-windows-amd64'
$installedUpdater = 'C:\Program Files\MongojsonSteward\update-steward-production.ps1'

sudo pwsh -NoProfile -Command `
  "& '$installedUpdater' -SourceDir '$newRelease'"
```

必须运行当前安装目录内的 Updater，不能执行新发布目录里尚未验证的脚本。Updater 从受保护的 `release-trust.json` 读取固定发布者；显式提供不同 Thumbprint 会被拒绝。它先把新包复制到同卷管理员 staging，在 staging 内验证新 verifier 的 Authenticode 签名、package-local Manifest 和全部文件哈希，全部通过后才停止服务。随后原子切换主目录和 Broker，刷新 System Tool Host 哈希。已安装 Companion 时会停止任务和进程、备份整个用户目录后原子更新；未安装 Companion 时保持未安装，除非显式传入 `-InstallCompanion`。任一步失败都会恢复主程序、Broker、policy、Companion 目录与原计划任务。

### 11.4 旧版单服务迁移

仅当旧 `MongojsonSteward` 仍以 LocalSystem 运行、尚未安装新 Broker 时使用：

```powershell
$downloadedMigrationRelease = 'C:\Deploy\MongojsonSteward\steward-<version>-windows-amd64'
$migrationRelease = New-VerifiedStewardBootstrap `
  -SourceDir $downloadedMigrationRelease `
  -TrustedSignerThumbprint $signingThumbprint
$migration = Join-Path $migrationRelease 'migrate-steward-production.ps1'
$migrationSignature = Get-AuthenticodeSignature -LiteralPath $migration
if ($migrationSignature.Status -ne 'Valid' -or
    (($migrationSignature.SignerCertificate.Thumbprint -replace '\s','') -ne ($signingThumbprint -replace '\s',''))) {
  throw '迁移器没有由预期的 Steward 发布证书签名。'
}
try {
  sudo pwsh -NoProfile -Command `
    "& '$migration' -SourceDir '$migrationRelease' -TrustedSignerThumbprint '$signingThumbprint' -InstallCompanion -Verify"
} finally {
  if (Test-Path -LiteralPath $migrationRelease) {
    Remove-Item -LiteralPath $migrationRelease -Recurse -Force
  }
}
```

全新机器和已经是 LocalService 的机器都不要执行它。

### 11.5 轮换 Broker 请求与恢复密钥

```powershell
sudo pwsh -NoProfile -Command `
  "& 'C:\Program Files\MongojsonSteward\rotate-steward-broker-keys.ps1'"
```

轮换会保留签名身份，以保持历史 audit/checkpoint 连续性。轮换前仍应备份 Broker 数据目录。

### 11.6 卸载

默认卸载程序但保留数据：

```powershell
sudo pwsh -NoProfile -Command `
  "& 'C:\Program Files\MongojsonSteward\uninstall-steward-production.ps1'"
```

只有明确要永久销毁主服务、Broker 与 Companion 数据时才使用：

```powershell
sudo pwsh -NoProfile -Command `
  "& 'C:\Program Files\MongojsonSteward\uninstall-steward-production.ps1' -RemoveData -RemoveCompanionData"
```

卸载脚本不会删除 PostgreSQL 容器或 volume。删除 `mongojson-postgres-data` 会永久删除对话、记忆、任务、工具目录元数据和证据索引，因此不应作为普通卸载步骤。

## 12. 常见故障

### Docker 健康，但 `/readyz` 连接数据库失败

```powershell
docker inspect --format '{{.State.Health.Status}}' mongojson-postgres
Test-NetConnection 127.0.0.1 -Port 55439
Get-Service MongojsonSteward
```

确认安装时的 `DATABASE_URL` 使用宿主机 `127.0.0.1:55439`，不能使用 Docker 内部服务名 `postgres:5432`。数据库恢复后启动主服务：

```powershell
sudo pwsh -NoProfile -Command 'Start-Service MongojsonSteward'
```

### `18080` 已被占用

```powershell
$listener = Get-NetTCPConnection -LocalPort 18080 -State Listen
$listener | Select-Object LocalAddress,OwningProcess
Get-Process -Id $listener.OwningProcess
docker ps --format 'table {{.Names}}\t{{.Ports}}'
```

最常见原因是误启动了 Compose `backend`。停止错误容器，不要同时运行两套后端。

### 模型测试成功，但对话说“模型暂时不可用”

模型弹窗的测试请求与后台服务实际设置必须一致。重新“保存并生效”，再查看主服务日志和模型状态。Base URL 只填基础地址，不包含 `/chat/completions`。

### Broker 显示未配置

全新安装必须使用 `install-steward-production.ps1 -Start -Verify`。不要手工复制 `steward.exe` 后直接 `run` 作为生产安装。检查：

```powershell
Get-Service MongojsonStewardBroker
Get-NetTCPConnection -LocalPort 18100 -State Listen
```

### Companion 不在线或没有桌面能力

Companion 必须在真实登录用户会话安装和运行：

```powershell
Get-ScheduledTask MongojsonStewardCompanion
Start-ScheduledTask MongojsonStewardCompanion
Get-Process steward-companion -IncludeUserName
```

不要以 SYSTEM、服务账户或无人值守 Session 0 安装 Companion。

### Session 0 测试失败

重新运行诊断矩阵：

```powershell
sudo pwsh -NoProfile -Command `
  "& 'C:\Program Files\MongojsonSteward\test-steward-broker-session0.ps1' -SourceDir 'C:\Program Files\MongojsonSteward'"
```

检查输出中 `default`、`privileges-only` 和 `system-restricting-sid` 的差异，并检查杀毒/EDR 是否拦截临时服务、受限 Token、Windows PowerShell 或 System Tool Host。不要通过关闭 Broker 校验或放宽密钥 ACL 来绕过。

## 13. 目录与入口速查

| 内容 | 默认位置 |
|---|---|
| 主程序 | `C:\Program Files\MongojsonSteward` |
| 固定发布者与安装信任记录 | `C:\Program Files\MongojsonSteward\release-trust.json` |
| 主数据、上传、日志、私密环境 | `C:\ProgramData\MongojsonSteward` |
| 主服务私密环境 | `C:\ProgramData\MongojsonSteward\config\service-secrets.json` |
| 主日志 | `C:\ProgramData\MongojsonSteward\logs\MongojsonSteward.log` |
| Broker 程序 | `C:\Program Files\MongoJSON\StewardBroker` |
| Broker policy/key/state/audit/checkpoint | `C:\ProgramData\MongoJSON\StewardBroker` |
| Companion 程序、密钥、日志 | `%LOCALAPPDATA%\MongojsonSteward` |
| 当前用户管理密钥 | `%LOCALAPPDATA%\MongojsonSteward\management-access-token.txt` |
| PostgreSQL 数据 | Docker volume `mongojson-postgres-data` |
| 完整工作台 | `http://127.0.0.1:18080/` |
| 私人管家 | `http://127.0.0.1:18080/tools/steward` |
| 健康检查 | `http://127.0.0.1:18080/healthz` |
| 就绪检查 | `http://127.0.0.1:18080/readyz` |

## 14. 最终检查表

- [ ] Windows x64、虚拟化和 WSL 2 正常。
- [ ] Docker Desktop 使用 Linux/WSL 2 后端并设置登录后启动。
- [ ] 只有 PostgreSQL 在 Docker 中运行，没有容器 backend/frontend/nginx。
- [ ] PostgreSQL 17 为 `healthy`，named volume 存在，只绑定 `127.0.0.1:55439`。
- [ ] Windows 发布物先进入受保护 bootstrap，固定发布者、package-local Manifest、SHA-256、版本和 commit 均通过。
- [ ] Session 0 三种 Token case 全部通过。
- [ ] 主服务是 LocalService + Restricted Service SID。
- [ ] Broker 是 LocalSystem，系统工具执行返回已审计签名 receipt。
- [ ] Companion 是当前用户 + Limited，并能执行通知/剪贴板等会话工具。
- [ ] `/healthz`、`/readyz` 和完整工作台正常。
- [ ] 管理密钥文件 ACL 正确，浏览器可解锁，匿名 `/api/...` 请求返回 401。
- [ ] 模型配置已测试、保存并完成至少一次多轮工具调用。
- [ ] Windows 原生活动采集和一次主动 daily 归纳已验证。
- [ ] 数据库、ProgramData、release trust、Manifest、checksum 和加密保存的管理密钥已建立备份。
- [ ] Windows 重启、用户重新登录后再次完成生产验收。

安全隔离的实现细节见 [R5.1 Windows 生产安装与权限隔离](steward-r5.1-windows-production-isolation.md)。
