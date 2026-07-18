[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$SourceDir,
  [Parameter(Mandatory = $true)][string]$DatabaseURL,
  [string]$BrokerPolicyPath = "",
  [string]$InstallDir = "C:\Program Files\MongojsonSteward",
  [string]$DataDir = "C:\ProgramData\MongojsonSteward",
  [string]$BrokerInstallDir = "C:\Program Files\MongoJSON\StewardBroker",
  [string]$BrokerDataDir = "C:\ProgramData\MongoJSON\StewardBroker",
  [string]$ServiceName = "MongojsonSteward",
  [string]$BrokerServiceName = "MongojsonStewardBroker",
  [string]$HTTPAddress = "127.0.0.1:18080",
  [string]$PeerHTTPAddress = "127.0.0.1:18081",
  [string]$AgentID = $env:COMPUTERNAME,
  [string]$SyncSecret = "",
  [string]$DevicePrivateKey = "",
  [string]$DevicePublicKey = "",
  [string]$SyncEncryptionKey = "",
  [string]$SyncEncryptionKeyID = "",
  [string]$SyncEncryptionPreviousKeys = "",
  [string]$LocalEncryptionKey = "",
  [string]$LocalEncryptionKeyID = "",
  [string]$LocalEncryptionPreviousKeys = "",
  [string]$LLMBaseURL = "",
  [string]$LLMModel = "",
  [string]$LLMAPIKey = "",
  [switch]$RecoverModelSettingsFromEnvironment,
  [switch]$InstallCompanion,
  [switch]$Start,
  [switch]$Verify
)

$ErrorActionPreference = "Stop"
function Test-Administrator {
  $p=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function New-Key([int]$Length=32) { $b=New-Object byte[] $Length; $rng=[Security.Cryptography.RandomNumberGenerator]::Create();try{$rng.GetBytes($b)}finally{$rng.Dispose()};return [Convert]::ToBase64String($b) }
function Assert-Loopback([string]$Address,[string]$Name) {
  $hostPart=($Address -split ':')[0].Trim('[',']')
  if ($hostPart -notin @('127.0.0.1','localhost','::1')) { throw "$Name must bind to loopback: $Address" }
}
function Protect-AdminPath([string]$Path) {
  & icacls.exe $Path /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to protect $Path" }
}
function Grant-RestrictedCapabilityHostReadExecute([string]$Executable) {
  # The production capability token retains the LocalSystem user SID so native
  # and CLR runtimes can initialize, but disables Administrators, all service
  # SIDs and optional privileges. Only this immutable hash-pinned host receives
  # SYSTEM RX; Broker policy, keys, state and audit deliberately do not.
  & icacls.exe $Executable /grant '*S-1-5-18:(RX)' '*S-1-5-12:(RX)'|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to grant the production capability token read/execute access to $Executable"}
}
function Set-ServicePublicEnvironmentValue([string]$Name,[string]$Key,[string]$Value) {
  $path="HKLM:\SYSTEM\CurrentControlSet\Services\$Name"
  $entries=@((Get-ItemProperty -LiteralPath $path -Name Environment -ErrorAction Stop).Environment)
  $updated=[Collections.Generic.List[string]]::new();$replaced=$false
  foreach($entry in $entries){
    if(([string]$entry).StartsWith("$Key=",[StringComparison]::OrdinalIgnoreCase)){
      $updated.Add("$Key=$Value");$replaced=$true
    }else{$updated.Add([string]$entry)}
  }
  if(-not $replaced){$updated.Add("$Key=$Value")}
  Set-ItemProperty -LiteralPath $path -Name Environment -Value ([string[]]$updated)
}
function Move-OrphanedBrokerData([string]$Path,[string]$Reason) {
  if(-not(Test-Path -LiteralPath $Path)){return ''}
  $programData=[IO.Path]::GetFullPath($env:ProgramData).TrimEnd('\')+'\'
  $full=[IO.Path]::GetFullPath($Path).TrimEnd('\')
  if(-not $full.StartsWith($programData,[StringComparison]::OrdinalIgnoreCase)){throw "refusing to quarantine Broker data outside ProgramData: $full"}
  $target="$full.$Reason-$(Get-Date -Format yyyyMMdd-HHmmss)"
  Move-Item -LiteralPath $full -Destination $target -Force
  return $target
}

if (-not (Test-Administrator)) { throw "Run this installer from an elevated PowerShell session." }
Assert-Loopback $HTTPAddress "HTTPAddress"; Assert-Loopback $PeerHTTPAddress "PeerHTTPAddress"
$source=(Resolve-Path -LiteralPath $SourceDir).Path
$policy=$null
if($BrokerPolicyPath){$policy=(Resolve-Path -LiteralPath $BrokerPolicyPath).Path}
foreach($name in @('steward.exe','steward-broker.exe','steward-approval.exe','steward-companion.exe','steward-system-tool-host.exe','ui\index.html')) {
  if(-not (Test-Path -LiteralPath (Join-Path $source $name))){throw "release is missing $name"}
}
if(Get-Service -Name $ServiceName -ErrorAction SilentlyContinue){throw "service already exists: $ServiceName"}
if(Get-Service -Name $BrokerServiceName -ErrorAction SilentlyContinue){throw "service already exists: $BrokerServiceName"}

$orphanedBrokerData=Move-OrphanedBrokerData $BrokerDataDir 'orphaned'
$createdMain=$false; $createdBroker=$false; $companionInstalled=$false
try {
  New-Item -ItemType Directory -Force -Path $InstallDir,$DataDir,(Join-Path $DataDir 'data'),(Join-Path $DataDir 'logs') | Out-Null
  Protect-AdminPath $InstallDir; Protect-AdminPath $DataDir
  Copy-Item -Path (Join-Path $source '*') -Destination $InstallDir -Recurse -Force

  $brokerExe=Join-Path $InstallDir 'steward-broker.exe'
  if(-not $policy){
    $bootstrapDir=Join-Path $DataDir 'broker-bootstrap'
    $bootstrapArgs=@('bootstrap','--output-dir',$bootstrapDir,'--system-tool-host',(Join-Path $InstallDir 'steward-system-tool-host.exe'))
    if(Test-Path -LiteralPath $bootstrapDir){$bootstrapArgs+='--force'}
    $bootstrapRaw=& $brokerExe @bootstrapArgs 2>&1|Out-String
    if($LASTEXITCODE -ne 0){throw "Broker bootstrap failed: $bootstrapRaw"}
    Protect-AdminPath $bootstrapDir
    $bootstrap=$bootstrapRaw|ConvertFrom-Json
    $policy=$bootstrap.policy
    $brokerSecrets=Get-Content -LiteralPath $bootstrap.broker_secrets -Raw|ConvertFrom-Json
    $stewardClient=Get-Content -LiteralPath $bootstrap.steward_client -Raw|ConvertFrom-Json
    $keys=[pscustomobject]@{keys=[pscustomobject]@{client_key=$brokerSecrets.STEWARD_BROKER_CLIENT_KEY;control_key=$brokerSecrets.STEWARD_BROKER_CONTROL_KEY;signing_private_key=$brokerSecrets.STEWARD_BROKER_SIGNING_PRIVATE_KEY};steward_env=$stewardClient}
  }else{
    $keysRaw=& $brokerExe keygen 2>&1 | Out-String
    if($LASTEXITCODE -ne 0){throw "Broker key generation failed: $keysRaw"}
    $keys=$keysRaw|ConvertFrom-Json
  }
  $localKey=$LocalEncryptionKey
  if(-not $localKey){$localKey=New-Key 32}
  $syncSecretValue=$SyncSecret
  if(-not $syncSecretValue){$syncSecretValue=New-Key 32}

  $brokerArgs=@('service','install','--name',$BrokerServiceName,'--scope','system','--policy',$policy,
    '--install-dir',$BrokerInstallDir,'--workdir',$BrokerDataDir,
    '--private-environment-file',(Join-Path $BrokerDataDir 'service-secrets.json'),
    '--state',(Join-Path $BrokerDataDir 'state.json'),'--audit',(Join-Path $BrokerDataDir 'audit.jsonl'),
    '--checkpoint',(Join-Path $BrokerDataDir 'checkpoint.json'),
    '--client-key',$keys.keys.client_key,'--control-key',$keys.keys.control_key,
    '--signing-private-key',$keys.keys.signing_private_key,'--device-id',$AgentID)
  if($Start){$brokerArgs+='--start'}
  $brokerOutput=& $brokerExe @brokerArgs 2>&1 | Out-String
  if($LASTEXITCODE -ne 0){$createdBroker=$null -ne (Get-Service $BrokerServiceName -ErrorAction SilentlyContinue);throw "Broker installation failed: $brokerOutput"}
  $createdBroker=$true
	if($bootstrapDir -and (Test-Path -LiteralPath $bootstrapDir)){
	  [System.IO.Directory]::Delete($bootstrapDir,$true)
	}

  $stewardExe=Join-Path $source 'steward.exe'
  $privateEnvironmentFile=Join-Path $DataDir 'config\service-secrets.json'
  $serviceArgs=@('service','install','--name',$ServiceName,'--scope','system','--binary',$stewardExe,
    '--workdir',$DataDir,'--http-addr',$HTTPAddress,'--peer-http-addr',$PeerHTTPAddress,
    '--database-url',$DatabaseURL,'--storage-dir',(Join-Path $DataDir 'data'),'--log-dir',(Join-Path $DataDir 'logs'),
    '--ui-dir',(Join-Path $InstallDir 'ui'),'--agent-id',$AgentID,'--sync-secret',$syncSecretValue,
    '--local-encryption-key',$localKey,'--runtime-v2','--runtime-r2','--runtime-r3',
    '--broker-url','http://127.0.0.1:18100','--broker-client-key',$keys.steward_env.STEWARD_BROKER_CLIENT_KEY,
    '--broker-public-key',$keys.steward_env.STEWARD_BROKER_PUBLIC_KEY,
    '--windows-hardened','--windows-install-dir',$InstallDir,'--windows-private-environment-file',$privateEnvironmentFile,
    '--windows-service-account','localservice','--windows-service-sid-type','restricted')
  if($DevicePrivateKey){$serviceArgs+=@('--device-private-key',$DevicePrivateKey)}
  if($DevicePublicKey){$serviceArgs+=@('--device-public-key',$DevicePublicKey)}
  if($SyncEncryptionKey){$serviceArgs+=@('--sync-encryption-key',$SyncEncryptionKey)}
  if($SyncEncryptionKeyID){$serviceArgs+=@('--sync-encryption-key-id',$SyncEncryptionKeyID)}
  if($SyncEncryptionPreviousKeys){$serviceArgs+=@('--sync-encryption-previous-keys',$SyncEncryptionPreviousKeys)}
  if($LocalEncryptionKeyID){$serviceArgs+=@('--local-encryption-key-id',$LocalEncryptionKeyID)}
  if($LocalEncryptionPreviousKeys){$serviceArgs+=@('--local-encryption-previous-keys',$LocalEncryptionPreviousKeys)}
  if($LLMBaseURL){$serviceArgs+=@('--llm-provider','openai-compatible','--llm-base-url',$LLMBaseURL)}
  if($LLMModel){$serviceArgs+=@('--llm-model',$LLMModel)}
  if($LLMAPIKey){$serviceArgs+=@('--llm-api-key',$LLMAPIKey)}
  if($RecoverModelSettingsFromEnvironment){$serviceArgs+='--recover-model-settings-from-env'}
  if($Start){$serviceArgs+='--start'}
  $mainOutput=& $stewardExe @serviceArgs 2>&1 | Out-String
  if($LASTEXITCODE -ne 0){$createdMain=$null -ne (Get-Service $ServiceName -ErrorAction SilentlyContinue);throw "main service installation failed: $mainOutput"}
  $createdMain=$true
  Grant-RestrictedCapabilityHostReadExecute (Join-Path $InstallDir 'steward-system-tool-host.exe')

  if($InstallCompanion){
    & (Join-Path $source 'install-steward-companion.ps1') -SourceDir $source -LocalEncryptionKey $localKey -ServiceName $ServiceName -Start:$Start | Out-Host
    if($LASTEXITCODE -ne 0){throw "Session Companion installation failed"}
    $companionInstalled=$true
  }
  [IO.File]::WriteAllText((Join-Path $DataDir 'installation.json'),(@{ service_name=$ServiceName; broker_service_name=$BrokerServiceName; installed_at=(Get-Date).ToUniversalTime().ToString('o'); quarantined_broker_data=$orphanedBrokerData }|ConvertTo-Json),[Text.UTF8Encoding]::new($false))
  if($Verify){
    if(-not $Start){throw '-Verify requires -Start'}
    & (Join-Path $source 'test-steward-production.ps1') -ServiceName $ServiceName -BrokerServiceName $BrokerServiceName -RequireCompanion:$InstallCompanion | Out-Host
    if($LASTEXITCODE -ne 0){throw "production verification failed"}
  }
  if($RecoverModelSettingsFromEnvironment -and $Start){
    # The running process already consumed the explicit recovery marker. Keep
    # future restarts fail-closed if the encrypted database value is damaged.
    Set-ServicePublicEnvironmentValue $ServiceName 'STEWARD_MODEL_SETTINGS_KEY_RECOVERY' 'false'
  }
  [ordered]@{ok=$true;service=$ServiceName;service_account='NT AUTHORITY\LocalService';service_sid='restricted';broker=$BrokerServiceName;broker_account='LocalSystem';companion=$companionInstalled;install_dir=$InstallDir;data_dir=$DataDir}|ConvertTo-Json
} catch {
  $failure=$_.Exception.Message
  if($companionInstalled){& (Join-Path $source 'uninstall-steward-companion.ps1') -ErrorAction SilentlyContinue|Out-Null}
  if($createdMain){& (Join-Path $InstallDir 'steward.exe') service stop --name $ServiceName 2>$null|Out-Null;& (Join-Path $InstallDir 'steward.exe') service uninstall --name $ServiceName 2>$null|Out-Null}
  if($createdBroker){& (Join-Path $InstallDir 'steward-broker.exe') service stop --name $BrokerServiceName 2>$null|Out-Null;& (Join-Path $InstallDir 'steward-broker.exe') service uninstall --name $BrokerServiceName 2>$null|Out-Null}
  try{Move-OrphanedBrokerData $BrokerDataDir 'failed-install'|Out-Null}catch{}
  throw "R5.1 installation failed and created services were rolled back: $failure"
}
