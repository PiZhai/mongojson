param(
  [string]$EvidenceDir = "",

  [string]$BinaryPath = "",

  [string]$WatchDuration = "8s",

  [string]$WatchInterval = "2s",

  [switch]$SkipDistPreflight,

  [switch]$SkipPostgresE2E,

  [switch]$SkipServicePreflight,

  [switch]$SkipServiceEnvPreflight,

  [switch]$SkipPairingBootstrapPreflight,

  [switch]$SkipLocalMesh,

  [switch]$SkipAdvisorE2E,

  [switch]$SkipRuntimeWatch
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

function Invoke-CommandCapture {
  param(
    [string]$FilePath,
    [string[]]$Arguments
  )
  $output = & $FilePath @Arguments 2>&1
  return [pscustomobject]@{
    exit_code = $LASTEXITCODE
    output = @($output | ForEach-Object { "$_" })
    text = (($output | ForEach-Object { "$_" }) -join "`n")
  }
}

function Convert-CommandJson {
  param([object]$Result)
  if ([string]::IsNullOrWhiteSpace($Result.text)) {
    return $null
  }
  try {
    return $Result.text | ConvertFrom-Json
  } catch {
    return $null
  }
}

function Invoke-ScriptCapture {
  param(
    [string]$ScriptPath,
    [hashtable]$Parameters
  )
  $output = & $ScriptPath @Parameters 2>&1
  return [pscustomobject]@{
    exit_code = $LASTEXITCODE
    output = @($output | ForEach-Object { "$_" })
    text = (($output | ForEach-Object { "$_" }) -join "`n")
  }
}

function Get-OrBuild-Binary {
  param(
    [string]$RepoRoot,
    [string]$BackendDir,
    [string]$RunRoot,
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
  $binaryDir = Join-Path $RunRoot "bin"
  New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null
  $outputPath = Join-Path $binaryDir ("steward-local-readiness-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)
  Assert-ChildPath -Parent $RepoRoot -Child $outputPath -Label "Local readiness binary"

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

function Invoke-ReadinessStep {
  param(
    [string]$ID,
    [string]$ScriptPath,
    [hashtable]$Parameters,
    [System.Collections.ArrayList]$Checks,
    [System.Collections.ArrayList]$Steps
  )
  $startedAt = (Get-Date).ToUniversalTime()
  $result = Invoke-ScriptCapture -ScriptPath $ScriptPath -Parameters $Parameters
  $completedAt = (Get-Date).ToUniversalTime()
  $summary = Convert-CommandJson -Result $result
  $detail = [ordered]@{
    exit_code = $result.exit_code
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    evidence_path = if ($null -ne $summary) { $summary.evidence_path } else { $null }
    output = $result.output
  }
  [void]$Steps.Add([pscustomobject]@{
    id = $ID
    ok = ($result.exit_code -eq 0 -and $null -ne $summary -and $summary.ok -eq $true)
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = $detail.duration_ms
    evidence_path = $detail.evidence_path
    exit_code = $result.exit_code
  })
  if ($result.exit_code -eq 0 -and $null -ne $summary -and $summary.ok -eq $true) {
    Add-Check $Checks ("local_readiness." + $ID) "ok" "$ID passed" $detail
  } else {
    Add-Check $Checks ("local_readiness." + $ID) "error" "$ID failed" $detail
  }
}

function Add-ManifestRequirement {
  param(
    [System.Collections.ArrayList]$List,
    [string]$Flag,
    [string]$Value
  )
  [void]$List.Add($Flag)
  [void]$List.Add($Value)
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-local-readiness"
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$timestamp = New-Timestamp
$runRoot = Join-Path $evidenceRoot ("run-" + $timestamp)
Assert-ChildPath -Parent $repoRoot -Child $runRoot -Label "Readiness run directory"
New-Item -ItemType Directory -Force -Path $runRoot | Out-Null

$startedAt = (Get-Date).ToUniversalTime()
$checks = New-Object System.Collections.ArrayList
$steps = New-Object System.Collections.ArrayList
$binary = ""
$manifestResult = $null
$manifestOutput = $null
$errorMessage = ""
$hostPlatform = Get-HostPlatform

try {
  $binary = Get-OrBuild-Binary -RepoRoot $repoRoot -BackendDir $backendDir -RunRoot $runRoot -BinaryPath $BinaryPath
  Add-Check $checks "local_readiness.binary" "ok" "steward local readiness binary is available" @{ path = $binary }

  if (-not $SkipDistPreflight) {
    Invoke-ReadinessStep -ID "dist_preflight" -ScriptPath (Join-Path $PSScriptRoot "run-steward-dist-preflight.ps1") -Parameters @{
      EvidenceDir = (Join-Path $runRoot "dist-preflight")
    } -Checks $checks -Steps $steps
  }

  if (-not $SkipPostgresE2E) {
    Invoke-ReadinessStep -ID "postgres_e2e" -ScriptPath (Join-Path $PSScriptRoot "run-steward-postgres-e2e.ps1") -Parameters @{
      EvidenceDir = (Join-Path $runRoot "postgres-e2e")
      StartPostgres = $true
    } -Checks $checks -Steps $steps
  }
  if (-not $SkipServicePreflight) {
    Invoke-ReadinessStep -ID "service_preflight" -ScriptPath (Join-Path $PSScriptRoot "run-steward-service-preflight.ps1") -Parameters @{
      EvidenceDir = (Join-Path $runRoot "service-preflight")
      BinaryPath = $binary
    } -Checks $checks -Steps $steps
  }
  if (-not $SkipServiceEnvPreflight) {
    Invoke-ReadinessStep -ID "service_env_preflight" -ScriptPath (Join-Path $PSScriptRoot "run-steward-service-env-preflight.ps1") -Parameters @{
      EvidenceDir = (Join-Path $runRoot "service-env-preflight")
      BinaryPath = $binary
    } -Checks $checks -Steps $steps
  }
  if (-not $SkipPairingBootstrapPreflight) {
    Invoke-ReadinessStep -ID "pairing_bootstrap_preflight" -ScriptPath (Join-Path $PSScriptRoot "run-steward-pairing-bootstrap-preflight.ps1") -Parameters @{
      EvidenceDir = (Join-Path $runRoot "pairing-bootstrap-preflight")
      BinaryPath = $binary
    } -Checks $checks -Steps $steps
  }
  if (-not $SkipLocalMesh) {
    Invoke-ReadinessStep -ID "local_mesh" -ScriptPath (Join-Path $PSScriptRoot "run-steward-local-mesh.ps1") -Parameters @{
      EvidenceDir = (Join-Path $runRoot "local-mesh")
      BinaryPath = $binary
    } -Checks $checks -Steps $steps
  }
  if (-not $SkipAdvisorE2E) {
    Invoke-ReadinessStep -ID "advisor_e2e" -ScriptPath (Join-Path $PSScriptRoot "run-steward-advisor-e2e.ps1") -Parameters @{
      EvidenceDir = (Join-Path $runRoot "advisor-e2e")
      BinaryPath = $binary
    } -Checks $checks -Steps $steps
  }
  if (-not $SkipRuntimeWatch) {
    Invoke-ReadinessStep -ID "runtime_watch" -ScriptPath (Join-Path $PSScriptRoot "run-steward-runtime-watch.ps1") -Parameters @{
      EvidenceDir = (Join-Path $runRoot "runtime-watch")
      BinaryPath = $binary
      WatchDuration = $WatchDuration
      WatchInterval = $WatchInterval
    } -Checks $checks -Steps $steps
  }

  $manifestArgs = New-Object System.Collections.ArrayList
  foreach ($arg in @("verify", "evidence", "--dir", $runRoot, "--require-passing", "--require-platform", $hostPlatform)) {
    [void]$manifestArgs.Add($arg)
  }
  if (-not $SkipRuntimeWatch) {
    Add-ManifestRequirement $manifestArgs "--min-watch-duration" $WatchDuration
  }
  if (-not $SkipDistPreflight) {
    Add-ManifestRequirement $manifestArgs "--require-kind" "dist-preflight"
    foreach ($check in @("dist_preflight.build", "dist_preflight.integrity", "dist_preflight.targets", "dist_preflight.ui", "dist_preflight.current_binary")) {
      Add-ManifestRequirement $manifestArgs "--require-check" $check
    }
  }
  if (-not $SkipPostgresE2E) {
    Add-ManifestRequirement $manifestArgs "--require-kind" "postgres-e2e"
    Add-ManifestRequirement $manifestArgs "--require-check" "postgres_e2e.go_test"
  }
  if (-not $SkipServicePreflight) {
    Add-ManifestRequirement $manifestArgs "--require-kind" "service-preflight"
    foreach ($check in @("service_preflight.strict_dry_run", "service_preflight.redaction", "service_preflight.verification_advice", "service_preflight.advisor_config")) {
      Add-ManifestRequirement $manifestArgs "--require-check" $check
    }
  }
  if (-not $SkipServiceEnvPreflight) {
    Add-ManifestRequirement $manifestArgs "--require-kind" "service-env-preflight"
    foreach ($check in @("service_env_preflight.plan", "service_env_preflight.redaction", "service_env_preflight.rotation", "service_env_preflight.retry_policy", "service_env_preflight.verification_advice", "service_env_preflight.no_service_manager", "service_env_preflight.install_plan", "service_env_preflight.private_environment_artifacts")) {
      Add-ManifestRequirement $manifestArgs "--require-check" $check
    }
  }
  if (-not $SkipPairingBootstrapPreflight) {
    Add-ManifestRequirement $manifestArgs "--require-kind" "pairing-bootstrap-preflight"
    foreach ($check in @("pairing_bootstrap_preflight.bundle", "pairing_bootstrap_preflight.bootstrap", "pairing_bootstrap_preflight.suggested_env", "pairing_bootstrap_preflight.service_env_plan", "pairing_bootstrap_preflight.verification_advice", "pairing_bootstrap_preflight.command_advice", "pairing_bootstrap_preflight.redaction", "pairing_bootstrap_preflight.no_mutation")) {
      Add-ManifestRequirement $manifestArgs "--require-check" $check
    }
  }
  if (-not $SkipLocalMesh) {
    Add-ManifestRequirement $manifestArgs "--require-kind" "local-mesh"
    Add-ManifestRequirement $manifestArgs "--require-kind" "mesh"
    foreach ($check in @("local_mesh.verify_mesh", "local_mesh.autonomy_execute", "daemon.loops.status", "s3.device.policy_contract", "s3.sync.change_contract", "s3.sync.security.strict", "s4.autonomy.status", "s4.autonomy.policy_contract", "s4.autonomy.policy_gate", "s4.autonomy.retry_policy")) {
      Add-ManifestRequirement $manifestArgs "--require-check" $check
    }
    foreach ($check in @("s3.peer_probe.task", "s3.peer_probe.source_ref", "s3.peer_probe.data_tag", "s3.peer_probe.entity_tag", "s3.peer_probe.event", "s3.peer_probe.timeline_segment", "s3.peer_probe.relations")) {
      Add-ManifestRequirement $manifestArgs "--require-kind-check-platform" ("mesh:" + $check + ":" + $hostPlatform)
    }
  }
  if (-not $SkipAdvisorE2E) {
    Add-ManifestRequirement $manifestArgs "--require-kind" "advisor-e2e"
    Add-ManifestRequirement $manifestArgs "--require-kind" "runtime"
    foreach ($check in @(
      "advisor_e2e.verify_runtime",
      "advisor_e2e.advisor_request_count",
      "advisor_e2e.timeout",
      "advisor_e2e.circuit_open",
      "advisor_e2e.circuit_short_circuit",
      "advisor_e2e.local_fallback",
      "advisor_e2e.failure_audit",
      "advisor_e2e.recovery"
    )) {
      Add-ManifestRequirement $manifestArgs "--require-check" $check
    }
    Add-ManifestRequirement $manifestArgs "--require-kind-check-platform" ("runtime:s4.advisor.probe:" + $hostPlatform)
    Add-ManifestRequirement $manifestArgs "--require-kind-check-platform" ("runtime:s4.advisor.privacy_probe:" + $hostPlatform)
  }
  if (-not $SkipRuntimeWatch) {
    Add-ManifestRequirement $manifestArgs "--require-kind" "runtime-watch"
    Add-ManifestRequirement $manifestArgs "--require-kind" "runtime"
    foreach ($check in @("runtime_watch.verify_runtime", "runtime_watch.heartbeat", "runtime.watch.heartbeat", "runtime.watch", "daemon.loops.status", "s3.device.policy_contract", "s3.sync.change_contract", "s3.sync.security.strict", "s4.autonomy.policy_contract", "s4.autonomy.policy_gate", "s4.autonomy.retry_policy", "s4.write.autonomy_run")) {
      Add-ManifestRequirement $manifestArgs "--require-check" $check
    }
  }

  $hasEnabledEvidenceStep = -not ($SkipDistPreflight -and $SkipPostgresE2E -and $SkipServicePreflight -and $SkipServiceEnvPreflight -and $SkipPairingBootstrapPreflight -and $SkipLocalMesh -and $SkipAdvisorE2E -and $SkipRuntimeWatch)
  if ($hasEnabledEvidenceStep -and -not $manifestArgs.Contains("--require-kind")) {
    throw "local readiness manifest contract lost all dynamic evidence requirements"
  }

  $manifestPath = Join-Path $runRoot "manifest.json"
  [void]$manifestArgs.Add("--output")
  [void]$manifestArgs.Add($manifestPath)
  $manifestResult = Invoke-CommandCapture -FilePath $binary -Arguments ([string[]]$manifestArgs)
  $manifestOutput = Convert-CommandJson -Result $manifestResult
  $manifestContractOK = $null -ne $manifestOutput
  if ($manifestContractOK -and $hasEnabledEvidenceStep) {
    $manifestContractOK = @($manifestOutput.manifest.options.require_kinds).Count -gt 0
  }
  if ($manifestContractOK -and -not $SkipServiceEnvPreflight) {
    $manifestContractOK = @($manifestOutput.manifest.options.require_checks) -contains "service_env_preflight.retry_policy"
  }
  if ($manifestContractOK -and -not $SkipLocalMesh) {
    $manifestContractOK = @($manifestOutput.manifest.options.require_kind_check_platforms) -contains ("mesh:s3.peer_probe.relations:" + $hostPlatform)
  }
  if ($manifestContractOK -and -not $SkipAdvisorE2E) {
    $manifestContractOK = @($manifestOutput.manifest.options.require_checks) -contains "advisor_e2e.recovery"
  }
  if ($manifestContractOK -and -not $SkipRuntimeWatch) {
    $requiredChecks = @($manifestOutput.manifest.options.require_checks)
    $manifestContractOK = ($requiredChecks -contains "daemon.loops.status") -and ($requiredChecks -contains "s3.device.policy_contract") -and ($requiredChecks -contains "s3.sync.change_contract") -and ($requiredChecks -contains "s4.autonomy.policy_contract") -and ($requiredChecks -contains "s4.autonomy.policy_gate") -and ($requiredChecks -contains "s4.autonomy.retry_policy")
  }
  if ($manifestContractOK) {
    Add-Check $checks "local_readiness.manifest_contract" "ok" "combined manifest retained the configured S3/S4 evidence requirements" $null
  } else {
    Add-Check $checks "local_readiness.manifest_contract" "error" "combined manifest did not retain the configured S3/S4 evidence requirements" $null
  }
  if ($manifestResult.exit_code -eq 0 -and $null -ne $manifestOutput -and $manifestOutput.manifest.ok -eq $true -and $manifestContractOK) {
    Add-Check $checks "local_readiness.manifest" "ok" "combined local readiness evidence manifest passed" @{
      manifest_path = $manifestPath
    }
  } else {
    Add-Check $checks "local_readiness.manifest" "error" "combined local readiness evidence manifest failed" @{
      exit_code = $manifestResult.exit_code
      output = $manifestResult.output
      manifest_path = $manifestPath
    }
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "local_readiness.runner" "error" $errorMessage $null
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
$evidencePath = New-UniquePath -Directory $runRoot -BaseName "steward-verify-local-readiness-$timestamp" -Suffix "-$status.json"

$payload = [ordered]@{
  verification = [ordered]@{
    ok = $ok
    platform = $hostPlatform
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    binary_path = $binary
    run_root = $runRoot
    watch_duration = $WatchDuration
    watch_interval = $WatchInterval
    manifest = if ($null -ne $manifestOutput) { $manifestOutput.manifest } else { $null }
    manifest_exit_code = if ($null -ne $manifestResult) { $manifestResult.exit_code } else { $null }
    steps = @($steps)
    error = $errorMessage
    checks = @($checks)
  }
}

$command = @(
  "deploy/run-steward-local-readiness.ps1",
  "-WatchDuration", $WatchDuration,
  "-WatchInterval", $WatchInterval
)
foreach ($item in @(
  @{ switch = $SkipDistPreflight; name = "-SkipDistPreflight" },
  @{ switch = $SkipPostgresE2E; name = "-SkipPostgresE2E" },
  @{ switch = $SkipServicePreflight; name = "-SkipServicePreflight" },
  @{ switch = $SkipServiceEnvPreflight; name = "-SkipServiceEnvPreflight" },
  @{ switch = $SkipPairingBootstrapPreflight; name = "-SkipPairingBootstrapPreflight" },
  @{ switch = $SkipLocalMesh; name = "-SkipLocalMesh" },
  @{ switch = $SkipAdvisorE2E; name = "-SkipAdvisorE2E" },
  @{ switch = $SkipRuntimeWatch; name = "-SkipRuntimeWatch" }
)) {
  if ($item.switch) {
    $command += $item.name
  }
}

$envelope = [ordered]@{
  kind = "local-readiness"
  ok = $ok
  command = $command
  created_at = $startedAt.ToString("o")
  payload = $payload
}
$envelope | ConvertTo-Json -Depth 18 | Set-Content -LiteralPath $evidencePath -Encoding UTF8

$summary = [ordered]@{
  ok = $ok
  platform = $hostPlatform
  evidence_path = $evidencePath
  run_root = $runRoot
  manifest_path = Join-Path $runRoot "manifest.json"
  binary_path = $binary
  watch_duration = $WatchDuration
  watch_interval = $WatchInterval
  steps = @($steps)
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 8

if (-not $ok) {
  exit 1
}
