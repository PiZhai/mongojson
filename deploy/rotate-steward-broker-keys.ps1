[CmdletBinding()]
param(
  [string]$InstallDir='C:\Program Files\MongojsonSteward',
  [string]$MainDataDir='C:\ProgramData\MongojsonSteward',
  [string]$BrokerDataDir='C:\ProgramData\MongoJSON\StewardBroker',
  [string]$ServiceName='MongojsonSteward',
  [string]$BrokerServiceName='MongojsonStewardBroker',
  [string]$HealthURL='http://127.0.0.1:18080/healthz'
)
$ErrorActionPreference='Stop'
function Test-Administrator{$p=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent());$p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)}
function Write-JsonAtomic([string]$Path,$Value){$tmp="$Path.tmp";[IO.File]::WriteAllText($tmp,($Value|ConvertTo-Json -Depth 20),[Text.UTF8Encoding]::new($false));Move-Item $tmp $Path -Force}
if(-not(Test-Administrator)){throw 'Run key rotation from an elevated PowerShell session.'}
$brokerExe=Join-Path $InstallDir 'steward-broker.exe'
$brokerSecretPath=Join-Path $BrokerDataDir 'service-secrets.json'
$mainSecretPath=Join-Path $MainDataDir 'config\service-secrets.json'
foreach($path in @($brokerExe,$brokerSecretPath,$mainSecretPath)){if(-not(Test-Path -LiteralPath $path)){throw "required rotation input is missing: $path"}}
$stamp=Get-Date -Format yyyyMMdd-HHmmss
$brokerBackup="$brokerSecretPath.backup-$stamp";$mainBackup="$mainSecretPath.backup-$stamp"
Copy-Item $brokerSecretPath $brokerBackup -Force;Copy-Item $mainSecretPath $mainBackup -Force
try{
  $newKeysRaw=& $brokerExe keygen 2>&1|Out-String;if($LASTEXITCODE -ne 0){throw "key generation failed: $newKeysRaw"}
  $newKeys=$newKeysRaw|ConvertFrom-Json
  $brokerSecrets=Get-Content $brokerSecretPath -Raw|ConvertFrom-Json
  $mainSecrets=Get-Content $mainSecretPath -Raw|ConvertFrom-Json
  # Rotate request and independent resume authentication. The long-lived
  # signing identity remains pinned so checkpoint/audit continuity is kept.
  $brokerSecrets.STEWARD_BROKER_CLIENT_KEY=$newKeys.keys.client_key
  $brokerSecrets.STEWARD_BROKER_CONTROL_KEY=$newKeys.keys.control_key
  $mainSecrets.STEWARD_BROKER_CLIENT_KEY=$newKeys.keys.client_key
  Stop-Service $ServiceName -Force;Stop-Service $BrokerServiceName -Force
  Write-JsonAtomic $brokerSecretPath $brokerSecrets;Write-JsonAtomic $mainSecretPath $mainSecrets
  Start-Service $BrokerServiceName;Start-Service $ServiceName
  $health=Invoke-RestMethod -Uri $HealthURL -TimeoutSec 10
  if($health.status -ne 'ok'){throw 'main health endpoint did not return status=ok'}
  [ordered]@{ok=$true;rotated=@('client_authentication','independent_resume_control');signing_identity_preserved=$true;backups=@($brokerBackup,$mainBackup)}|ConvertTo-Json
}catch{
  $reason=$_.Exception.Message
  Stop-Service $ServiceName,$BrokerServiceName -Force -ErrorAction SilentlyContinue
  Copy-Item $brokerBackup $brokerSecretPath -Force;Copy-Item $mainBackup $mainSecretPath -Force
  Start-Service $BrokerServiceName -ErrorAction SilentlyContinue;Start-Service $ServiceName -ErrorAction SilentlyContinue
  throw "Broker key rotation failed and previous keys were restored: $reason"
}
