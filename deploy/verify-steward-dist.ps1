param(
  [string]$DistDir = "",

  [string[]]$RequiredTargets = @("windows/amd64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"),

  [string]$ExpectedVersion = "",

  [switch]$RunCurrentBinary
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
    throw "$Label is outside steward dist directory: $childFull"
  }
}

function Normalize-ArtifactPath {
  param([string]$Path)
  return ($Path -replace "\\", "/").Trim()
}

function Read-ChecksumFile {
  param([string]$Path)
  $checksums = @{}
  $lines = Get-Content -LiteralPath $Path
  foreach ($line in $lines) {
    if ([string]::IsNullOrWhiteSpace($line)) {
      continue
    }
    if ($line -notmatch '^([A-Fa-f0-9]{64})\s+(.+)$') {
      throw "Invalid SHA256SUMS line: $line"
    }
    $hash = $Matches[1].ToLowerInvariant()
    $artifactPath = Normalize-ArtifactPath $Matches[2]
    if ($checksums.ContainsKey($artifactPath)) {
      throw "Duplicate checksum path: $artifactPath"
    }
    $checksums[$artifactPath] = $hash
  }
  return $checksums
}

function Get-CurrentTarget {
  $goos = "unknown"
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
    $goos = "windows"
  } elseif ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)) {
    $goos = "darwin"
  } elseif ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)) {
    $goos = "linux"
  }

  $arch = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString().ToLowerInvariant()
  $goarch = switch ($arch) {
    "x64" { "amd64" }
    "arm64" { "arm64" }
    default { $arch }
  }
  return "$goos/$goarch"
}

function Invoke-CurrentBinarySmoke {
  param(
    [string]$BinaryPath,
    [object]$Manifest,
    [string]$Target
  )
  $output = & $BinaryPath version
  if ($LASTEXITCODE -ne 0) {
    throw "Current platform steward binary version smoke failed with exit code $LASTEXITCODE"
  }
  $version = $output | ConvertFrom-Json
  if ($version.name -ne "steward") {
    throw "Current platform binary returned unexpected name '$($version.name)'"
  }
  if ($version.version -ne $Manifest.version) {
    throw "Current platform binary version '$($version.version)' does not match manifest '$($Manifest.version)'"
  }
  if ($version.commit -ne $Manifest.commit) {
    throw "Current platform binary commit '$($version.commit)' does not match manifest '$($Manifest.commit)'"
  }
  $reportedTarget = "$($version.goos)/$($version.goarch)"
  if ($reportedTarget -ne $Target) {
    throw "Current platform binary target '$reportedTarget' does not match expected '$Target'"
  }
  return $version
}

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
if ([string]::IsNullOrWhiteSpace($DistDir)) {
  $DistDir = Join-Path $repoRoot "backend\dist\steward"
}

$distRoot = (Resolve-Path -LiteralPath $DistDir).Path
$manifestPath = Join-Path $distRoot "manifest.json"
$checksumsPath = Join-Path $distRoot "SHA256SUMS.txt"

if (-not (Test-Path -LiteralPath $manifestPath)) {
  throw "Missing steward dist manifest: $manifestPath"
}
if (-not (Test-Path -LiteralPath $checksumsPath)) {
  throw "Missing steward dist checksums: $checksumsPath"
}

$manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
if ($manifest.name -ne "steward") {
  throw "Manifest name must be steward, got '$($manifest.name)'"
}
if (-not [string]::IsNullOrWhiteSpace($ExpectedVersion) -and $manifest.version -ne $ExpectedVersion) {
  throw "Manifest version '$($manifest.version)' does not match expected '$ExpectedVersion'"
}
if ($null -eq $manifest.artifacts -or $manifest.artifacts.Count -eq 0) {
  throw "Manifest does not contain steward artifacts"
}

$checksums = Read-ChecksumFile $checksumsPath
$artifactTargets = @{}
$artifactPaths = @{}
$verifiedArtifacts = @()
$currentTarget = Get-CurrentTarget
$currentSmoke = $null

foreach ($artifact in $manifest.artifacts) {
  $target = [string]$artifact.target
  $relativePath = Normalize-ArtifactPath ([string]$artifact.path)
  $expectedHash = ([string]$artifact.sha256).ToLowerInvariant()
  if ([string]::IsNullOrWhiteSpace($target) -or [string]::IsNullOrWhiteSpace($relativePath) -or [string]::IsNullOrWhiteSpace($expectedHash)) {
    throw "Manifest artifact is missing target, path, or sha256"
  }
  if ($artifactTargets.ContainsKey($target)) {
    throw "Duplicate artifact target in manifest: $target"
  }
  if ($artifactPaths.ContainsKey($relativePath)) {
    throw "Duplicate artifact path in manifest: $relativePath"
  }
  if ($expectedHash -notmatch '^[a-f0-9]{64}$') {
    throw "Invalid sha256 for $relativePath"
  }
  $artifactTargets[$target] = $true
  $artifactPaths[$relativePath] = $true

  $artifactPath = Join-Path $distRoot $relativePath
  Assert-ChildPath -Parent $distRoot -Child $artifactPath -Label "Artifact path"
  if (-not (Test-Path -LiteralPath $artifactPath)) {
    throw "Missing artifact file: $artifactPath"
  }
  $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $artifactPath).Hash.ToLowerInvariant()
  if ($actualHash -ne $expectedHash) {
    throw "Hash mismatch for $relativePath"
  }
  if (-not $checksums.ContainsKey($relativePath)) {
    throw "SHA256SUMS is missing $relativePath"
  }
  if ($checksums[$relativePath] -ne $expectedHash) {
    throw "SHA256SUMS hash for $relativePath does not match manifest"
  }
  if ($RunCurrentBinary -and $target -eq $currentTarget) {
    $currentSmoke = Invoke-CurrentBinarySmoke -BinaryPath $artifactPath -Manifest $manifest -Target $target
  }
  $verifiedArtifacts += [pscustomobject]@{
    target = $target
    path = $relativePath
    sha256 = $actualHash
  }
}

foreach ($path in $checksums.Keys) {
  if (-not $artifactPaths.ContainsKey($path)) {
    throw "SHA256SUMS contains an artifact not present in manifest: $path"
  }
}

foreach ($target in $RequiredTargets) {
  $target = $target.Trim()
  if ($target -eq "") {
    continue
  }
  if (-not $artifactTargets.ContainsKey($target)) {
    throw "Required steward target is missing: $target"
  }
}

if ($RunCurrentBinary -and $null -eq $currentSmoke) {
  throw "No artifact matched current platform target $currentTarget for binary smoke test"
}

$summary = [pscustomobject]@{
  ok = $true
  dist_dir = $distRoot
  version = $manifest.version
  commit = $manifest.commit
  required_targets = $RequiredTargets
  artifact_count = $verifiedArtifacts.Count
  current_target = $currentTarget
  current_binary_smoke = $currentSmoke
  artifacts = $verifiedArtifacts
}

$summary | ConvertTo-Json -Depth 6
