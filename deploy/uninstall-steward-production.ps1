[CmdletBinding(SupportsShouldProcess,ConfirmImpact='Medium')]
param(
  [string]$InstallDir="C:\Program Files\MongojsonSteward",
  [string]$DataDir="C:\ProgramData\MongojsonSteward",
  [string]$ServiceName="MongojsonSteward",
  [string]$BrokerServiceName="MongojsonStewardBroker",
  [string]$BrokerInstallDir="C:\Program Files\MongoJSON\StewardBroker",
  [string]$BrokerDataDir="C:\ProgramData\MongoJSON\StewardBroker",
  [string]$CompanionTaskName="MongojsonStewardCompanion",
  [string]$CompanionInstallDir=(Join-Path $env:LOCALAPPDATA 'MongojsonSteward'),
  [switch]$RemoveData,
  [switch]$RemoveCompanionData
)
$ErrorActionPreference='Stop'

function Test-Administrator {
  $principal=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Get-CanonicalPath([string]$Path,[string]$Name,[bool]$MustExist=$false) {
  if([string]::IsNullOrWhiteSpace($Path)){throw "$Name must not be empty"}
  if([WildcardPattern]::ContainsWildcardCharacters($Path)){throw "$Name must not contain wildcard characters"}
  if(-not[IO.Path]::IsPathRooted($Path)){throw "$Name must be absolute"}
  try{$full=[IO.Path]::GetFullPath($Path);if($full.Length -gt [IO.Path]::GetPathRoot($full).Length){$full=$full.TrimEnd('\')}}catch{throw "$Name is not a valid absolute path"}
  if($MustExist -and -not(Test-Path -LiteralPath $full)){throw "$Name does not exist: $full"}
  return $full
}
function Assert-SafeSystemName([string]$Value,[string]$Name) {if($Value -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$'){throw "$Name contains unsupported characters"}}
function Assert-DedicatedChildPath([string]$Path,[string]$Root,[string]$Name) {
  $full=Get-CanonicalPath $Path $Name $false;$rootFull=Get-CanonicalPath $Root "$Name root" $true
  if($full.Equals($rootFull,[StringComparison]::OrdinalIgnoreCase) -or -not $full.StartsWith($rootFull+'\',[StringComparison]::OrdinalIgnoreCase)){throw "$Name must be a dedicated child below $rootFull"}
  return $full
}
function Assert-NoReparsePath([string]$Path,[string]$Name,[bool]$InspectTree=$false) {
  $current=Get-CanonicalPath $Path $Name $false
  while($true){
    if(Test-Path -LiteralPath $current){$item=Get-Item -LiteralPath $current -Force;if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Name contains a reparse point at $current"}}
    $parent=Split-Path -Parent $current;if([string]::IsNullOrWhiteSpace($parent) -or $parent -eq $current){break};$current=$parent
  }
  if($InspectTree -and (Test-Path -LiteralPath $Path)){
    $nested=@(Get-ChildItem -LiteralPath $Path -Force -Recurse|Where-Object{($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0})
    if($nested.Count -gt 0){throw "$Name contains a nested reparse point at $($nested[0].FullName)"}
  }
}
function Assert-DisjointPaths([hashtable]$Paths) {
  $entries=@($Paths.GetEnumerator())
  for($i=0;$i -lt $entries.Count;$i++){for($j=$i+1;$j -lt $entries.Count;$j++){
    $left=[string]$entries[$i].Value;$right=[string]$entries[$j].Value
    if($left.Equals($right,[StringComparison]::OrdinalIgnoreCase) -or $left.StartsWith($right+'\',[StringComparison]::OrdinalIgnoreCase) -or $right.StartsWith($left+'\',[StringComparison]::OrdinalIgnoreCase)){throw "$($entries[$i].Key) and $($entries[$j].Key) must not overlap"}
  }}
}
function Get-ServiceExecutablePath([string]$ImagePath,[string]$Name) {
  $match=[regex]::Match($ImagePath,'^\s*(?:"([^"]+)"|(\S+))');if(-not $match.Success){throw "$Name has an invalid ImagePath"}
  $value=if($match.Groups[1].Success){$match.Groups[1].Value}else{$match.Groups[2].Value};return Get-CanonicalPath $value "$Name executable" $true
}
function Assert-ServiceOwnership([object]$Service,[string]$ExpectedExecutable,[string]$ExpectedAccount,[string]$Name) {
  if($null -eq $Service){return}
  if(-not([string]$Service.StartName).Equals($ExpectedAccount,[StringComparison]::OrdinalIgnoreCase)){throw "refusing uninstall: $Name is not owned by the expected service account"}
  $actual=Get-ServiceExecutablePath ([string]$Service.PathName) $Name
  if(-not $actual.Equals($ExpectedExecutable,[StringComparison]::OrdinalIgnoreCase)){throw "refusing uninstall: $Name ImagePath is outside the owned installation: $actual"}
}
function Assert-MarkerValue([object]$Marker,[string]$Property,[string]$Expected,[bool]$PathValue=$false) {
  $actual=[string]$Marker.$Property;if([string]::IsNullOrWhiteSpace($actual)){return};if($PathValue){$actual=Get-CanonicalPath $actual "installation marker $Property" $false}
  if(-not $actual.Equals($Expected,[StringComparison]::OrdinalIgnoreCase)){throw "installation marker $Property does not match this uninstall target"}
}
function Remove-ServiceAndWait([string]$Name) {
  if(-not(Get-Service -Name $Name -ErrorAction SilentlyContinue)){return}
  Stop-Service -Name $Name -Force -ErrorAction SilentlyContinue
  & sc.exe delete $Name|Out-Null;if($LASTEXITCODE -ne 0){throw "failed to delete Windows service $Name"}
  for($i=0;$i -lt 50 -and (Get-Service -Name $Name -ErrorAction SilentlyContinue);$i++){Start-Sleep -Milliseconds 200}
  if(Get-Service -Name $Name -ErrorAction SilentlyContinue){throw "Windows service is still pending deletion: $Name"}
}

if(-not(Test-Administrator)){throw 'Run this uninstaller from an elevated PowerShell session.'}
Assert-SafeSystemName $ServiceName 'ServiceName';Assert-SafeSystemName $BrokerServiceName 'BrokerServiceName';Assert-SafeSystemName $CompanionTaskName 'CompanionTaskName'
$InstallDir=Assert-DedicatedChildPath $InstallDir $env:ProgramFiles 'InstallDir';$BrokerInstallDir=Assert-DedicatedChildPath $BrokerInstallDir $env:ProgramFiles 'BrokerInstallDir'
$DataDir=Assert-DedicatedChildPath $DataDir $env:ProgramData 'DataDir';$BrokerDataDir=Assert-DedicatedChildPath $BrokerDataDir $env:ProgramData 'BrokerDataDir'
$localAppData=Get-CanonicalPath $env:LOCALAPPDATA 'LocalApplicationData' $true;$CompanionInstallDir=Get-CanonicalPath $CompanionInstallDir 'CompanionInstallDir' $false
if($CompanionInstallDir.Equals($localAppData,[StringComparison]::OrdinalIgnoreCase) -or -not $CompanionInstallDir.StartsWith($localAppData+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'CompanionInstallDir must be a dedicated child below the current user LocalAppData'}
Assert-DisjointPaths @{InstallDir=$InstallDir;BrokerInstallDir=$BrokerInstallDir;DataDir=$DataDir;BrokerDataDir=$BrokerDataDir}
foreach($entry in @(@($InstallDir,'InstallDir'),@($BrokerInstallDir,'BrokerInstallDir'),@($DataDir,'DataDir'),@($BrokerDataDir,'BrokerDataDir'))){Assert-NoReparsePath $entry[0] $entry[1] $true}
Assert-NoReparsePath $CompanionInstallDir 'CompanionInstallDir' $true
$expectedScript=Join-Path $InstallDir 'uninstall-steward-production.ps1';$runningScript=Get-CanonicalPath $PSCommandPath 'running uninstaller' $true
if(-not $runningScript.Equals($expectedScript,[StringComparison]::OrdinalIgnoreCase)){throw "production uninstall must be launched from the owned installation: $expectedScript"}
$markerPath=Join-Path $DataDir 'installation.json'
if(-not(Test-Path -LiteralPath $markerPath -PathType Leaf)){throw "refusing uninstall without the installation ownership marker: $markerPath"}
Assert-NoReparsePath $markerPath 'installation marker' $false
$marker=Get-Content -LiteralPath $markerPath -Raw|ConvertFrom-Json
if([string]$marker.schema -and [string]$marker.schema -ne 'mongojson.steward.windows-installation/v2'){throw "unsupported installation marker schema: $($marker.schema)"}
if([string]::IsNullOrWhiteSpace([string]$marker.service_name) -or [string]::IsNullOrWhiteSpace([string]$marker.broker_service_name)){throw 'installation marker does not identify both owned services'}
if([string]$marker.schema -eq 'mongojson.steward.windows-installation/v2'){foreach($property in @('install_dir','data_dir','broker_install_dir','broker_data_dir')){if([string]::IsNullOrWhiteSpace([string]$marker.$property)){throw "v2 installation marker is missing $property"}}}
Assert-MarkerValue $marker 'service_name' $ServiceName;Assert-MarkerValue $marker 'broker_service_name' $BrokerServiceName
Assert-MarkerValue $marker 'companion_task_name' $CompanionTaskName
Assert-MarkerValue $marker 'install_dir' $InstallDir $true;Assert-MarkerValue $marker 'data_dir' $DataDir $true;Assert-MarkerValue $marker 'broker_install_dir' $BrokerInstallDir $true;Assert-MarkerValue $marker 'broker_data_dir' $BrokerDataDir $true
$main=Get-CimInstance Win32_Service|Where-Object Name -eq $ServiceName|Select-Object -First 1;$broker=Get-CimInstance Win32_Service|Where-Object Name -eq $BrokerServiceName|Select-Object -First 1
Assert-ServiceOwnership $main (Join-Path $InstallDir 'steward.exe') 'NT AUTHORITY\LocalService' $ServiceName;Assert-ServiceOwnership $broker (Join-Path $BrokerInstallDir 'steward-broker.exe') 'LocalSystem' $BrokerServiceName
$companionTask=Get-ScheduledTask -TaskName $CompanionTaskName -ErrorAction SilentlyContinue
if($null -ne $companionTask){
  $taskAction=Get-CanonicalPath ([string](@($companionTask.Actions)[0].Execute)) 'Companion task executable' $true
  if(-not $taskAction.Equals((Join-Path $CompanionInstallDir 'steward-companion.exe'),[StringComparison]::OrdinalIgnoreCase)){throw 'refusing uninstall: Session Companion task is not owned by CompanionInstallDir'}
  $taskUser=[string]$companionTask.Principal.UserId;$taskSID=if($taskUser -match '^S-1-'){$taskUser}else{(New-Object Security.Principal.NTAccount($taskUser)).Translate([Security.Principal.SecurityIdentifier]).Value}
  if($taskSID -ne [Security.Principal.WindowsIdentity]::GetCurrent().User.Value){throw 'refusing uninstall: Session Companion task belongs to a different user'}
}
$errors=[Collections.Generic.List[string]]::new()
if($PSCmdlet.ShouldProcess($ServiceName,'remove Steward production services and owned binaries')){
  $companionUninstaller=Join-Path $InstallDir 'uninstall-steward-companion.ps1'
  if(Test-Path -LiteralPath $companionUninstaller -PathType Leaf){try{& $companionUninstaller -InstallDir $CompanionInstallDir -TaskName $CompanionTaskName -RemoveData:$RemoveCompanionData -Confirm:$false|Out-Host;if($LASTEXITCODE -ne 0){throw 'companion uninstaller returned failure'}}catch{$errors.Add("Session Companion: $($_.Exception.Message)")}}
  try{Remove-ServiceAndWait $ServiceName}catch{$errors.Add($_.Exception.Message)};try{Remove-ServiceAndWait $BrokerServiceName}catch{$errors.Add($_.Exception.Message)}
  if($errors.Count -eq 0){
    foreach($entry in @(@($InstallDir,'main install'),@($BrokerInstallDir,'Broker install'))){try{if(Test-Path -LiteralPath $entry[0]){Remove-Item -LiteralPath $entry[0] -Recurse -Force}}catch{$errors.Add("remove $($entry[1]): $($_.Exception.Message)")}}
    if($errors.Count -eq 0 -and (Test-Path -LiteralPath $markerPath)){try{Remove-Item -LiteralPath $markerPath -Force}catch{$errors.Add("remove installation marker: $($_.Exception.Message)")}}
    if($errors.Count -eq 0 -and $RemoveData){foreach($entry in @(@($DataDir,'main data'),@($BrokerDataDir,'Broker data'))){try{if(Test-Path -LiteralPath $entry[0]){Remove-Item -LiteralPath $entry[0] -Recurse -Force}}catch{$errors.Add("remove $($entry[1]): $($_.Exception.Message)")}}}
  }
}
if($errors.Count -gt 0){throw "production uninstall was incomplete: $($errors -join '; ')"}
[ordered]@{ok=$true;services_removed=$true;install_removed=$true;broker_install_removed=$true;data_removed=[bool]$RemoveData;companion_data_removed=[bool]$RemoveCompanionData}|ConvertTo-Json
