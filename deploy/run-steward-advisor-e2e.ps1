param(
  [string]$BinaryPath = "",

  [string]$EvidenceDir = "",

  [int]$ManagementPort = 19380,

  [int]$PeerPort = 19381,

  [int]$AdvisorPort = 19382,

  [int]$PostgresHostPort = 5432,

  [int]$StartupTimeoutSeconds = 60,

  [string]$AgentID = "advisor-e2e-node",

  [string]$SyncKeyID = "advisor-e2e-sync-v1",

  [string]$LocalKeyID = "advisor-e2e-local-v1",

  [string]$AdvisorBaseURL = "",

  [string]$AdvisorModel = "advisor-e2e-model",

  [string]$AdvisorAPIKey = "",

  [string]$AdvisorMaxDataLevel = "D1",

  [switch]$UseExternalAdvisor,

  [switch]$AdvisorAllowNoAPIKey,

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
      Add-Check $Checks "advisor_e2e.compose_up" "error" "docker compose up -d postgres failed" @{ exit_code = $up.exit_code; output = $up.output }
      throw "docker compose up -d postgres failed with exit code $($up.exit_code)"
    }
    Add-Check $Checks "advisor_e2e.compose_up" "ok" "postgres compose service start requested" $null

    $deadline = (Get-Date).ToUniversalTime().AddSeconds($TimeoutSeconds)
    $lastOutput = @()
    while ((Get-Date).ToUniversalTime() -lt $deadline) {
      $ready = Invoke-DockerCompose @("compose", "exec", "-T", "postgres", "pg_isready", "-U", "postgres", "-d", "mongojson")
      $lastOutput = $ready.output
      if ($ready.exit_code -eq 0) {
        Add-Check $Checks "advisor_e2e.postgres_ready" "ok" "postgres compose service is ready" $null
        return
      }
      Start-Sleep -Seconds 2
    }
    Add-Check $Checks "advisor_e2e.postgres_ready" "error" "postgres compose service did not become ready before timeout" @{ output = $lastOutput }
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
  Add-Check $Checks "advisor_e2e.database" "ok" "temporary Postgres database created" @{ database = $DatabaseName }
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
  $outputPath = Join-Path $binaryDir ("steward-advisor-e2e-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)
  Assert-ChildPath -Parent $RepoRoot -Child $outputPath -Label "Advisor E2E binary"

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

function Write-MockAdvisorServer {
  param([string]$ScriptPath)
  $content = @'
param(
  [string]$Prefix,
  [string]$RequestLogPath,
  [string]$ReadyPath
)

$ErrorActionPreference = "Stop"
$listener = [System.Net.HttpListener]::new()
$listener.Prefixes.Add($Prefix)
$listener.Start()
Set-Content -LiteralPath $ReadyPath -Value "ready" -Encoding UTF8

try {
  while ($listener.IsListening) {
    $context = $listener.GetContext()
    $request = $context.Request
    $response = $context.Response
    $reader = [System.IO.StreamReader]::new($request.InputStream, $request.ContentEncoding)
    try {
      $body = $reader.ReadToEnd()
    } finally {
      $reader.Close()
    }

    if ($request.HttpMethod -ne "POST" -or $request.Url.AbsolutePath -ne "/v1/chat/completions") {
      $response.StatusCode = 404
      $bytes = [System.Text.Encoding]::UTF8.GetBytes('{"error":"not found"}')
      $response.ContentType = "application/json"
      $response.OutputStream.Write($bytes, 0, $bytes.Length)
      $response.Close()
      continue
    }

    $entry = [ordered]@{
      at = (Get-Date).ToUniversalTime().ToString("o")
      method = $request.HttpMethod
      path = $request.Url.AbsolutePath
      body = $body
    }
    Add-Content -LiteralPath $RequestLogPath -Value ($entry | ConvertTo-Json -Compress -Depth 20) -Encoding UTF8

    $content = [ordered]@{
      title = "advisor e2e local suggestion"
      summary = "local OpenAI-compatible advisor e2e response"
      trigger_reason = "runtime advisor probe reached the model endpoint"
      suggested_action = "create a low-risk local task draft only"
      impact_summary = "only candidate text is generated"
    } | ConvertTo-Json -Compress

    $payload = [ordered]@{
      id = "chatcmpl-steward-advisor-e2e"
      object = "chat.completion"
      created = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
      model = "advisor-e2e-model"
      choices = @(
        [ordered]@{
          index = 0
          message = [ordered]@{
            role = "assistant"
            content = $content
          }
          finish_reason = "stop"
        }
      )
    } | ConvertTo-Json -Compress -Depth 12

    $bytes = [System.Text.Encoding]::UTF8.GetBytes($payload)
    $response.StatusCode = 200
    $response.ContentType = "application/json"
    $response.OutputStream.Write($bytes, 0, $bytes.Length)
    $response.Close()
  }
} finally {
  if ($listener.IsListening) {
    $listener.Stop()
  }
  $listener.Close()
}
'@
  Set-Content -LiteralPath $ScriptPath -Value $content -Encoding UTF8
}

function Start-MockAdvisor {
  param(
    [string]$EvidenceRoot,
    [int]$Port,
    [int]$TimeoutSeconds,
    [System.Collections.ArrayList]$Checks
  )
  $serverDir = Join-Path $EvidenceRoot "mock-advisor"
  New-Item -ItemType Directory -Force -Path $serverDir | Out-Null
  $scriptPath = Join-Path $serverDir "mock-openai-compatible.ps1"
  $readyPath = Join-Path $serverDir "ready.txt"
  $requestLogPath = Join-Path $serverDir "requests.jsonl"
  Write-MockAdvisorServer -ScriptPath $scriptPath
  if (Test-Path -LiteralPath $readyPath) {
    Remove-Item -LiteralPath $readyPath -Force
  }
  if (Test-Path -LiteralPath $requestLogPath) {
    Remove-Item -LiteralPath $requestLogPath -Force
  }

  $prefix = "http://127.0.0.1:$Port/"
  $powershellPath = (Get-Process -Id $PID).Path
  $arguments = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $scriptPath, "-Prefix", $prefix, "-RequestLogPath", $requestLogPath, "-ReadyPath", $readyPath)
  $process = Start-Process -FilePath $powershellPath -ArgumentList $arguments -PassThru -WindowStyle Hidden
  $deadline = (Get-Date).ToUniversalTime().AddSeconds($TimeoutSeconds)
  while ((Get-Date).ToUniversalTime() -lt $deadline) {
    if ($process.HasExited) {
      Add-Check $Checks "advisor_e2e.mock_server" "error" "mock advisor server exited before ready" @{ exit_code = $process.ExitCode }
      throw "mock advisor server exited before ready with exit code $($process.ExitCode)"
    }
    if (Test-Path -LiteralPath $readyPath) {
      Add-Check $Checks "advisor_e2e.mock_server" "ok" "local OpenAI-compatible mock advisor is listening" @{ base_url = "http://127.0.0.1:$Port/v1" }
      return [pscustomobject]@{
        process = $process
        base_url = "http://127.0.0.1:$Port/v1"
        request_log_path = $requestLogPath
      }
    }
    Start-Sleep -Milliseconds 200
  }
  Add-Check $Checks "advisor_e2e.mock_server" "error" "mock advisor server did not become ready before timeout" $null
  throw "mock advisor server did not become ready before timeout"
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

function Run-RuntimeVerification {
  param(
    [string]$BinaryPath,
    [string]$APIBase,
    [string]$EvidenceDir,
    [string]$AdvisorModel,
    [string]$AdvisorMaxDataLevel,
    [string]$SyncKeyID,
    [string]$LocalKeyID
  )
  $args = @(
    "--api", $APIBase,
    "verify", "runtime",
    "--strict-security",
    "--advisor-probe",
    "--advisor-privacy-probe",
    "--evidence-dir", $EvidenceDir,
    "--expect-agent-id", $AgentID,
    "--expect-agent-platform", (Get-HostPlatform),
    "--expect-sync-key-id", $SyncKeyID,
    "--expect-local-key-id", $LocalKeyID,
    "--expect-advisor-provider", "openai-compatible",
    "--expect-advisor-model", $AdvisorModel,
    "--expect-advisor-max-data-level", $AdvisorMaxDataLevel
  )
  return Invoke-StewardCommand -BinaryPath $BinaryPath -Arguments $args
}

function Get-MockAdvisorRequestCount {
  param([string]$RequestLogPath)
  if ([string]::IsNullOrWhiteSpace($RequestLogPath) -or -not (Test-Path -LiteralPath $RequestLogPath)) {
    return 0
  }
  return @((Get-Content -LiteralPath $RequestLogPath) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }).Count
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-advisor-e2e"
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$timestamp = New-Timestamp
$runID = ($timestamp -replace '[^0-9]', '').Substring(0, 14)
$databaseName = ("steward_advisor_" + $runID).ToLowerInvariant()
$nodeRoot = Join-Path $evidenceRoot "node"
$storageDir = Join-Path $nodeRoot "data"
$logDir = Join-Path $nodeRoot "logs"
New-Item -ItemType Directory -Force -Path $storageDir, $logDir | Out-Null

$startedAt = (Get-Date).ToUniversalTime()
$checks = New-Object System.Collections.ArrayList
$binary = ""
$advisorProcess = $null
$stewardProcess = $null
$advisorRequestLogPath = ""
$runtimeResult = $null
$errorMessage = ""
$usingMockAdvisor = -not $UseExternalAdvisor

try {
  if (-not $SkipStartPostgres) {
    Start-ComposePostgres -RepoRoot $repoRoot -TimeoutSeconds $StartupTimeoutSeconds -Checks $checks
  }

  $binary = Get-OrBuild-Binary -RepoRoot $repoRoot -BackendDir $backendDir -EvidenceRoot $evidenceRoot -BinaryPath $BinaryPath
  Add-Check $checks "advisor_e2e.binary" "ok" "steward advisor e2e binary is available" @{ path = $binary }
  Invoke-StewardJSON -BinaryPath $binary -Arguments @("version") | Out-Null
  Add-Check $checks "advisor_e2e.version" "ok" "steward binary returned version metadata" $null

  Initialize-Database -RepoRoot $repoRoot -DatabaseName $databaseName -Checks $checks

  if ($usingMockAdvisor) {
    $mock = Start-MockAdvisor -EvidenceRoot $evidenceRoot -Port $AdvisorPort -TimeoutSeconds $StartupTimeoutSeconds -Checks $checks
    $advisorProcess = $mock.process
    $AdvisorBaseURL = $mock.base_url
    $advisorRequestLogPath = $mock.request_log_path
    $AdvisorAllowNoAPIKey = $true
  } else {
    if ([string]::IsNullOrWhiteSpace($AdvisorBaseURL)) {
      throw "-UseExternalAdvisor requires -AdvisorBaseURL"
    }
    Add-Check $checks "advisor_e2e.external_advisor" "ok" "external OpenAI-compatible advisor configuration selected" @{ base_url = $AdvisorBaseURL; model = $AdvisorModel }
  }

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
    "STEWARD_LLM_PROVIDER" = "openai-compatible"
    "STEWARD_LLM_BASE_URL" = $AdvisorBaseURL.TrimEnd("/")
    "STEWARD_LLM_MODEL" = $AdvisorModel
    "STEWARD_LLM_ALLOW_NO_API_KEY" = [string]::Format("{0}", [bool]$AdvisorAllowNoAPIKey).ToLowerInvariant()
    "STEWARD_LLM_MAX_DATA_LEVEL" = $AdvisorMaxDataLevel
    "STEWARD_LLM_TIMEOUT" = "10s"
    "STEWARD_LLM_FAILURE_THRESHOLD" = "2"
    "STEWARD_LLM_FAILURE_COOLDOWN" = "10s"
  }
  if (-not [string]::IsNullOrWhiteSpace($AdvisorAPIKey)) {
    $env["STEWARD_LLM_API_KEY"] = $AdvisorAPIKey
  }

  $stewardProcess = Start-StewardProcess -BinaryPath $binary -NodeRoot $nodeRoot -LogDir $logDir -Environment $env
  Add-Check $checks "advisor_e2e.process_started" "ok" "steward run process started with advisor configuration" @{
    api_base = $apiBase
    advisor_base_url = $AdvisorBaseURL
    model = $AdvisorModel
    mock_advisor = $usingMockAdvisor
  }

  Wait-StewardReady -ReadyURL $readyURL -Process $stewardProcess -LogDir $logDir -TimeoutSeconds $StartupTimeoutSeconds
  Add-Check $checks "advisor_e2e.ready" "ok" "steward management API reported ready" $null

  $runtimeEvidenceDir = Join-Path $evidenceRoot "runtime-evidence"
  New-Item -ItemType Directory -Force -Path $runtimeEvidenceDir | Out-Null
  $runtimeResult = Run-RuntimeVerification -BinaryPath $binary -APIBase $apiBase -EvidenceDir $runtimeEvidenceDir -AdvisorModel $AdvisorModel -AdvisorMaxDataLevel $AdvisorMaxDataLevel -SyncKeyID $SyncKeyID -LocalKeyID $LocalKeyID
  if ($runtimeResult.exit_code -eq 0) {
    Add-Check $checks "advisor_e2e.verify_runtime" "ok" "runtime advisor probe and privacy probe passed against a real steward process" @{ evidence_dir = $runtimeEvidenceDir }
  } else {
    Add-Check $checks "advisor_e2e.verify_runtime" "error" "runtime advisor verification failed" @{ exit_code = $runtimeResult.exit_code; output = $runtimeResult.output }
  }

  if ($usingMockAdvisor) {
    $requestCount = Get-MockAdvisorRequestCount -RequestLogPath $advisorRequestLogPath
    if ($requestCount -eq 1) {
      Add-Check $checks "advisor_e2e.advisor_request_count" "ok" "D0 advisor probe reached the model endpoint and D2 privacy probe was blocked locally" @{ request_count = $requestCount; request_log = $advisorRequestLogPath }
    } else {
      Add-Check $checks "advisor_e2e.advisor_request_count" "error" "unexpected mock advisor request count" @{ request_count = $requestCount; expected = 1; request_log = $advisorRequestLogPath }
    }
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "advisor_e2e.runner" "error" $errorMessage $null
} finally {
  Stop-ProcessQuietly -Process $stewardProcess
  Stop-ProcessQuietly -Process $advisorProcess
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
$evidencePath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-verify-advisor-e2e-$timestamp" -Suffix "-$status.json"

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
    advisor = [ordered]@{
      mock = $usingMockAdvisor
      base_url = $AdvisorBaseURL
      model = $AdvisorModel
      max_data_level = $AdvisorMaxDataLevel
      request_log_path = $advisorRequestLogPath
    }
    sync_key_id = $SyncKeyID
    local_key_id = $LocalKeyID
    runtime_exit_code = if ($null -ne $runtimeResult) { $runtimeResult.exit_code } else { $null }
    runtime_output = if ($null -ne $runtimeResult) { $runtimeResult.output } else { $null }
    error = $errorMessage
    checks = @($checks)
  }
}

$command = @(
  "deploy/run-steward-advisor-e2e.ps1",
  "-ManagementPort", "$ManagementPort",
  "-PeerPort", "$PeerPort",
  "-AdvisorPort", "$AdvisorPort",
  "-AgentID", $AgentID,
  "-AdvisorModel", $AdvisorModel,
  "-AdvisorMaxDataLevel", $AdvisorMaxDataLevel
)
if ($UseExternalAdvisor) {
  $command += @("-UseExternalAdvisor", "-AdvisorBaseURL", $AdvisorBaseURL)
}

$envelope = [ordered]@{
  kind = "advisor-e2e"
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
  advisor_base_url = $AdvisorBaseURL
  model = $AdvisorModel
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 6

if (-not $ok) {
  exit 1
}
