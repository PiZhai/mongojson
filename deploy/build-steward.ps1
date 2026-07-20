param(
  [string[]]$Targets = @("windows/amd64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"),

  [string]$OutputDir = "",

  [string]$Version = "",

  [switch]$SkipTests,

  [switch]$SkipUI,

  [switch]$SkipFrontendBuild,

  [switch]$Clean,

  [switch]$AllowDirtyWorktree,

  [string]$SigningCertificateThumbprint = "",

  [string]$TimestampServer = "",

  [switch]$RequireSignedPackage
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
    $ldflags = "-s -w"
    if ($GOOSValue -eq "windows") {
      # Session Companion is a background desktop helper. Building it as a GUI
      # subsystem executable prevents Windows Terminal from appearing at logon.
      $ldflags += " -H windowsgui"
    }
    go build -buildvcs=false -trimpath -ldflags $ldflags -o $OutputPath ./cmd/steward-companion
    if ($LASTEXITCODE -ne 0) {
      throw "go build steward-companion failed for $GOOSValue/$GOARCHValue with exit code $LASTEXITCODE"
    }
  } finally {
    $env:CGO_ENABLED = $oldCGO
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
  }
}

function New-ProtectedBuildStage {
  param([string]$FinalOutputRoot)

  $finalRoot = [System.IO.Path]::GetFullPath($FinalOutputRoot).TrimEnd($PathSeparators)
  $parent = Split-Path -Parent $finalRoot
  $leaf = Split-Path -Leaf $finalRoot
  if ([string]::IsNullOrWhiteSpace($parent) -or [string]::IsNullOrWhiteSpace($leaf)) {
    throw "Unable to derive a staging location for output path: $finalRoot"
  }
  New-Item -ItemType Directory -Force -Path $parent | Out-Null

  # Keep the sibling stage name deliberately short. Windows' FileCatalog
  # implementation still reaches MAX_PATH-era hashing code even when normal
  # PowerShell file APIs can read the same payload. Repeating a long release
  # name here made deeply nested UI assets cross that boundary during signing.
  # A GUID keeps concurrent stages unique while the shared parent preserves the
  # same-volume atomic rename required by Publish-StagedBuild.
  $stage = Join-Path $parent (".stg-" + [guid]::NewGuid().ToString("N"))
  New-Item -ItemType Directory -Path $stage | Out-Null
  try {
    if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
      $currentSID = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
      & icacls.exe $stage /inheritance:r /grant:r "*$($currentSID):(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null
      if ($LASTEXITCODE -ne 0) {
        throw "icacls failed with exit code $LASTEXITCODE"
      }
    } else {
      & chmod 700 -- $stage
      if ($LASTEXITCODE -ne 0) {
        throw "chmod failed with exit code $LASTEXITCODE"
      }
    }
    return [System.IO.Path]::GetFullPath($stage)
  } catch {
    Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    throw "Unable to protect release staging directory '$stage': $($_.Exception.Message)"
  }
}

function Publish-StagedBuild {
  param(
    [string]$StageRoot,
    [string]$FinalOutputRoot
  )

  $stage = [System.IO.Path]::GetFullPath($StageRoot).TrimEnd($PathSeparators)
  $final = [System.IO.Path]::GetFullPath($FinalOutputRoot).TrimEnd($PathSeparators)
  $stageParent = Split-Path -Parent $stage
  $finalParent = Split-Path -Parent $final
  if (-not $stageParent.Equals($finalParent, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Release stage and final output must be siblings so publication is an atomic same-volume rename"
  }
  if (-not (Test-Path -LiteralPath $stage -PathType Container)) {
    throw "Release stage is missing before publication: $stage"
  }

  $backup = Join-Path $finalParent ("." + (Split-Path -Leaf $final) + ".previous-" + [guid]::NewGuid().ToString("N"))
  $previousMoved = $false
  try {
    if (Test-Path -LiteralPath $final) {
      $existing = Get-Item -LiteralPath $final -Force
      if (-not $existing.PSIsContainer -or ($existing.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
        throw "Refusing to replace a non-directory or reparse-point output path: $final"
      }
      [System.IO.Directory]::Move($final, $backup)
      $previousMoved = $true
    }
    [System.IO.Directory]::Move($stage, $final)
  } catch {
    $publishError = $_.Exception.Message
    if ($previousMoved -and -not (Test-Path -LiteralPath $final) -and (Test-Path -LiteralPath $backup -PathType Container)) {
      try {
        [System.IO.Directory]::Move($backup, $final)
      } catch {
        throw "Atomic release publication failed ('$publishError') and restoring the previous output also failed: $($_.Exception.Message)"
      }
    }
    throw "Atomic release publication failed: $publishError"
  }

  if ($previousMoved -and (Test-Path -LiteralPath $backup)) {
    try {
      Remove-Item -LiteralPath $backup -Recurse -Force
    } catch {
      Write-Warning "The new release was published, but the isolated previous output could not be removed: $backup ($($_.Exception.Message))"
    }
  }
}

function Get-ReleaseSigningCertificate {
  param([string]$Thumbprint)
  if ([string]::IsNullOrWhiteSpace($Thumbprint)) {
    return $null
  }
  if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
    throw "Authenticode release signing is only supported by this build script on Windows"
  }
  $normalized = ($Thumbprint -replace '\s', '').ToUpperInvariant()
  foreach ($store in @("Cert:\CurrentUser\My", "Cert:\LocalMachine\My")) {
    $candidate = Get-ChildItem -LiteralPath $store -ErrorAction SilentlyContinue |
      Where-Object { $_.Thumbprint -eq $normalized -and $_.HasPrivateKey } |
      Select-Object -First 1
    if ($null -ne $candidate) {
      return $candidate
    }
  }
  throw "Release signing certificate '$normalized' with a private key was not found in CurrentUser or LocalMachine My stores"
}

function Resolve-ReleaseTimestampServer {
  param([string]$TimestampURL)
  if ([string]::IsNullOrWhiteSpace($TimestampURL)) {
    return ""
  }
  try {
    $uri = [Uri]$TimestampURL
  } catch {
    throw "TimestampServer must be an absolute HTTP URL: $TimestampURL"
  }
  if (-not $uri.IsAbsoluteUri -or $uri.Scheme -ne [Uri]::UriSchemeHttp) {
    throw "TimestampServer must start with http://. Set-AuthenticodeSignature uses a Windows API that does not support HTTPS timestamp URLs."
  }
  return $uri.AbsoluteUri
}

function Set-ReleaseAuthenticodeSignature {
  param(
    [string[]]$Paths,
    [System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate,
    [string]$TimestampURL
  )
  foreach ($path in $Paths) {
    $parameters = @{ FilePath = $path; Certificate = $Certificate; HashAlgorithm = "SHA256" }
    if (-not [string]::IsNullOrWhiteSpace($TimestampURL)) {
      $parameters.TimestampServer = $TimestampURL
    }
    $signature = Set-AuthenticodeSignature @parameters
    if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::Valid) {
      throw "Authenticode signing failed for '$path': $($signature.StatusMessage)"
    }
    if (-not [string]::IsNullOrWhiteSpace($TimestampURL) -and $null -eq $signature.TimeStamperCertificate) {
      throw "Authenticode signing did not produce a trusted timestamp for '$path'"
    }
  }
}

function Write-DetachedManifestSignature {
  param(
    [string]$ManifestPath,
    [string]$SignaturePath,
    [System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate
  )
  Add-Type -AssemblyName System.Security.Cryptography.Pkcs
  $content = [System.Security.Cryptography.Pkcs.ContentInfo]::new([IO.File]::ReadAllBytes($ManifestPath))
  $cms = [System.Security.Cryptography.Pkcs.SignedCms]::new($content, $true)
  $signer = [System.Security.Cryptography.Pkcs.CmsSigner]::new($Certificate)
  $signer.IncludeOption = [System.Security.Cryptography.X509Certificates.X509IncludeOption]::ExcludeRoot
  $cms.ComputeSignature($signer)
  [IO.File]::WriteAllBytes($SignaturePath, $cms.Encode())
}

function Invoke-BrokerGoBuild {
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
    go build -buildvcs=false -trimpath -ldflags $ldflags -o $OutputPath ./cmd/steward-broker
    if ($LASTEXITCODE -ne 0) {
      throw "go build steward-broker failed for $GOOSValue/$GOARCHValue with exit code $LASTEXITCODE"
    }
  } finally {
    $env:CGO_ENABLED = $oldCGO
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
  }
}

function Invoke-ApprovalGoBuild {
  param([string]$GOOSValue,[string]$GOARCHValue,[string]$OutputPath)
  $oldCGO=$env:CGO_ENABLED; $oldGOOS=$env:GOOS; $oldGOARCH=$env:GOARCH
  try {
    $env:CGO_ENABLED='0'; $env:GOOS=$GOOSValue; $env:GOARCH=$GOARCHValue
    go build -buildvcs=false -trimpath -ldflags '-s -w' -o $OutputPath ./cmd/steward-approval
    if($LASTEXITCODE -ne 0){throw "go build steward-approval failed for $GOOSValue/$GOARCHValue"}
  } finally { $env:CGO_ENABLED=$oldCGO; $env:GOOS=$oldGOOS; $env:GOARCH=$oldGOARCH }
}

function Invoke-SystemToolHostGoBuild {
  param([string]$GOOSValue,[string]$GOARCHValue,[string]$OutputPath)
  $oldCGO=$env:CGO_ENABLED; $oldGOOS=$env:GOOS; $oldGOARCH=$env:GOARCH
  try {
    $env:CGO_ENABLED='0'; $env:GOOS=$GOOSValue; $env:GOARCH=$GOARCHValue
    go build -buildvcs=false -trimpath -ldflags '-s -w' -o $OutputPath ./cmd/steward-system-tool-host
    if($LASTEXITCODE -ne 0){throw "go build steward-system-tool-host failed for $GOOSValue/$GOARCHValue"}
  } finally { $env:CGO_ENABLED=$oldCGO; $env:GOOS=$oldGOOS; $env:GOARCH=$oldGOARCH }
}

function Invoke-WindowsNotifierBuild {
  param([string]$OutputDirectory)
  Require-Command "dotnet"
  $project = Join-Path $backendDir "cmd\steward-windows-notifier\Steward.WindowsNotifier.csproj"
  # Keep the .NET runtime framework-dependent and publish the Windows App SDK
  # files beside the helper. A fully self-contained single executable is over
  # 200 MB and provides no operational benefit inside the Steward artifact.
  $publishDir = Join-Path $OutputDirectory "windows-notifier"
  if (Test-Path -LiteralPath $publishDir) {
    Remove-Item -LiteralPath $publishDir -Recurse -Force
  }
  dotnet publish $project -c Release -r win-x64 --self-contained false -p:PublishSingleFile=false -p:RestoreLockedMode=true -o $publishDir | Out-Host
  if ($LASTEXITCODE -ne 0) {
    throw "steward Windows notifier build failed with exit code $LASTEXITCODE"
  }
  $source = Join-Path $publishDir "steward-windows-notifier.exe"
  if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
    throw "steward Windows notifier output is missing: $source"
  }
  return $publishDir
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
$frontendDir = Join-Path $repoRoot "frontend"
$frontendDist = Join-Path $frontendDir "dist"
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
  $OutputDir = Join-Path $backendDir "dist\steward"
}

$finalOutputRoot = [System.IO.Path]::GetFullPath($OutputDir)
Assert-ChildPath -Parent $repoRoot -Child $finalOutputRoot

$gitTopLevel = (git -C $repoRoot rev-parse --show-toplevel 2>$null | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($gitTopLevel)) {
  throw "Steward production artifacts must be built from a Git worktree"
}
$gitStatus = @(git -C $repoRoot status --porcelain=v1 --untracked-files=all 2>$null)
if ($LASTEXITCODE -ne 0) {
  throw "Unable to inspect Git worktree state"
}
$sourceDirty = $gitStatus.Count -gt 0
$initialGitStatus = $gitStatus -join "`n"
if ($sourceDirty -and -not $AllowDirtyWorktree) {
  $preview = ($gitStatus | Select-Object -First 20) -join [Environment]::NewLine
  throw "Refusing to build a release from a dirty Git worktree. Commit or remove all changes, or use -AllowDirtyWorktree for an explicitly marked development package.`n$preview"
}
if ($sourceDirty) {
  Write-Warning "DEVELOPMENT BUILD: the Git worktree is dirty. The package will be marked source_clean=false and production installers will reject it by default."
}

if ([string]::IsNullOrWhiteSpace($Version)) {
  $Version = Get-DefaultVersion
}
$safeVersion = $Version -replace '[^A-Za-z0-9._-]', '-'
if ([string]::IsNullOrWhiteSpace($safeVersion)) {
  throw "Version resolved to an empty artifact suffix"
}
$buildCommit = Get-GitCommit
if ($buildCommit -notmatch '^[a-fA-F0-9]{40}$') {
  throw "Unable to resolve a full Git commit for the release"
}
$buildDate = (Get-Date).ToUniversalTime().ToString("o")
$signingCertificate = Get-ReleaseSigningCertificate -Thumbprint $SigningCertificateThumbprint
if ($RequireSignedPackage -and $null -eq $signingCertificate) {
  throw "-RequireSignedPackage requires -SigningCertificateThumbprint"
}
$packageSigned = $null -ne $signingCertificate
if ($packageSigned -and [string]::IsNullOrWhiteSpace($TimestampServer)) {
  throw "Every signed package requires -TimestampServer so executable and package-catalog signatures remain valid after certificate expiry"
}
if ($packageSigned) {
  $TimestampServer = Resolve-ReleaseTimestampServer -TimestampURL $TimestampServer
}
if ($packageSigned -and ($SkipTests -or $SkipFrontendBuild -or $SkipUI)) {
  throw "Signed release packages require the full Go test suite and a freshly tested, linted, and built frontend; do not use -SkipTests, -SkipFrontendBuild, or -SkipUI"
}
if (-not $packageSigned) {
  Write-Warning "DEVELOPMENT UNSIGNED PACKAGE: no signing certificate was configured. Production installation requires a signed package unless -AllowUnsignedPackage is explicitly supplied."
}

if (Test-Path -LiteralPath $finalOutputRoot) {
  $existingOutput = Get-Item -LiteralPath $finalOutputRoot -Force
  if (-not $existingOutput.PSIsContainer -or ($existingOutput.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
    throw "Refusing to use a non-directory or reparse-point output path: $finalOutputRoot"
  }
}

$stageRoot = New-ProtectedBuildStage -FinalOutputRoot $finalOutputRoot
$outputRoot = $stageRoot
$published = $false
try {
# A release is always assembled from an empty stage. Carrying files forward
# from a previous output would let stale or unrelated payloads become part of
# the new manifest and (for signed Windows builds) the trusted package catalog.
# -Clean is retained for command-line compatibility; atomic publication replaces
# the previous output only after the new package has passed all checks.

if (-not $SkipUI) {
  if (-not $SkipFrontendBuild) {
    Require-Command "npm"
    Push-Location $frontendDir
    try {
      if (-not $SkipTests) {
        Write-Host "[steward] Testing and linting bundled workspace"
        npm test -- --run
        if ($LASTEXITCODE -ne 0) {
          throw "npm test failed with exit code $LASTEXITCODE"
        }
        npm run lint
        if ($LASTEXITCODE -ne 0) {
          throw "npm run lint failed with exit code $LASTEXITCODE"
        }
      }
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
    Write-Host "[steward] Running the complete backend test suite"
    go test ./...
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
    $brokerPath = Join-Path $artifactDir ("steward-broker" + $extension)
	$approvalPath = Join-Path $artifactDir ("steward-approval" + $extension)
	$systemToolHostPath = Join-Path $artifactDir ("steward-system-tool-host" + $extension)

    Write-Host "[steward] Building $target -> $binaryPath"
    Invoke-GoBuild -GOOSValue $goos -GOARCHValue $goarch -OutputPath $binaryPath -BuildVersion $safeVersion -BuildCommit $buildCommit -BuildDate $buildDate
    Write-Host "[steward] Building companion $target -> $companionPath"
    Invoke-CompanionGoBuild -GOOSValue $goos -GOARCHValue $goarch -OutputPath $companionPath
	Write-Host "[steward] Building Privilege Broker $target -> $brokerPath"
	Invoke-BrokerGoBuild -GOOSValue $goos -GOARCHValue $goarch -OutputPath $brokerPath -BuildVersion $safeVersion -BuildCommit $buildCommit -BuildDate $buildDate
	Write-Host "[steward] Building approval authority $target -> $approvalPath"
	Invoke-ApprovalGoBuild -GOOSValue $goos -GOARCHValue $goarch -OutputPath $approvalPath
	Write-Host "[steward] Building System Tool Host $target -> $systemToolHostPath"
	Invoke-SystemToolHostGoBuild -GOOSValue $goos -GOARCHValue $goarch -OutputPath $systemToolHostPath
	$notifierDirectory = $null
	if ($goos -eq "windows" -and $goarch -eq "amd64") {
	  Write-Host "[steward] Building Windows App SDK notification helper"
	  $notifierDirectory = Invoke-WindowsNotifierBuild -OutputDirectory $artifactDir
	}

    $uiRelativePath = $null
    if (-not $SkipUI) {
      $uiDir = Join-Path $artifactDir "ui"
      New-Item -ItemType Directory -Force -Path $uiDir | Out-Null
      Copy-Item -Path (Join-Path $frontendDist "*") -Destination $uiDir -Recurse -Force
      $uiRelativePath = $uiDir.Substring($outputRoot.Length).TrimStart($PathSeparators) -replace "\\", "/"
    }
	$deploymentScriptPaths = @()
	if ($goos -eq "windows") {
	  foreach ($scriptName in @(
	    "install-steward-production.ps1", "update-steward-production.ps1", "uninstall-steward-production.ps1",
	    "test-steward-production.ps1", "install-steward-companion.ps1", "uninstall-steward-companion.ps1",
	    "migrate-steward-production.ps1", "rotate-steward-broker-keys.ps1", "test-steward-broker-session0.ps1",
	    "verify-steward-dist.ps1"
	  )) {
	    $scriptSource = Join-Path $repoRoot ("deploy\" + $scriptName)
	    $scriptTarget = Join-Path $artifactDir $scriptName
	    Copy-Item -LiteralPath $scriptSource -Destination $scriptTarget -Force
	    $deploymentScriptPaths += $scriptTarget
	  }
	}

    $ownedSignablePaths = @($binaryPath, $companionPath, $brokerPath, $approvalPath, $systemToolHostPath) + $deploymentScriptPaths
    if ($null -ne $notifierDirectory) {
      $notifierExecutable = Join-Path $notifierDirectory "steward-windows-notifier.exe"
      if (Test-Path -LiteralPath $notifierExecutable -PathType Leaf) {
        $ownedSignablePaths += $notifierExecutable
      }
    }
    if ($packageSigned -and $goos -eq "windows") {
      Write-Host "[steward] Authenticode signing owned Windows release files"
      Set-ReleaseAuthenticodeSignature -Paths $ownedSignablePaths -Certificate $signingCertificate -TimestampURL $TimestampServer
    }

    $payloadPaths = @(Get-ChildItem -LiteralPath $artifactDir -Recurse -File | Select-Object -ExpandProperty FullName)
    $packageFiles = @()
    foreach ($path in $payloadPaths) {
      $fileRelativePath = $path.Substring($artifactDir.Length).TrimStart($PathSeparators) -replace "\\", "/"
      $packageFiles += [pscustomobject]@{
        path = $fileRelativePath
        sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
      }
    }
    $packageChecksumsPath = Join-Path $artifactDir "SHA256SUMS.txt"
    $packageChecksumLines = $packageFiles | Sort-Object path | ForEach-Object { "$($_.sha256)  $($_.path)" }
    Set-Content -LiteralPath $packageChecksumsPath -Value $packageChecksumLines -Encoding ASCII

    $packageManifestPath = Join-Path $artifactDir "release-manifest.json"
    $packageSignaturePath = Join-Path $artifactDir "release-manifest.p7s"
    $packageCatalogPath = Join-Path $artifactDir "release-catalog.cat"
    $packageCatalogRelativePath = if ($packageSigned -and $goos -eq "windows") { "release-catalog.cat" } else { "" }
    $signedRelativePaths = @()
    if ($packageSigned -and $goos -eq "windows") {
      $signedRelativePaths = @($ownedSignablePaths | ForEach-Object {
        $_.Substring($artifactDir.Length).TrimStart($PathSeparators) -replace "\\", "/"
      } | Sort-Object -Unique)
    }
    $releaseKind = if (-not $packageSigned) { "development_unsigned" } elseif ($sourceDirty) { "development_dirty" } else { "production_signed" }
    $packageManifest = [ordered]@{
      schema = "mongojson.steward.release/v1"
      name = "steward"
      target = $target
      package_target = $target
      ui_included = -not $SkipUI
      version = $safeVersion
      commit = $buildCommit
      built_at = $buildDate
      go_version = $goVersion
      source_clean = -not $sourceDirty
      release_kind = $releaseKind
      files = @($packageFiles)
      signing = [ordered]@{
        required = $packageSigned
        signer_thumbprint = if ($packageSigned) { $signingCertificate.Thumbprint.ToUpperInvariant() } else { "" }
        manifest_signature = if ($packageSigned) { "release-manifest.p7s" } else { "" }
        package_catalog = $packageCatalogRelativePath
        signed_files = @($signedRelativePaths)
      }
    }
    [IO.File]::WriteAllText($packageManifestPath, ($packageManifest | ConvertTo-Json -Depth 7), [Text.UTF8Encoding]::new($false))
    if ($packageSigned) {
      Write-DetachedManifestSignature -ManifestPath $packageManifestPath -SignaturePath $packageSignaturePath -Certificate $signingCertificate
    }
    if ($packageSigned -and $goos -eq "windows") {
      Write-Host "[steward] Creating and timestamp-signing the Windows package catalog"
      New-FileCatalog -Path $artifactDir -CatalogFilePath $packageCatalogPath -CatalogVersion 2 | Out-Null
      Set-ReleaseAuthenticodeSignature -Paths @($packageCatalogPath) -Certificate $signingCertificate -TimestampURL $TimestampServer
      $catalogStatus = Test-FileCatalog -Path $artifactDir -CatalogFilePath $packageCatalogPath
      if ($catalogStatus -ne [System.Management.Automation.CatalogValidationStatus]::Valid) {
        throw "Generated Windows package catalog is not valid: $catalogStatus"
      }
    }

    $artifactFiles = @()
    $paths = @(Get-ChildItem -LiteralPath $artifactDir -Recurse -File | Select-Object -ExpandProperty FullName)
    foreach ($path in $paths) {
      $fileHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
      $fileRelativePath = $path.Substring($outputRoot.Length).TrimStart($PathSeparators) -replace "\\", "/"
      $artifactFiles += [pscustomobject]@{
        path = $fileRelativePath
        sha256 = $fileHash
      }
    }
    $toRootRelative = {
      param([string]$Path)
      return $Path.Substring($outputRoot.Length).TrimStart($PathSeparators) -replace "\\", "/"
    }
    $binaryRelative = & $toRootRelative $binaryPath
    $companionRelative = & $toRootRelative $companionPath
    $brokerRelative = & $toRootRelative $brokerPath
    $approvalRelative = & $toRootRelative $approvalPath
    $systemToolHostRelative = & $toRootRelative $systemToolHostPath
    $binaryRecord = $artifactFiles | Where-Object { $_.path -eq $binaryRelative } | Select-Object -First 1
    $companionRecord = $artifactFiles | Where-Object { $_.path -eq $companionRelative } | Select-Object -First 1
    $brokerRecord = $artifactFiles | Where-Object { $_.path -eq $brokerRelative } | Select-Object -First 1
	$approvalRecord = $artifactFiles | Where-Object { $_.path -eq $approvalRelative } | Select-Object -First 1
	$systemToolHostRecord = $artifactFiles | Where-Object { $_.path -eq $systemToolHostRelative } | Select-Object -First 1
    $artifacts += [pscustomobject]@{
      target = $target
      package_target = $target
      ui_included = -not $SkipUI
      path = $binaryRecord.path
      sha256 = $binaryRecord.sha256
      companion_path = $companionRecord.path
      broker_path = $brokerRecord.path
	  approval_path = $approvalRecord.path
	  system_tool_host_path = $systemToolHostRecord.path
	  production_installer = if ($goos -eq "windows") { ($deploymentScriptPaths[0].Substring($outputRoot.Length).TrimStart($PathSeparators) -replace "\\", "/") } else { $null }
      ui_dir = $uiRelativePath
      source_clean = -not $sourceDirty
      release_kind = $releaseKind
      signer_thumbprint = if ($packageSigned) { $signingCertificate.Thumbprint.ToUpperInvariant() } else { $null }
      files = @($artifactFiles)
    }
  }
} finally {
  Pop-Location
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
    source_clean = -not $sourceDirty
    release_kind = if (-not $packageSigned) { "development_unsigned" } elseif ($sourceDirty) { "development_dirty" } else { "production_signed" }
    signer_thumbprint = if ($packageSigned) { $signingCertificate.Thumbprint.ToUpperInvariant() } else { $null }
    ui_included = -not $SkipUI
    artifacts = $artifacts
  }
  $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $manifestPath -Encoding ASCII

  if ($env:STEWARD_BUILD_TEST_FAULT_INJECTION -eq "before_publish") {
    throw "Injected release build failure before atomic publication"
  }

  # This is deliberately the last operation before the same-volume rename.
  # A source change at any earlier point invalidates every already-built and
  # already-signed artifact, so the stage must never become a formal release.
  $finalCommit = (git -C $repoRoot rev-parse HEAD 2>$null | Out-String).Trim()
  $finalStatus = @(git -C $repoRoot status --porcelain=v1 --untracked-files=all 2>$null)
  $finalGitStatus = $finalStatus -join "`n"
  if ($LASTEXITCODE -ne 0 -or $finalCommit -ne $buildCommit -or $finalGitStatus -cne $initialGitStatus) {
    throw "Source Git HEAD or worktree changed while the release was being built; refusing to publish mixed-provenance artifacts"
  }

  Publish-StagedBuild -StageRoot $stageRoot -FinalOutputRoot $finalOutputRoot
  $published = $true

  Write-Host ""
  Write-Host "[steward] Built $($artifacts.Count) artifact(s)"
  Write-Host "[steward] Output: $finalOutputRoot"
  Write-Host "[steward] Checksums: $(Join-Path $finalOutputRoot 'SHA256SUMS.txt')"
  Write-Host "[steward] Manifest: $(Join-Path $finalOutputRoot 'manifest.json')"
} finally {
  if (-not $published -and (Test-Path -LiteralPath $stageRoot)) {
    try {
      Remove-Item -LiteralPath $stageRoot -Recurse -Force
    } catch {
      Write-Warning "Failed to remove isolated release stage '$stageRoot': $($_.Exception.Message)"
    }
  }
}
