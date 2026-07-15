param(
  [string]$EvidenceDir = "",

  [string]$Version = "",

  [switch]$SkipFrontendBuild
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
$errorMessage = ""

try {
  $buildParameters = @{
    OutputDir = $distRoot
    Version = $Version
    Clean = $true
  }
  if ($SkipFrontendBuild) {
    $buildParameters.SkipFrontendBuild = $true
  }
  $buildOutput = @(& (Join-Path $PSScriptRoot "build-steward.ps1") @buildParameters *>&1 | ForEach-Object { "$_" })
  if ($LASTEXITCODE -ne 0) {
    throw "steward dist build failed with exit code $LASTEXITCODE"
  }
  Add-Check $checks "dist_preflight.build" "ok" "five target steward distribution directories were built" @{ dist_dir = $distRoot }

  $verifyOutput = @(& (Join-Path $PSScriptRoot "verify-steward-dist.ps1") -DistDir $distRoot -ExpectedVersion $Version -RunCurrentBinary *>&1 | ForEach-Object { "$_" })
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
    dist_dir = $distRoot
    distribution = $verification
    error = $errorMessage
    checks = @($checks)
  }
}
$envelope = [ordered]@{
  kind = "dist-preflight"
  ok = $ok
  command = @("deploy/run-steward-dist-preflight.ps1", "-Version", $Version)
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
  artifact_count = if ($null -ne $verification) { $verification.artifact_count } else { 0 }
  ui_included = if ($null -ne $verification) { $verification.ui_included } else { $false }
  error = $errorMessage
} | ConvertTo-Json -Depth 5

if (-not $ok) {
  exit 1
}
