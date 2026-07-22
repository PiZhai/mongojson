$repoRoot=(Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path

function Read-DeployScript([string]$Name){Get-Content -LiteralPath (Join-Path $repoRoot "deploy\$Name") -Raw}
function Get-DeployParseErrors([string]$Name){
  $tokens=$null;$errors=$null
  [Management.Automation.Language.Parser]::ParseFile((Join-Path $repoRoot "deploy\$Name"),[ref]$tokens,[ref]$errors)|Out-Null
  return @($errors)
}

Describe 'Steward production release supply-chain contracts' {
  It 'keeps release scripts syntactically valid' {
    foreach($name in @('build-steward.ps1','verify-steward-dist.ps1','test-steward-production.ps1','update-steward-windows-service.ps1')){
      @(Get-DeployParseErrors $name).Count|Should Be 0
    }
  }

  It 'does not permit a signed release to reuse untested or stale outputs' {
    $script=Read-DeployScript 'build-steward.ps1'
    $script.Contains('$packageSigned -and ($SkipTests -or $SkipFrontendBuild -or $SkipUI)')|Should Be $true
    $script.Contains('go test ./...')|Should Be $true
    $script.Contains('npm test -- --run')|Should Be $true
    $script.Contains('npm run lint')|Should Be $true
    $script.Contains('npm run build')|Should Be $true
  }

  It 'creates and requires a timestamped Windows package catalog' {
    $build=Read-DeployScript 'build-steward.ps1'
    $verify=Read-DeployScript 'verify-steward-dist.ps1'
    $build.Contains('New-FileCatalog')|Should Be $true
    $build.Contains('release-catalog.cat')|Should Be $true
    $build.Contains('TimeStamperCertificate')|Should Be $true
    $verify.Contains('Test-FileCatalog')|Should Be $true
    $verify.Contains('package catalog signature does not contain a trusted timestamp')|Should Be $true
  }

  It 'preserves non-ASCII PowerShell scripts while Authenticode signing' {
    $build=Read-DeployScript 'build-steward.ps1'
    $build.Contains('[Text.UTF8Encoding]::new($true)')|Should Be $true
    $build.Contains('[Management.Automation.Language.Parser]::ParseFile')|Should Be $true
    $build.Contains('Authenticode signing produced an invalid PowerShell script')|Should Be $true
  }

  It 'rejects unsupported HTTPS timestamp URLs before release assembly' {
    $build=Read-DeployScript 'build-steward.ps1'
    $build.Contains('Resolve-ReleaseTimestampServer')|Should Be $true
    $build.Contains('$uri.Scheme -ne [Uri]::UriSchemeHttp')|Should Be $true
    $build.Contains('Set-AuthenticodeSignature uses a Windows API that does not support HTTPS timestamp URLs')|Should Be $true
    $build.IndexOf('$TimestampServer = Resolve-ReleaseTimestampServer')|Should BeLessThan $build.IndexOf('$packageSigned -and ($SkipTests -or $SkipFrontendBuild -or $SkipUI)')
  }

  It 'requires every Windows lifecycle and desktop-notification asset' {
    $script=Read-DeployScript 'verify-steward-dist.ps1'
    foreach($asset in @(
      'windows-notifier/steward-windows-notifier.exe',
      'uninstall-steward-production.ps1','test-steward-production.ps1',
      'install-steward-companion.ps1','uninstall-steward-companion.ps1',
      'migrate-steward-production.ps1','rotate-steward-broker-keys.ps1',
      'test-steward-broker-session0.ps1'
    )){$script.Contains($asset)|Should Be $true}
  }

  It 'keeps public readiness generic and validates UI plus protected installed-release trust' {
    $script=Read-DeployScript 'test-steward-production.ps1'
    $script.Contains('anonymous readiness response exposed internal diagnostics')|Should Be $true
    $script.Contains('main.readiness_authenticated')|Should Be $true
    $script.Contains('main.ui')|Should Be $true
    $script.Contains('main.installed_release_integrity')|Should Be $true
    $script.Contains('mongojson.steward.release-trust/v2')|Should Be $true
    $script.Contains('Assert-InstalledReleaseCryptographicTrust')|Should Be $true
    $script.Contains("Test-FileCatalog -Path `$Root")|Should Be $true
    $script.Contains('$script:verificationFailed = $false')|Should Be $true
    $script.Contains('$script:failed = $true')|Should Be $false
    $script.Contains('recent service log: $tail')|Should Be $false
  }

  It 'records an exact install-time authentication baseline for install and update' {
    foreach($name in @('install-steward-production.ps1','update-steward-production.ps1')){
      $script=Read-DeployScript $name
      $script.Contains("schema='mongojson.steward.release-trust/v2'")|Should Be $true
      $script.Contains('authenticated_files=')|Should Be $true
      $script.Contains("undeclared_files_policy='deny'")|Should Be $true
    }
  }

  It 'fails closed on the legacy unverified Windows updater' {
    $script=Read-DeployScript 'update-steward-windows-service.ps1'
    $script.Contains('This legacy updater is disabled')|Should Be $true
    $script.Contains('update-steward-production.ps1')|Should Be $true
  }
}
