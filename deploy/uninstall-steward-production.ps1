[CmdletBinding()]
param(
  [string]$InstallDir="C:\Program Files\MongojsonSteward",
  [string]$DataDir="C:\ProgramData\MongojsonSteward",
  [string]$ServiceName="MongojsonSteward",
  [string]$BrokerServiceName="MongojsonStewardBroker",
  [string]$BrokerInstallDir="C:\Program Files\MongoJSON\StewardBroker",
  [string]$BrokerDataDir="C:\ProgramData\MongoJSON\StewardBroker",
  [switch]$RemoveData,
  [switch]$RemoveCompanionData
)
$ErrorActionPreference='Stop'
if(Test-Path (Join-Path $PSScriptRoot 'uninstall-steward-companion.ps1')){& (Join-Path $PSScriptRoot 'uninstall-steward-companion.ps1') -RemoveData:$RemoveCompanionData|Out-Host}
if(Get-Service $ServiceName -ErrorAction SilentlyContinue){Stop-Service $ServiceName -Force -ErrorAction SilentlyContinue;& sc.exe delete $ServiceName|Out-Null}
if(Get-Service $BrokerServiceName -ErrorAction SilentlyContinue){Stop-Service $BrokerServiceName -Force -ErrorAction SilentlyContinue;& sc.exe delete $BrokerServiceName|Out-Null}
if(Test-Path $InstallDir){Remove-Item $InstallDir -Recurse -Force}
if(Test-Path $BrokerInstallDir){Remove-Item $BrokerInstallDir -Recurse -Force}
if($RemoveData -and (Test-Path $DataDir)){Remove-Item $DataDir -Recurse -Force}
if($RemoveData -and (Test-Path $BrokerDataDir)){Remove-Item $BrokerDataDir -Recurse -Force}
[ordered]@{ok=$true;services_removed=$true;install_removed=$true;broker_install_removed=$true;data_removed=[bool]$RemoveData}|ConvertTo-Json
