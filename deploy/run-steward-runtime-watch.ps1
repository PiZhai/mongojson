param(
  [string]$BinaryPath = "",

  [string]$EvidenceDir = "",

  [int]$ManagementPort = 19480,

  [int]$PeerPort = 19481,

  [int]$PostgresHostPort = 5432,

  [int]$StartupTimeoutSeconds = 60,

  [string]$WatchDuration = "8s",

  [string]$WatchInterval = "2s",

  [string]$AgentID = "runtime-watch-node",

  [string]$SyncKeyID = "runtime-watch-sync-v1",

  [string]$LocalKeyID = "runtime-watch-local-v1",

  [switch]$SkipWriteProbes,

  [switch]$SkipStartPostgres,

  [switch]$KeepDatabase
)

$ErrorActionPreference = "Stop"
$PathSeparators = @([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)

function Require-Command {
  param([string]$Name)
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "Missing required command: $Name"
  }
}

function Resolve-RepoPath {
  param([string]$Path)
  return (Resolve-Path -LiteralPath $Path).Path
}

function Assert-ChildPath {
  param(
    [string]$Parent,
    [string]$Child,
    [string]$Label
  )
  $parentFull = [System.IO.Path]::GetFullPath($Parent).TrimEnd($PathSeparators)
  $childFull = [System.IO.Path]::GetFullPath($Child).TrimEnd($PathSeparators)
  $comparison = [System.StringComparison]::OrdinalIgnoreCase
  if (-not ($childFull.StartsWith($parentFull + [System.IO.Path]::DirectorySeparatorChar, $comparison) -or $childFull.StartsWith($parentFull + [System.IO.Path]::AltDirectorySeparatorChar, $comparison))) {
    throw "$Label is outside repository: $childFull"
  }
}

function Get-HostPlatform {
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
    return "windows"
  }
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)) {
    return "darwin"
  }
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)) {
    return "linux"
  }
  return "unknown"
}

function Get-HostArch {
  $arch = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString().ToLowerInvariant()
  switch ($arch) {
    "x64" { return "amd64" }
    "arm64" { return "arm64" }
    default { return $arch }
  }
}

function New-Timestamp {
  return (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmss.fffffffZ")
}

function New-Secret {
  $bytes = New-Object byte[] 32
  [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
  return [Convert]::ToBase64String($bytes)
}

function New-UniquePath {
  param(
    [string]$Directory,
    [string]$BaseName,
    [string]$Suffix
  )
  $path = Join-Path $Directory ($BaseName + $Suffix)
  for ($attempt = 2; Test-Path -LiteralPath $path; $attempt++) {
    $path = Join-Path $Directory ("$BaseName-$('{0:D2}' -f $attempt)$Suffix")
  }
  return $path
}

function Quote-Arg {
  param([string]$Value)
  if ($Value -notmatch '[\s"]') {
    return $Value
  }
  return '"' + ($Value -replace '"', '\"') + '"'
}

function Add-Check {
  param(
    [System.Collections.ArrayList]$Checks,
    [string]$ID,
    [string]$Status,
    [string]$Message,
    [object]$Detail = $null
  )
  $check = [ordered]@{
    id = $ID
    status = $Status
    message = $Message
  }
  if ($null -ne $Detail) {
    $check.detail = $Detail
  }
  [void]$Checks.Add([pscustomobject]$check)
}

function Invoke-DockerCompose {
  param([string[]]$Arguments)
  $output = & docker @Arguments 2>&1
  return [pscustomobject]@{
    exit_code = $LASTEXITCODE
    output = @($output | ForEach-Object { "$_" })
  }
}

function Start-ComposePostgres {
  param(
    [string]$RepoRoot,
    [int]$TimeoutSeconds,
    [System.Collections.ArrayList]$Checks
  )
  Require-Command "docker"
  Push-Location $RepoRoot
  try {
    $up = Invoke-DockerCompose @("compose", "up", "-d", "postgres")
    if ($up.exit_code -ne 0) {
      Add-Check $Checks "runtime_watch.compose_up" "error" "docker compose up -d postgres failed" @{ exit_code = $up.exit_code; output = $up.output }
      throw "docker compose up -d postgres failed with exit code $($up.exit_code)"
    }
    Add-Check $Checks "runtime_watch.compose_up" "ok" "postgres compose service start requested" $null

    $deadline = (Get-Date).ToUniversalTime().AddSeconds($TimeoutSeconds)
    $lastOutput = @()
    while ((Get-Date).ToUniversalTime() -lt $deadline) {
      $ready = Invoke-DockerCompose @("compose", "exec", "-T", "postgres", "pg_isready", "-U", "postgres", "-d", "mongojson")
      $lastOutput = $ready.output
      if ($ready.exit_code -eq 0) {
        Add-Check $Checks "runtime_watch.postgres_ready" "ok" "postgres compose service is ready" $null
        return
      }
      Start-Sleep -Seconds 2
    }
    Add-Check $Checks "runtime_watch.postgres_ready" "error" "postgres compose service did not become ready before timeout" @{ output = $lastOutput }
    throw "postgres compose service did not become ready before timeout"
  } finally {
    Pop-Location
  }
}

function Invoke-PostgresSQL {
  param(
    [string]$RepoRoot,
    [string]$SQL
  )
  Push-Location $RepoRoot
  try {
    $output = & docker compose exec -T postgres psql -U postgres -d postgres -v ON_ERROR_STOP=1 -c $SQL 2>&1
    if ($LASTEXITCODE -ne 0) {
      throw "psql failed with exit code $LASTEXITCODE`: $($output -join "`n")"
    }
  } finally {
    Pop-Location
  }
}

function Initialize-Database {
  param(
    [string]$RepoRoot,
    [string]$DatabaseName,
    [System.Collections.ArrayList]$Checks
  )
  Invoke-PostgresSQL -RepoRoot $RepoRoot -SQL "drop database if exists $DatabaseName with (force);"
  Invoke-PostgresSQL -RepoRoot $RepoRoot -SQL "create database $DatabaseName;"
  Add-Check $Checks "runtime_watch.database" "ok" "temporary Postgres database created" @{ database = $DatabaseName }
}

function Remove-Database {
  param(
    [string]$RepoRoot,
    [string]$DatabaseName
  )
  try {
    Invoke-PostgresSQL -RepoRoot $RepoRoot -SQL "drop database if exists $DatabaseName with (force);"
  } catch {
  }
}

function Invoke-StewardCommand {
  param(
    [string]$BinaryPath,
    [string[]]$Arguments
  )
  $output = & $BinaryPath @Arguments 2>&1
  return [pscustomobject]@{
    exit_code = $LASTEXITCODE
    output = @($output | ForEach-Object { "$_" })
    text = (($output | ForEach-Object { "$_" }) -join "`n")
  }
}

function Invoke-StewardJSON {
  param(
    [string]$BinaryPath,
    [string[]]$Arguments
  )
  $result = Invoke-StewardCommand -BinaryPath $BinaryPath -Arguments $Arguments
  if ($result.exit_code -ne 0) {
    throw "steward $($Arguments -join ' ') failed with exit code $($result.exit_code): $($result.text)"
  }
  try {
    return $result.text | ConvertFrom-Json
  } catch {
    throw "steward $($Arguments -join ' ') did not return JSON: $($result.text)"
  }
}

function Get-OrBuild-Binary {
  param(
    [string]$RepoRoot,
    [string]$BackendDir,
    [string]$EvidenceRoot,
    [string]$BinaryPath
  )
  if (-not [string]::IsNullOrWhiteSpace($BinaryPath)) {
    return (Resolve-Path -LiteralPath $BinaryPath).Path
  }

  Require-Command "go"
  $extension = ""
  if ((Get-HostPlatform) -eq "windows") {
    $extension = ".exe"
  }
  $binaryDir = Join-Path $EvidenceRoot "bin"
  New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null
  $outputPath = Join-Path $binaryDir ("steward-runtime-watch-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)
  Assert-ChildPath -Parent $RepoRoot -Child $outputPath -Label "Runtime watch binary"

  Push-Location $BackendDir
  try {
    go build -trimpath -o $outputPath ./cmd/steward
    if ($LASTEXITCODE -ne 0) {
      throw "go build ./cmd/steward failed with exit code $LASTEXITCODE"
    }
  } finally {
    Pop-Location
  }
  return $outputPath
}

function Stop-ProcessQuietly {
  param([System.Diagnostics.Process]$Process)
  if ($null -eq $Process) {
    return
  }
  try {
    if (-not $Process.HasExited) {
      $Process.Kill()
    }
    $Process.WaitForExit(5000) | Out-Null
  } catch {
  }
}

function Start-StewardProcess {
  param(
    [string]$BinaryPath,
    [string]$NodeRoot,
    [string]$LogDir,
    [hashtable]$Environment
  )
  $arguments = @("run", "--workdir", $NodeRoot, "--log-dir", $LogDir, "--service-name", $AgentID)
  $psi = [System.Diagnostics.ProcessStartInfo]::new()
  $psi.FileName = $BinaryPath
  $psi.Arguments = (($arguments | ForEach-Object { Quote-Arg $_ }) -join " ")
  $psi.WorkingDirectory = $NodeRoot
  $psi.UseShellExecute = $false
  $psi.CreateNoWindow = $true

  $env = $psi.EnvironmentVariables
  foreach ($key in $Environment.Keys) {
    $env[$key] = [string]$Environment[$key]
  }
  return [System.Diagnostics.Process]::Start($psi)
}

function Wait-StewardReady {
  param(
    [string]$ReadyURL,
    [System.Diagnostics.Process]$Process,
    [string]$LogDir,
    [int]$TimeoutSeconds
  )
  $deadline = (Get-Date).ToUniversalTime().AddSeconds($TimeoutSeconds)
  $lastError = ""
  while ((Get-Date).ToUniversalTime() -lt $deadline) {
    if ($Process.HasExited) {
      $logPath = Join-Path $LogDir ($AgentID + ".log")
      $tail = @()
      if (Test-Path -LiteralPath $logPath) {
        $tail = @(Get-Content -LiteralPath $logPath -Tail 30)
      }
      throw "steward process exited before ready with code $($Process.ExitCode): $($tail -join "`n")"
    }
    try {
      $response = Invoke-RestMethod -Method Get -Uri $ReadyURL -TimeoutSec 3
      if ($response.status -eq "ok" -or $response.status -eq "ready") {
        return
      }
      $lastError = "unexpected ready status $($response.status)"
    } catch {
      $lastError = $_.Exception.Message
    }
    Start-Sleep -Seconds 1
  }
  throw "steward process did not become ready before timeout: $lastError"
}

function Run-RuntimeWatchVerification {
  param(
    [string]$BinaryPath,
    [string]$APIBase,
    [string]$EvidenceDir,
    [string]$WatchDuration,
    [string]$WatchInterval,
    [string]$SyncKeyID,
    [string]$LocalKeyID,
    [bool]$IncludeWriteProbes
  )
  $args = @(
    "--api", $APIBase,
    "verify", "runtime",
    "--strict-security",
    "--watch-duration", $WatchDuration,
    "--watch-interval", $WatchInterval,
    "--evidence-dir", $EvidenceDir,
    "--expect-agent-id", $AgentID,
    "--expect-agent-platform", (Get-HostPlatform),
    "--expect-sync-key-id", $SyncKeyID,
    "--expect-local-key-id", $LocalKeyID
  )
  if ($IncludeWriteProbes) {
    $args += "--write-probes"
  }
  return Invoke-StewardCommand -BinaryPath $BinaryPath -Arguments $args
}

function Test-VerificationCheck {
  param(
    [object]$VerificationPayload,
    [string]$ID
  )
  foreach ($check in @($VerificationPayload.verification.checks)) {
    if ($check.id -eq $ID -and $check.status -eq "ok") {
      return $true
    }
  }
  return $false
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-runtime-watch"
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$timestamp = New-Timestamp
$runID = ($timestamp -replace '[^0-9]', '').Substring(0, 14)
$databaseName = ("steward_watch_" + $runID).ToLowerInvariant()
$nodeRoot = Join-Path $evidenceRoot "node"
$storageDir = Join-Path $nodeRoot "data"
$logDir = Join-Path $nodeRoot "logs"
New-Item -ItemType Directory -Force -Path $storageDir, $logDir | Out-Null

$startedAt = (Get-Date).ToUniversalTime()
$checks = New-Object System.Collections.ArrayList
$binary = ""
$stewardProcess = $null
$runtimeResult = $null
$runtimePayload = $null
$errorMessage = ""

try {
  if (-not $SkipStartPostgres) {
    Start-ComposePostgres -RepoRoot $repoRoot -TimeoutSeconds $StartupTimeoutSeconds -Checks $checks
  }

  $binary = Get-OrBuild-Binary -RepoRoot $repoRoot -BackendDir $backendDir -EvidenceRoot $evidenceRoot -BinaryPath $BinaryPath
  Add-Check $checks "runtime_watch.binary" "ok" "steward runtime watch binary is available" @{ path = $binary }
  Invoke-StewardJSON -BinaryPath $binary -Arguments @("version") | Out-Null
  Add-Check $checks "runtime_watch.version" "ok" "steward binary returned version metadata" $null

  Initialize-Database -RepoRoot $repoRoot -DatabaseName $databaseName -Checks $checks

  $syncKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $SyncKeyID)
  $localKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $LocalKeyID)
  $deviceKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("keygen", "--prefix", $AgentID)

  $databaseURL = "postgres://postgres:postgres@localhost:$PostgresHostPort/$databaseName`?sslmode=disable"
  $apiBase = "http://127.0.0.1:$ManagementPort/api"
  $readyURL = "http://127.0.0.1:$ManagementPort/readyz"
  $env = @{
    "DATABASE_URL" = $databaseURL
    "HTTP_ADDR" = "127.0.0.1:$ManagementPort"
    "STORAGE_DIR" = $storageDir
    "STEWARD_AGENT_ID" = $AgentID
    "STEWARD_PEER_HTTP_ADDR" = "127.0.0.1:$PeerPort"
    "STEWARD_PUBLIC_API_BASE" = "http://127.0.0.1:$PeerPort/api"
    "STEWARD_SYNC_SECRET" = New-Secret
    "STEWARD_SYNC_REQUIRE_AUTH" = "true"
    "STEWARD_SYNC_ALLOW_INSECURE" = "false"
    "STEWARD_SYNC_ENCRYPTION_KEY" = $syncKey.key
    "STEWARD_SYNC_ENCRYPTION_KEY_ID" = $SyncKeyID
    "STEWARD_LOCAL_ENCRYPTION_KEY" = $localKey.key
    "STEWARD_LOCAL_ENCRYPTION_KEY_ID" = $LocalKeyID
    "STEWARD_DEVICE_PRIVATE_KEY" = $deviceKey.private_key
    "STEWARD_DEVICE_PUBLIC_KEY" = $deviceKey.public_key
    "STEWARD_HEARTBEAT_INTERVAL" = "1s"
    "STEWARD_SYNC_INTERVAL" = "2s"
    "STEWARD_AUTONOMY_INTERVAL" = "2s"
    "STEWARD_AUTONOMY_LIMIT" = "12"
    "STEWARD_LLM_PROVIDER" = "disabled"
  }

  $stewardProcess = Start-StewardProcess -BinaryPath $binary -NodeRoot $nodeRoot -LogDir $logDir -Environment $env
  Add-Check $checks "runtime_watch.process_started" "ok" "steward run process started for runtime watch" @{
    api_base = $apiBase
    watch_duration = $WatchDuration
    watch_interval = $WatchInterval
  }

  Wait-StewardReady -ReadyURL $readyURL -Process $stewardProcess -LogDir $logDir -TimeoutSeconds $StartupTimeoutSeconds
  Add-Check $checks "runtime_watch.ready" "ok" "steward management API reported ready" $null

  $runtimeEvidenceDir = Join-Path $evidenceRoot "runtime-evidence"
  New-Item -ItemType Directory -Force -Path $runtimeEvidenceDir | Out-Null
  $runtimeResult = Run-RuntimeWatchVerification -BinaryPath $binary -APIBase $apiBase -EvidenceDir $runtimeEvidenceDir -WatchDuration $WatchDuration -WatchInterval $WatchInterval -SyncKeyID $SyncKeyID -LocalKeyID $LocalKeyID -IncludeWriteProbes (-not $SkipWriteProbes)
  if ($runtimeResult.exit_code -eq 0) {
    Add-Check $checks "runtime_watch.verify_runtime" "ok" "runtime watch verification passed against a real steward process" @{ evidence_dir = $runtimeEvidenceDir }
    try {
      $runtimePayload = $runtimeResult.text | ConvertFrom-Json
      if (Test-VerificationCheck -VerificationPayload $runtimePayload -ID "runtime.watch.heartbeat") {
        Add-Check $checks "runtime_watch.heartbeat" "ok" "runtime watch evidence proves agent heartbeat advanced" $null
      } else {
        Add-Check $checks "runtime_watch.heartbeat" "error" "runtime watch heartbeat check was not present or not passing" $runtimePayload.verification.checks
      }
    } catch {
      Add-Check $checks "runtime_watch.output_parse" "error" "runtime watch output was not valid JSON" @{ output = $runtimeResult.output }
    }
  } else {
    Add-Check $checks "runtime_watch.verify_runtime" "error" "runtime watch verification failed" @{ exit_code = $runtimeResult.exit_code; output = $runtimeResult.output }
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "runtime_watch.runner" "error" $errorMessage $null
} finally {
  Stop-ProcessQuietly -Process $stewardProcess
  if (-not $KeepDatabase) {
    Remove-Database -RepoRoot $repoRoot -DatabaseName $databaseName
  }
}

$completedAt = (Get-Date).ToUniversalTime()
$hasFailingCheck = $false
foreach ($check in $checks) {
  if ($check.status -ne "ok") {
    $hasFailingCheck = $true
  }
}
$ok = ($errorMessage -eq "" -and -not $hasFailingCheck)
$status = "fail"
if ($ok) {
  $status = "pass"
}
$evidencePath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-verify-runtime-watch-$timestamp" -Suffix "-$status.json"

$payload = [ordered]@{
  verification = [ordered]@{
    ok = $ok
    platform = Get-HostPlatform
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    binary_path = $binary
    agent_id = $AgentID
    api_base = "http://127.0.0.1:$ManagementPort/api"
    database = $databaseName
    watch_duration = $WatchDuration
    watch_interval = $WatchInterval
    write_probes = (-not $SkipWriteProbes)
    sync_key_id = $SyncKeyID
    local_key_id = $LocalKeyID
    runtime_exit_code = if ($null -ne $runtimeResult) { $runtimeResult.exit_code } else { $null }
    runtime_output = if ($null -ne $runtimeResult) { $runtimeResult.output } else { $null }
    error = $errorMessage
    checks = @($checks)
  }
}

$command = @(
  "deploy/run-steward-runtime-watch.ps1",
  "-ManagementPort", "$ManagementPort",
  "-PeerPort", "$PeerPort",
  "-AgentID", $AgentID,
  "-WatchDuration", $WatchDuration,
  "-WatchInterval", $WatchInterval,
  "-SyncKeyID", $SyncKeyID,
  "-LocalKeyID", $LocalKeyID
)
if ($SkipWriteProbes) {
  $command += "-SkipWriteProbes"
}

$envelope = [ordered]@{
  kind = "runtime-watch"
  ok = $ok
  command = $command
  created_at = $startedAt.ToString("o")
  payload = $payload
}
$envelope | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $evidencePath -Encoding UTF8

$summary = [ordered]@{
  ok = $ok
  platform = Get-HostPlatform
  evidence_path = $evidencePath
  binary_path = $binary
  api_base = "http://127.0.0.1:$ManagementPort/api"
  watch_duration = $WatchDuration
  watch_interval = $WatchInterval
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 6

if (-not $ok) {
  exit 1
}
