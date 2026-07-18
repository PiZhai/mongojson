$repoRoot=(Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$verifierPath=Join-Path $repoRoot 'deploy\verify-steward-dist.ps1'
$pwshPath=(Get-Process -Id $PID).Path

function New-MinimalStewardPackage {
  param(
    [string]$Target,
    [bool]$UIIncluded=$false,
    [switch]$OmitUIProperty,
    [switch]$IncludeProductionAssets
  )

  $packageRoot=Join-Path ([IO.Path]::GetTempPath()) ('steward-verifier-'+[guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $packageRoot -Force|Out-Null
  $targetOS=$Target.Split('/')[0]
  $extension=if($targetOS -eq 'windows'){'.exe'}else{''}
  $relativePaths=@(
    "steward$extension",
    "steward-broker$extension",
    "steward-approval$extension",
    "steward-companion$extension",
    "steward-system-tool-host$extension"
  )
  if($UIIncluded -or $IncludeProductionAssets){$relativePaths+='ui/index.html'}
  if($IncludeProductionAssets){
    $relativePaths+=@(
      'windows-notifier/steward-windows-notifier.exe',
      'install-steward-production.ps1','update-steward-production.ps1','uninstall-steward-production.ps1',
      'test-steward-production.ps1','install-steward-companion.ps1','uninstall-steward-companion.ps1',
      'migrate-steward-production.ps1','rotate-steward-broker-keys.ps1','test-steward-broker-session0.ps1',
      'verify-steward-dist.ps1'
    )
  }

  $files=@()
  foreach($relativePath in $relativePaths){
    $fullPath=Join-Path $packageRoot $relativePath
    New-Item -ItemType Directory -Path (Split-Path -Parent $fullPath) -Force|Out-Null
    [IO.File]::WriteAllText($fullPath,"fixture:$Target`n$relativePath",[Text.UTF8Encoding]::new($false))
    $files+=[ordered]@{
      path=($relativePath -replace '\\','/')
      sha256=(Get-FileHash -Algorithm SHA256 -LiteralPath $fullPath).Hash.ToLowerInvariant()
    }
  }
  $manifest=[ordered]@{
    schema='mongojson.steward.release/v1'
    name='steward'
    target=$Target
    version='verifier-fixture'
    commit=('a'*40)
    built_at='2026-07-19T00:00:00Z'
    go_version='go1.25.0'
    source_clean=$true
    release_kind='development_unsigned'
    files=$files
    signing=[ordered]@{
      required=$false
      signer_thumbprint=''
      manifest_signature=''
      package_catalog=''
      signed_files=@()
    }
  }
  if(-not $OmitUIProperty){$manifest.ui_included=$UIIncluded}
  [IO.File]::WriteAllText((Join-Path $packageRoot 'release-manifest.json'),($manifest|ConvertTo-Json -Depth 7),[Text.UTF8Encoding]::new($false))
  $checksumLines=$files|Sort-Object path|ForEach-Object{"$($_.sha256)  $($_.path)"}
  [IO.File]::WriteAllLines((Join-Path $packageRoot 'SHA256SUMS.txt'),[string[]]$checksumLines,[Text.Encoding]::ASCII)
  return $packageRoot
}

function Invoke-MinimalPackageVerifier {
  param([string]$PackageRoot,[string[]]$AdditionalArguments=@())
  $output=@(& $pwshPath -NoProfile -File $verifierPath -DistDir $PackageRoot -AllowUnsignedPackage @AdditionalArguments 2>&1)
  return [pscustomobject]@{ExitCode=$LASTEXITCODE;Output=($output -join "`n")}
}

Describe 'Steward package verifier platform compatibility' {
  It 'accepts a minimal Linux package without Windows assets or UI' {
    $package=New-MinimalStewardPackage -Target 'linux/amd64' -UIIncluded:$false
    try{
      $result=Invoke-MinimalPackageVerifier -PackageRoot $package
      $result.ExitCode|Should Be 0
      $result.Output|Should Match '"target":\s*"linux/amd64"'
    }finally{Remove-Item -LiteralPath $package -Recurse -Force}
  }

  It 'accepts an explicit UI-free Windows development package outside production mode' {
    $package=New-MinimalStewardPackage -Target 'windows/amd64' -UIIncluded:$false
    try{
      (Invoke-MinimalPackageVerifier -PackageRoot $package).ExitCode|Should Be 0
    }finally{Remove-Item -LiteralPath $package -Recurse -Force}
  }

  It 'requires UI when a development package declares ui_included true' {
    $package=New-MinimalStewardPackage -Target 'linux/amd64' -UIIncluded:$false
    try{
      $manifestPath=Join-Path $package 'release-manifest.json'
      $manifest=Get-Content -Raw -LiteralPath $manifestPath|ConvertFrom-Json
      $manifest.ui_included=$true
      [IO.File]::WriteAllText($manifestPath,($manifest|ConvertTo-Json -Depth 7),[Text.UTF8Encoding]::new($false))
      $result=Invoke-MinimalPackageVerifier -PackageRoot $package
      $result.ExitCode|Should Be 1
      $result.Output|Should Match 'missing required file: ui/index.html'
    }finally{Remove-Item -LiteralPath $package -Recurse -Force}
  }

  It 'rejects non-Windows targets in production installation mode' {
    $package=New-MinimalStewardPackage -Target 'darwin/arm64' -UIIncluded:$false
    try{
      $result=Invoke-MinimalPackageVerifier -PackageRoot $package -AdditionalArguments @('-RequirePackageMode')
      $result.ExitCode|Should Be 1
      $result.Output|Should Match 'Production installation package target must be windows/amd64'
    }finally{Remove-Item -LiteralPath $package -Recurse -Force}
  }

  It 'rejects an explicit UI-free Windows package in production installation mode' {
    $package=New-MinimalStewardPackage -Target 'windows/amd64' -UIIncluded:$false -IncludeProductionAssets
    try{
      $result=Invoke-MinimalPackageVerifier -PackageRoot $package -AdditionalArguments @('-RequirePackageMode')
      $result.ExitCode|Should Be 1
      $result.Output|Should Match 'must declare ui_included=true'
    }finally{Remove-Item -LiteralPath $package -Recurse -Force}
  }

  It 'accepts a complete unsigned Windows development package in explicit production mode' {
    $package=New-MinimalStewardPackage -Target 'windows/amd64' -UIIncluded:$true -IncludeProductionAssets
    try{
      $result=Invoke-MinimalPackageVerifier -PackageRoot $package -AdditionalArguments @('-RequirePackageMode')
      $result.ExitCode|Should Be 0
    }finally{Remove-Item -LiteralPath $package -Recurse -Force}
  }

  It 'treats a missing ui_included field as strict only in Windows production mode' {
    $package=New-MinimalStewardPackage -Target 'windows/amd64' -OmitUIProperty
    try{
      (Invoke-MinimalPackageVerifier -PackageRoot $package).ExitCode|Should Be 0
      $production=Invoke-MinimalPackageVerifier -PackageRoot $package -AdditionalArguments @('-RequirePackageMode')
      $production.ExitCode|Should Be 1
      $production.Output|Should Match 'missing required file: ui/index.html'
    }finally{Remove-Item -LiteralPath $package -Recurse -Force}
  }
}
