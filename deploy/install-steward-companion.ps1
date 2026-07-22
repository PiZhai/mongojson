[CmdletBinding(SupportsShouldProcess)]
param(
  [Parameter(Mandatory = $true)][string]$SourceDir,
  [Parameter(Mandatory = $true)][string]$LocalEncryptionKey,
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "MongojsonSteward"),
  [string]$TaskName = "MongojsonStewardCompanion",
  [string]$ServiceName = "MongojsonSteward",
  [string]$ManagementAccessTokenFile = "",
  [string]$APIBase = "http://127.0.0.1:18080/api",
  [switch]$AllowUnauthenticatedDevelopment,
  [string]$RollbackRoot = "",
  [switch]$KeepRollbackData,
  [switch]$Start
)

$ErrorActionPreference = "Stop"

function Protect-CurrentUserPath([string]$Path) {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  $userGrant="*${identity}:F"; $systemGrant="*S-1-5-18:F"; $administratorsGrant='*S-1-5-32-544:F'
  if(Test-Path -LiteralPath $Path -PathType Container){
    $userGrant="*${identity}:(OI)(CI)F";$systemGrant="*S-1-5-18:(OI)(CI)F";$administratorsGrant='*S-1-5-32-544:(OI)(CI)F'
  }
  & icacls.exe $Path /inheritance:r /grant:r $userGrant $systemGrant $administratorsGrant | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to protect companion path: $Path" }
  & icacls.exe $Path /setowner "*${identity}" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to set current-user ownership on companion path: $Path" }
}
function Write-Utf8NoBom([string]$Path,[string]$Value){[IO.File]::WriteAllText($Path,$Value,[Text.UTF8Encoding]::new($false))}
function Assert-NoReparsePoints([string]$Root,[string]$Label) {
  $item=Get-Item -LiteralPath $Root -Force -ErrorAction Stop
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Label must not be a reparse point: $Root"}
  $nested=@(Get-ChildItem -LiteralPath $Root -Force -Recurse -ErrorAction Stop | Where-Object {($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0})
  if($nested.Count -gt 0){throw "$Label contains a reparse point: $($nested[0].FullName)"}
}
function Assert-NotReparseRoot([string]$Root,[string]$Label) {
  $item=Get-Item -LiteralPath $Root -Force -ErrorAction Stop
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Label must not be a reparse point: $Root"}
}
function Assert-PathWithin([string]$Path,[string]$Root,[string]$Label) {
  $rootFull=[IO.Path]::GetFullPath($Root).TrimEnd('\')+'\'
  $pathFull=[IO.Path]::GetFullPath($Path).TrimEnd('\')
  if(-not $pathFull.StartsWith($rootFull,[StringComparison]::OrdinalIgnoreCase)){throw "$Label must remain under ${rootFull}: $pathFull"}
  return $pathFull
}
function Normalize-CompanionAPIBase([string]$Value) {
  if([string]::IsNullOrWhiteSpace($Value)){throw 'APIBase must not be empty'}
  try{$uri=[Uri]$Value.Trim()}catch{throw "APIBase must be an absolute HTTP URL: $Value"}
  if(-not $uri.IsAbsoluteUri -or @('http','https') -notcontains $uri.Scheme){throw "APIBase must be an absolute HTTP URL: $Value"}
  if(-not [string]::IsNullOrEmpty($uri.UserInfo) -or -not [string]::IsNullOrEmpty($uri.Query) -or -not [string]::IsNullOrEmpty($uri.Fragment)){throw 'APIBase must not contain credentials, a query, or a fragment'}
  $loopback=$uri.Host.Equals('localhost',[StringComparison]::OrdinalIgnoreCase)
  $address=$null
  if(-not $loopback -and [Net.IPAddress]::TryParse($uri.Host,[ref]$address)){$loopback=[Net.IPAddress]::IsLoopback($address)}
  if(-not $loopback){throw "APIBase must target loopback: $Value"}
  $path=$uri.AbsolutePath.TrimEnd('/')
  if(-not $path.Equals('/api',[StringComparison]::OrdinalIgnoreCase)){throw "APIBase must end with /api: $Value"}
  return $uri.GetLeftPart([UriPartial]::Authority)+'/api'
}
function Get-WorkspaceShortcutPath {
  $programs=[Environment]::GetFolderPath([Environment+SpecialFolder]::Programs)
  if([string]::IsNullOrWhiteSpace($programs)){throw 'current-user Start Menu programs directory is unavailable'}
  return Join-Path $programs 'MongoJSON Steward.lnk'
}
function Write-WorkspaceShortcut([string]$Path,[string]$Executable,[string]$Arguments,[string]$WorkingDirectory) {
  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Path)|Out-Null
  $shell=New-Object -ComObject WScript.Shell
  try{
    $shortcut=$shell.CreateShortcut($Path)
    $shortcut.TargetPath=$Executable
    $shortcut.Arguments=$Arguments
    $shortcut.WorkingDirectory=$WorkingDirectory
    $shortcut.Description='打开 MongoJSON Steward 工作区'
    $shortcut.IconLocation="$Executable,0"
    $shortcut.Save()
  }finally{
    if($null -ne $shortcut){[Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut)|Out-Null}
    if($null -ne $shell){[Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell)|Out-Null}
  }
  Protect-CurrentUserPath $Path
}
function Stop-CompanionInstance([string]$ExecutablePath) {
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  $expected=[IO.Path]::GetFullPath($ExecutablePath)
  foreach($process in @(Get-CimInstance Win32_Process -Filter "Name='steward-companion.exe'" -ErrorAction SilentlyContinue)) {
    if(-not [string]::IsNullOrWhiteSpace([string]$process.ExecutablePath) -and [IO.Path]::GetFullPath([string]$process.ExecutablePath).Equals($expected,[StringComparison]::OrdinalIgnoreCase)) {
      Invoke-CimMethod -InputObject $process -MethodName Terminate -ErrorAction SilentlyContinue | Out-Null
    }
  }
  for($attempt=0;$attempt -lt 50;$attempt++) {
    $remaining=@(Get-CimInstance Win32_Process -Filter "Name='steward-companion.exe'" -ErrorAction SilentlyContinue | Where-Object {
      -not [string]::IsNullOrWhiteSpace([string]$_.ExecutablePath) -and [IO.Path]::GetFullPath([string]$_.ExecutablePath).Equals($expected,[StringComparison]::OrdinalIgnoreCase)
    })
    if($remaining.Count -eq 0){return}
    Start-Sleep -Milliseconds 100
  }
  throw "Session Companion did not stop and still holds $expected"
}
function Remove-DirectoryWithRetry([string]$Path) {
  if(-not(Test-Path -LiteralPath $Path)){return}
  $lastError=''
  for($attempt=0;$attempt -lt 10;$attempt++){
    try{Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction Stop;return}catch{$lastError=$_.Exception.Message;Start-Sleep -Milliseconds (100*($attempt+1))}
  }
  throw "failed to remove Companion directory '$Path': $lastError"
}
function Assert-OperationDirectory([string]$Path,[string]$OperationID) {
  $marker=Join-Path $Path '.steward-companion-operation'
  if(-not(Test-Path -LiteralPath $marker -PathType Leaf)){throw "refusing to remove an unmarked Companion directory: $Path"}
  if((Get-Content -LiteralPath $marker -Raw).Trim() -ne $OperationID){throw "refusing to remove a Companion directory owned by another operation: $Path"}
}

if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) { throw "LOCALAPPDATA is unavailable" }
$source = (Resolve-Path -LiteralPath $SourceDir).Path
Assert-NoReparsePoints $source 'Companion release source'
$sourceExe = Join-Path $source "steward-companion.exe"
if (-not (Test-Path -LiteralPath $sourceExe -PathType Leaf)) { throw "missing steward-companion.exe in $source" }
if ($LocalEncryptionKey -notmatch '^[A-Za-z0-9+/]{43}=$') { throw "LocalEncryptionKey must be a base64 encoded 32-byte key" }
$installFull=[IO.Path]::GetFullPath($InstallDir).TrimEnd('\')
$APIBase=Normalize-CompanionAPIBase $APIBase
$effectiveManagementTokenFile=if(-not [string]::IsNullOrWhiteSpace($ManagementAccessTokenFile)){[IO.Path]::GetFullPath($ManagementAccessTokenFile)}else{Join-Path $installFull 'management-access-token.txt'}
$requireManagementToken=-not [bool]$AllowUnauthenticatedDevelopment
$shortcutPath=Get-WorkspaceShortcutPath
if(Test-Path -LiteralPath $effectiveManagementTokenFile -PathType Leaf){
  $managementTokenItem=Get-Item -LiteralPath $effectiveManagementTokenFile -Force
  if(($managementTokenItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "ManagementAccessTokenFile must not be a reparse point: $effectiveManagementTokenFile"}
  $managementToken=[IO.File]::ReadAllText($effectiveManagementTokenFile).Trim()
  if([string]::IsNullOrWhiteSpace($managementToken)){throw "ManagementAccessTokenFile is empty: $effectiveManagementTokenFile"}
  if($managementToken.Contains("`r") -or $managementToken.Contains("`n")){throw "ManagementAccessTokenFile must contain one line: $effectiveManagementTokenFile"}
  $managementToken=$null
}elseif($requireManagementToken){
  throw "ManagementAccessTokenFile is required for a production Companion installation: $effectiveManagementTokenFile. Use -AllowUnauthenticatedDevelopment only for an explicit local development instance."
}

$result=[ordered]@{ok=$true;task_name=$TaskName;install_dir=$InstallDir;service_name=$ServiceName;api_base=$APIBase;management_auth_required=$requireManagementToken;run_level="Limited";shortcut_path=$shortcutPath;updated=$false;rollback_dir=$null;rollback_task_xml=$null;previous_task_present=$false;previous_task_running=$false;transaction_state='not_started'}
if ($PSCmdlet.ShouldProcess($InstallDir, "atomically install Steward Session Companion")) {
  $parent=Split-Path -Parent $installFull
  if([string]::IsNullOrWhiteSpace($parent)){throw "Companion InstallDir must have a parent directory: $installFull"}
  if([string]::IsNullOrWhiteSpace($RollbackRoot)){$RollbackRoot=$parent}
  New-Item -ItemType Directory -Force -Path $parent,$RollbackRoot | Out-Null
  $rollbackFull=[IO.Path]::GetFullPath((Resolve-Path -LiteralPath $RollbackRoot).Path).TrimEnd('\')
  Assert-NotReparseRoot $rollbackFull 'Companion rollback base'
  # Never rewrite ACLs on the caller's broad base (normally LOCALAPPDATA).
  # All transaction artifacts live in a dedicated protected child instead.
  $rollbackArea=Join-Path $rollbackFull '.MongojsonStewardRollbacks'
  New-Item -ItemType Directory -Force -Path $rollbackArea|Out-Null
  Assert-NoReparsePoints $rollbackArea 'Companion rollback root'
  Protect-CurrentUserPath $rollbackArea
  $operationID=[guid]::NewGuid().ToString('N')
  $stageDir=Assert-PathWithin (Join-Path $parent (".MongojsonSteward.stage-"+$operationID)) $parent 'Companion stage directory'
  $backupDir=Assert-PathWithin (Join-Path $rollbackArea ("MongojsonSteward.rollback-"+$operationID)) $rollbackArea 'Companion rollback directory'
  $taskXMLPath=Assert-PathWithin (Join-Path $rollbackArea ("MongojsonSteward.task-"+$operationID+".xml")) $rollbackArea 'Companion task backup'
  $shortcutBackupPath=Assert-PathWithin (Join-Path $rollbackArea ("MongojsonSteward.shortcut-"+$operationID+".lnk")) $rollbackArea 'Companion shortcut backup'

  # These flags are the transaction journal. Rollback is deliberately limited
  # to mutations which this invocation actually completed; an early copy or
  # ACL failure must never delete the pre-existing installation or task.
  $stageCreated=$false;$taskExported=$false;$oldTaskStopped=$false;$oldTaskUnregistered=$false
  $oldInstallMoved=$false;$stageActivated=$false;$newTaskRegistered=$false;$newTaskStarted=$false
  $oldShortcutPresent=Test-Path -LiteralPath $shortcutPath -PathType Leaf
  $shortcutBackedUp=$false;$newShortcutWritten=$false
  $oldTask=Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  $oldTaskRunning=$false
  $hadInstallDir=Test-Path -LiteralPath $installFull
  try {
    if($hadInstallDir){Assert-NoReparsePoints $installFull 'existing Companion installation'}
    if($oldTask){
      $result.previous_task_present=$true
      $oldTaskRunning=$oldTask.State -eq 'Running'
      $result.previous_task_running=$oldTaskRunning
      Export-ScheduledTask -TaskName $TaskName | Set-Content -LiteralPath $taskXMLPath -Encoding Unicode
      Protect-CurrentUserPath $taskXMLPath
      $taskExported=$true
    }
    if($oldShortcutPresent){Copy-Item -LiteralPath $shortcutPath -Destination $shortcutBackupPath -Force;Protect-CurrentUserPath $shortcutBackupPath;$shortcutBackedUp=$true}

    New-Item -ItemType Directory -Path $stageDir | Out-Null
    $stageCreated=$true;$result.transaction_state='staged'
    Write-Utf8NoBom (Join-Path $stageDir '.steward-companion-operation') $operationID
    Copy-Item -LiteralPath $sourceExe -Destination (Join-Path $stageDir "steward-companion.exe") -Force
    $sourceNotifierDir=Join-Path $source 'windows-notifier'
    if(Test-Path -LiteralPath $sourceNotifierDir -PathType Container){
      Copy-Item -LiteralPath $sourceNotifierDir -Destination (Join-Path $stageDir 'windows-notifier') -Recurse -Force
    }
    $stageSecret=Join-Path $stageDir "companion-secrets.json"
    Write-Utf8NoBom $stageSecret (@{ STEWARD_LOCAL_ENCRYPTION_KEY = $LocalEncryptionKey } | ConvertTo-Json -Compress)
    $existingManagementToken=$effectiveManagementTokenFile
    $stagedManagementToken=Join-Path $stageDir 'management-access-token.txt'
    if(Test-Path -LiteralPath $existingManagementToken -PathType Leaf){Copy-Item -LiteralPath $existingManagementToken -Destination $stagedManagementToken -Force}
    Protect-CurrentUserPath $stageDir
    Protect-CurrentUserPath (Join-Path $stageDir "steward-companion.exe")
    $stagedNotifier=Join-Path $stageDir 'windows-notifier'
    if(Test-Path -LiteralPath $stagedNotifier -PathType Container){Protect-CurrentUserPath $stagedNotifier}
    Protect-CurrentUserPath $stageSecret
    if(Test-Path -LiteralPath $stagedManagementToken -PathType Leaf){Protect-CurrentUserPath $stagedManagementToken}

    if($oldTask -or $hadInstallDir){
      Stop-CompanionInstance (Join-Path $installFull "steward-companion.exe")
      $oldTaskStopped=[bool]$oldTask
    }
    if($oldTask){Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction Stop;$oldTaskUnregistered=$true}
    if($hadInstallDir){Move-Item -LiteralPath $installFull -Destination $backupDir -ErrorAction Stop;$oldInstallMoved=$true}
    Move-Item -LiteralPath $stageDir -Destination $installFull -ErrorAction Stop
    $stageCreated=$false;$stageActivated=$true;$result.transaction_state='activated'
    Protect-CurrentUserPath $installFull
    if($env:STEWARD_COMPANION_TEST_FAIL_AFTER_ACTIVATION -eq '1'){throw 'injected Companion transaction failure after activation'}

    $secretPath = Join-Path $installFull "companion-secrets.json"
    $exe = Join-Path $installFull "steward-companion.exe"
    $managementTokenPath=Join-Path $installFull 'management-access-token.txt'
    $requireManagementTokenArgument=$requireManagementToken.ToString().ToLowerInvariant()
    $arguments = "--service-name `"$ServiceName`" --private-environment-file `"$secretPath`" --management-access-token-file `"$managementTokenPath`" --require-management-token=$requireManagementTokenArgument --api `"$APIBase`""
    $action = New-ScheduledTaskAction -Execute $exe -Argument $arguments -WorkingDirectory $installFull
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User ([Security.Principal.WindowsIdentity]::GetCurrent().Name)
    $principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force -ErrorAction Stop | Out-Null
    $newTaskRegistered=$true;$result.transaction_state='registered'
    $launchArguments = "--open-workspace --management-access-token-file `"$managementTokenPath`" --require-management-token=$requireManagementTokenArgument --api `"$APIBase`""
    Write-WorkspaceShortcut $shortcutPath $exe $launchArguments $installFull
    $newShortcutWritten=$true
    if ($Start) { Start-ScheduledTask -TaskName $TaskName -ErrorAction Stop;$newTaskStarted=$true }
    $result.updated=$hadInstallDir -or $null -ne $oldTask
    if($KeepRollbackData -and $hadInstallDir){$result.rollback_dir=$backupDir}else{Remove-DirectoryWithRetry $backupDir}
    if($KeepRollbackData -and $oldTask){$result.rollback_task_xml=$taskXMLPath}elseif($taskExported -and (Test-Path -LiteralPath $taskXMLPath)){Remove-Item -LiteralPath $taskXMLPath -Force}
    if($shortcutBackedUp -and (Test-Path -LiteralPath $shortcutBackupPath)){Remove-Item -LiteralPath $shortcutBackupPath -Force}
    $result.transaction_state='committed'
  } catch {
    $reason=$_.Exception.Message
    $rollbackErrors=New-Object System.Collections.Generic.List[string]
    if($newTaskStarted){try{Stop-ScheduledTask -TaskName $TaskName -ErrorAction Stop}catch{$rollbackErrors.Add("stop new task: $($_.Exception.Message)")}}
    if($newShortcutWritten -and (Test-Path -LiteralPath $shortcutPath)){try{Remove-Item -LiteralPath $shortcutPath -Force -ErrorAction Stop}catch{$rollbackErrors.Add("remove new shortcut: $($_.Exception.Message)")}}
    if($shortcutBackedUp){try{Copy-Item -LiteralPath $shortcutBackupPath -Destination $shortcutPath -Force -ErrorAction Stop}catch{$rollbackErrors.Add("restore workspace shortcut: $($_.Exception.Message)")}}
    if($newTaskRegistered){try{Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction Stop}catch{$rollbackErrors.Add("remove new task: $($_.Exception.Message)")}}
    if($stageActivated){
      try{Assert-OperationDirectory $installFull $operationID;Remove-DirectoryWithRetry $installFull}catch{$rollbackErrors.Add("remove activated Companion: $($_.Exception.Message)")}
    }
    if($oldInstallMoved){try{Move-Item -LiteralPath $backupDir -Destination $installFull -ErrorAction Stop}catch{$rollbackErrors.Add("restore Companion directory: $($_.Exception.Message)")}}
    if($oldTaskUnregistered -and $taskExported){
      try{
        Register-ScheduledTask -TaskName $TaskName -Xml (Get-Content -LiteralPath $taskXMLPath -Raw) -Force -ErrorAction Stop | Out-Null
        if($oldTaskRunning){Start-ScheduledTask -TaskName $TaskName -ErrorAction Stop}
      }catch{$rollbackErrors.Add("restore Companion task: $($_.Exception.Message)")}
    }elseif($oldTaskStopped -and $oldTaskRunning){
      try{Start-ScheduledTask -TaskName $TaskName -ErrorAction Stop}catch{$rollbackErrors.Add("restart existing Companion task: $($_.Exception.Message)")}
    }
    if($stageCreated){try{Assert-OperationDirectory $stageDir $operationID;Remove-DirectoryWithRetry $stageDir}catch{$rollbackErrors.Add("remove Companion stage: $($_.Exception.Message)")}}
    if($taskExported -and (Test-Path -LiteralPath $taskXMLPath)){Remove-Item -LiteralPath $taskXMLPath -Force -ErrorAction SilentlyContinue}
    if($shortcutBackedUp -and (Test-Path -LiteralPath $shortcutBackupPath)){Remove-Item -LiteralPath $shortcutBackupPath -Force -ErrorAction SilentlyContinue}
    $suffix=if($rollbackErrors.Count -gt 0){" Rollback also reported: $($rollbackErrors -join '; ')"}else{''}
    throw "Session Companion installation failed; completed transaction steps were rolled back: $reason.$suffix"
  }
}

$result | ConvertTo-Json
