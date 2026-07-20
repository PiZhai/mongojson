$repoRoot=(Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path

function Read-Script([string]$Name){Get-Content -LiteralPath (Join-Path $repoRoot "deploy\$Name") -Raw}
function Get-ParseErrors([string]$Name){
  $tokens=$null;$errors=$null
  [Management.Automation.Language.Parser]::ParseFile((Join-Path $repoRoot "deploy\$Name"),[ref]$tokens,[ref]$errors)|Out-Null
  return @($errors)
}

Describe 'Windows production installer transaction contracts' {
  It 'keeps all transaction scripts syntactically valid' {
    foreach($name in @('migrate-steward-production.ps1','install-steward-companion.ps1','install-steward-production.ps1','update-steward-production.ps1','test-steward-production.ps1','rotate-steward-broker-keys.ps1','run-steward-service-install-e2e.ps1')){
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

  It 'fails closed instead of replacing an existing local encryption key' {
    $installer=Read-Script 'install-steward-production.ps1'
    foreach($contract in @(
      'Read-ProtectedLocalEncryptionKeyring',
      'Resolve-InstallationLocalEncryptionKey',
      'DataDir already exists but no protected STEWARD_LOCAL_ENCRYPTION_KEY could be inherited',
      'DatabaseURL points to an external PostgreSQL server',
      'LocalEncryptionKey does not match the protected key already associated with this DataDir'
    )){$installer.Contains($contract)|Should Be $true}
    $installer.IndexOf('$localKeyResolution=Resolve-InstallationLocalEncryptionKey')|Should BeLessThan $installer.IndexOf('$installMutationStarted=$true')
    foreach($field in @('key','key_id','previous_keys')){
      $installer.Contains("`$LocalEncryption$($field -replace '^key$','Key' -replace '^key_id$','KeyID' -replace '^previous_keys$','PreviousKeys')=[string]`$localKeyResolution.$field")|Should Be $true
    }
  }

  It 'requires migration key recovery before destructive migration and preserves a generated key after started verification failure' {
    $migration=Read-Script 'migrate-steward-production.ps1'
    $migration.Contains("Assert-RecoverableLocalEncryptionKey `$envMap 'legacy installation'")|Should Be $true
    $migration.IndexOf("Assert-RecoverableLocalEncryptionKey `$envMap 'legacy installation'")|Should BeLessThan $migration.IndexOf('Stop-Service $ServiceName -Force -ErrorAction Stop')
    $installer=Read-Script 'install-steward-production.ps1'
    foreach($contract in @(
      'local-encryption-key-recovery.json',
      '$localKeyMayHaveReachedDatabase=$true',
      '$retainLocalKeyRecovery=$localKeyGenerated -and $localKeyMayHaveReachedDatabase',
      'Reuse that key on the next installation attempt'
    )){$installer.Contains($contract)|Should Be $true}
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
      try{& (Join-Path $repoRoot 'deploy\install-steward-companion.ps1') -SourceDir $source -LocalEncryptionKey ('A'*43+'=') -InstallDir $install -RollbackRoot $rollback -TaskName ('StewardFixture-'+[guid]::NewGuid().ToString('N')) -AllowUnauthenticatedDevelopment -ErrorAction Stop|Out-Null}catch{$threw=$true}
      $threw|Should Be $true
      Test-Path -LiteralPath (Join-Path $install 'sentinel.txt')|Should Be $true
      Test-Path -LiteralPath (Join-Path $install '.steward-companion-operation')|Should Be $false
    }finally{
      $env:STEWARD_COMPANION_TEST_FAIL_AFTER_ACTIVATION=$previousInjection
      if(Test-Path -LiteralPath $fixtureRoot){Remove-Item -LiteralPath $fixtureRoot -Recurse -Force}
    }
  }

  It 'pins the Companion API base and fails closed on production management authentication' {
    $companion=Read-Script 'install-steward-companion.ps1'
    foreach($contract in @(
      '[string]$APIBase = "http://127.0.0.1:18080/api"',
      '[switch]$AllowUnauthenticatedDevelopment',
      '--require-management-token=$requireManagementTokenArgument',
      '--api `"$APIBase`"',
      'ManagementAccessTokenFile is required for a production Companion installation'
    )){$companion.Contains($contract)|Should Be $true}
    foreach($aclContract in @('*S-1-5-18:F','*S-1-5-32-544:F','/setowner "*${identity}"')){
      $companion.Contains($aclContract)|Should Be $true
    }

    $installer=Read-Script 'install-steward-production.ps1'
    foreach($contract in @(
      '-APIBase $companionAPIBase',
      'companion_api_base=$companionAPIBase',
      '-DetailedReadyURL "$companionAPIBase/system/readiness"'
    )){$installer.Contains($contract)|Should Be $true}

    $migration=Read-Script 'migrate-steward-production.ps1'
    foreach($contract in @(
      "[string]`$HTTPAddress='127.0.0.1:18080'",
      "[string]`$PeerHTTPAddress='127.0.0.1:18081'",
      'HTTPAddress=$HTTPAddress',
      'PeerHTTPAddress=$PeerHTTPAddress'
    )){$migration.Contains($contract)|Should Be $true}

    $updater=Read-Script 'update-steward-production.ps1'
    foreach($contract in @(
      '[string]$CompanionAPIBase=""',
      'installationMarker.companion_api_base',
      '-APIBase $CompanionAPIBase',
      '-DetailedReadyURL $DetailedReadyURL'
    )){$updater.Contains($contract)|Should Be $true}
  }

  It 'rejects a production Companion install before mutation when its management token is absent' {
    $fixtureRoot=Join-Path ([IO.Path]::GetTempPath()) ('steward-companion-auth-fi-'+[guid]::NewGuid().ToString('N'))
    $source=Join-Path $fixtureRoot 'source';$install=Join-Path $fixtureRoot 'installed'
    New-Item -ItemType Directory -Force -Path $source|Out-Null
    try{
      Set-Content -LiteralPath (Join-Path $source 'steward-companion.exe') -Value 'fixture'
      $message=''
      try{& (Join-Path $repoRoot 'deploy\install-steward-companion.ps1') -SourceDir $source -LocalEncryptionKey ('A'*43+'=') -InstallDir $install -TaskName ('StewardFixture-'+[guid]::NewGuid().ToString('N')) -ErrorAction Stop|Out-Null}catch{$message=$_.Exception.Message}
      $message.Contains('ManagementAccessTokenFile is required for a production Companion installation')|Should Be $true
      Test-Path -LiteralPath $install|Should Be $false
    }finally{Remove-Item -LiteralPath $fixtureRoot -Recurse -Force -ErrorAction SilentlyContinue}
  }

  It 'accepts a custom loopback Companion API base and rejects a remote one before mutation' {
    $fixtureRoot=Join-Path ([IO.Path]::GetTempPath()) ('steward-companion-api-fi-'+[guid]::NewGuid().ToString('N'))
    $source=Join-Path $fixtureRoot 'source';$install=Join-Path $fixtureRoot 'installed';$token=Join-Path $fixtureRoot 'token.txt'
    New-Item -ItemType Directory -Force -Path $source|Out-Null
    try{
      Set-Content -LiteralPath (Join-Path $source 'steward-companion.exe') -Value 'fixture'
      Set-Content -LiteralPath $token -Value ('x'*40)
      {& (Join-Path $repoRoot 'deploy\install-steward-companion.ps1') -SourceDir $source -LocalEncryptionKey ('A'*43+'=') -InstallDir $install -ManagementAccessTokenFile $token -APIBase 'http://127.0.0.1:19090/api/' -WhatIf -ErrorAction Stop|Out-Null}|Should Not Throw
      $message=''
      try{& (Join-Path $repoRoot 'deploy\install-steward-companion.ps1') -SourceDir $source -LocalEncryptionKey ('A'*43+'=') -InstallDir $install -ManagementAccessTokenFile $token -APIBase 'https://example.com/api' -WhatIf -ErrorAction Stop|Out-Null}catch{$message=$_.Exception.Message}
      $message.Contains('must target loopback')|Should Be $true
      Test-Path -LiteralPath $install|Should Be $false
    }finally{Remove-Item -LiteralPath $fixtureRoot -Recurse -Force -ErrorAction SilentlyContinue}
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

  It 'requires a post-probe Companion activity sample to reach durable storage' {
    $verifier=Read-Script 'test-steward-production.ps1'
    foreach($contract in @(
      'companion.real_activity_ingestion',
      '/api/steward/background/status',
      "collector_name -eq 'companion:windows-activity'",
      'last_ingested_at',
      'ingestion_fresh'
    )){$verifier.Contains($contract)|Should Be $true}
    $verifier.IndexOf('$managementToken=$null')|Should BeGreaterThan $verifier.IndexOf('companion.real_activity_ingestion')
  }

  It 'installs, reapplies, and verifies resilient Windows service recovery policies' {
    $installer=Read-Script 'install-steward-production.ps1'
    $updater=Read-Script 'update-steward-production.ps1'
    $verifier=Read-Script 'test-steward-production.ps1'
    foreach($script in @($installer,$updater)){
      foreach($contract in @(
        'Set-StewardServiceRecoveryPolicy',
        "'restart/15000/restart/30000/restart/60000'",
        "'restart/5000/restart/15000/restart/30000'",
        'failureflag $Name 1',
        'reset= 86400'
      )){$script.Contains($contract)|Should Be $true}
    }
    foreach($contract in @(
      'main.delayed_auto_start',
      'main.failure_recovery',
      'broker.failure_recovery',
      'Get-ServiceRecoveryProfile'
    )){$verifier.Contains($contract)|Should Be $true}
  }
}
