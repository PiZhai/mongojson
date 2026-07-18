$repoRoot=(Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path

function Read-Script([string]$Name){Get-Content -LiteralPath (Join-Path $repoRoot "deploy\$Name") -Raw}
function Get-ParseErrors([string]$Name){
  $tokens=$null;$errors=$null
  [Management.Automation.Language.Parser]::ParseFile((Join-Path $repoRoot "deploy\$Name"),[ref]$tokens,[ref]$errors)|Out-Null
  return @($errors)
}

Describe 'Windows production installer transaction contracts' {
  It 'keeps all transaction scripts syntactically valid' {
    foreach($name in @('migrate-steward-production.ps1','install-steward-companion.ps1','rotate-steward-broker-keys.ps1','run-steward-service-install-e2e.ps1')){
      @(Get-ParseErrors $name).Count|Should Be 0
    }
  }

  It 'backs up and restores every migration-owned directory plus ACL and Companion task state' {
    $script=Read-Script 'migrate-steward-production.ps1'
    foreach($contract in @(
      'main_install=Save-DirectorySnapshot',
      'main_data=Save-DirectorySnapshot',
      'broker_install=Save-DirectorySnapshot',
      'broker_data=Save-DirectorySnapshot',
      'companion_install=Save-DirectorySnapshot',
      '$companionTaskSnapshot=Save-CompanionTask',
      'companion_task=$companionTaskSnapshot',
      'Import-AclTree',
      'Assert-MigrationStateBindings',
      'Restore-CompanionTask',
      'Wait-LegacyHealth',
      'rollback_verified=$true'
    )){$script.Contains($contract)|Should Be $true}
    $script.Contains('Join-Path $DataDir "migration-backup-')|Should Be $false
  }

  It 'uses a mutation journal so an early Companion failure cannot delete the old install' {
    $script=Read-Script 'install-steward-companion.ps1'
    foreach($flag in @('$stageCreated','$oldTaskUnregistered','$oldInstallMoved','$stageActivated','$newTaskRegistered','$newTaskStarted')){
      $script.Contains($flag)|Should Be $true
    }
    $script.Contains('if($stageActivated)')|Should Be $true
    $script.Contains('if($oldInstallMoved)')|Should Be $true
    $script.Contains('Assert-OperationDirectory')|Should Be $true
    $script.Contains("'.MongojsonStewardRollbacks'")|Should Be $true
  }

  It 'restores an existing Companion directory when activation is faulted' {
    $fixtureRoot=Join-Path ([IO.Path]::GetTempPath()) ('steward-companion-fi-'+[guid]::NewGuid().ToString('N'))
    $source=Join-Path $fixtureRoot 'source';$install=Join-Path $fixtureRoot 'installed';$rollback=Join-Path $fixtureRoot 'rollback'
    New-Item -ItemType Directory -Force -Path $source,$install,$rollback|Out-Null
    try{
      Set-Content -LiteralPath (Join-Path $source 'steward-companion.exe') -Value 'fixture'
      Set-Content -LiteralPath (Join-Path $install 'sentinel.txt') -Value 'legacy'
      $previousInjection=$env:STEWARD_COMPANION_TEST_FAIL_AFTER_ACTIVATION
      $env:STEWARD_COMPANION_TEST_FAIL_AFTER_ACTIVATION='1'
      $threw=$false
      try{& (Join-Path $repoRoot 'deploy\install-steward-companion.ps1') -SourceDir $source -LocalEncryptionKey ('A'*43+'=') -InstallDir $install -RollbackRoot $rollback -TaskName ('StewardFixture-'+[guid]::NewGuid().ToString('N')) -ErrorAction Stop|Out-Null}catch{$threw=$true}
      $threw|Should Be $true
      Test-Path -LiteralPath (Join-Path $install 'sentinel.txt')|Should Be $true
      Test-Path -LiteralPath (Join-Path $install '.steward-companion-operation')|Should Be $false
    }finally{
      $env:STEWARD_COMPANION_TEST_FAIL_AFTER_ACTIVATION=$previousInjection
      if(Test-Path -LiteralPath $fixtureRoot){Remove-Item -LiteralPath $fixtureRoot -Recurse -Force}
    }
  }

  It 'proves health and readiness after both key rotation and rollback' {
    $script=Read-Script 'rotate-steward-broker-keys.ps1'
    $script.Contains('Wait-StewardEndpoints')|Should Be $true
    $script.Contains('Start-And-ProveServices')|Should Be $true
    $script.Contains('health_proven=$true')|Should Be $true
    $script.Contains('readiness_proven=$true')|Should Be $true
    $script.Contains('rollback health could not be proven')|Should Be $true
  }

  It 'atomically overwrites credential files without an empty File.Replace backup path' {
    $script=Read-Script 'update-steward-production.ps1'
    $script.Contains('[IO.File]::Replace(')|Should Be $false
    $script.Contains('[IO.File]::Move($temporary,$PrivateEnvironmentPath,$true)')|Should Be $true
    $fixtureRoot=Join-Path ([IO.Path]::GetTempPath()) ('steward-atomic-file-'+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $fixtureRoot|Out-Null
    try{
      $source=Join-Path $fixtureRoot 'source';$destination=Join-Path $fixtureRoot 'destination'
      [IO.File]::WriteAllText($source,'new');[IO.File]::WriteAllText($destination,'old')
      [IO.File]::Move($source,$destination,$true)
      [IO.File]::ReadAllText($destination)|Should Be 'new'
      (Test-Path -LiteralPath $source)|Should Be $false
    }finally{Remove-Item -LiteralPath $fixtureRoot -Recurse -Force -ErrorAction SilentlyContinue}
  }

  It 'refuses the legacy direct SCM real-install path on Windows' {
    $script=Read-Script 'run-steward-service-install-e2e.ps1'
    $script.Contains('$platform -eq "windows" -and $ConfirmInstall')|Should Be $true
    $script.Contains('legacy direct-SCM Windows install E2E is retired')|Should Be $true
    $script.Contains('test-steward-production.ps1')|Should Be $true
  }
}
