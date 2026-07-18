[CmdletBinding()]
param(
  [string]$ServiceName = "MongojsonSteward",
  [string]$BrokerServiceName = "MongojsonStewardBroker",
  [string]$CompanionTaskName = "MongojsonStewardCompanion",
  [string]$HealthURL = "http://127.0.0.1:18080/healthz",
  [string]$BrokerURL = "http://127.0.0.1:18100/v1/status",
  [string]$InstallDir = "C:\Program Files\MongojsonSteward",
  [string]$MainDataDir = "C:\ProgramData\MongojsonSteward",
  [string]$BrokerDataDir = "C:\ProgramData\MongoJSON\StewardBroker",
  [ValidateRange(1,600)][int]$StartupTimeoutSeconds = 60,
  [switch]$RequireCompanion
)

$ErrorActionPreference = "Stop"
$checks = [Collections.Generic.List[object]]::new()
function Add-Check([string]$Name, [bool]$OK, [string]$Detail) {
  $checks.Add([pscustomobject]@{ name=$Name; ok=$OK; detail=$Detail })
  if (-not $OK) { $script:failed = $true }
}
function Read-EnvironmentMap([object[]]$Entries) {
  $result=@{}
  foreach($entry in @($Entries)){
    if([string]$entry -match '^([^=]+)=(.*)$'){$result[$matches[1]]=$matches[2]}
  }
  return $result
}

$main = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
Add-Check "main.service" ($null -ne $main -and $main.State -eq "Running") $(if($main){"state=$($main.State)"}else{"missing"})
Add-Check "main.account" ($null -ne $main -and $main.StartName -eq "NT AUTHORITY\LocalService") $(if($main){"account=$($main.StartName)"}else{"missing"})
$sidOutput = (& sc.exe qsidtype $ServiceName 2>&1 | Out-String)
Add-Check "main.restricted_sid" ($LASTEXITCODE -eq 0 -and $sidOutput -match "RESTRICTED") $sidOutput.Trim()

$broker = Get-CimInstance Win32_Service -Filter "Name='$BrokerServiceName'" -ErrorAction SilentlyContinue
Add-Check "broker.service" ($null -ne $broker -and $broker.State -eq "Running") $(if($broker){"state=$($broker.State)"}else{"missing"})
Add-Check "broker.account" ($null -ne $broker -and $broker.StartName -eq "LocalSystem") $(if($broker){"account=$($broker.StartName)"}else{"missing"})

$health=$null;$healthError='';$healthDeadline=(Get-Date).AddSeconds($StartupTimeoutSeconds)
do {
  try {
    $health=Invoke-RestMethod -Uri $HealthURL -TimeoutSec 5
    if($health.status -eq 'ok'){break}
    $healthError="unexpected health response: $($health|ConvertTo-Json -Compress)"
  } catch {$healthError=$_.Exception.Message}
  Start-Sleep -Milliseconds 500
} while((Get-Date) -lt $healthDeadline)
$healthOK=$null -ne $health -and $health.status -eq 'ok'
$healthDetail=if($healthOK){$health|ConvertTo-Json -Compress}else{"service did not become healthy within ${StartupTimeoutSeconds}s: $healthError"}
if(-not $healthOK){
  $serviceLog=Join-Path (Join-Path $MainDataDir 'logs') ($ServiceName+'.log')
  if(Test-Path -LiteralPath $serviceLog){
    $tail=(Get-Content -LiteralPath $serviceLog -Tail 30 -ErrorAction SilentlyContinue|Out-String).Trim()
    if($tail){$healthDetail+="; recent service log: $tail"}
  }
}
Add-Check "main.health" $healthOK $healthDetail
$mainAfterStartup=Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
Add-Check 'main.running_after_startup' ($null -ne $mainAfterStartup -and $mainAfterStartup.State -eq 'Running') $(if($mainAfterStartup){"state=$($mainAfterStartup.State); exit_code=$($mainAfterStartup.ExitCode)"}else{'missing'})
$mainListener = Get-NetTCPConnection -LocalPort 18080 -State Listen -ErrorAction SilentlyContinue
Add-Check "main.loopback" ($null -ne $mainListener -and @($mainListener | Where-Object LocalAddress -notin @("127.0.0.1","::1")).Count -eq 0) (($mainListener | Select-Object LocalAddress,LocalPort | ConvertTo-Json -Compress))
$brokerListener = Get-NetTCPConnection -LocalPort 18100 -State Listen -ErrorAction SilentlyContinue
Add-Check "broker.loopback" ($null -ne $brokerListener -and @($brokerListener | Where-Object LocalAddress -notin @("127.0.0.1","::1")).Count -eq 0) (($brokerListener | Select-Object LocalAddress,LocalPort | ConvertTo-Json -Compress))

$mainPrivate=Join-Path $MainDataDir 'config\service-secrets.json';$brokerPrivate=Join-Path $BrokerDataDir 'service-secrets.json';$policy=Join-Path $BrokerDataDir 'policy.json'
Add-Check 'main.private_environment' (Test-Path $mainPrivate) $mainPrivate
Add-Check 'broker.private_environment' (Test-Path $brokerPrivate) $brokerPrivate
if(Test-Path $brokerPrivate){
  try{
    $brokerSID=([Security.Principal.NTAccount]::new("NT SERVICE\$BrokerServiceName")).Translate([Security.Principal.SecurityIdentifier]).Value
    $acl=Get-Acl -LiteralPath $brokerPrivate
    $allowSIDs=@($acl.Access|Where-Object AccessControlType -eq 'Allow'|ForEach-Object{
      try{$_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value}catch{[string]$_.IdentityReference}
    })
    $isolated=($allowSIDs -contains $brokerSID) -and ($allowSIDs -notcontains 'S-1-5-18')
    Add-Check 'broker.service_sid_secret_acl' $isolated "allow_sids=$($allowSIDs -join ',')"
  }catch{Add-Check 'broker.service_sid_secret_acl' $false $_.Exception.Message}
}
$registryEnvironment=@((Get-ItemProperty -LiteralPath "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName" -Name Environment -ErrorAction SilentlyContinue).Environment)
$publicEnvironment=Read-EnvironmentMap $registryEnvironment
$leaked=@($registryEnvironment|Where-Object{$_ -match '(?i)(DATABASE_URL|API_KEY|SECRET|ENCRYPTION_KEY|BROKER_CLIENT_KEY)='})
Add-Check 'main.scm_secrets_absent' ($leaked.Count -eq 0) $(if($leaked.Count){'sensitive keys remain in SCM Environment'}else{'SCM Environment contains no private keys'})
$systemHost=Join-Path $InstallDir 'steward-system-tool-host.exe';$brokerCLI=Join-Path $InstallDir 'steward-broker.exe'
Add-Check 'broker.system_tool_host' (Test-Path $systemHost) $systemHost
if((Test-Path $systemHost) -and (Test-Path $policy)){
  try{$catalog=& $systemHost catalog|ConvertFrom-Json;Add-Check 'broker.system_tool_catalog' ($catalog.protocol -eq 'steward-system-tool-catalog/1' -and $catalog.tools.Count -gt 0) "tools=$($catalog.tools.Count)"}catch{Add-Check 'broker.system_tool_catalog' $false $_.Exception.Message}
  try{$validated=& $brokerCLI validate-policy --policy $policy|ConvertFrom-Json;$parameterized=@($validated.capabilities|Where-Object accepts_input);Add-Check 'broker.parameterized_policy' ($validated.valid -and $parameterized.Count -gt 0) "parameterized_capabilities=$($parameterized.Count)"}catch{Add-Check 'broker.parameterized_policy' $false $_.Exception.Message}
}
if((Test-Path $brokerPrivate) -and (Test-Path $mainPrivate) -and (Test-Path $brokerCLI)){
  $oldClient=$env:STEWARD_BROKER_CLIENT_KEY;$oldPublic=$env:STEWARD_BROKER_PUBLIC_KEY;$oldURL=$env:STEWARD_BROKER_URL
  try{
    $bs=Get-Content $brokerPrivate -Raw|ConvertFrom-Json
    $brokerPublicKey=[string]$publicEnvironment['STEWARD_BROKER_PUBLIC_KEY']
    if($brokerPublicKey -notmatch '^[A-Za-z0-9+/]{43}=$'){throw 'STEWARD_BROKER_PUBLIC_KEY is missing or invalid in the main service SCM Environment'}
    $env:STEWARD_BROKER_CLIENT_KEY=$bs.STEWARD_BROKER_CLIENT_KEY;$env:STEWARD_BROKER_PUBLIC_KEY=$brokerPublicKey;$env:STEWARD_BROKER_URL='http://127.0.0.1:18100'
    $smoke=& $brokerCLI tool-execute --capability tool:system.uptime --arguments-json '{}' 2>&1|Out-String
    $smokeExit=$LASTEXITCODE
    if($smokeExit -ne 0){
      Add-Check 'broker.system_tool_execution' $false "Broker CLI failed with exit code ${smokeExit}: $($smoke.Trim())"
    }else{
      $brokerResult=$smoke|ConvertFrom-Json
      $toolResult=([string]$brokerResult.stdout)|ConvertFrom-Json
      $receipt=$brokerResult.receipt.payload
      $hasUptime=$null -ne $toolResult.output -and $null -ne $toolResult.output.PSObject.Properties['uptime_seconds']
      $receiptOK=$null -ne $receipt -and $receipt.succeeded -eq $true -and $receipt.audit_persisted -eq $true -and $receipt.capability -eq 'tool:system.uptime' -and -not [string]::IsNullOrWhiteSpace([string]$brokerResult.receipt.key_id) -and -not [string]::IsNullOrWhiteSpace([string]$brokerResult.receipt.signature)
      $toolOK=$toolResult.ok -eq $true -and $hasUptime
      if($receiptOK -and $toolOK){
        Add-Check 'broker.system_tool_execution' $true "system.uptime returned a verified signed Broker receipt and uptime_seconds=$($toolResult.output.uptime_seconds)"
      }else{
        $reason=if($toolResult.ok -ne $true){"tool host error: $($toolResult.error)"}elseif(-not $hasUptime){'tool host response omitted output.uptime_seconds'}else{'signed receipt fields did not match the requested successful audited capability'}
        Add-Check 'broker.system_tool_execution' $false $reason
      }
    }
  }catch{Add-Check 'broker.system_tool_execution' $false $_.Exception.Message}
  finally{$env:STEWARD_BROKER_CLIENT_KEY=$oldClient;$env:STEWARD_BROKER_PUBLIC_KEY=$oldPublic;$env:STEWARD_BROKER_URL=$oldURL}
}

$task = Get-ScheduledTask -TaskName $CompanionTaskName -ErrorAction SilentlyContinue
$companionOK = $null -ne $task -and $task.Principal.RunLevel -eq "Limited"
Add-Check "companion.task" ($companionOK -or -not $RequireCompanion) $(if($task){"state=$($task.State); run_level=$($task.Principal.RunLevel)"}else{"missing"})

$result = [ordered]@{ ok=(-not $failed); checked_at=(Get-Date).ToUniversalTime().ToString("o"); checks=@($checks) }
$result | ConvertTo-Json -Depth 6
if ($failed) { exit 1 }
