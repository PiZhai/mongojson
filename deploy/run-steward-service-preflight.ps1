param(
  [string]$BinaryPath = "",

  [string]$EvidenceDir = "",

  [string]$ServiceName = "",

  [string]$ServiceScope = "",

  [string]$AgentID = "",

  [string]$HTTPAddr = "127.0.0.1:18080",

  [string]$PeerHTTPAddr = "127.0.0.1:18081",

  [string]$PublicAPIBase = "http://127.0.0.1:18081/api",

  [string]$DatabaseURL = "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable",

  [string]$SyncKeyID = "home-sync-v1",

  [string]$PreviousSyncKeyID = "home-sync-v0",

  [string]$LocalKeyID = "local-preflight-v1",

  [string]$PreviousLocalKeyID = "local-preflight-v0",

  [string]$AdvisorBaseURL = "http://127.0.0.1:11434/v1",

  [string]$AdvisorModel = "local-preflight-advisor",

  [switch]$SkipAdvisorConfig
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

function Invoke-StewardJSON {
  param(
    [string]$BinaryPath,
    [string[]]$Arguments
  )
  $output = & $BinaryPath @Arguments 2>&1
  $exitCode = $LASTEXITCODE
  $text = ($output | ForEach-Object { "$_" }) -join "`n"
  if ($exitCode -ne 0) {
    throw "steward $($Arguments -join ' ') failed with exit code ${exitCode}: $text"
  }
  try {
    return $text | ConvertFrom-Json
  } catch {
    throw "steward $($Arguments -join ' ') did not return JSON: $text"
  }
}

function New-Secret {
  $bytes = New-Object byte[] 32
  [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
  return [Convert]::ToBase64String($bytes)
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
  $outputPath = Join-Path $binaryDir ("steward-preflight-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)
  Assert-ChildPath -Parent $RepoRoot -Child $outputPath -Label "Preflight binary"

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

function Test-RedactedEnvironment {
  param([object]$Environment)
  $sensitiveKeys = @(
    "STEWARD_SYNC_SECRET",
    "STEWARD_DEVICE_PRIVATE_KEY",
    "STEWARD_SYNC_ENCRYPTION_KEY",
    "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS",
    "STEWARD_LOCAL_ENCRYPTION_KEY",
    "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS",
    "STEWARD_LLM_API_KEY"
  )
  foreach ($key in $sensitiveKeys) {
    $property = $Environment.PSObject.Properties[$key]
    if ($null -ne $property -and $property.Value -ne "<redacted>") {
      return $false
    }
  }
  return $true
}

function Contains-ArgPair {
  param(
    [object[]]$Arguments,
    [string]$Flag,
    [string]$Value
  )
  for ($i = 0; $i -lt $Arguments.Count - 1; $i++) {
    if ([string]$Arguments[$i] -eq $Flag -and [string]$Arguments[$i + 1] -eq $Value) {
      return $true
    }
  }
  return $false
}

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-service-preflight"
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$platform = Get-HostPlatform
if ([string]::IsNullOrWhiteSpace($AgentID)) {
  $AgentID = "$platform-preflight"
}
if ([string]::IsNullOrWhiteSpace($ServiceName)) {
  $ServiceName = "MongojsonStewardPreflight"
}
if ([string]::IsNullOrWhiteSpace($ServiceScope)) {
  if ($platform -eq "windows") {
    $ServiceScope = "system"
  } else {
    $ServiceScope = "user"
  }
}

$timestamp = New-Timestamp
$startedAt = (Get-Date).ToUniversalTime()
$checks = New-Object System.Collections.ArrayList
$errorMessage = ""
$installOutput = $null
$binary = ""

try {
  $binary = Get-OrBuild-Binary -RepoRoot $repoRoot -BackendDir $backendDir -EvidenceRoot $evidenceRoot -BinaryPath $BinaryPath
  Add-Check $checks "service_preflight.binary" "ok" "steward preflight binary is available" @{ path = $binary }

  $version = Invoke-StewardJSON -BinaryPath $binary -Arguments @("version")
  Add-Check $checks "service_preflight.version" "ok" "steward binary returned version metadata" $version

  $deviceKeys = Invoke-StewardJSON -BinaryPath $binary -Arguments @("keygen", "--prefix", $AgentID)
  $syncKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $SyncKeyID)
  $previousSyncKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $PreviousSyncKeyID)
  $localKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $LocalKeyID)
  $previousLocalKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $PreviousLocalKeyID)
  Add-Check $checks "service_preflight.key_material" "ok" "ephemeral device and AES key material generated for dry-run validation" @{
    sync_key_id = $SyncKeyID
    local_key_id = $LocalKeyID
  }

  $syncSecret = New-Secret
  $installArgs = @(
    "service", "install",
    "--dry-run",
    "--strict-security",
    "--name", $ServiceName,
    "--scope", $ServiceScope,
    "--binary", $binary,
    "--workdir", $backendDir,
    "--http-addr", $HTTPAddr,
    "--peer-http-addr", $PeerHTTPAddr,
    "--database-url", $DatabaseURL,
    "--storage-dir", (Join-Path $backendDir "data"),
    "--agent-id", $AgentID,
    "--public-api-base", $PublicAPIBase,
    "--sync-secret", $syncSecret,
    "--device-private-key", $deviceKeys.private_key,
    "--device-public-key", $deviceKeys.public_key,
    "--sync-encryption-key", $syncKey.key,
    "--sync-encryption-key-id", $SyncKeyID,
    "--sync-encryption-previous-keys", ($PreviousSyncKeyID + ":" + $previousSyncKey.key),
    "--local-encryption-key", $localKey.key,
    "--local-encryption-key-id", $LocalKeyID,
    "--local-encryption-previous-keys", ($PreviousLocalKeyID + ":" + $previousLocalKey.key),
    "--heartbeat-interval", "1m",
    "--sync-interval", "5m",
    "--autonomy-interval", "15m",
    "--log-dir", (Join-Path $backendDir "logs\steward-preflight")
  )
  if (-not $SkipAdvisorConfig) {
    $installArgs += @(
      "--llm-provider", "openai-compatible",
      "--llm-base-url", $AdvisorBaseURL,
      "--llm-model", $AdvisorModel,
      "--llm-allow-no-api-key=true",
      "--llm-max-data-level", "D1",
      "--llm-timeout", "20s",
      "--llm-failure-threshold", "3",
      "--llm-failure-cooldown", "1m"
    )
  }

  $installOutput = Invoke-StewardJSON -BinaryPath $binary -Arguments $installArgs
  Add-Check $checks "service_preflight.strict_dry_run" "ok" "service install --dry-run --strict-security passed" @{
    service_name = $installOutput.service.name
    service_scope = $installOutput.service.scope
    platform = $installOutput.service.platform
    status = $installOutput.service.status
  }

  if (Test-RedactedEnvironment $installOutput.service.environment) {
    Add-Check $checks "service_preflight.redaction" "ok" "dry-run service environment redacted sensitive values" $null
  } else {
    Add-Check $checks "service_preflight.redaction" "error" "dry-run service environment leaked a sensitive value" $null
  }

  if ($installOutput.verification -and
      (Contains-ArgPair $installOutput.verification.runtime_args "--expect-agent-id" $AgentID) -and
      (Contains-ArgPair $installOutput.verification.runtime_args "--expect-sync-key-id" $SyncKeyID) -and
      (Contains-ArgPair $installOutput.verification.runtime_args "--expect-local-key-id" $LocalKeyID) -and
      (Contains-ArgPair $installOutput.verification.service_args "--scope" $ServiceScope)) {
    Add-Check $checks "service_preflight.verification_advice" "ok" "dry-run output included strict runtime/service/watch verification advice" @{
      runtime_command = $installOutput.verification.runtime_command
      service_command = $installOutput.verification.service_command
      watch_command = $installOutput.verification.watch_command
    }
  } else {
    Add-Check $checks "service_preflight.verification_advice" "error" "dry-run output did not include complete verification advice" $installOutput.verification
  }

  if (-not $SkipAdvisorConfig) {
    if ((Contains-ArgPair $installOutput.verification.runtime_args "--expect-advisor-model" $AdvisorModel) -and
        (Contains-ArgPair $installOutput.verification.runtime_args "--expect-advisor-max-data-level" "D1")) {
      Add-Check $checks "service_preflight.advisor_config" "ok" "strict dry-run accepted loopback OpenAI-compatible advisor configuration" @{
        advisor_base_url = $AdvisorBaseURL
        advisor_model = $AdvisorModel
      }
    } else {
      Add-Check $checks "service_preflight.advisor_config" "error" "verification advice did not include advisor expectations" $installOutput.verification
    }
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "service_preflight.runner" "error" $errorMessage $null
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
$evidencePath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-verify-service-preflight-$timestamp" -Suffix "-$status.json"

$payload = [ordered]@{
  verification = [ordered]@{
    ok = $ok
    platform = $platform
    agent_id = $AgentID
    service_name = $ServiceName
    service_scope = $ServiceScope
    binary_path = $binary
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    advisor_config_included = (-not $SkipAdvisorConfig)
    error = $errorMessage
    checks = @($checks)
    service = if ($null -ne $installOutput) { $installOutput.service } else { $null }
    verification_advice = if ($null -ne $installOutput) { $installOutput.verification } else { $null }
  }
}

$command = @(
  "deploy/run-steward-service-preflight.ps1",
  "-ServiceName", $ServiceName,
  "-ServiceScope", $ServiceScope,
  "-AgentID", $AgentID,
  "-HTTPAddr", $HTTPAddr,
  "-PeerHTTPAddr", $PeerHTTPAddr,
  "-PublicAPIBase", $PublicAPIBase
)
if ($SkipAdvisorConfig) {
  $command += "-SkipAdvisorConfig"
}

$envelope = [ordered]@{
  kind = "service-preflight"
  ok = $ok
  command = $command
  created_at = $startedAt.ToString("o")
  payload = $payload
}
$envelope | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $evidencePath -Encoding UTF8

$summary = [ordered]@{
  ok = $ok
  platform = $platform
  service_name = $ServiceName
  service_scope = $ServiceScope
  agent_id = $AgentID
  binary_path = $binary
  evidence_path = $evidencePath
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 5

if (-not $ok) {
  exit 1
}
