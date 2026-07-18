param(
  [string]$EvidenceDir = "",

  [string]$Version = "",

  [switch]$SkipFrontendBuild,

  [string]$SigningCertificateThumbprint = "",

  [string]$TimestampServer = "",

  [string]$TrustedSignerThumbprint = "",

  [switch]$DevelopmentUnsigned,

  [switch]$AllowDirtyWorktree
)

$ErrorActionPreference = "Stop"
$PathSeparators = @([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)

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

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $repoRoot "backend\dist\steward-dist-preflight"
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$timestamp = New-Timestamp
if ([string]::IsNullOrWhiteSpace($Version)) {
  $Version = "dist-preflight-" + ($timestamp -replace '[^0-9]', '').Substring(0, 14)
}
$distRoot = Join-Path $evidenceRoot "dist"
Assert-ChildPath -Parent $repoRoot -Child $distRoot -Label "Distribution directory"
$startedAt = (Get-Date).ToUniversalTime()
$checks = New-Object System.Collections.ArrayList
$buildOutput = @()
$verification = $null
$packageVerification = $null
$errorMessage = ""
$preflightMode = if ($DevelopmentUnsigned) { "development_unsigned" } else { "production_signed" }

try {
  if ($DevelopmentUnsigned) {
    if (-not [string]::IsNullOrWhiteSpace($SigningCertificateThumbprint) -or -not [string]::IsNullOrWhiteSpace($TrustedSignerThumbprint)) {
      throw "-DevelopmentUnsigned cannot be combined with release signer parameters"
    }
    Write-Warning "DEVELOPMENT PREFLIGHT: the Windows package may be unsigned and is not production eligible."
  } else {
    if ($AllowDirtyWorktree) {
      throw "Production dist preflight does not allow -AllowDirtyWorktree"
    }
    if ([string]::IsNullOrWhiteSpace($SigningCertificateThumbprint)) {
      throw "Production dist preflight requires -SigningCertificateThumbprint; use -DevelopmentUnsigned only for an explicit local development run"
    }
    if ([string]::IsNullOrWhiteSpace($TimestampServer)) {
      throw "Production dist preflight requires -TimestampServer"
    }
    if ([string]::IsNullOrWhiteSpace($TrustedSignerThumbprint)) {
      $TrustedSignerThumbprint = $SigningCertificateThumbprint
    }
  }

  $buildParameters = @{
    OutputDir = $distRoot
    Version = $Version
    Clean = $true
  }
  if ($SkipFrontendBuild) {
    $buildParameters.SkipFrontendBuild = $true
  }
  if ($AllowDirtyWorktree) {
    $buildParameters.AllowDirtyWorktree = $true
  }
  if (-not $DevelopmentUnsigned) {
    $buildParameters.SigningCertificateThumbprint = $SigningCertificateThumbprint
    $buildParameters.TimestampServer = $TimestampServer
    $buildParameters.RequireSignedPackage = $true
  }
  $buildOutput = @(& (Join-Path $PSScriptRoot "build-steward.ps1") @buildParameters *>&1 | ForEach-Object { "$_" })
  if ($LASTEXITCODE -ne 0) {
    throw "steward dist build failed with exit code $LASTEXITCODE"
  }
  Add-Check $checks "dist_preflight.build" "ok" "five target steward distribution directories were built" @{ dist_dir = $distRoot }

  $aggregateVerifyParameters = @{
    DistDir = $distRoot
    ExpectedVersion = $Version
    RunCurrentBinary = $true
  }
  if ($AllowDirtyWorktree) {
    $aggregateVerifyParameters.AllowDirtyPackage = $true
  }
  $verifyOutput = @(& (Join-Path $PSScriptRoot "verify-steward-dist.ps1") @aggregateVerifyParameters)
  if ($LASTEXITCODE -ne 0) {
    throw "steward dist verification failed with exit code $LASTEXITCODE"
  }
  $verification = ($verifyOutput -join "`n") | ConvertFrom-Json
  if (-not $verification.ok) {
    throw "steward dist verifier returned ok=false"
  }
  Add-Check $checks "dist_preflight.integrity" "ok" "distribution manifest and every binary/UI checksum passed" @{
    artifact_count = $verification.artifact_count
    dist_dir = $verification.dist_dir
  }

  $requiredTargets = @("windows/amd64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64")
  $actualTargets = @($verification.artifacts | ForEach-Object { $_.target } | Sort-Object -Unique)
  $missingTargets = @($requiredTargets | Where-Object { $_ -notin $actualTargets })
  if ($verification.artifact_count -ne 5 -or $missingTargets.Count -ne 0) {
    throw "steward dist target coverage is incomplete: $($missingTargets -join ', ')"
  }
  Add-Check $checks "dist_preflight.targets" "ok" "Windows, macOS Intel/Apple Silicon, and Linux amd64/arm64 targets are present" @{ targets = $actualTargets }

  if ([string]::IsNullOrWhiteSpace([string]$verification.go_version) -or $verification.current_binary_smoke.go_version -ne $verification.go_version) {
    throw "distribution Go toolchain metadata is missing or does not match the current binary"
  }
  Add-Check $checks "dist_preflight.toolchain" "ok" "distribution manifest and current binary report the same Go toolchain" @{
    go_version = $verification.go_version
  }

  $invalidUI = @($verification.artifacts | Where-Object { [string]::IsNullOrWhiteSpace([string]$_.ui_dir) -or [int]$_.file_count -le 1 })
  if (-not $verification.ui_included -or $invalidUI.Count -ne 0) {
    throw "one or more steward target directories do not include the bundled workspace"
  }
  Add-Check $checks "dist_preflight.ui" "ok" "every target directory contains a checksum-verified bundled workspace" @{
    target_file_counts = @($verification.artifacts | ForEach-Object { @{ target = $_.target; files = $_.file_count } })
  }

  $missingCompanions = @($verification.artifacts | Where-Object { [string]::IsNullOrWhiteSpace([string]$_.companion_path) })
  if ($missingCompanions.Count -ne 0) {
    throw "one or more steward target directories do not include steward-companion"
  }
  Add-Check $checks "dist_preflight.companion" "ok" "every target directory contains a checksum-verified companion buffer" @{
    companion_paths = @($verification.artifacts | ForEach-Object { $_.companion_path })
  }

  $windowsArtifact = @($verification.artifacts | Where-Object { $_.target -eq "windows/amd64" }) | Select-Object -First 1
  if ($null -eq $windowsArtifact -or [string]::IsNullOrWhiteSpace([string]$windowsArtifact.path)) {
    throw "distribution verification did not return the Windows amd64 artifact path"
  }
  $windowsArtifactPath = ([string]$windowsArtifact.path) -replace "/", [IO.Path]::DirectorySeparatorChar
  $windowsPackageDir = Split-Path -Parent (Join-Path $distRoot $windowsArtifactPath)
  $packageVerifyParameters = @{
    DistDir = $windowsPackageDir
    ExpectedVersion = $Version
    RequirePackageMode = $true
  }
  if ((Get-HostPlatform) -eq "windows") {
    $packageVerifyParameters.RunCurrentBinary = $true
  }
  if ($DevelopmentUnsigned) {
    $packageVerifyParameters.AllowUnsignedPackage = $true
  } else {
    $packageVerifyParameters.TrustedSignerThumbprint = $TrustedSignerThumbprint
  }
  if ($AllowDirtyWorktree) {
    $packageVerifyParameters.AllowDirtyPackage = $true
  }
  $packageVerifyOutput = @(& (Join-Path $PSScriptRoot "verify-steward-dist.ps1") @packageVerifyParameters)
  if ($LASTEXITCODE -ne 0) {
    throw "Windows package verification failed with exit code $LASTEXITCODE"
  }
  $packageVerification = ($packageVerifyOutput -join "`n") | ConvertFrom-Json
  if (-not $packageVerification.ok -or -not $packageVerification.package_mode) {
    throw "Windows package verifier did not complete package-local verification"
  }
  if (-not $DevelopmentUnsigned -and (-not $packageVerification.signed -or [string]::IsNullOrWhiteSpace([string]$packageVerification.signer_thumbprint))) {
    throw "Production Windows package did not produce a pinned signed verification result"
  }
  $packageIntegrityMessage = if ($DevelopmentUnsigned) {
    "Windows package-local manifest and hashes passed with explicit unsigned development overrides"
  } else {
    "Windows package-local manifest, hashes, executable signatures, and pinned publisher policy passed"
  }
  Add-Check $checks "dist_preflight.package_integrity" "ok" $packageIntegrityMessage @{
    package_dir = $windowsPackageDir
    signed = [bool]$packageVerification.signed
    signer_thumbprint = $packageVerification.signer_thumbprint
    source_clean = [bool]$packageVerification.source_clean
    development_override = [bool]$DevelopmentUnsigned
  }
  if (-not $DevelopmentUnsigned) {
    if (-not [bool]$packageVerification.source_clean) {
      throw "Production Windows package was not built from a clean source worktree"
    }
    Add-Check $checks "dist_preflight.production_eligibility" "ok" "Windows release is clean, signed, package-local verified, and pinned to the expected publisher" @{
      signer_thumbprint = $packageVerification.signer_thumbprint
      release_kind = $packageVerification.release_kind
    }
  } else {
    Add-Check $checks "dist_preflight.development_override" "ok" "Unsigned development mode was explicitly requested and recorded; this evidence is not production eligible" $null
  }

  $missingBrokers = @($verification.artifacts | Where-Object { [string]::IsNullOrWhiteSpace([string]$_.broker_path) })
  if ($missingBrokers.Count -ne 0) {
    throw "one or more steward target directories do not include steward-broker"
  }
  Add-Check $checks "dist_preflight.broker" "ok" "every target directory contains a checksum-verified Privilege Broker" @{
    broker_paths = @($verification.artifacts | ForEach-Object { $_.broker_path })
  }

  $missingApprovalHelpers = @($verification.artifacts | Where-Object { [string]::IsNullOrWhiteSpace([string]$_.approval_path) })
  if ($missingApprovalHelpers.Count -ne 0) { throw "one or more steward target directories do not include steward-approval" }
  Add-Check $checks "dist_preflight.approval" "ok" "every target directory contains the approval authority helper" @{
    approval_paths = @($verification.artifacts | ForEach-Object { $_.approval_path })
  }

  $missingSystemToolHosts = @($verification.artifacts | Where-Object { [string]::IsNullOrWhiteSpace([string]$_.system_tool_host_path) })
  if ($missingSystemToolHosts.Count -ne 0) { throw "one or more steward target directories do not include steward-system-tool-host" }
  Add-Check $checks "dist_preflight.system_tool_host" "ok" "every target directory contains the schema-bound System Tool Host" @{
    system_tool_host_paths = @($verification.artifacts | ForEach-Object { $_.system_tool_host_path })
  }

  if ($null -eq $verification.current_binary_smoke -or $verification.current_binary_smoke.name -ne "steward") {
    throw "current platform steward binary smoke result is missing"
  }
  Add-Check $checks "dist_preflight.current_binary" "ok" "current platform binary reports the expected version, commit, and target" $verification.current_binary_smoke
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "dist_preflight.runner" "error" $errorMessage @{
    build_output = $buildOutput
  }
}

$completedAt = (Get-Date).ToUniversalTime()
$hasFailingCheck = @($checks | Where-Object { $_.status -ne "ok" }).Count -gt 0
$ok = ($errorMessage -eq "" -and -not $hasFailingCheck)
$status = if ($ok) { "pass" } else { "fail" }
$evidencePath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-verify-dist-preflight-$timestamp" -Suffix "-$status.json"

$payload = [ordered]@{
  verification = [ordered]@{
    ok = $ok
    platform = Get-HostPlatform
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    version = $Version
    mode = $preflightMode
    production_eligible = (-not $DevelopmentUnsigned -and $null -ne $packageVerification -and [bool]$packageVerification.signed -and [bool]$packageVerification.source_clean)
    trusted_signer_thumbprint = if ($DevelopmentUnsigned) { $null } else { $TrustedSignerThumbprint }
    dist_dir = $distRoot
    distribution = $verification
    windows_package = $packageVerification
    error = $errorMessage
    checks = @($checks)
  }
}
$recordedCommand = @("deploy/run-steward-dist-preflight.ps1", "-Version", $Version)
if ($DevelopmentUnsigned) {
  $recordedCommand += "-DevelopmentUnsigned"
  if ($AllowDirtyWorktree) { $recordedCommand += "-AllowDirtyWorktree" }
} else {
  $recordedCommand += @("-SigningCertificateThumbprint", $SigningCertificateThumbprint, "-TimestampServer", $TimestampServer, "-TrustedSignerThumbprint", $TrustedSignerThumbprint)
}
$envelope = [ordered]@{
  kind = "dist-preflight"
  ok = $ok
  command = $recordedCommand
  created_at = $startedAt.ToString("o")
  payload = $payload
}
$envelope | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $evidencePath -Encoding UTF8

[ordered]@{
  ok = $ok
  platform = Get-HostPlatform
  evidence_path = $evidencePath
  dist_dir = $distRoot
  version = $Version
  mode = $preflightMode
  artifact_count = if ($null -ne $verification) { $verification.artifact_count } else { 0 }
  ui_included = if ($null -ne $verification) { $verification.ui_included } else { $false }
  windows_package_verified = if ($null -ne $packageVerification) { [bool]$packageVerification.package_mode } else { $false }
  production_eligible = if ($null -ne $packageVerification) { [bool]$packageVerification.signed -and [bool]$packageVerification.source_clean -and -not $DevelopmentUnsigned } else { $false }
  error = $errorMessage
} | ConvertTo-Json -Depth 5

if (-not $ok) {
  exit 1
}
