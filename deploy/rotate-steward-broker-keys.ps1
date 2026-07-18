[CmdletBinding()]
param(
  [string]$InstallDir='C:\Program Files\MongojsonSteward',
  [string]$MainDataDir='C:\ProgramData\MongojsonSteward',
  [string]$BrokerDataDir='C:\ProgramData\MongoJSON\StewardBroker',
  [string]$ServiceName='MongojsonSteward',
  [string]$BrokerServiceName='MongojsonStewardBroker',
  [string]$HealthURL='http://127.0.0.1:18080/healthz',
  [string]$ReadinessURL='http://127.0.0.1:18080/readyz',
  [int]$ReadinessTimeoutSeconds=60,
  [int]$RetryIntervalMilliseconds=500
)
$ErrorActionPreference='Stop'
function Test-Administrator{$p=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent());$p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)}
function Write-JsonAtomic([string]$Path,$Value){
  $tmp="$Path.tmp-$([guid]::NewGuid().ToString('N'))"
  $acl=Get-Acl -LiteralPath $Path
  try{
    [IO.File]::WriteAllText($tmp,($Value|ConvertTo-Json -Depth 20),[Text.UTF8Encoding]::new($false))
    Set-Acl -LiteralPath $tmp -AclObject $acl
    Move-Item -LiteralPath $tmp -Destination $Path -Force
  }finally{if(Test-Path -LiteralPath $tmp){Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue}}
}
function Wait-ServiceRunning([string]$Name,[datetime]$Deadline) {
  $last='missing'
  while((Get-Date) -lt $Deadline){
    $service=Get-Service $Name -ErrorAction SilentlyContinue
    if($service -and $service.Status -eq 'Running'){return}
    if($service){$last=[string]$service.Status}
    Start-Sleep -Milliseconds $RetryIntervalMilliseconds
  }
  throw "service did not become Running: $Name (last state: $last)"
}
function Wait-StewardEndpoints([datetime]$Deadline) {
  $lastHealth='';$lastReadiness=''
  while((Get-Date) -lt $Deadline){
    try{$health=Invoke-RestMethod -Uri $HealthURL -TimeoutSec 5;if($health.status -ne 'ok'){$lastHealth="status=$($health.status)"}else{$lastHealth='ok'}}catch{$lastHealth=$_.Exception.Message}
    try{$ready=Invoke-RestMethod -Uri $ReadinessURL -TimeoutSec 5;if($ready.status -ne 'ready'){$lastReadiness="status=$($ready.status)"}else{$lastReadiness='ready'}}catch{$lastReadiness=$_.Exception.Message}
    if($lastHealth -eq 'ok' -and $lastReadiness -eq 'ready'){return}
    Start-Sleep -Milliseconds $RetryIntervalMilliseconds
  }
  throw "Steward did not become healthy and ready within ${ReadinessTimeoutSeconds}s (health: $lastHealth; readiness: $lastReadiness)"
}
function Start-And-ProveServices {
  Start-Service $BrokerServiceName -ErrorAction Stop
  Start-Service $ServiceName -ErrorAction Stop
  $deadline=(Get-Date).AddSeconds($ReadinessTimeoutSeconds)
  Wait-ServiceRunning $BrokerServiceName $deadline
  Wait-ServiceRunning $ServiceName $deadline
  Wait-StewardEndpoints $deadline
}

if(-not(Test-Administrator)){throw 'Run key rotation from an elevated PowerShell session.'}
if($ReadinessTimeoutSeconds -lt 1){throw 'ReadinessTimeoutSeconds must be at least 1'}
if($RetryIntervalMilliseconds -lt 50){throw 'RetryIntervalMilliseconds must be at least 50'}
$brokerExe=Join-Path $InstallDir 'steward-broker.exe'
$brokerSecretPath=Join-Path $BrokerDataDir 'service-secrets.json'
$mainSecretPath=Join-Path $MainDataDir 'config\service-secrets.json'
foreach($path in @($brokerExe,$brokerSecretPath,$mainSecretPath)){if(-not(Test-Path -LiteralPath $path -PathType Leaf)){throw "required rotation input is missing: $path"}}
$stamp=(Get-Date -Format yyyyMMdd-HHmmss)+'-'+[guid]::NewGuid().ToString('N').Substring(0,8)
$brokerBackup="$brokerSecretPath.backup-$stamp";$mainBackup="$mainSecretPath.backup-$stamp"
Copy-Item $brokerSecretPath $brokerBackup -Force;Copy-Item $mainSecretPath $mainBackup -Force
Set-Acl -LiteralPath $brokerBackup -AclObject (Get-Acl -LiteralPath $brokerSecretPath)
Set-Acl -LiteralPath $mainBackup -AclObject (Get-Acl -LiteralPath $mainSecretPath)
try{
  $newKeysRaw=& $brokerExe keygen 2>&1|Out-String;if($LASTEXITCODE -ne 0){throw "key generation failed: $newKeysRaw"}
  $newKeys=$newKeysRaw|ConvertFrom-Json
  if([string]::IsNullOrWhiteSpace([string]$newKeys.keys.client_key) -or [string]::IsNullOrWhiteSpace([string]$newKeys.keys.control_key)){throw 'key generation returned incomplete client/control keys'}
  $brokerSecrets=Get-Content $brokerSecretPath -Raw|ConvertFrom-Json
  $mainSecrets=Get-Content $mainSecretPath -Raw|ConvertFrom-Json
  # Rotate request and independent resume authentication. The long-lived
  # signing identity remains pinned so checkpoint/audit continuity is kept.
  $brokerSecrets.STEWARD_BROKER_CLIENT_KEY=$newKeys.keys.client_key
  $brokerSecrets.STEWARD_BROKER_CONTROL_KEY=$newKeys.keys.control_key
  $mainSecrets.STEWARD_BROKER_CLIENT_KEY=$newKeys.keys.client_key
  Stop-Service $ServiceName -Force -ErrorAction Stop;Stop-Service $BrokerServiceName -Force -ErrorAction Stop
  Write-JsonAtomic $brokerSecretPath $brokerSecrets;Write-JsonAtomic $mainSecretPath $mainSecrets
  Start-And-ProveServices
  [ordered]@{ok=$true;rotated=@('client_authentication','independent_resume_control');signing_identity_preserved=$true;health_proven=$true;readiness_proven=$true;backups=@($brokerBackup,$mainBackup)}|ConvertTo-Json
}catch{
  $reason=$_.Exception.Message;$rollbackErrors=New-Object System.Collections.Generic.List[string]
  Stop-Service $ServiceName,$BrokerServiceName -Force -ErrorAction SilentlyContinue
  try{Copy-Item $brokerBackup $brokerSecretPath -Force;Set-Acl -LiteralPath $brokerSecretPath -AclObject (Get-Acl -LiteralPath $brokerBackup)}catch{$rollbackErrors.Add("restore Broker secrets: $($_.Exception.Message)")}
  try{Copy-Item $mainBackup $mainSecretPath -Force;Set-Acl -LiteralPath $mainSecretPath -AclObject (Get-Acl -LiteralPath $mainBackup)}catch{$rollbackErrors.Add("restore main secrets: $($_.Exception.Message)")}
  if($rollbackErrors.Count -eq 0){try{Start-And-ProveServices}catch{$rollbackErrors.Add("prove restored services: $($_.Exception.Message)")}}
  if($rollbackErrors.Count -gt 0){throw "Broker key rotation failed and rollback health could not be proven: $reason. Rollback errors: $($rollbackErrors -join '; ')"}
  throw "Broker key rotation failed; previous keys were restored and both services are healthy and ready: $reason"
}
