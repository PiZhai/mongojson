param(
  [string[]]$Targets = @("windows/amd64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"),

  [string]$OutputDir = "",

  [string]$Version = "",

  [switch]$SkipTests,

  [switch]$SkipUI,

  [switch]$SkipFrontendBuild,

  [switch]$Clean
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
    [string]$Child
  )
  $parentFull = [System.IO.Path]::GetFullPath($Parent).TrimEnd($PathSeparators)
  $childFull = [System.IO.Path]::GetFullPath($Child).TrimEnd($PathSeparators)
  $comparison = [System.StringComparison]::OrdinalIgnoreCase
  if (-not ($childFull.StartsWith($parentFull + [System.IO.Path]::DirectorySeparatorChar, $comparison) -or $childFull.StartsWith($parentFull + [System.IO.Path]::AltDirectorySeparatorChar, $comparison))) {
    throw "Refusing to use output path outside repository: $childFull"
  }
}

function Get-DefaultVersion {
  $tag = ""
  try {
    $tag = (git rev-parse --short HEAD 2>$null).Trim()
  } catch {
    $tag = ""
  }
  if ([string]::IsNullOrWhiteSpace($tag)) {
    return (Get-Date -Format "yyyyMMddHHmmss")
  }
  return $tag
}

function Get-GitCommit {
  try {
    $commit = (git rev-parse HEAD 2>$null).Trim()
    if (-not [string]::IsNullOrWhiteSpace($commit)) {
      return $commit
    }
  } catch {
  }
  return "unknown"
}

function Invoke-GoBuild {
  param(
    [string]$GOOSValue,
    [string]$GOARCHValue,
    [string]$OutputPath,
    [string]$BuildVersion,
    [string]$BuildCommit,
    [string]$BuildDate
  )
  $oldCGO = $env:CGO_ENABLED
  $oldGOOS = $env:GOOS
  $oldGOARCH = $env:GOARCH
  try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = $GOOSValue
    $env:GOARCH = $GOARCHValue
    $ldflags = "-s -w -X mongojson/backend/internal/buildinfo.Version=$BuildVersion -X mongojson/backend/internal/buildinfo.Commit=$BuildCommit -X mongojson/backend/internal/buildinfo.BuildDate=$BuildDate"
    go build -buildvcs=false -trimpath -ldflags $ldflags -o $OutputPath ./cmd/steward
    if ($LASTEXITCODE -ne 0) {
      throw "go build failed for $GOOSValue/$GOARCHValue with exit code $LASTEXITCODE"
    }
  } finally {
    $env:CGO_ENABLED = $oldCGO
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
  }
}

function Invoke-CompanionGoBuild {
  param(
    [string]$GOOSValue,
    [string]$GOARCHValue,
    [string]$OutputPath
  )
  $oldCGO = $env:CGO_ENABLED
  $oldGOOS = $env:GOOS
  $oldGOARCH = $env:GOARCH
  try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = $GOOSValue
    $env:GOARCH = $GOARCHValue
    go build -buildvcs=false -trimpath -ldflags "-s -w" -o $OutputPath ./cmd/steward-companion
    if ($LASTEXITCODE -ne 0) {
      throw "go build steward-companion failed for $GOOSValue/$GOARCHValue with exit code $LASTEXITCODE"
    }
  } finally {
    $env:CGO_ENABLED = $oldCGO
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
  }
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
$frontendDir = Join-Path $repoRoot "frontend"
$frontendDist = Join-Path $frontendDir "dist"
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
  $OutputDir = Join-Path $backendDir "dist\steward"
}

$outputRoot = [System.IO.Path]::GetFullPath($OutputDir)
Assert-ChildPath -Parent $repoRoot -Child $outputRoot

if ([string]::IsNullOrWhiteSpace($Version)) {
  $Version = Get-DefaultVersion
}
$safeVersion = $Version -replace '[^A-Za-z0-9._-]', '-'
if ([string]::IsNullOrWhiteSpace($safeVersion)) {
  throw "Version resolved to an empty artifact suffix"
}
$buildCommit = Get-GitCommit
$buildDate = (Get-Date).ToUniversalTime().ToString("o")

if ($Clean -and (Test-Path -LiteralPath $outputRoot)) {
  $resolvedOutputRoot = [System.IO.Path]::GetFullPath($outputRoot)
  Assert-ChildPath -Parent $repoRoot -Child $resolvedOutputRoot
  Remove-Item -LiteralPath $resolvedOutputRoot -Recurse -Force
}

New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null

if (-not $SkipUI) {
  if (-not $SkipFrontendBuild) {
    Require-Command "npm"
    Push-Location $frontendDir
    try {
      Write-Host "[steward] Building bundled workspace"
      npm run build
      if ($LASTEXITCODE -ne 0) {
        throw "npm run build failed with exit code $LASTEXITCODE"
      }
    } finally {
      Pop-Location
    }
  }
  $frontendIndex = Join-Path $frontendDist "index.html"
  if (-not (Test-Path -LiteralPath $frontendIndex -PathType Leaf)) {
    throw "Bundled steward workspace is missing index.html: $frontendIndex"
  }
}

Push-Location $backendDir
try {
  $goVersion = (go env GOVERSION).Trim()
  if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($goVersion)) {
    throw "go env GOVERSION failed"
  }
  if (-not $SkipTests) {
    Write-Host "[steward] Running CLI and companion tests"
    go test ./cmd/steward ./cmd/steward-companion ./internal/service/stewardcompanion
    if ($LASTEXITCODE -ne 0) {
      throw "steward packaging tests failed with exit code $LASTEXITCODE"
    }
  }

  $artifacts = @()
  foreach ($target in $Targets) {
    $parts = $target.Split("/")
    if ($parts.Count -ne 2 -or [string]::IsNullOrWhiteSpace($parts[0]) -or [string]::IsNullOrWhiteSpace($parts[1])) {
      throw "Invalid target '$target'. Expected GOOS/GOARCH, for example windows/amd64."
    }
    $goos = $parts[0].Trim()
    $goarch = $parts[1].Trim()
    $extension = ""
    if ($goos -eq "windows") {
      $extension = ".exe"
    }
    $artifactName = "steward-$safeVersion-$goos-$goarch"
    $artifactDir = Join-Path $outputRoot $artifactName
    if (Test-Path -LiteralPath $artifactDir) {
      Assert-ChildPath -Parent $outputRoot -Child $artifactDir
      Remove-Item -LiteralPath $artifactDir -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $artifactDir | Out-Null
    $binaryPath = Join-Path $artifactDir ("steward" + $extension)
    $companionPath = Join-Path $artifactDir ("steward-companion" + $extension)

    Write-Host "[steward] Building $target -> $binaryPath"
    Invoke-GoBuild -GOOSValue $goos -GOARCHValue $goarch -OutputPath $binaryPath -BuildVersion $safeVersion -BuildCommit $buildCommit -BuildDate $buildDate
    Write-Host "[steward] Building companion $target -> $companionPath"
    Invoke-CompanionGoBuild -GOOSValue $goos -GOARCHValue $goarch -OutputPath $companionPath

    $uiRelativePath = $null
    if (-not $SkipUI) {
      $uiDir = Join-Path $artifactDir "ui"
      New-Item -ItemType Directory -Force -Path $uiDir | Out-Null
      Copy-Item -Path (Join-Path $frontendDist "*") -Destination $uiDir -Recurse -Force
      $uiRelativePath = $uiDir.Substring($outputRoot.Length).TrimStart($PathSeparators) -replace "\\", "/"
    }

    $artifactFiles = @()
    $paths = @($binaryPath, $companionPath)
    if (-not $SkipUI) {
      $paths += @(Get-ChildItem -LiteralPath (Join-Path $artifactDir "ui") -Recurse -File | Select-Object -ExpandProperty FullName)
    }
    foreach ($path in $paths) {
      $fileHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
      $fileRelativePath = $path.Substring($outputRoot.Length).TrimStart($PathSeparators) -replace "\\", "/"
      $artifactFiles += [pscustomobject]@{
        path = $fileRelativePath
        sha256 = $fileHash
      }
    }
    $binaryRecord = $artifactFiles[0]
    $companionRecord = $artifactFiles[1]
    $artifacts += [pscustomobject]@{
      target = $target
      path = $binaryRecord.path
      sha256 = $binaryRecord.sha256
      companion_path = $companionRecord.path
      ui_dir = $uiRelativePath
      files = @($artifactFiles)
    }
  }

  $checksumsPath = Join-Path $outputRoot "SHA256SUMS.txt"
  $manifestPath = Join-Path $outputRoot "manifest.json"
  $checksumRecords = @{}
  foreach ($artifact in $artifacts) {
    foreach ($file in $artifact.files) {
      if ($checksumRecords.ContainsKey($file.path)) {
        throw "Duplicate steward artifact path: $($file.path)"
      }
      $checksumRecords[$file.path] = $file.sha256
    }
  }
  $checksumLines = $checksumRecords.GetEnumerator() | Sort-Object Name | ForEach-Object { "$($_.Value)  $($_.Name)" }
  Set-Content -LiteralPath $checksumsPath -Value $checksumLines -Encoding ASCII
  $manifest = [pscustomobject]@{
    name = "steward"
    version = $safeVersion
    commit = $buildCommit
    built_at = $buildDate
    go_version = $goVersion
    ui_included = -not $SkipUI
    artifacts = $artifacts
  }
  $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $manifestPath -Encoding ASCII

  Write-Host ""
  Write-Host "[steward] Built $($artifacts.Count) artifact(s)"
  Write-Host "[steward] Output: $outputRoot"
  Write-Host "[steward] Checksums: $checksumsPath"
  Write-Host "[steward] Manifest: $manifestPath"
} finally {
  Pop-Location
}
