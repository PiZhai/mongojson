$repoRoot=(Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$productionTestPath=Join-Path $repoRoot 'deploy\test-steward-production.ps1'

$tokens=$null;$errors=$null
$productionTestAST=[Management.Automation.Language.Parser]::ParseFile($productionTestPath,[ref]$tokens,[ref]$errors)
if(@($errors).Count -ne 0){throw "test-steward-production.ps1 has parse errors"}
foreach($functionName in @('ConvertTo-ReleaseRelativePath','Get-RequiredInstalledReleaseFiles','Assert-InstalledReleaseCryptographicTrust','Assert-InstalledReleaseBaseline')){
  $definition=$productionTestAST.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $functionName},$true)
  if($null -eq $definition){throw "production integrity function is missing: $functionName"}
  . ([scriptblock]::Create($definition.Extent.Text))
}

function Write-UTF8File([string]$Path,[string]$Value) {
  $parent=Split-Path -Parent $Path
  if(-not(Test-Path -LiteralPath $parent)){New-Item -ItemType Directory -Force -Path $parent|Out-Null}
  [IO.File]::WriteAllText($Path,$Value,[Text.UTF8Encoding]::new($false))
}

function New-UnsignedIntegrityFixture([string]$Root) {
  New-Item -ItemType Directory -Force -Path $Root|Out-Null
  $payload=[Collections.Generic.List[object]]::new()
  foreach($relative in Get-RequiredInstalledReleaseFiles){
    $full=Join-Path $Root ($relative -replace '/','\')
    Write-UTF8File $full "fixture payload: $relative"
    $payload.Add([ordered]@{path=$relative;sha256=(Get-FileHash -Algorithm SHA256 -LiteralPath $full).Hash.ToLowerInvariant()})
  }
  $manifest=[ordered]@{
    schema='mongojson.steward.release/v1';name='steward';target='windows/amd64';version='integrity-test';commit=('a'*40)
    built_at='2026-07-19T00:00:00Z';source_clean=$false;release_kind='development_unsigned';files=@($payload)
    signing=[ordered]@{required=$false;signer_thumbprint='';manifest_signature='';package_catalog='';signed_files=@()}
  }
  $manifestPath=Join-Path $Root 'release-manifest.json'
  Write-UTF8File $manifestPath ($manifest|ConvertTo-Json -Depth 8)
  $sumsPath=Join-Path $Root 'SHA256SUMS.txt'
  Write-UTF8File $sumsPath ((@($payload|Sort-Object path|ForEach-Object{"$($_.sha256)  $($_.path)"}) -join "`n")+"`n")
  $authenticated=[Collections.Generic.List[object]]::new()
  foreach($record in $payload){$authenticated.Add([ordered]@{path=[string]$record.path;sha256=[string]$record.sha256;kind='payload'})}
  $authenticated.Add([ordered]@{path='release-manifest.json';sha256=(Get-FileHash -Algorithm SHA256 -LiteralPath $manifestPath).Hash.ToLowerInvariant();kind='manifest'})
  $authenticated.Add([ordered]@{path='SHA256SUMS.txt';sha256=(Get-FileHash -Algorithm SHA256 -LiteralPath $sumsPath).Hash.ToLowerInvariant();kind='checksums'})
  $trust=[ordered]@{
    schema='mongojson.steward.release-trust/v2';signer_thumbprint='';release_version='integrity-test';release_commit=('a'*40)
    release_built_at='2026-07-19T00:00:00.0000000+00:00';release_built_at_utc_ticks='639200160000000000';signature_required=$false;manifest_signature_path='';package_catalog_path=''
    undeclared_files_policy='deny';authenticated_files=@($authenticated|Sort-Object path);recorded_at='2026-07-19T00:01:00Z'
  }
  Write-UTF8File (Join-Path $Root 'release-trust.json') ($trust|ConvertTo-Json -Depth 8)
  return [pscustomobject]@{root=$Root;manifest=$manifest;payload=$payload}
}

Describe 'Installed release authentication baseline' {
  BeforeEach {$script:fixtureRoot=Join-Path ([IO.Path]::GetTempPath()) ('steward-integrity-'+[guid]::NewGuid().ToString('N'));$script:fixture=New-UnsignedIntegrityFixture $script:fixtureRoot}
  AfterEach {if(Test-Path -LiteralPath $script:fixtureRoot){Remove-Item -LiteralPath $script:fixtureRoot -Recurse -Force}}

  It 'accepts an exact installation tree anchored by the install-time baseline' {
    $result=Assert-InstalledReleaseBaseline $script:fixtureRoot $true
    $result.file_count|Should BeGreaterThan 10
    $result.signed|Should Be $false
  }

  It 'rejects manifest shrink instead of trusting the reduced installed manifest' {
    $manifestPath=Join-Path $script:fixtureRoot 'release-manifest.json'
    $manifest=Get-Content -LiteralPath $manifestPath -Raw|ConvertFrom-Json
    $manifest.files=@($manifest.files|Select-Object -Skip 1)
    Write-UTF8File $manifestPath ($manifest|ConvertTo-Json -Depth 8)
    $caught='';try{Assert-InstalledReleaseBaseline $script:fixtureRoot $true|Out-Null}catch{$caught=$_.Exception.Message}
    $caught|Should Match 'protected baseline'
  }

  It 'rejects synchronized payload manifest and checksum tampering' {
    $target=[string]$script:fixture.payload[0].path
    $targetPath=Join-Path $script:fixtureRoot ($target -replace '/','\')
    Write-UTF8File $targetPath 'attacker-controlled replacement'
    $replacementHash=(Get-FileHash -Algorithm SHA256 -LiteralPath $targetPath).Hash.ToLowerInvariant()
    $manifestPath=Join-Path $script:fixtureRoot 'release-manifest.json'
    $manifest=Get-Content -LiteralPath $manifestPath -Raw|ConvertFrom-Json
    foreach($record in $manifest.files){if([string]$record.path -eq $target){$record.sha256=$replacementHash}}
    Write-UTF8File $manifestPath ($manifest|ConvertTo-Json -Depth 8)
    $sumLines=@($manifest.files|Sort-Object path|ForEach-Object{"$($_.sha256)  $($_.path)"})
    Write-UTF8File (Join-Path $script:fixtureRoot 'SHA256SUMS.txt') (($sumLines -join "`n")+"`n")
    $caught='';try{Assert-InstalledReleaseBaseline $script:fixtureRoot $true|Out-Null}catch{$caught=$_.Exception.Message}
    $caught|Should Match 'protected baseline'
  }

  It 'rejects undeclared files even when all protected baseline files are intact' {
    Write-UTF8File (Join-Path $script:fixtureRoot 'injected.exe') 'undeclared'
    $caught='';try{Assert-InstalledReleaseBaseline $script:fixtureRoot $true|Out-Null}catch{$caught=$_.Exception.Message}
    $caught|Should Match 'undeclared file'
  }
}
