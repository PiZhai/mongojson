param(
  [string]$DistDir = "",

  [string[]]$RequiredTargets = @("windows/amd64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"),

  [string]$ExpectedVersion = "",

  [switch]$RunCurrentBinary,

  [switch]$AllowUnsignedPackage,

  [switch]$AllowDirtyPackage,

  [string]$TrustedSignerThumbprint = "",

  [switch]$SkipCertificateRevocationCheck,

  [switch]$RequirePackageMode
)

$ErrorActionPreference = "Stop"
$PathSeparators = @([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
if($SkipCertificateRevocationCheck){Write-Warning 'SIGNATURE TRUST OVERRIDE: certificate revocation checks are disabled for this verification.'}

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
  if ($version.go_version -ne $Manifest.go_version) {
    throw "Current platform binary Go version '$($version.go_version)' does not match manifest '$($Manifest.go_version)'"
  }
  $reportedTarget = "$($version.goos)/$($version.goarch)"
  if ($reportedTarget -ne $Target) {
    throw "Current platform binary target '$reportedTarget' does not match expected '$Target'"
  }
  return $version
}

function Assert-PackageManifestSignature {
  param(
    [string]$ManifestPath,
    [string]$SignaturePath,
    [object]$Manifest,
    [string]$ExpectedThumbprint,
    [switch]$SignatureTrustProvedByCatalog
  )
  Add-Type -AssemblyName System.Security.Cryptography.Pkcs
  $content = [System.Security.Cryptography.Pkcs.ContentInfo]::new([IO.File]::ReadAllBytes($ManifestPath))
  $cms = [System.Security.Cryptography.Pkcs.SignedCms]::new($content, $true)
  try {
    $cms.Decode([IO.File]::ReadAllBytes($SignaturePath))
    $cms.CheckSignature($true)
  } catch {
    throw "Release manifest detached signature is invalid: $($_.Exception.Message)"
  }
  if ($cms.SignerInfos.Count -ne 1 -or $null -eq $cms.SignerInfos[0].Certificate) {
    throw "Release manifest must contain exactly one signer certificate"
  }
  $certificate = $cms.SignerInfos[0].Certificate
  $ekuExtension=$certificate.Extensions | Where-Object { $_ -is [System.Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension] } | Select-Object -First 1
  $codeSigningAllowed=$false
  if($null -ne $ekuExtension){
    foreach($oid in $ekuExtension.EnhancedKeyUsages){if($oid.Value -eq '1.3.6.1.5.5.7.3.3'){$codeSigningAllowed=$true;break}}
  }
  if(-not $codeSigningAllowed){throw 'Release signer certificate is not valid for code signing'}
  $actualThumbprint = ($certificate.Thumbprint -replace '\s', '').ToUpperInvariant()
  $manifestThumbprint = ([string]$Manifest.signing.signer_thumbprint -replace '\s', '').ToUpperInvariant()
  if ([string]::IsNullOrWhiteSpace($manifestThumbprint) -or $actualThumbprint -ne $manifestThumbprint) {
    throw "Release manifest signer does not match its declared signer thumbprint"
  }
  if (-not [string]::IsNullOrWhiteSpace($ExpectedThumbprint)) {
    $normalizedExpected = ($ExpectedThumbprint -replace '\s', '').ToUpperInvariant()
    if ($actualThumbprint -ne $normalizedExpected) {
      throw "Release signer '$actualThumbprint' does not match trusted signer '$normalizedExpected'"
    }
  }
  if (-not $SignatureTrustProvedByCatalog) {
    $chain = [System.Security.Cryptography.X509Certificates.X509Chain]::new()
    try {
      $chain.ChainPolicy.RevocationMode = if($SkipCertificateRevocationCheck){[System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck}else{[System.Security.Cryptography.X509Certificates.X509RevocationMode]::Online}
      $chain.ChainPolicy.RevocationFlag = [System.Security.Cryptography.X509Certificates.X509RevocationFlag]::ExcludeRoot
      $chain.ChainPolicy.UrlRetrievalTimeout = [TimeSpan]::FromSeconds(15)
      $chain.ChainPolicy.VerificationFlags = [System.Security.Cryptography.X509Certificates.X509VerificationFlags]::NoFlag
      if (-not $chain.Build($certificate)) {
        $errors = @($chain.ChainStatus | ForEach-Object { $_.StatusInformation.Trim() }) -join "; "
        throw "Release signer certificate is not trusted: $errors"
      }
    } finally {
      $chain.Dispose()
    }
  }
  return $certificate
}

function Invoke-PackageVerification {
  param([string]$PackageRoot)
  $rootItem=Get-Item -LiteralPath $PackageRoot -Force
  if(($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "Release package root must not be a reparse point: $PackageRoot"}
  $reparsePoints=@(Get-ChildItem -LiteralPath $PackageRoot -Force -Recurse|Where-Object{($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0})
  if($reparsePoints.Count -gt 0){throw "Release package contains a reparse point: $($reparsePoints[0].FullName)"}
  $packageManifestPath = Join-Path $PackageRoot "release-manifest.json"
  $packageChecksumsPath = Join-Path $PackageRoot "SHA256SUMS.txt"
  if (-not (Test-Path -LiteralPath $packageChecksumsPath -PathType Leaf)) {
    throw "Missing package checksums: $packageChecksumsPath"
  }
  $packageManifest = Get-Content -Raw -LiteralPath $packageManifestPath | ConvertFrom-Json
  if ($packageManifest.schema -ne "mongojson.steward.release/v1" -or $packageManifest.name -ne "steward") {
    throw "Unsupported Steward release manifest schema or package name"
  }
  $packageTarget = ([string]$packageManifest.target).Trim().ToLowerInvariant()
  if ($packageTarget -notmatch '^(windows|darwin|linux)/(amd64|arm64)$') {
    throw "Unsupported Steward release target '$($packageManifest.target)'"
  }
  $targetOS = $packageTarget.Split('/')[0]
  $isWindowsPackage = $targetOS -eq 'windows'
  if ($RequirePackageMode -and $packageTarget -ne "windows/amd64") {
    throw "Production installation package target must be windows/amd64, got '$($packageManifest.target)'"
  }
  if ([string]::IsNullOrWhiteSpace([string]$packageManifest.version) -or [string]::IsNullOrWhiteSpace([string]$packageManifest.commit)) {
    throw "Release manifest is missing version or commit"
  }
  if ($packageManifest.commit -notmatch '^[a-fA-F0-9]{40}$') {
    throw "Release manifest commit is not a full Git commit: '$($packageManifest.commit)'"
  }
  if (-not [string]::IsNullOrWhiteSpace($ExpectedVersion) -and $packageManifest.version -ne $ExpectedVersion) {
    throw "Manifest version '$($packageManifest.version)' does not match expected '$ExpectedVersion'"
  }
  if (-not [bool]$packageManifest.source_clean) {
    if (-not $AllowDirtyPackage) {
      throw "Release was built from a dirty worktree and is not eligible for production installation. Use -AllowDirtyPackage only for local development."
    }
    Write-Warning "DEVELOPMENT OVERRIDE: accepting a package built from a dirty worktree."
  }

  $signatureRequired = [bool]$packageManifest.signing.required
  if($RequirePackageMode -and -not $AllowUnsignedPackage -and [string]::IsNullOrWhiteSpace($TrustedSignerThumbprint)){
    throw "TrustedSignerThumbprint is required for production package verification"
  }
  $signatureRelativePath = Normalize-ArtifactPath ([string]$packageManifest.signing.manifest_signature)
  $signaturePath = if ($signatureRelativePath) { Join-Path $PackageRoot $signatureRelativePath } else { "" }
  $signerCertificate = $null
  if ($signatureRequired) {
    if ([string]::IsNullOrWhiteSpace($signatureRelativePath) -or -not (Test-Path -LiteralPath $signaturePath -PathType Leaf)) {
      throw "Signed release is missing its detached manifest signature"
    }
    Assert-ChildPath -Parent $PackageRoot -Child $signaturePath -Label "Manifest signature path"
    $catalogRelativePath = Normalize-ArtifactPath ([string]$packageManifest.signing.package_catalog)
    $catalogProvesTrust = $false
    if ($isWindowsPackage -and -not [string]::IsNullOrWhiteSpace($catalogRelativePath)) {
      $catalogPath = Join-Path $PackageRoot $catalogRelativePath
      Assert-ChildPath -Parent $PackageRoot -Child $catalogPath -Label "Package catalog path"
      if (-not (Test-Path -LiteralPath $catalogPath -PathType Leaf)) { throw "Signed Windows release is missing its package catalog" }
      $catalogStatus = Test-FileCatalog -Path $PackageRoot -CatalogFilePath $catalogPath
      if ($catalogStatus -ne [System.Management.Automation.CatalogValidationStatus]::Valid) { throw "Windows package catalog validation failed: $catalogStatus" }
      $catalogSignature = Get-AuthenticodeSignature -LiteralPath $catalogPath
      if ($catalogSignature.Status -ne [System.Management.Automation.SignatureStatus]::Valid) { throw "Windows package catalog signature is not valid or timestamp trusted: $($catalogSignature.StatusMessage)" }
      if ($null -eq $catalogSignature.TimeStamperCertificate) { throw "Windows package catalog signature does not contain a trusted timestamp" }
      $catalogThumbprint = ($catalogSignature.SignerCertificate.Thumbprint -replace '\s', '').ToUpperInvariant()
      $expectedCatalogThumbprint = ([string]$packageManifest.signing.signer_thumbprint -replace '\s', '').ToUpperInvariant()
      if ($catalogThumbprint -ne $expectedCatalogThumbprint) { throw "Windows package catalog signer does not match the manifest signer" }
      if (-not [string]::IsNullOrWhiteSpace($TrustedSignerThumbprint) -and $catalogThumbprint -ne (($TrustedSignerThumbprint -replace '\s', '').ToUpperInvariant())) { throw "Windows package catalog signer does not match the pinned trusted signer" }
      $catalogProvesTrust = $true
    } elseif ($isWindowsPackage) {
      throw "Signed Windows release is missing its timestamped package catalog"
    }
    $signerCertificate = Assert-PackageManifestSignature -ManifestPath $packageManifestPath -SignaturePath $signaturePath -Manifest $packageManifest -ExpectedThumbprint $TrustedSignerThumbprint -SignatureTrustProvedByCatalog:$catalogProvesTrust
  } elseif (-not $AllowUnsignedPackage) {
    throw "Unsigned Steward package is not eligible for production installation. Use -AllowUnsignedPackage only for local development."
  } else {
    Write-Warning "DEVELOPMENT OVERRIDE: accepting an unsigned Steward package."
  }

  $packageChecksums = Read-ChecksumFile $packageChecksumsPath
  $declaredFiles = @{}
  foreach ($file in @($packageManifest.files)) {
    $relativePath = Normalize-ArtifactPath ([string]$file.path)
    $expectedHash = ([string]$file.sha256).ToLowerInvariant()
    if ([string]::IsNullOrWhiteSpace($relativePath) -or $expectedHash -notmatch '^[a-f0-9]{64}$') {
      throw "Release manifest contains an invalid file record"
    }
    if ($declaredFiles.ContainsKey($relativePath)) {
      throw "Duplicate release file path: $relativePath"
    }
    $declaredFiles[$relativePath] = $expectedHash
    $fullPath = Join-Path $PackageRoot $relativePath
    Assert-ChildPath -Parent $PackageRoot -Child $fullPath -Label "Release file path"
    if (-not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
      throw "Release file is missing: $relativePath"
    }
    $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $fullPath).Hash.ToLowerInvariant()
    if ($actualHash -ne $expectedHash) {
      throw "Hash mismatch for release file: $relativePath"
    }
    if (-not $packageChecksums.ContainsKey($relativePath) -or $packageChecksums[$relativePath] -ne $expectedHash) {
      throw "SHA256SUMS does not match release manifest for: $relativePath"
    }
  }
  foreach ($relativePath in $packageChecksums.Keys) {
    if (-not $declaredFiles.ContainsKey($relativePath)) {
      throw "SHA256SUMS contains an undeclared release file: $relativePath"
    }
  }

  $allowedMetadata = @("release-manifest.json", "SHA256SUMS.txt")
  if ($signatureRequired) {
    $allowedMetadata += $signatureRelativePath
    if ($isWindowsPackage -and -not [string]::IsNullOrWhiteSpace([string]$packageManifest.signing.package_catalog)) { $allowedMetadata += (Normalize-ArtifactPath ([string]$packageManifest.signing.package_catalog)) }
  }
  foreach ($file in Get-ChildItem -LiteralPath $PackageRoot -Recurse -File) {
    $relativePath = $file.FullName.Substring($PackageRoot.Length).TrimStart($PathSeparators) -replace "\\", "/"
    if (-not $declaredFiles.ContainsKey($relativePath) -and $relativePath -notin $allowedMetadata) {
      throw "Release package contains an undeclared file: $relativePath"
    }
  }

  if ($signatureRequired -and $isWindowsPackage) {
    $signerThumbprint = ($signerCertificate.Thumbprint -replace '\s', '').ToUpperInvariant()
    $signedFiles = @($packageManifest.signing.signed_files)
    if ($signedFiles.Count -eq 0) {
      throw "Signed release does not declare any Authenticode-signed files"
    }
    foreach ($relativePathRaw in $signedFiles) {
      $relativePath = Normalize-ArtifactPath ([string]$relativePathRaw)
      if (-not $declaredFiles.ContainsKey($relativePath)) {
        throw "Signed release file is not declared in the package: $relativePath"
      }
      $signature = Get-AuthenticodeSignature -LiteralPath (Join-Path $PackageRoot $relativePath)
      if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::Valid) {
        throw "Authenticode signature is not valid for '$relativePath': $($signature.StatusMessage)"
      }
      if ($null -eq $signature.TimeStamperCertificate) { throw "Authenticode signature is missing its trusted timestamp for '$relativePath'" }
      $fileThumbprint = ($signature.SignerCertificate.Thumbprint -replace '\s', '').ToUpperInvariant()
      if ($fileThumbprint -ne $signerThumbprint) {
        throw "Authenticode signer mismatch for '$relativePath'"
      }
    }
  }

  $binaryExtension = if ($isWindowsPackage) { '.exe' } else { '' }
  $binaryName = "steward$binaryExtension"
  $binaryPath = Join-Path $PackageRoot $binaryName
  $requiredPaths = @(
    $binaryName,
    "steward-broker$binaryExtension",
    "steward-approval$binaryExtension",
    "steward-companion$binaryExtension",
    "steward-system-tool-host$binaryExtension"
  )
  $uiProperty = $packageManifest.PSObject.Properties['ui_included']
  if ($RequirePackageMode -and $null -ne $uiProperty -and -not [bool]$uiProperty.Value) {
    throw 'Production installation package must declare ui_included=true'
  }
  $uiRequired = ($null -ne $uiProperty -and [bool]$uiProperty.Value) -or $RequirePackageMode
  if ($uiRequired) {
    $requiredPaths += 'ui/index.html'
  }
  if ($RequirePackageMode) {
    $requiredPaths += @(
      "windows-notifier/steward-windows-notifier.exe",
      "install-steward-production.ps1", "update-steward-production.ps1", "uninstall-steward-production.ps1",
      "test-steward-production.ps1", "install-steward-companion.ps1", "uninstall-steward-companion.ps1",
      "migrate-steward-production.ps1", "rotate-steward-broker-keys.ps1", "test-steward-broker-session0.ps1",
      "verify-steward-dist.ps1"
    )
  }
  foreach ($requiredPath in $requiredPaths) {
    $normalized = Normalize-ArtifactPath $requiredPath
    if (-not $declaredFiles.ContainsKey($normalized)) {
      throw "Release package is missing required file: $normalized"
    }
  }
  $smoke = $null
  if ($RunCurrentBinary) {
    $smoke = Invoke-CurrentBinarySmoke -BinaryPath $binaryPath -Manifest $packageManifest -Target $packageTarget
  }
  return [pscustomobject]@{
    ok = $true
    package_mode = $true
    dist_dir = $PackageRoot
    version = $packageManifest.version
    commit = $packageManifest.commit
    target = $packageTarget
    source_clean = [bool]$packageManifest.source_clean
    release_kind = $packageManifest.release_kind
    signed = $signatureRequired
    signer_thumbprint = if ($null -ne $signerCertificate) { $signerCertificate.Thumbprint } else { $null }
    file_count = $declaredFiles.Count
    current_binary_smoke = $smoke
  }
}

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
if ([string]::IsNullOrWhiteSpace($DistDir)) {
  $DistDir = Join-Path $repoRoot "backend\dist\steward"
}

$distRoot = (Resolve-Path -LiteralPath $DistDir).Path
$packageManifestPath = Join-Path $distRoot "release-manifest.json"
if (Test-Path -LiteralPath $packageManifestPath -PathType Leaf) {
  Invoke-PackageVerification -PackageRoot $distRoot | ConvertTo-Json -Depth 6
  return
}
if($RequirePackageMode){
  throw "Production verification requires a package-local release-manifest.json; legacy distribution manifests are not accepted"
}
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
if (-not [bool]$manifest.source_clean) {
  if (-not $AllowDirtyPackage) {
    throw "Distribution manifest records a dirty source worktree. Use -AllowDirtyPackage only for local development."
  }
  Write-Warning "DEVELOPMENT OVERRIDE: accepting a distribution built from a dirty worktree."
}
if (-not [string]::IsNullOrWhiteSpace($ExpectedVersion) -and $manifest.version -ne $ExpectedVersion) {
  throw "Manifest version '$($manifest.version)' does not match expected '$ExpectedVersion'"
}
if ([string]::IsNullOrWhiteSpace([string]$manifest.go_version) -or [string]$manifest.go_version -notmatch '^go1\.[0-9]+\.[0-9]+') {
  throw "Manifest go_version is missing or invalid: '$($manifest.go_version)'"
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
  $companionRelativePath = Normalize-ArtifactPath ([string]$artifact.companion_path)
  $brokerRelativePath = Normalize-ArtifactPath ([string]$artifact.broker_path)
	$approvalRelativePath = Normalize-ArtifactPath ([string]$artifact.approval_path)
	$systemToolHostRelativePath = Normalize-ArtifactPath ([string]$artifact.system_tool_host_path)
	$productionInstallerRelativePath = Normalize-ArtifactPath ([string]$artifact.production_installer)
  $expectedHash = ([string]$artifact.sha256).ToLowerInvariant()
  if ([string]::IsNullOrWhiteSpace($target) -or [string]::IsNullOrWhiteSpace($relativePath) -or [string]::IsNullOrWhiteSpace($companionRelativePath) -or [string]::IsNullOrWhiteSpace($brokerRelativePath) -or [string]::IsNullOrWhiteSpace($approvalRelativePath) -or [string]::IsNullOrWhiteSpace($systemToolHostRelativePath) -or [string]::IsNullOrWhiteSpace($expectedHash)) {
	throw "Manifest artifact is missing target, path, companion_path, broker_path, approval_path, system_tool_host_path, or sha256"
  }
  if ($artifactTargets.ContainsKey($target)) {
    throw "Duplicate artifact target in manifest: $target"
  }
  if ($expectedHash -notmatch '^[a-f0-9]{64}$') {
    throw "Invalid sha256 for $relativePath"
  }
  $artifactTargets[$target] = $true

  $fileRecords = @($artifact.files)
  if ($fileRecords.Count -eq 0) {
    $fileRecords = @([pscustomobject]@{ path = $relativePath; sha256 = $expectedHash })
  }
  $primaryFound = $false
  foreach ($file in $fileRecords) {
    $fileRelativePath = Normalize-ArtifactPath ([string]$file.path)
    $fileExpectedHash = ([string]$file.sha256).ToLowerInvariant()
    if ([string]::IsNullOrWhiteSpace($fileRelativePath) -or $fileExpectedHash -notmatch '^[a-f0-9]{64}$') {
      throw "Invalid file record for target $target"
    }
    if ($artifactPaths.ContainsKey($fileRelativePath)) {
      throw "Duplicate artifact path in manifest: $fileRelativePath"
    }
    $artifactPaths[$fileRelativePath] = $true
    $filePath = Join-Path $distRoot $fileRelativePath
    Assert-ChildPath -Parent $distRoot -Child $filePath -Label "Artifact path"
    if (-not (Test-Path -LiteralPath $filePath -PathType Leaf)) {
      throw "Missing artifact file: $filePath"
    }
    $fileActualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $filePath).Hash.ToLowerInvariant()
    if ($fileActualHash -ne $fileExpectedHash) {
      throw "Hash mismatch for $fileRelativePath"
    }
    if (-not $checksums.ContainsKey($fileRelativePath)) {
      throw "SHA256SUMS is missing $fileRelativePath"
    }
    if ($checksums[$fileRelativePath] -ne $fileExpectedHash) {
      throw "SHA256SUMS hash for $fileRelativePath does not match manifest"
    }
    if ($fileRelativePath -eq $relativePath) {
      if ($fileExpectedHash -ne $expectedHash) {
        throw "Primary binary hash for $relativePath does not match its file record"
      }
      $primaryFound = $true
    }
  }
  if (-not $primaryFound) {
    throw "Primary binary $relativePath is missing from target file records"
  }
  if (-not $artifactPaths.ContainsKey($companionRelativePath)) {
    throw "Companion binary $companionRelativePath is missing from target file records"
  }
  if (-not $artifactPaths.ContainsKey($brokerRelativePath)) {
    throw "Privilege Broker binary $brokerRelativePath is missing from target file records"
  }
	if (-not $artifactPaths.ContainsKey($approvalRelativePath)) { throw "Approval helper $approvalRelativePath is missing from target file records" }
	if (-not $artifactPaths.ContainsKey($systemToolHostRelativePath)) { throw "System Tool Host $systemToolHostRelativePath is missing from target file records" }
	if ($target -eq "windows/amd64") {
	  if ([string]::IsNullOrWhiteSpace($productionInstallerRelativePath) -or -not $artifactPaths.ContainsKey($productionInstallerRelativePath)) {
	    throw "Windows production installer is missing from target file records"
	  }
	  foreach ($requiredScript in @('install-steward-production.ps1','update-steward-production.ps1','uninstall-steward-production.ps1','test-steward-production.ps1','install-steward-companion.ps1','uninstall-steward-companion.ps1','migrate-steward-production.ps1','rotate-steward-broker-keys.ps1','verify-steward-dist.ps1')) {
	    $candidate = ($relativePath.Substring(0, $relativePath.LastIndexOf('/') + 1) + $requiredScript)
	    if (-not $artifactPaths.ContainsKey($candidate)) { throw "Windows deployment script is missing: $requiredScript" }
	  }
	}

  $artifactPath = Join-Path $distRoot $relativePath
  $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $artifactPath).Hash.ToLowerInvariant()
  $uiRelativePath = Normalize-ArtifactPath ([string]$artifact.ui_dir)
  if (-not [string]::IsNullOrWhiteSpace($uiRelativePath)) {
    $uiIndexRelativePath = ($uiRelativePath.TrimEnd("/") + "/index.html")
    if (-not $artifactPaths.ContainsKey($uiIndexRelativePath)) {
      throw "Bundled UI index is missing from target file records: $uiIndexRelativePath"
    }
    $uiIndexPath = Join-Path $distRoot $uiIndexRelativePath
    Assert-ChildPath -Parent $distRoot -Child $uiIndexPath -Label "Bundled UI index"
    if (-not (Test-Path -LiteralPath $uiIndexPath -PathType Leaf)) {
      throw "Bundled UI index is missing: $uiIndexPath"
    }
  }
  if ($RunCurrentBinary -and $target -eq $currentTarget) {
    $currentSmoke = Invoke-CurrentBinarySmoke -BinaryPath $artifactPath -Manifest $manifest -Target $target
  }
  $verifiedArtifacts += [pscustomobject]@{
    target = $target
    path = $relativePath
    companion_path = $companionRelativePath
    broker_path = $brokerRelativePath
	approval_path = $approvalRelativePath
	system_tool_host_path = $systemToolHostRelativePath
	production_installer = $productionInstallerRelativePath
    sha256 = $actualHash
    ui_dir = $uiRelativePath
    file_count = $fileRecords.Count
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
  go_version = $manifest.go_version
  required_targets = $RequiredTargets
  artifact_count = $verifiedArtifacts.Count
  ui_included = [bool]$manifest.ui_included
  current_target = $currentTarget
  current_binary_smoke = $currentSmoke
  artifacts = $verifiedArtifacts
}

$summary | ConvertTo-Json -Depth 6
