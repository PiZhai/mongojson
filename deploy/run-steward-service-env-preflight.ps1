param(
  [string]$BinaryPath = "",

  [string]$EvidenceDir = "",

  [string]$ServiceName = "MongojsonStewardEnvPreflight",

  [string]$ServiceScope = "",

  [string]$AgentID = "env-preflight-node",

  [string]$HTTPAddr = "127.0.0.1:19580",

  [string]$PeerHTTPAddr = "127.0.0.1:19581",

  [string]$PublicAPIBase = "http://127.0.0.1:19581/api",

  [string]$DatabaseURL = "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable",

  [string]$SyncKeyID = "env-preflight-sync-v1",

  [string]$RotatedSyncKeyID = "env-preflight-sync-v2",

  [string]$LocalKeyID = "env-preflight-local-v1",

  [string]$RotatedLocalKeyID = "env-preflight-local-v2",

  [string]$AdvisorBaseURL = "http://127.0.0.1:11434/v1",

  [string]$AdvisorModel = "env-preflight-advisor",

  [string]$AdvisorAPIKey = "env-preflight-advisor-secret",

  [switch]$SkipAdvisorConfig
)

$ErrorActionPreference = "Stop"
$PathSeparators = @([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
$serviceScopeExplicit = $PSBoundParameters.ContainsKey("ServiceScope")

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
  $outputPath = Join-Path $binaryDir ("steward-service-env-preflight-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)
  Assert-ChildPath -Parent $RepoRoot -Child $outputPath -Label "Service env preflight binary"

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

function Contains-ArgPair {
  param(
    [object[]]$ArgList,
    [string]$Flag,
    [string]$Value
  )
  for ($index = 0; $index -lt ($ArgList.Count - 1); $index++) {
    if ([string]$ArgList[$index] -eq $Flag -and [string]$ArgList[$index + 1] -eq $Value) {
      return $true
    }
  }
  return $false
}

function Write-CurrentEnvFile {
  param(
    [string]$Path,
    [hashtable]$Environment
  )
  $ordered = [ordered]@{}
  foreach ($key in ($Environment.Keys | Sort-Object)) {
    $ordered[$key] = [string]$Environment[$key]
  }
  $ordered | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $Path -Encoding UTF8
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-service-env-preflight"
}
if ([string]::IsNullOrWhiteSpace($ServiceScope)) {
  if ((Get-HostPlatform) -eq "windows") {
    $ServiceScope = "system"
  } else {
    $ServiceScope = "user"
  }
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$timestamp = New-Timestamp
$startedAt = (Get-Date).ToUniversalTime()
$checks = New-Object System.Collections.ArrayList
$binary = ""
$planResult = $null
$planOutput = $null
$installPlanResult = $null
$installPlanOutput = $null
$currentEnvPath = ""
$errorMessage = ""
$secretValues = @()

try {
  $binary = Get-OrBuild-Binary -RepoRoot $repoRoot -BackendDir $backendDir -EvidenceRoot $evidenceRoot -BinaryPath $BinaryPath
  Add-Check $checks "service_env_preflight.binary" "ok" "steward service env preflight binary is available" @{ path = $binary }
  Invoke-StewardJSON -BinaryPath $binary -Arguments @("version") | Out-Null
  Add-Check $checks "service_env_preflight.version" "ok" "steward binary returned version metadata" $null

  $deviceKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("keygen", "--prefix", $AgentID)
  $syncKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $SyncKeyID)
  $localKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $LocalKeyID)
  $oldSyncKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", "env-preflight-sync-v0")
  $oldLocalKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", "env-preflight-local-v0")

  $currentEnv = @{
    "HTTP_ADDR" = $HTTPAddr
    "STEWARD_PEER_HTTP_ADDR" = $PeerHTTPAddr
    "DATABASE_URL" = $DatabaseURL
    "STORAGE_DIR" = (Join-Path $evidenceRoot "data")
    "STEWARD_AGENT_ID" = $AgentID
    "STEWARD_PUBLIC_API_BASE" = $PublicAPIBase
    "STEWARD_SYNC_SECRET" = New-Secret
    "STEWARD_DEVICE_PRIVATE_KEY" = $deviceKey.private_key
    "STEWARD_DEVICE_PUBLIC_KEY" = $deviceKey.public_key
    "STEWARD_SYNC_ENCRYPTION_KEY" = $syncKey.key
    "STEWARD_SYNC_ENCRYPTION_KEY_ID" = $SyncKeyID
    "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS" = "env-preflight-sync-v0:" + $oldSyncKey.key
    "STEWARD_LOCAL_ENCRYPTION_KEY" = $localKey.key
    "STEWARD_LOCAL_ENCRYPTION_KEY_ID" = $LocalKeyID
    "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS" = "env-preflight-local-v0:" + $oldLocalKey.key
    "STEWARD_HEARTBEAT_INTERVAL" = "1m"
    "STEWARD_SYNC_INTERVAL" = "5m"
    "STEWARD_AUTONOMY_INTERVAL" = "15m"
    "STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS" = "3"
    "STEWARD_AUTONOMY_RETRY_BACKOFF" = "5m"
    "STEWARD_AUTONOMY_RETRY_MAX_BACKOFF" = "1h"
  }
  if (-not $SkipAdvisorConfig) {
    $currentEnv["STEWARD_LLM_PROVIDER"] = "openai-compatible"
    $currentEnv["STEWARD_LLM_BASE_URL"] = $AdvisorBaseURL
    $currentEnv["STEWARD_LLM_MODEL"] = $AdvisorModel
    $currentEnv["STEWARD_LLM_API_KEY"] = $AdvisorAPIKey
    $currentEnv["STEWARD_LLM_MAX_DATA_LEVEL"] = "D1"
    $currentEnv["STEWARD_LLM_TIMEOUT"] = "20s"
    $currentEnv["STEWARD_LLM_FAILURE_THRESHOLD"] = "3"
    $currentEnv["STEWARD_LLM_FAILURE_COOLDOWN"] = "1m"
  }

  $secretValues = @(
    $currentEnv["STEWARD_SYNC_SECRET"],
    $currentEnv["STEWARD_DEVICE_PRIVATE_KEY"],
    $currentEnv["STEWARD_SYNC_ENCRYPTION_KEY"],
    $currentEnv["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"],
    $currentEnv["STEWARD_LOCAL_ENCRYPTION_KEY"],
    $currentEnv["STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"],
    $currentEnv["STEWARD_LLM_API_KEY"]
  ) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }

  $scratchDir = Join-Path $evidenceRoot "scratch"
  New-Item -ItemType Directory -Force -Path $scratchDir | Out-Null
  $currentEnvPath = Join-Path $scratchDir "current-env.json"
  Write-CurrentEnvFile -Path $currentEnvPath -Environment $currentEnv

  $args = @(
    "service", "env", "plan",
    "--name", $ServiceName,
    "--scope", $ServiceScope,
    "--current-env-file", $currentEnvPath,
    "--rotate-sync-key-id", $RotatedSyncKeyID,
    "--rotate-local-key-id", $RotatedLocalKeyID,
    "--strict-security"
  )
  $planResult = Invoke-StewardCommand -BinaryPath $binary -Arguments $args
  if ($planResult.exit_code -ne 0) {
    Add-Check $checks "service_env_preflight.plan" "error" "service env plan failed" @{ exit_code = $planResult.exit_code; output = $planResult.output }
    throw "service env plan failed with exit code $($planResult.exit_code)"
  }
  $planOutput = $planResult.text | ConvertFrom-Json
  Add-Check $checks "service_env_preflight.plan" "ok" "service env plan --current-env-file --strict-security passed" $null

  $leaked = @()
  foreach ($secret in $secretValues) {
    if ($planResult.text.Contains($secret)) {
      $leaked += $secret
    }
  }
  if ($leaked.Count -eq 0) {
    Add-Check $checks "service_env_preflight.redaction" "ok" "service env plan output did not leak sensitive values" $null
  } else {
    Add-Check $checks "service_env_preflight.redaction" "error" "service env plan output leaked sensitive values" @{ leaked_count = $leaked.Count }
  }

  $environment = $planOutput.service_env.environment
  if ($environment.STEWARD_SYNC_ENCRYPTION_KEY_ID -eq $RotatedSyncKeyID -and
      $environment.STEWARD_LOCAL_ENCRYPTION_KEY_ID -eq $RotatedLocalKeyID -and
      $environment.STEWARD_SYNC_ENCRYPTION_KEY -eq "<redacted>" -and
      $environment.STEWARD_LOCAL_ENCRYPTION_KEY -eq "<redacted>" -and
      $environment.STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS -eq "<redacted>") {
    Add-Check $checks "service_env_preflight.rotation" "ok" "rotation plan produced new key IDs and redacted key material" @{
      sync_key_id = $environment.STEWARD_SYNC_ENCRYPTION_KEY_ID
      local_key_id = $environment.STEWARD_LOCAL_ENCRYPTION_KEY_ID
    }
  } else {
    Add-Check $checks "service_env_preflight.rotation" "error" "rotation plan did not produce expected redacted target environment" $environment
  }

  if ($environment.STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS -eq "3" -and
      $environment.STEWARD_AUTONOMY_RETRY_BACKOFF -eq "5m" -and
      $environment.STEWARD_AUTONOMY_RETRY_MAX_BACKOFF -eq "1h") {
    Add-Check $checks "service_env_preflight.retry_policy" "ok" "service environment plan preserved the bounded autonomy retry policy" @{
      max_attempts = $environment.STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS
      backoff = $environment.STEWARD_AUTONOMY_RETRY_BACKOFF
      max_backoff = $environment.STEWARD_AUTONOMY_RETRY_MAX_BACKOFF
    }
  } else {
    Add-Check $checks "service_env_preflight.retry_policy" "error" "service environment plan did not preserve the bounded autonomy retry policy" $environment
  }

  if ($planOutput.service_env.message -match "explicit current environment") {
    Add-Check $checks "service_env_preflight.no_service_manager" "ok" "plan used explicit current environment without reading service manager state" $null
  } else {
    Add-Check $checks "service_env_preflight.no_service_manager" "error" "plan output did not report explicit current environment mode" $planOutput.service_env
  }

  $runtimeArgs = @($planOutput.verification.runtime_args)
  $hasExpectedArgs = (Contains-ArgPair -ArgList $runtimeArgs -Flag "--expect-sync-key-id" -Value $RotatedSyncKeyID) -and
    (Contains-ArgPair -ArgList $runtimeArgs -Flag "--expect-local-key-id" -Value $RotatedLocalKeyID) -and
    (Contains-ArgPair -ArgList $runtimeArgs -Flag "--expect-agent-id" -Value $AgentID)
  if (-not $SkipAdvisorConfig) {
    $hasExpectedArgs = $hasExpectedArgs -and
      (Contains-ArgPair -ArgList $runtimeArgs -Flag "--expect-advisor-model" -Value $AdvisorModel) -and
      (Contains-ArgPair -ArgList $runtimeArgs -Flag "--expect-advisor-max-data-level" -Value "D1")
  }
  if ($hasExpectedArgs) {
    Add-Check $checks "service_env_preflight.verification_advice" "ok" "verification advice includes rotated key and advisor expectations" $planOutput.verification
  } else {
    Add-Check $checks "service_env_preflight.verification_advice" "error" "verification advice did not include expected assertions" $planOutput.verification
  }

  $installPlanArgs = @(
    "service", "plan",
    "--current-env-file", $currentEnvPath,
    "--target", "windows,darwin,linux",
    "--binary", $binary,
    "--workdir", $backendDir,
    "--log-dir", (Join-Path $evidenceRoot "logs"),
    "--strict-security"
  )
  if ($serviceScopeExplicit) {
    $installPlanArgs += @("--scope", $ServiceScope)
  }
  $installPlanResult = Invoke-StewardCommand -BinaryPath $binary -Arguments $installPlanArgs
  if ($installPlanResult.exit_code -ne 0) {
    Add-Check $checks "service_env_preflight.install_plan" "error" "service plan failed" @{ exit_code = $installPlanResult.exit_code; output = $installPlanResult.output }
    throw "service plan failed with exit code $($installPlanResult.exit_code)"
  }
  $installPlanOutput = $installPlanResult.text | ConvertFrom-Json
  $planPlatforms = @($installPlanOutput.plans | ForEach-Object { $_.platform })
  $hasAllPlatforms = ($planPlatforms -contains "windows") -and ($planPlatforms -contains "darwin") -and ($planPlatforms -contains "linux")
  $artifactOK = $true
  $privateEnvironmentArtifactsOK = $true
  $verificationByPlatformOK = $true
  foreach ($servicePlan in @($installPlanOutput.plans)) {
    if ($servicePlan.environment.STEWARD_SYNC_ENCRYPTION_KEY -ne "<redacted>" -or
        $servicePlan.environment.STEWARD_DEVICE_PRIVATE_KEY -ne "<redacted>") {
      $artifactOK = $false
    }
    if ($servicePlan.platform -eq "darwin") {
      if (-not ([string]$servicePlan.artifacts.plist).Contains("<key>KeepAlive</key>")) {
        $artifactOK = $false
      }
      if ($servicePlan.artifacts.plist_mode -ne "0600") {
        $privateEnvironmentArtifactsOK = $false
      }
    }
    if ($servicePlan.platform -eq "linux") {
      $unit = [string]$servicePlan.artifacts.systemd_unit
      $environmentFile = [string]$servicePlan.artifacts.environment_file
      if (-not $unit.Contains("Restart=always")) {
        $artifactOK = $false
      }
      if (-not $unit.Contains("EnvironmentFile=") -or $unit.Contains("STEWARD_SYNC_SECRET") -or
          -not $environmentFile.Contains('STEWARD_SYNC_SECRET="<redacted>"') -or
          $servicePlan.artifacts.environment_file_mode -ne "0600" -or @($servicePlan.files).Count -ne 2) {
        $privateEnvironmentArtifactsOK = $false
      }
    }
    if ($servicePlan.platform -eq "windows" -and $servicePlan.artifacts.service_type -ne "Windows Service") {
      $artifactOK = $false
    }
    $platformAdviceProperty = $installPlanOutput.verification_by_platform.PSObject.Properties[$servicePlan.platform]
    if ($null -eq $platformAdviceProperty) {
      $verificationByPlatformOK = $false
    } else {
      $platformRuntimeArgs = @($platformAdviceProperty.Value.runtime_args)
      if (-not (Contains-ArgPair -ArgList $platformRuntimeArgs -Flag "--expect-agent-platform" -Value $servicePlan.platform)) {
        $verificationByPlatformOK = $false
      }
    }
  }
  $installPlanText = $installPlanResult.text
  $installPlanLeaked = @()
  foreach ($secret in $secretValues) {
    if ($installPlanText.Contains($secret)) {
      $installPlanLeaked += $secret
    }
  }
  if ($hasAllPlatforms -and $artifactOK -and $verificationByPlatformOK -and $installPlanLeaked.Count -eq 0) {
    Add-Check $checks "service_env_preflight.install_plan" "ok" "offline service plan rendered Windows, macOS, and Linux install artifacts without leaking secrets" @{
      platforms = $planPlatforms
      verification_by_platform = $true
    }
  } else {
    Add-Check $checks "service_env_preflight.install_plan" "error" "offline service plan did not satisfy platform coverage, artifact, or redaction checks" @{
      platforms = $planPlatforms
      artifact_ok = $artifactOK
      verification_by_platform_ok = $verificationByPlatformOK
      leaked_count = $installPlanLeaked.Count
    }
  }
  if ($privateEnvironmentArtifactsOK) {
    Add-Check $checks "service_env_preflight.private_environment_artifacts" "ok" "macOS and Linux plans protect persisted service secrets with mode 0600 and Linux keeps them out of the unit" $null
  } else {
    Add-Check $checks "service_env_preflight.private_environment_artifacts" "error" "macOS or Linux service plan does not protect persisted service secrets" $installPlanOutput.plans
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "service_env_preflight.runner" "error" $errorMessage $null
} finally {
  if (-not [string]::IsNullOrWhiteSpace($currentEnvPath) -and (Test-Path -LiteralPath $currentEnvPath)) {
    Remove-Item -LiteralPath $currentEnvPath -Force
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
$evidencePath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-verify-service-env-preflight-$timestamp" -Suffix "-$status.json"

$payload = [ordered]@{
  verification = [ordered]@{
    ok = $ok
    platform = Get-HostPlatform
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    binary_path = $binary
    service_name = $ServiceName
    service_scope = $ServiceScope
    service_scope_explicit = [bool]$serviceScopeExplicit
    agent_id = $AgentID
    sync_key_id = $RotatedSyncKeyID
    local_key_id = $RotatedLocalKeyID
    advisor_config_included = (-not $SkipAdvisorConfig)
    plan_exit_code = if ($null -ne $planResult) { $planResult.exit_code } else { $null }
    plan_output = if ($null -ne $planResult) { $planResult.output } else { $null }
    install_plan_exit_code = if ($null -ne $installPlanResult) { $installPlanResult.exit_code } else { $null }
    install_plan_output = if ($null -ne $installPlanResult) { $installPlanResult.output } else { $null }
    error = $errorMessage
    checks = @($checks)
  }
}

$command = @(
  "deploy/run-steward-service-env-preflight.ps1",
  "-ServiceName", $ServiceName,
  "-ServiceScope", $ServiceScope,
  "-AgentID", $AgentID,
  "-SyncKeyID", $SyncKeyID,
  "-RotatedSyncKeyID", $RotatedSyncKeyID,
  "-LocalKeyID", $LocalKeyID,
  "-RotatedLocalKeyID", $RotatedLocalKeyID
)
if ($SkipAdvisorConfig) {
  $command += "-SkipAdvisorConfig"
}

$envelope = [ordered]@{
  kind = "service-env-preflight"
  ok = $ok
  command = $command
  created_at = $startedAt.ToString("o")
  payload = $payload
}
$envelope | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $evidencePath -Encoding UTF8

$summary = [ordered]@{
  ok = $ok
  platform = Get-HostPlatform
  evidence_path = $evidencePath
  binary_path = $binary
  service_name = $ServiceName
  service_scope = $ServiceScope
  sync_key_id = $RotatedSyncKeyID
  local_key_id = $RotatedLocalKeyID
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 6

if (-not $ok) {
  exit 1
}
