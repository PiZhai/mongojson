[CmdletBinding()]
param(
  [Parameter(Mandatory=$true)][string]$SourceDir,
  [string]$InstallDir="C:\Program Files\MongojsonSteward",
  [string]$ServiceName="MongojsonSteward",
  [string]$BrokerServiceName="MongojsonStewardBroker",
  [string]$BrokerPolicyPath="C:\ProgramData\MongoJSON\StewardBroker\policy.json",
  [string]$HealthURL="http://127.0.0.1:18080/healthz"
)
$ErrorActionPreference='Stop'
$source=(Resolve-Path $SourceDir).Path
foreach($name in @('steward.exe','steward-broker.exe','steward-approval.exe','steward-companion.exe','steward-system-tool-host.exe','ui\index.html')){if(-not(Test-Path (Join-Path $source $name))){throw "release is missing $name"}}
$main=Get-CimInstance Win32_Service -Filter "Name='$ServiceName'"; $broker=Get-CimInstance Win32_Service -Filter "Name='$BrokerServiceName'"
if($main.StartName -ne 'NT AUTHORITY\LocalService'){throw "refusing update: main service is not LocalService"}
if($broker.StartName -ne 'LocalSystem'){throw "refusing update: Broker is not LocalSystem"}
$stamp=Get-Date -Format yyyyMMdd-HHmmss; $backup="$InstallDir.backup-$stamp"; $failed="$InstallDir.failed-$stamp"
$brokerBinary='C:\Program Files\MongoJSON\StewardBroker\steward-broker.exe'
$brokerBackup="$brokerBinary.backup-$stamp"
$policyBackup=$null
if(-not(Test-Path $brokerBinary)){throw "installed Broker binary is missing: $brokerBinary"}
try{
  Stop-Service $ServiceName -Force; Stop-Service $BrokerServiceName -Force
  Copy-Item $brokerBinary $brokerBackup -Force
  Copy-Item (Join-Path $source 'steward-broker.exe') $brokerBinary -Force
  Move-Item $InstallDir $backup; New-Item -ItemType Directory $InstallDir|Out-Null; Copy-Item (Join-Path $source '*') $InstallDir -Recurse -Force
  & icacls.exe $InstallDir /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
  $sid=(New-Object Security.Principal.NTAccount("NT SERVICE\$ServiceName")).Translate([Security.Principal.SecurityIdentifier]).Value
  & icacls.exe $InstallDir /grant:r "*${sid}:(OI)(CI)RX"|Out-Null
  & icacls.exe (Join-Path $InstallDir 'steward-system-tool-host.exe') /grant '*S-1-5-18:(RX)' '*S-1-5-12:(RX)'|Out-Null
  if($LASTEXITCODE -ne 0){throw 'failed to grant the production capability token access to the System Tool Host'}
  $refreshRaw=& $brokerBinary refresh-system-policy --policy $BrokerPolicyPath --system-tool-host (Join-Path $InstallDir 'steward-system-tool-host.exe') 2>&1|Out-String
  if($LASTEXITCODE -ne 0){throw "Broker System Tool policy refresh failed: $refreshRaw"}
  $policyBackup=($refreshRaw|ConvertFrom-Json).backup
  Start-Service $BrokerServiceName; Start-Service $ServiceName
  & (Join-Path $source 'install-steward-companion.ps1') -SourceDir $source -LocalEncryptionKey ((Get-Content (Join-Path $env:LOCALAPPDATA 'MongojsonSteward\companion-secrets.json') -Raw|ConvertFrom-Json).STEWARD_LOCAL_ENCRYPTION_KEY) -ServiceName $ServiceName -Start|Out-Host
  & (Join-Path $source 'test-steward-production.ps1') -ServiceName $ServiceName -BrokerServiceName $BrokerServiceName -RequireCompanion|Out-Host
  if($LASTEXITCODE -ne 0){throw 'post-update verification failed'}
  [ordered]@{ok=$true;backup=$backup;install_dir=$InstallDir}|ConvertTo-Json
}catch{
  $reason=$_.Exception.Message; Stop-Service $ServiceName,$BrokerServiceName -Force -ErrorAction SilentlyContinue
  if(Test-Path $brokerBackup){Copy-Item $brokerBackup $brokerBinary -Force}
  if($policyBackup -and (Test-Path $policyBackup)){Copy-Item $policyBackup $BrokerPolicyPath -Force}
  if(Test-Path $InstallDir){Move-Item $InstallDir $failed};if(Test-Path $backup){Move-Item $backup $InstallDir;Start-Service $BrokerServiceName;Start-Service $ServiceName}
  throw "update failed and previous release was restored: $reason"
}
