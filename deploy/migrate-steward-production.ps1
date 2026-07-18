[CmdletBinding()]
param(
  [Parameter(Mandatory=$true)][string]$SourceDir,
  [string]$DatabaseURL='',
  [string]$InstallDir='C:\Program Files\MongojsonSteward',
  [string]$DataDir='C:\ProgramData\MongojsonSteward',
  [string]$ServiceName='MongojsonSteward',
  [string]$BrokerServiceName='MongojsonStewardBroker',
  [string]$CompanionTaskName='MongojsonStewardCompanion',
  [string]$ResumeFromBackup='',
  [switch]$InstallCompanion,
  [switch]$Verify
)
$ErrorActionPreference='Stop'

function Test-Administrator {
  $p=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Read-ServiceEnvironment([string]$Name) {
  $result=@{};$key="HKLM:\SYSTEM\CurrentControlSet\Services\$Name"
  try{$values=(Get-ItemProperty -LiteralPath $key -Name Environment -ErrorAction Stop).Environment}catch{return $result}
  foreach($entry in @($values)){if($entry -match '^([^=]+)=(.*)$'){$result[$matches[1]]=$matches[2]}}
  return $result
}
function ConvertTo-StringMap($Value) {
  $result=@{}
  if($null -eq $Value){return $result}
  if($Value -is [System.Collections.IDictionary]){
    foreach($key in $Value.Keys){$result[[string]$key]=[string]$Value[$key]}
    return $result
  }
  foreach($property in $Value.PSObject.Properties){$result[$property.Name]=[string]$property.Value}
  return $result
}
function StartMode([string]$Mode) {
  switch($Mode){'Auto'{'auto'};'Automatic'{'auto'};'Disabled'{'disabled'};default{'demand'}}
}
function Stop-InstalledCompanion([string]$TaskName,[string]$ReleaseDir) {
  $task=Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  if($task){
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
  }
  $releasePrefix=[IO.Path]::GetFullPath($ReleaseDir).TrimEnd('\')+'\'
  $targets=@(Get-CimInstance Win32_Process -Filter "Name='steward-companion.exe'" -ErrorAction SilentlyContinue | Where-Object {
    $path=[string]$_.ExecutablePath
    $command=[string]$_.CommandLine
    ($path -and ([IO.Path]::GetFullPath($path) -eq (Join-Path $ReleaseDir 'steward-companion.exe') -or [IO.Path]::GetFullPath($path).StartsWith($releasePrefix,[StringComparison]::OrdinalIgnoreCase))) -or
      ($command -and $command.IndexOf($ReleaseDir,[StringComparison]::OrdinalIgnoreCase) -ge 0)
  })
  foreach($process in $targets){Invoke-CimMethod -InputObject $process -MethodName Terminate -ErrorAction SilentlyContinue|Out-Null}
  for($i=0;$i -lt 50;$i++){
    $remaining=@(Get-CimInstance Win32_Process -Filter "Name='steward-companion.exe'" -ErrorAction SilentlyContinue | Where-Object {
      $path=[string]$_.ExecutablePath;$command=[string]$_.CommandLine
      ($path -and $path.StartsWith($releasePrefix,[StringComparison]::OrdinalIgnoreCase)) -or ($command -and $command.IndexOf($ReleaseDir,[StringComparison]::OrdinalIgnoreCase) -ge 0)
    })
    if($remaining.Count -eq 0){return}
    Start-Sleep -Milliseconds 100
  }
  throw "Session Companion did not stop and still holds files under $ReleaseDir"
}
function Remove-DirectoryWithRetry([string]$Path) {
  if(-not(Test-Path -LiteralPath $Path)){return}
  $lastError=$null
  for($i=0;$i -lt 10;$i++){
    try{Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction Stop;return}catch{$lastError=$_.Exception.Message;Start-Sleep -Milliseconds (200*($i+1))}
  }
  throw "failed to remove $Path after stopping services and Companion: $lastError"
}
function Find-LatestMigrationBackup([string]$Root) {
  return Get-ChildItem -LiteralPath $Root -Directory -Filter 'migration-backup-*' -ErrorAction SilentlyContinue |
    Where-Object {Test-Path -LiteralPath (Join-Path $_.FullName 'legacy-service.json')} |
    Sort-Object LastWriteTime -Descending | Select-Object -First 1
}
function Restore-LegacyService($Metadata,[hashtable]$Environment,[string]$Name) {
  if(Get-Service $Name -ErrorAction SilentlyContinue){return}
  $startName=[string]$Metadata.start_name
  if([string]::IsNullOrWhiteSpace($startName)){$startName='LocalSystem'}
  & sc.exe create $Name 'binPath=' ([string]$Metadata.path_name) 'start=' (StartMode ([string]$Metadata.start_mode)) 'obj=' $startName 'DisplayName=' ([string]$Metadata.display_name)|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to recreate legacy service $Name"}
  if($Environment.Count -gt 0){
    $entries=@($Environment.GetEnumerator()|ForEach-Object{"$($_.Key)=$($_.Value)"})
    New-ItemProperty -LiteralPath "HKLM:\SYSTEM\CurrentControlSet\Services\$Name" -Name Environment -PropertyType MultiString -Value $entries -Force|Out-Null
  }
  $lastStartError=''
  for($attempt=0;$attempt -lt 10;$attempt++){
    try{Start-Service $Name -ErrorAction Stop;break}catch{$lastStartError=$_.Exception.Message;Start-Sleep -Milliseconds (300*($attempt+1))}
  }
  $restored=Get-Service $Name -ErrorAction SilentlyContinue
  if($null -eq $restored -or $restored.Status -ne 'Running'){throw "legacy service was recreated but did not return to Running: $lastStartError"}
}

if(-not(Test-Administrator)){throw 'Run migration from an elevated PowerShell session.'}
$source=(Resolve-Path -LiteralPath $SourceDir).Path
$old=Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
$backupRoot='';$installBackup='';$metadata=$null;$envMap=@{};$resuming=$false

if($old){
  if($old.StartName -eq 'NT AUTHORITY\LocalService'){throw 'service already uses LocalService; use update-steward-production.ps1 instead'}
  if(Get-Service $BrokerServiceName -ErrorAction SilentlyContinue){throw "Broker service already exists; use the production updater: $BrokerServiceName"}
  $envMap=Read-ServiceEnvironment $ServiceName
  $oldPrivate=Join-Path $DataDir 'config\service-secrets.json'
  if(Test-Path $oldPrivate){$private=Get-Content $oldPrivate -Raw|ConvertFrom-Json;foreach($p in $private.PSObject.Properties){$envMap[$p.Name]=[string]$p.Value}}
  $stamp=Get-Date -Format yyyyMMdd-HHmmss
  $backupRoot=Join-Path $DataDir "migration-backup-$stamp"
  New-Item -ItemType Directory -Force $backupRoot|Out-Null
  $installBackup=Join-Path $backupRoot 'install'
  if(Test-Path $InstallDir){Copy-Item $InstallDir $installBackup -Recurse -Force}
  $metadata=[ordered]@{name=$old.Name;display_name=$old.DisplayName;path_name=$old.PathName;start_name=$old.StartName;start_mode=$old.StartMode;state=$old.State;environment=$envMap;migrated_at=(Get-Date).ToUniversalTime().ToString('o')}
  [IO.File]::WriteAllText((Join-Path $backupRoot 'legacy-service.json'),($metadata|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
}else{
  $candidate=$null
  if($ResumeFromBackup){
    $resolved=Resolve-Path -LiteralPath $ResumeFromBackup -ErrorAction Stop
    $candidate=Get-Item -LiteralPath $resolved.Path
  }else{$candidate=Find-LatestMigrationBackup $DataDir}
  if(-not $candidate){throw "legacy service is missing: $ServiceName; no resumable migration backup was found"}
  $backupRoot=$candidate.FullName
  $installBackup=Join-Path $backupRoot 'install'
  if(-not(Test-Path -LiteralPath $installBackup)){throw "migration backup has no install snapshot: $backupRoot"}
  $metadata=Get-Content -LiteralPath (Join-Path $backupRoot 'legacy-service.json') -Raw|ConvertFrom-Json
  if([string]$metadata.name -ne $ServiceName){throw "migration backup belongs to service $($metadata.name), not $ServiceName"}
  $envMap=ConvertTo-StringMap $metadata.environment
  $resuming=$true
  Write-Warning "Resuming interrupted migration from $backupRoot"
}

if(-not $DatabaseURL){$DatabaseURL=[string]$envMap['DATABASE_URL']}
if(-not $DatabaseURL){throw 'DATABASE_URL was not recoverable; provide -DatabaseURL explicitly'}
$removedOld=$resuming
$rollbackErrors=New-Object System.Collections.Generic.List[string]
try{
  Stop-Service $ServiceName -Force -ErrorAction SilentlyContinue
  if(Get-Service $ServiceName -ErrorAction SilentlyContinue){
    & sc.exe delete $ServiceName|Out-Null
    if($LASTEXITCODE -ne 0){throw 'failed to delete legacy service'}
    $removedOld=$true
  }
  for($i=0;$i -lt 30 -and (Get-Service $ServiceName -ErrorAction SilentlyContinue);$i++){Start-Sleep -Milliseconds 200}
  if(Get-Service $ServiceName -ErrorAction SilentlyContinue){throw 'legacy service is still pending deletion; close service handles and retry'}
  Stop-InstalledCompanion $CompanionTaskName $InstallDir
  Remove-DirectoryWithRetry $InstallDir
  $installArgs=@{SourceDir=$source;DatabaseURL=$DatabaseURL;InstallDir=$InstallDir;DataDir=$DataDir;ServiceName=$ServiceName;BrokerServiceName=$BrokerServiceName;InstallCompanion=$InstallCompanion;Start=$true;Verify=$Verify}
  if($envMap['STEWARD_AGENT_ID']){$installArgs.AgentID=$envMap['STEWARD_AGENT_ID']}
  if($envMap['STEWARD_SYNC_SECRET']){$installArgs.SyncSecret=$envMap['STEWARD_SYNC_SECRET']}
  if($envMap['STEWARD_DEVICE_PRIVATE_KEY']){$installArgs.DevicePrivateKey=$envMap['STEWARD_DEVICE_PRIVATE_KEY']}
  if($envMap['STEWARD_DEVICE_PUBLIC_KEY']){$installArgs.DevicePublicKey=$envMap['STEWARD_DEVICE_PUBLIC_KEY']}
  if($envMap['STEWARD_SYNC_ENCRYPTION_KEY']){$installArgs.SyncEncryptionKey=$envMap['STEWARD_SYNC_ENCRYPTION_KEY']}
  if($envMap['STEWARD_SYNC_ENCRYPTION_KEY_ID']){$installArgs.SyncEncryptionKeyID=$envMap['STEWARD_SYNC_ENCRYPTION_KEY_ID']}
  if($envMap['STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS']){$installArgs.SyncEncryptionPreviousKeys=$envMap['STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS']}
  if($envMap['STEWARD_LOCAL_ENCRYPTION_KEY']){$installArgs.LocalEncryptionKey=$envMap['STEWARD_LOCAL_ENCRYPTION_KEY']}
  if($envMap['STEWARD_LOCAL_ENCRYPTION_KEY_ID']){$installArgs.LocalEncryptionKeyID=$envMap['STEWARD_LOCAL_ENCRYPTION_KEY_ID']}
  if($envMap['STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS']){$installArgs.LocalEncryptionPreviousKeys=$envMap['STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS']}
  if($envMap['STEWARD_LLM_BASE_URL']){$installArgs.LLMBaseURL=$envMap['STEWARD_LLM_BASE_URL']}
  if($envMap['STEWARD_LLM_MODEL']){$installArgs.LLMModel=$envMap['STEWARD_LLM_MODEL']}
  if($envMap['STEWARD_LLM_API_KEY']){$installArgs.LLMAPIKey=$envMap['STEWARD_LLM_API_KEY']}
  if($envMap['STEWARD_LLM_API_KEY']){$installArgs.RecoverModelSettingsFromEnvironment=$true}
  & (Join-Path $source 'install-steward-production.ps1') @installArgs|Out-Host
  if($LASTEXITCODE -ne 0){throw 'production installer returned a failure'}
  [ordered]@{ok=$true;resumed=$resuming;migrated_from=[string]$metadata.start_name;migrated_to='NT AUTHORITY\LocalService';backup=$backupRoot;broker=$BrokerServiceName}|ConvertTo-Json
}catch{
  $reason=$_.Exception.Message
  try{Stop-InstalledCompanion $CompanionTaskName $InstallDir}catch{$rollbackErrors.Add($_.Exception.Message)}
  Stop-Service $ServiceName,$BrokerServiceName -Force -ErrorAction SilentlyContinue
  if(Get-Service $ServiceName -ErrorAction SilentlyContinue){& sc.exe delete $ServiceName|Out-Null}
  if(Get-Service $BrokerServiceName -ErrorAction SilentlyContinue){& sc.exe delete $BrokerServiceName|Out-Null}
  try{Remove-DirectoryWithRetry $InstallDir}catch{$rollbackErrors.Add($_.Exception.Message)}
  try{if(Test-Path $installBackup){Copy-Item $installBackup $InstallDir -Recurse -Force}}catch{$rollbackErrors.Add("restore install snapshot: $($_.Exception.Message)")}
  if($removedOld){try{Restore-LegacyService $metadata $envMap $ServiceName}catch{$rollbackErrors.Add($_.Exception.Message)}}
  $suffix=''
  if($rollbackErrors.Count -gt 0){$suffix=" Rollback also reported: $($rollbackErrors -join '; ')"}
  throw "migration failed. The legacy installation restore was attempted from $backupRoot. Cause: $reason.$suffix"
}
