[CmdletBinding()]
param(
  [Parameter(Mandatory=$true)][string]$SourceDir,
  [string]$DatabaseURL='',
  [string]$InstallDir='C:\Program Files\MongojsonSteward',
  [string]$DataDir='C:\ProgramData\MongojsonSteward',
  [string]$BrokerInstallDir='C:\Program Files\MongoJSON\StewardBroker',
  [string]$BrokerDataDir='C:\ProgramData\MongoJSON\StewardBroker',
  [string]$CompanionInstallDir=(Join-Path $env:LOCALAPPDATA 'MongojsonSteward'),
  [string]$MigrationBackupRoot=(Join-Path $env:ProgramData 'MongojsonStewardMigrationBackups'),
  [string]$ServiceName='MongojsonSteward',
  [string]$BrokerServiceName='MongojsonStewardBroker',
  [string]$CompanionTaskName='MongojsonStewardCompanion',
  [string]$ResumeFromBackup='',
  [switch]$AllowUnsignedPackage,
  [switch]$AllowDirtyPackage,
  [string]$TrustedSignerThumbprint='',
  [switch]$SkipCertificateRevocationCheck,
  [string]$ReleaseStagingRoot=(Join-Path $env:ProgramData 'MongojsonStewardReleaseStaging'),
  [switch]$InstallCompanion,
  [switch]$Verify,
  [string]$LegacyHealthURL='http://127.0.0.1:18080/healthz',
  [int]$RollbackHealthTimeoutSeconds=60
)
$ErrorActionPreference='Stop'

function Test-Administrator {
  $p=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Assert-PathWithin([string]$Path,[string]$Root,[string]$Name) {
  $rootFull=[IO.Path]::GetFullPath($Root).TrimEnd('\')+'\'
  $pathFull=[IO.Path]::GetFullPath($Path).TrimEnd('\')
  if(-not $pathFull.StartsWith($rootFull,[StringComparison]::OrdinalIgnoreCase)){throw "$Name must remain under ${rootFull}: $pathFull"}
  return $pathFull
}
function Protect-AdminPath([string]$Path) {
  $item=Get-Item -LiteralPath $Path -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "sensitive migration path must not be a reparse point: $Path"}
  $inheritance=[Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
  $propagation=[Security.AccessControl.PropagationFlags]::None
  $allow=[Security.AccessControl.AccessControlType]::Allow
  $full=[Security.AccessControl.FileSystemRights]::FullControl
  $system=[Security.Principal.SecurityIdentifier]::new('S-1-5-18')
  $administrators=[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
  $acl=[Security.AccessControl.DirectorySecurity]::new()
  $acl.SetOwner($administrators)
  $acl.SetAccessRuleProtection($true,$false)
  $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($system,$full,$inheritance,$propagation,$allow))
  $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($administrators,$full,$inheritance,$propagation,$allow))
  Set-Acl -LiteralPath $Path -AclObject $acl
}
function Normalize-SignerThumbprint([string]$Value) {
  $normalized=($Value -replace '\s','').ToUpperInvariant()
  if($normalized -and $normalized -notmatch '^[A-F0-9]{40}$'){throw 'TrustedSignerThumbprint must be a 40-character certificate thumbprint'}
  return $normalized
}
function Assert-NoReparsePoints([string]$Root,[string]$Label) {
  $rootItem=Get-Item -LiteralPath $Root -Force
  if(($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Label root must not be a reparse point: $Root"}
  $reparse=@(Get-ChildItem -LiteralPath $Root -Force -Recurse|Where-Object{($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0})
  if($reparse.Count -gt 0){throw "$Label contains a reparse point: $($reparse[0].FullName)"}
}
function Protect-StagingPath([string]$Path) {
  $item=Get-Item -LiteralPath $Path -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "release staging path must not be a reparse point: $Path"}
  & icacls.exe $Path /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to protect release staging path: $Path"}
}
function Protect-StagingTree([string]$Root) {
  $system=[Security.Principal.SecurityIdentifier]::new('S-1-5-18')
  $administrators=[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
  $allow=[Security.AccessControl.AccessControlType]::Allow
  $full=[Security.AccessControl.FileSystemRights]::FullControl
  $inheritance=[Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
  $noneInheritance=[Security.AccessControl.InheritanceFlags]::None
  $nonePropagation=[Security.AccessControl.PropagationFlags]::None
  foreach($directory in @((Get-Item -LiteralPath $Root -Force))+@(Get-ChildItem -LiteralPath $Root -Force -Recurse -Directory)){
    $acl=[Security.AccessControl.DirectorySecurity]::new();$acl.SetOwner($administrators);$acl.SetAccessRuleProtection($true,$false)
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($system,$full,$inheritance,$nonePropagation,$allow))
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($administrators,$full,$inheritance,$nonePropagation,$allow))
    Set-Acl -LiteralPath $directory.FullName -AclObject $acl
  }
  foreach($file in @(Get-ChildItem -LiteralPath $Root -Force -Recurse -File)){
    $acl=[Security.AccessControl.FileSecurity]::new();$acl.SetOwner($administrators);$acl.SetAccessRuleProtection($true,$false)
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($system,$full,$noneInheritance,$nonePropagation,$allow))
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($administrators,$full,$noneInheritance,$nonePropagation,$allow))
    Set-Acl -LiteralPath $file.FullName -AclObject $acl
  }
}
function New-ProtectedReleaseStage([string]$InputDir,[string]$StagingRoot) {
  $input=(Resolve-Path -LiteralPath $InputDir).Path
  Assert-NoReparsePoints $input 'release source'
  New-Item -ItemType Directory -Force -Path $StagingRoot|Out-Null
  Protect-StagingPath $StagingRoot
  $stage=Join-Path ([IO.Path]::GetFullPath($StagingRoot)) ('migration-'+[guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $stage|Out-Null
  Protect-StagingPath $stage
  try{
    Get-ChildItem -LiteralPath $input -Force|Copy-Item -Destination $stage -Recurse -Force
    Assert-NoReparsePoints $stage 'staged release'
    Protect-StagingTree $stage
    return $stage
  }catch{Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue;throw}
}
function Assert-StagedVerifierTrust([string]$Verifier,[string]$TrustedThumbprint,[bool]$UnsignedOverride) {
  if($TrustedThumbprint){
    $signature=Get-AuthenticodeSignature -LiteralPath $Verifier
    $actual=if($signature.SignerCertificate){($signature.SignerCertificate.Thumbprint -replace '\s','').ToUpperInvariant()}else{''}
    if($signature.Status -eq [Management.Automation.SignatureStatus]::Valid -and $actual -eq $TrustedThumbprint){return}
    if(-not $UnsignedOverride){throw "staged verifier is not validly signed by trusted signer '$TrustedThumbprint': status=$($signature.Status); signer=$actual"}
    Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: staged verifier signature does not match the requested trust pin.'
    return
  }
  if(-not $UnsignedOverride){throw 'TrustedSignerThumbprint is required for production migration'}
  Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: the staged verifier is unsigned and is being executed from an administrator-protected staging directory.'
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
function Find-LatestMigrationBackup([string]$Root,[string]$ExpectedServiceName) {
  return Get-ChildItem -LiteralPath $Root -Directory -Filter 'migration-backup-*' -ErrorAction SilentlyContinue |
    Where-Object {
      $path=Join-Path $_.FullName 'migration-state.json'
      if(-not(Test-Path -LiteralPath $path -PathType Leaf)){return $false}
      try{
        $candidateState=Get-Content -LiteralPath $path -Raw|ConvertFrom-Json
        return [string]$candidateState.phase -in @('backup_complete','migrating','rolling_back') -and [string]$candidateState.service.name -eq $ExpectedServiceName
      }catch{return $false}
    } |
    Sort-Object LastWriteTime -Descending | Select-Object -First 1
}
function Assert-SafeManagedDirectory([string]$Path,[string]$Label) {
  $full=[IO.Path]::GetFullPath($Path).TrimEnd('\')
  $root=[IO.Path]::GetPathRoot($full).TrimEnd('\')
  if([string]::IsNullOrWhiteSpace($full) -or $full.Equals($root,[StringComparison]::OrdinalIgnoreCase)){throw "$Label must not be a drive root: $full"}
  if([string]::IsNullOrWhiteSpace((Split-Path -Leaf $full))){throw "$Label must identify a concrete child directory: $full"}
  if(Test-Path -LiteralPath $full){Assert-NoReparsePoints $full $Label}
  return $full
}
function Assert-PathsDisjoint([string]$Left,[string]$Right,[string]$Label) {
  $leftFull=[IO.Path]::GetFullPath($Left).TrimEnd('\')
  $rightFull=[IO.Path]::GetFullPath($Right).TrimEnd('\')
  $leftPrefix=$leftFull+'\';$rightPrefix=$rightFull+'\'
  if($leftFull.Equals($rightFull,[StringComparison]::OrdinalIgnoreCase) -or $leftPrefix.StartsWith($rightPrefix,[StringComparison]::OrdinalIgnoreCase) -or $rightPrefix.StartsWith($leftPrefix,[StringComparison]::OrdinalIgnoreCase)){
    throw "$Label must be disjoint: '$leftFull' and '$rightFull'"
  }
}
function Export-AclTree([string]$Root,[string]$Destination) {
  if(-not(Test-Path -LiteralPath $Root)){return}
  $rootFull=[IO.Path]::GetFullPath($Root).TrimEnd('\')
  $items=@((Get-Item -LiteralPath $rootFull -Force))+@(Get-ChildItem -LiteralPath $rootFull -Force -Recurse)
  $entries=@($items|ForEach-Object{
    $relative=if($_.FullName.Equals($rootFull,[StringComparison]::OrdinalIgnoreCase)){''}else{$_.FullName.Substring($rootFull.Length+1)}
    [ordered]@{relative_path=$relative;container=[bool]$_.PSIsContainer;sddl=(Get-Acl -LiteralPath $_.FullName).Sddl}
  })
  [IO.File]::WriteAllText($Destination,($entries|ConvertTo-Json -Depth 6),[Text.UTF8Encoding]::new($false))
}
function Import-AclTree([string]$Root,[string]$Source) {
  if(-not(Test-Path -LiteralPath $Source -PathType Leaf)){return}
  $rootFull=[IO.Path]::GetFullPath($Root).TrimEnd('\')
  $entries=@(Get-Content -LiteralPath $Source -Raw|ConvertFrom-Json)
  # Children are restored before their parent so applying a parent ACL cannot
  # accidentally replace a child's explicitly captured protection state.
  foreach($entry in @($entries|Sort-Object {([string]$_.relative_path).Length} -Descending)){
    $target=if([string]::IsNullOrEmpty([string]$entry.relative_path)){$rootFull}else{Join-Path $rootFull ([string]$entry.relative_path)}
    if(-not(Test-Path -LiteralPath $target)){throw "ACL restore target is missing: $target"}
    $acl=if([bool]$entry.container){[Security.AccessControl.DirectorySecurity]::new()}else{[Security.AccessControl.FileSecurity]::new()}
    $acl.SetSecurityDescriptorSddlForm([string]$entry.sddl)
    Set-Acl -LiteralPath $target -AclObject $acl
  }
}
function Save-DirectorySnapshot([string]$Source,[string]$BackupRoot,[string]$Name) {
  $snapshot=Join-Path $BackupRoot $Name
  $acl=Join-Path $BackupRoot "$Name-acl.json"
  $present=Test-Path -LiteralPath $Source -PathType Container
  if($present){
    Assert-NoReparsePoints $Source "$Name source"
    Copy-Item -LiteralPath $Source -Destination $snapshot -Recurse -Force
    Export-AclTree $Source $acl
  }
  return [ordered]@{present=$present;snapshot=$snapshot;acl=$acl;source=[IO.Path]::GetFullPath($Source).TrimEnd('\')}
}
function Restore-DirectorySnapshot($Snapshot) {
  $target=Assert-SafeManagedDirectory ([string]$Snapshot.source) 'migration restore target'
  if(Test-Path -LiteralPath $target){Remove-DirectoryWithRetry $target}
  if([bool]$Snapshot.present){
    if(-not(Test-Path -LiteralPath ([string]$Snapshot.snapshot) -PathType Container)){throw "migration snapshot is missing: $($Snapshot.snapshot)"}
    Copy-Item -LiteralPath ([string]$Snapshot.snapshot) -Destination $target -Recurse -Force
    Import-AclTree $target ([string]$Snapshot.acl)
  }
}
function Write-MigrationState([string]$Path,$State) {
  $temporary="$Path.tmp"
  [IO.File]::WriteAllText($temporary,($State|ConvertTo-Json -Depth 20),[Text.UTF8Encoding]::new($false))
  Move-Item -LiteralPath $temporary -Destination $Path -Force
}
function Wait-ServiceAbsent([string]$Name,[int]$TimeoutSeconds=30) {
  $deadline=(Get-Date).AddSeconds($TimeoutSeconds)
  while((Get-Date) -lt $deadline){if(-not(Get-Service $Name -ErrorAction SilentlyContinue)){return};Start-Sleep -Milliseconds 250}
  throw "service is still pending deletion: $Name"
}
function Wait-LegacyHealth([string]$Name,[string]$URL,[int]$TimeoutSeconds) {
  $deadline=(Get-Date).AddSeconds($TimeoutSeconds);$last=''
  while((Get-Date) -lt $deadline){
    $service=Get-Service $Name -ErrorAction SilentlyContinue
    if($service -and $service.Status -eq 'Running'){
      try{$health=Invoke-RestMethod -Uri $URL -TimeoutSec 5;if($health.status -eq 'ok'){return}}catch{$last=$_.Exception.Message}
    }else{$last="service state=$($service.Status)"}
    Start-Sleep -Milliseconds 500
  }
  throw "restored legacy service did not become healthy within ${TimeoutSeconds}s: $last"
}
function Restore-LegacyService($Metadata,[hashtable]$Environment,[string]$Name,[string]$HealthURL,[int]$HealthTimeoutSeconds) {
  $existing=Get-Service $Name -ErrorAction SilentlyContinue
  if($existing){
    if([string]$Metadata.state -eq 'Running'){
      if($existing.Status -ne 'Running'){Start-Service $Name -ErrorAction Stop}
      Wait-LegacyHealth $Name $HealthURL $HealthTimeoutSeconds
    }
    return
  }
  $startName=[string]$Metadata.start_name
  if([string]::IsNullOrWhiteSpace($startName)){$startName='LocalSystem'}
  & sc.exe create $Name 'binPath=' ([string]$Metadata.path_name) 'start=' (StartMode ([string]$Metadata.start_mode)) 'obj=' $startName 'DisplayName=' ([string]$Metadata.display_name)|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to recreate legacy service $Name"}
  if($Environment.Count -gt 0){
    $entries=@($Environment.GetEnumerator()|ForEach-Object{"$($_.Key)=$($_.Value)"})
    New-ItemProperty -LiteralPath "HKLM:\SYSTEM\CurrentControlSet\Services\$Name" -Name Environment -PropertyType MultiString -Value $entries -Force|Out-Null
  }
  if([string]$Metadata.state -eq 'Running'){
    $lastStartError=''
    for($attempt=0;$attempt -lt 10;$attempt++){
      try{Start-Service $Name -ErrorAction Stop;break}catch{$lastStartError=$_.Exception.Message;Start-Sleep -Milliseconds (300*($attempt+1))}
    }
    $restored=Get-Service $Name -ErrorAction SilentlyContinue
    if($null -eq $restored -or $restored.Status -ne 'Running'){throw "legacy service was recreated but did not return to Running: $lastStartError"}
    Wait-LegacyHealth $Name $HealthURL $HealthTimeoutSeconds
  }
}
function Save-CompanionTask([string]$Name,[string]$BackupRoot) {
  $task=Get-ScheduledTask -TaskName $Name -ErrorAction SilentlyContinue
  $xml=Join-Path $BackupRoot 'companion-task.xml'
  if($task){Export-ScheduledTask -TaskName $Name|Set-Content -LiteralPath $xml -Encoding Unicode}
  return [ordered]@{present=$null -ne $task;running=$null -ne $task -and $task.State -eq 'Running';xml=$xml}
}
function Restore-CompanionTask($Snapshot,[string]$Name) {
  $current=Get-ScheduledTask -TaskName $Name -ErrorAction SilentlyContinue
  if($current){Stop-ScheduledTask -TaskName $Name -ErrorAction SilentlyContinue;Unregister-ScheduledTask -TaskName $Name -Confirm:$false -ErrorAction Stop}
  if([bool]$Snapshot.present){
    if(-not(Test-Path -LiteralPath ([string]$Snapshot.xml) -PathType Leaf)){throw "Companion task snapshot is missing: $($Snapshot.xml)"}
    Register-ScheduledTask -TaskName $Name -Xml (Get-Content -LiteralPath ([string]$Snapshot.xml) -Raw) -Force|Out-Null
    if([bool]$Snapshot.running){Start-ScheduledTask -TaskName $Name}
  }
}
function Assert-SnapshotBinding($Snapshot,[string]$ExpectedSource,[string]$BackupRoot,[string]$Label) {
  if($null -eq $Snapshot){throw "migration state is missing snapshot: $Label"}
  $actualSource=[IO.Path]::GetFullPath([string]$Snapshot.source).TrimEnd('\')
  $expected=[IO.Path]::GetFullPath($ExpectedSource).TrimEnd('\')
  if(-not $actualSource.Equals($expected,[StringComparison]::OrdinalIgnoreCase)){throw "$Label restore target does not match this invocation: $actualSource"}
  $snapshotPath=Assert-PathWithin ([string]$Snapshot.snapshot) $BackupRoot "$Label snapshot"
  $aclPath=Assert-PathWithin ([string]$Snapshot.acl) $BackupRoot "$Label ACL snapshot"
  if([bool]$Snapshot.present){
    if(-not(Test-Path -LiteralPath $snapshotPath -PathType Container)){throw "$Label directory snapshot is missing: $snapshotPath"}
    if(-not(Test-Path -LiteralPath $aclPath -PathType Leaf)){throw "$Label ACL snapshot is missing: $aclPath"}
  }
}
function Assert-MigrationStateBindings($State,[string]$BackupRoot) {
  Assert-SnapshotBinding $State.snapshots.main_install $InstallDir $BackupRoot 'main install'
  Assert-SnapshotBinding $State.snapshots.main_data $DataDir $BackupRoot 'main data'
  Assert-SnapshotBinding $State.snapshots.broker_install $BrokerInstallDir $BackupRoot 'Broker install'
  Assert-SnapshotBinding $State.snapshots.broker_data $BrokerDataDir $BackupRoot 'Broker data'
  Assert-SnapshotBinding $State.snapshots.companion_install $CompanionInstallDir $BackupRoot 'Companion install'
  if($null -eq $State.companion_task){throw 'migration state is missing the Companion task snapshot'}
  if([bool]$State.companion_task.present){
    $taskXML=Assert-PathWithin ([string]$State.companion_task.xml) $BackupRoot 'Companion task snapshot'
    if(-not(Test-Path -LiteralPath $taskXML -PathType Leaf)){throw "Companion task snapshot is missing: $taskXML"}
  }
}

if(-not(Test-Administrator)){throw 'Run migration from an elevated PowerShell session.'}
$InstallDir=Assert-SafeManagedDirectory $InstallDir 'main installation directory'
$DataDir=Assert-SafeManagedDirectory $DataDir 'main data directory'
$BrokerInstallDir=Assert-SafeManagedDirectory $BrokerInstallDir 'Broker installation directory'
$BrokerDataDir=Assert-SafeManagedDirectory $BrokerDataDir 'Broker data directory'
$CompanionInstallDir=Assert-SafeManagedDirectory $CompanionInstallDir 'Companion installation directory'
$MigrationBackupRoot=Assert-SafeManagedDirectory $MigrationBackupRoot 'migration backup root'
foreach($managedRoot in @($InstallDir,$DataDir,$BrokerInstallDir,$BrokerDataDir,$CompanionInstallDir)){Assert-PathsDisjoint $MigrationBackupRoot $managedRoot 'migration backup root and managed installation'}
New-Item -ItemType Directory -Force -Path $MigrationBackupRoot|Out-Null
Assert-NoReparsePoints $MigrationBackupRoot 'migration backup root'
Protect-AdminPath $MigrationBackupRoot
$trustedSigner=Normalize-SignerThumbprint $TrustedSignerThumbprint
$releaseStage=$null
try{
  $releaseStage=New-ProtectedReleaseStage $SourceDir $ReleaseStagingRoot
  $source=$releaseStage
  $verifier=Join-Path $source 'verify-steward-dist.ps1'
  if(-not(Test-Path -LiteralPath $verifier -PathType Leaf)){throw 'release is missing verify-steward-dist.ps1'}
  Assert-StagedVerifierTrust $verifier $trustedSigner ([bool]$AllowUnsignedPackage)
  $verifyArgs=@{DistDir=$source;RequiredTargets=@('windows/amd64');RunCurrentBinary=$true;AllowUnsignedPackage=$AllowUnsignedPackage;AllowDirtyPackage=$AllowDirtyPackage;SkipCertificateRevocationCheck=$SkipCertificateRevocationCheck;RequirePackageMode=$true}
  if($trustedSigner){$verifyArgs.TrustedSignerThumbprint=$trustedSigner}
  & $verifier @verifyArgs|Out-Host
  if($AllowUnsignedPackage){Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: migration accepts an unsigned release package.'}
  if($AllowDirtyPackage){Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: migration accepts a dirty-worktree release package.'}
$old=Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
$existingBroker=Get-Service $BrokerServiceName -ErrorAction SilentlyContinue
$backupRoot='';$metadata=$null;$envMap=@{};$resuming=$false;$state=$null;$statePath='';$resumeCandidate=$null;$forceRollback=$false
if($ResumeFromBackup){$resumeCandidate=Get-Item -LiteralPath (Resolve-Path -LiteralPath $ResumeFromBackup -ErrorAction Stop).Path}
else{$resumeCandidate=Find-LatestMigrationBackup $MigrationBackupRoot $ServiceName}

if($old -and -not $resumeCandidate){
  if($old.StartName -eq 'NT AUTHORITY\LocalService'){throw 'service already uses LocalService; use update-steward-production.ps1 instead'}
  if($existingBroker){throw "Broker service already exists; use the production updater: $BrokerServiceName"}
  $envMap=Read-ServiceEnvironment $ServiceName
  $oldPrivate=Join-Path $DataDir 'config\service-secrets.json'
  if(Test-Path $oldPrivate){$private=Get-Content $oldPrivate -Raw|ConvertFrom-Json;foreach($p in $private.PSObject.Properties){$envMap[$p.Name]=[string]$p.Value}}
  $stamp=(Get-Date -Format yyyyMMdd-HHmmss)+'-'+[guid]::NewGuid().ToString('N').Substring(0,8)
  $backupRoot=Assert-PathWithin (Join-Path $MigrationBackupRoot "migration-backup-$stamp") $MigrationBackupRoot 'migration backup'
  New-Item -ItemType Directory -Path $backupRoot|Out-Null
  Protect-AdminPath $backupRoot
  $metadata=[ordered]@{name=$old.Name;display_name=$old.DisplayName;path_name=$old.PathName;start_name=$old.StartName;start_mode=$old.StartMode;state=$old.State;environment=$envMap;migrated_at=(Get-Date).ToUniversalTime().ToString('o')}
  [IO.File]::WriteAllText((Join-Path $backupRoot 'legacy-service.json'),($metadata|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
  $companionTaskSnapshot=Save-CompanionTask $CompanionTaskName $backupRoot
  $legacyWasStopped=$false;$companionWasStopped=$false
  try{
    if([string]$metadata.state -eq 'Running'){Stop-Service $ServiceName -Force -ErrorAction Stop;$legacyWasStopped=$true}
    if([bool]$companionTaskSnapshot.running){Stop-ScheduledTask -TaskName $CompanionTaskName -ErrorAction Stop;$companionWasStopped=$true}
    $state=[ordered]@{
      schema_version=2;operation_id=[guid]::NewGuid().ToString('N');phase='capturing';rollback_verified=$false
      service=$metadata
      snapshots=[ordered]@{
        main_install=Save-DirectorySnapshot $InstallDir $backupRoot 'main-install'
        main_data=Save-DirectorySnapshot $DataDir $backupRoot 'main-data'
        broker_install=Save-DirectorySnapshot $BrokerInstallDir $backupRoot 'broker-install'
        broker_data=Save-DirectorySnapshot $BrokerDataDir $backupRoot 'broker-data'
        companion_install=Save-DirectorySnapshot $CompanionInstallDir $backupRoot 'companion-install'
      }
      companion_task=$companionTaskSnapshot
      created_at=(Get-Date).ToUniversalTime().ToString('o')
    }
    $state.phase='backup_complete'
    $statePath=Join-Path $backupRoot 'migration-state.json'
    Write-MigrationState $statePath $state
    Protect-StagingTree $backupRoot
  }catch{
    $captureReason=$_.Exception.Message;$captureRollbackErrors=New-Object System.Collections.Generic.List[string]
    if($legacyWasStopped){try{Start-Service $ServiceName -ErrorAction Stop;Wait-LegacyHealth $ServiceName $LegacyHealthURL $RollbackHealthTimeoutSeconds}catch{$captureRollbackErrors.Add("restart legacy service: $($_.Exception.Message)")}}
    if($companionWasStopped){try{Start-ScheduledTask -TaskName $CompanionTaskName -ErrorAction Stop}catch{$captureRollbackErrors.Add("restart Companion task: $($_.Exception.Message)")}}
    $captureSuffix=if($captureRollbackErrors.Count){" Recovery also reported: $($captureRollbackErrors -join '; ')"}else{''}
    throw "failed to capture a consistent migration snapshot before deleting anything: $captureReason.$captureSuffix"
  }
}else{
  $candidate=$resumeCandidate
  if(-not $candidate){throw "legacy service is missing: $ServiceName; no resumable migration backup was found"}
  $backupRoot=$candidate.FullName
  $backupRoot=Assert-PathWithin $backupRoot $MigrationBackupRoot 'resumed migration backup'
  Assert-NoReparsePoints $backupRoot 'resumed migration backup'
  $statePath=Join-Path $backupRoot 'migration-state.json'
  $state=Get-Content -LiteralPath $statePath -Raw|ConvertFrom-Json
  if([int]$state.schema_version -ne 2 -or [string]$state.phase -notin @('backup_complete','migrating','rolling_back')){throw "migration backup is incomplete or not resumable: $backupRoot"}
  Assert-MigrationStateBindings $state $backupRoot
  $metadata=$state.service
  if([string]$metadata.name -ne $ServiceName){throw "migration backup belongs to service $($metadata.name), not $ServiceName"}
  $envMap=ConvertTo-StringMap $metadata.environment
  # A process crash after the destructive phase may leave either service in an
  # indeterminate generation. Restore the captured generation first; a clean
  # follow-up invocation can then create a fresh migration transaction.
  $forceRollback=[string]$state.phase -in @('migrating','rolling_back')
  $resuming=$true
  Write-Warning "Resuming interrupted migration from $backupRoot"
}

if(-not $DatabaseURL){$DatabaseURL=[string]$envMap['DATABASE_URL']}
if(-not $DatabaseURL){throw 'DATABASE_URL was not recoverable; provide -DatabaseURL explicitly'}
$rollbackErrors=New-Object System.Collections.Generic.List[string]
try{
  if($forceRollback){throw 'recovering an interrupted migration before any further installation attempt'}
  $state.phase='migrating';Write-MigrationState $statePath $state
  Stop-Service $ServiceName -Force -ErrorAction SilentlyContinue
  if(Get-Service $ServiceName -ErrorAction SilentlyContinue){
    & sc.exe delete $ServiceName|Out-Null
    if($LASTEXITCODE -ne 0){throw 'failed to delete legacy service'}
  }
  Wait-ServiceAbsent $ServiceName
  Stop-InstalledCompanion $CompanionTaskName $CompanionInstallDir
  Remove-DirectoryWithRetry $InstallDir
  $installArgs=@{SourceDir=$source;DatabaseURL=$DatabaseURL;InstallDir=$InstallDir;DataDir=$DataDir;BrokerInstallDir=$BrokerInstallDir;BrokerDataDir=$BrokerDataDir;ServiceName=$ServiceName;BrokerServiceName=$BrokerServiceName;CompanionTaskName=$CompanionTaskName;InstallCompanion=$InstallCompanion;Start=$true;Verify=$Verify;AllowUnsignedPackage=$AllowUnsignedPackage;AllowDirtyPackage=$AllowDirtyPackage;SkipCertificateRevocationCheck=$SkipCertificateRevocationCheck;ReleaseStagingRoot=$ReleaseStagingRoot}
  if($trustedSigner){$installArgs.TrustedSignerThumbprint=$trustedSigner}
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
  if($envMap['STEWARD_ORCHESTRATION_SIGNING_KEY']){$installArgs.OrchestrationSigningKey=$envMap['STEWARD_ORCHESTRATION_SIGNING_KEY']}
  if($envMap['STEWARD_MANAGEMENT_AUTH_TOKEN']){$installArgs.ManagementAuthToken=$envMap['STEWARD_MANAGEMENT_AUTH_TOKEN']}
  if($envMap['STEWARD_LLM_BASE_URL']){$installArgs.LLMBaseURL=$envMap['STEWARD_LLM_BASE_URL']}
  if($envMap['STEWARD_LLM_MODEL']){$installArgs.LLMModel=$envMap['STEWARD_LLM_MODEL']}
  if($envMap['STEWARD_LLM_API_KEY']){$installArgs.LLMAPIKey=$envMap['STEWARD_LLM_API_KEY']}
  if($envMap['STEWARD_LLM_API_KEY']){$installArgs.RecoverModelSettingsFromEnvironment=$true}
  & (Join-Path $source 'install-steward-production.ps1') @installArgs|Out-Host
  if($LASTEXITCODE -ne 0){throw 'production installer returned a failure'}
  $state.phase='committed';$state.completed_at=(Get-Date).ToUniversalTime().ToString('o');Write-MigrationState $statePath $state
  [ordered]@{ok=$true;resumed=$resuming;migrated_from=[string]$metadata.start_name;migrated_to='NT AUTHORITY\LocalService';backup=$backupRoot;broker=$BrokerServiceName}|ConvertTo-Json
}catch{
  $reason=$_.Exception.Message
  $state.phase='rolling_back';try{Write-MigrationState $statePath $state}catch{$rollbackErrors.Add("record rollback state: $($_.Exception.Message)")}
  try{Stop-InstalledCompanion $CompanionTaskName $CompanionInstallDir}catch{$rollbackErrors.Add($_.Exception.Message)}
  Stop-Service $ServiceName,$BrokerServiceName -Force -ErrorAction SilentlyContinue
  foreach($name in @($ServiceName,$BrokerServiceName)){
    if(Get-Service $name -ErrorAction SilentlyContinue){& sc.exe delete $name|Out-Null;if($LASTEXITCODE -ne 0){$rollbackErrors.Add("delete rollback service ${name}: sc.exe exit $LASTEXITCODE")}}
    try{Wait-ServiceAbsent $name}catch{$rollbackErrors.Add($_.Exception.Message)}
  }
  foreach($snapshotName in @('main_install','main_data','broker_install','broker_data','companion_install')){
    try{Restore-DirectorySnapshot $state.snapshots.$snapshotName}catch{$rollbackErrors.Add("restore ${snapshotName}: $($_.Exception.Message)")}
  }
  try{Restore-CompanionTask $state.companion_task $CompanionTaskName}catch{$rollbackErrors.Add("restore Companion task: $($_.Exception.Message)")}
  try{Restore-LegacyService $metadata $envMap $ServiceName $LegacyHealthURL $RollbackHealthTimeoutSeconds}catch{$rollbackErrors.Add($_.Exception.Message)}
  if($rollbackErrors.Count -eq 0){
    try{$state.phase='rolled_back';$state.rollback_verified=$true;$state.rolled_back_at=(Get-Date).ToUniversalTime().ToString('o');Write-MigrationState $statePath $state}catch{$rollbackErrors.Add("record verified rollback: $($_.Exception.Message)")}
  }
  $suffix=''
  if($rollbackErrors.Count -gt 0){$suffix=" Rollback also reported: $($rollbackErrors -join '; ')"}
  if($rollbackErrors.Count -eq 0){throw "migration failed, but the complete legacy installation, Companion task, data, ACLs and healthy service were restored from $backupRoot. Cause: $reason"}
  throw "migration failed and rollback could not be fully verified from $backupRoot. Cause: $reason.$suffix"
}
}finally{
  if($releaseStage -and (Test-Path -LiteralPath $releaseStage)){Remove-Item -LiteralPath $releaseStage -Recurse -Force -ErrorAction SilentlyContinue}
}
