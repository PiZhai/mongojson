param(
    [string]$DatabaseUrl = "postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable",
    [string]$PostgresContainer = "mongojson-steward-r3-postgres",
    [int]$OriginManagementPort = 18186,
    [int]$OriginPeerPort = 18187,
    [int]$TargetManagementPort = 18196,
    [int]$TargetPeerPort = 18197
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true
$backendRoot = Split-Path $PSScriptRoot -Parent
$temp = Join-Path $env:TEMP ("steward-r43-validation-" + [guid]::NewGuid().ToString("N"))
$suffix = [guid]::NewGuid().ToString("N").Substring(0, 10)
$originDB = "steward_r43_origin_$suffix"
$targetDB = "steward_r43_target_$suffix"
$originProcess = $null
$targetProcess = $null
$managedEnvironment = @(
    "DATABASE_URL","STORAGE_DIR","HTTP_ADDR","STEWARD_PEER_HTTP_ADDR","STEWARD_PUBLIC_API_BASE",
    "STEWARD_AGENT_ID","STEWARD_DEVICE_PUBLIC_KEY","STEWARD_DEVICE_PRIVATE_KEY","STEWARD_SYNC_REQUIRE_AUTH",
    "STEWARD_SYNC_ALLOW_INSECURE","STEWARD_RUNTIME_V2","STEWARD_ORCHESTRATION_R4","STEWARD_ORCHESTRATION_REMOTE",
    "STEWARD_ORCHESTRATION_SIGNING_KEY","STEWARD_REMOTE_EXECUTION_LEASE","STEWARD_REMOTE_EXECUTION_TOOLS",
    "STEWARD_RUNTIME_INTERVAL","STEWARD_RUNTIME_LIMIT","STEWARD_SYNC_INTERVAL","STEWARD_AUTONOMY_INTERVAL"
)
$originalEnvironment = @{}
foreach ($name in $managedEnvironment) { $originalEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process") }

function Database-Url([string]$name) {
    return ($DatabaseUrl -replace '/[^/?]+\?', ("/{0}?" -f $name))
}

function Invoke-Json([string]$base, [string]$method, [string]$path, [object]$body = $null) {
    $args = @{ Method=$method; Uri=($base+$path); SkipHttpErrorCheck=$true }
    if ($null -ne $body) {
        $args.ContentType = "application/json"
        $args.Body = ($body | ConvertTo-Json -Depth 40)
    }
    $response = Invoke-WebRequest @args
    if ($response.StatusCode -ge 400) { throw "$method $path failed with HTTP $($response.StatusCode): $($response.Content)" }
    return ($response.Content | ConvertFrom-Json)
}

function Wait-Ready([int]$port) {
    for ($i=0; $i -lt 160; $i++) {
        try { Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/readyz" -f $port) -TimeoutSec 2 | Out-Null; return } catch {}
        Start-Sleep -Milliseconds 250
    }
    throw "server on port $port did not become ready"
}

function Start-Node([string]$binary, [string]$name, [string]$database, [int]$managementPort, [int]$peerPort, [object]$keys) {
    $env:DATABASE_URL = Database-Url $database
    $env:STORAGE_DIR = Join-Path $temp ("storage-"+$name)
    New-Item -ItemType Directory -Path $env:STORAGE_DIR -Force | Out-Null
    $env:HTTP_ADDR = "127.0.0.1:$managementPort"
    $env:STEWARD_PEER_HTTP_ADDR = "127.0.0.1:$peerPort"
    $env:STEWARD_PUBLIC_API_BASE = "http://127.0.0.1:$peerPort/api"
    $env:STEWARD_AGENT_ID = $name
    $env:STEWARD_DEVICE_PUBLIC_KEY = $keys.public_key
    $env:STEWARD_DEVICE_PRIVATE_KEY = $keys.private_key
    $env:STEWARD_SYNC_REQUIRE_AUTH = "true"
    $env:STEWARD_SYNC_ALLOW_INSECURE = "false"
    $env:STEWARD_RUNTIME_V2 = "true"
    $env:STEWARD_ORCHESTRATION_R4 = "true"
    $env:STEWARD_ORCHESTRATION_REMOTE = "true"
    $env:STEWARD_REMOTE_EXECUTION_LEASE = "6s"
    $env:STEWARD_REMOTE_EXECUTION_TOOLS = "runtime.echo"
    $env:STEWARD_RUNTIME_INTERVAL = $(if ($name -eq "r43-target") { "2s" } else { "100ms" })
    $env:STEWARD_RUNTIME_LIMIT = "10"
    $env:STEWARD_SYNC_INTERVAL = "0s"
    $env:STEWARD_AUTONOMY_INTERVAL = "0s"
    return Start-Process -FilePath $binary -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $temp ($name+".out.log")) `
        -RedirectStandardError (Join-Path $temp ($name+".err.log"))
}

function Stop-Node([object]$process) {
    if ($null -ne $process -and -not $process.HasExited) {
        Stop-Process -Id $process.Id -Force
        $process.WaitForExit()
    }
}

try {
    New-Item -ItemType Directory -Path $temp | Out-Null
    $serverBinary = Join-Path $temp "server.exe"
    $stewardBinary = Join-Path $temp "steward.exe"
    Push-Location $backendRoot
    try {
        go build -buildvcs=false -o $serverBinary ./cmd/server
        go build -buildvcs=false -o $stewardBinary ./cmd/steward
    } finally { Pop-Location }

    docker exec $PostgresContainer psql -U postgres -d postgres -v ON_ERROR_STOP=1 -c "create database $originDB" | Out-Null
    docker exec $PostgresContainer psql -U postgres -d postgres -v ON_ERROR_STOP=1 -c "create database $targetDB" | Out-Null
    $originKeys = (& $stewardBinary keygen --prefix r43-origin | ConvertFrom-Json)
    $targetKeys = (& $stewardBinary keygen --prefix r43-target | ConvertFrom-Json)
    $seed = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($seed)
    $env:STEWARD_ORCHESTRATION_SIGNING_KEY = [Convert]::ToBase64String($seed)

    $originProcess = Start-Node $serverBinary "r43-origin" $originDB $OriginManagementPort $OriginPeerPort $originKeys
    Wait-Ready $OriginManagementPort
    $targetProcess = Start-Node $serverBinary "r43-target" $targetDB $TargetManagementPort $TargetPeerPort $targetKeys
    Wait-Ready $TargetManagementPort
    $originAPI = "http://127.0.0.1:$OriginManagementPort/api"
    $targetAPI = "http://127.0.0.1:$TargetManagementPort/api"
    $enabled = $true
    Invoke-Json $originAPI "POST" "/steward/devices" @{
        id="r43-target";device_name="R43 Target";platform="windows";role="peer";sync_enabled=$enabled
        permission_level="A2";public_key=$targetKeys.public_key;api_base_url="http://127.0.0.1:$TargetPeerPort/api"
    } | Out-Null
    Invoke-Json $targetAPI "POST" "/steward/devices" @{
        id="r43-origin";device_name="R43 Origin";platform="windows";role="peer";sync_enabled=$enabled
        permission_level="A2";public_key=$originKeys.public_key;api_base_url="http://127.0.0.1:$OriginPeerPort/api"
    } | Out-Null
    Invoke-Json $originAPI "PUT" "/steward/orchestration/agents" @{
        id="r43-agent";name="R43 Remote Agent";role="remote low privilege";permission_ceiling="A2";data_level_ceiling="D2"
        tool_allowlist=@("runtime.echo");max_concurrency=1;max_runtime_seconds=900;max_attempts=20;max_evidence_bytes=262144
    } | Out-Null

    $created = (Invoke-Json $originAPI "POST" "/steward/orchestrations" @{
        goal="R4.3 real cross-process remote execution";auto_start=$true;permission_ceiling="A2";data_level="D2"
        nodes=@(@{key="remote";agent_id="r43-agent";goal="execute on selected peer";target_device="auto";
            steps=@(@{key="echo";tool_name="runtime.echo";arguments=@{value="remote-proof"}})})
    }).orchestration
    $accepted = $null
    for ($i=0; $i -lt 200; $i++) {
        $accepted = (Invoke-Json $originAPI "GET" ("/steward/orchestrations/{0}" -f $created.id)).orchestration
        if ($accepted.nodes[0].remote_dispatch.status -in @("accepted","running")) { break }
        Start-Sleep -Milliseconds 100
    }
    if ($accepted.nodes[0].selected_device_id -ne "r43-target" -or $null -eq $accepted.nodes[0].remote_dispatch.heartbeat_at) {
        throw "origin did not receive a signed target heartbeat"
    }
    Stop-Node $targetProcess
    $targetProcess = $null
    Start-Sleep -Seconds 3
    $offline = (Invoke-Json $originAPI "GET" ("/steward/orchestrations/{0}" -f $created.id)).orchestration
    $targetProcess = Start-Node $serverBinary "r43-target" $targetDB $TargetManagementPort $TargetPeerPort $targetKeys
    Wait-Ready $TargetManagementPort
    $completed = $null
    for ($i=0; $i -lt 300; $i++) {
        $completed = (Invoke-Json $originAPI "GET" ("/steward/orchestrations/{0}" -f $created.id)).orchestration
        if ($completed.status -in @("succeeded","failed","blocked","cancelled")) { break }
        Start-Sleep -Milliseconds 200
    }
    $dispatch = $completed.nodes[0].remote_dispatch
    if ($completed.status -ne "succeeded" -or $dispatch.status -ne "succeeded" -or [string]::IsNullOrWhiteSpace($dispatch.result_signature)) {
        throw "remote result was not signed, verified and reconciled"
    }
    [ordered]@{
        orchestration_id=$completed.id
        status=$completed.status
        selected_device_id=$completed.nodes[0].selected_device_id
        origin_process_id=$originProcess.Id
        target_process_id=$targetProcess.Id
        accepted_heartbeat=$accepted.nodes[0].remote_dispatch.heartbeat_at
        offline_status=$offline.nodes[0].remote_dispatch.status
        dispatch_attempts=$dispatch.attempt
        result_signature_verified= -not [string]::IsNullOrWhiteSpace($dispatch.result_signature)
        remote_run_id=$dispatch.remote_run_id
        evidence_artifacts=$completed.evidence.artifact_count
        evidence_manifest_sha256=$completed.evidence.manifest_sha256
    } | ConvertTo-Json -Depth 6
}
catch {
    foreach ($name in @("r43-origin","r43-target")) {
        $path = Join-Path $temp ($name+".err.log")
        if (Test-Path $path) { Write-Host "--- $path ---"; Get-Content $path -Tail 100 | Write-Host }
    }
    throw
}
finally {
    Stop-Node $targetProcess
    Stop-Node $originProcess
    foreach ($database in @($originDB,$targetDB)) {
        docker exec $PostgresContainer psql -U postgres -d postgres -c "select pg_terminate_backend(pid) from pg_stat_activity where datname='$database' and pid <> pg_backend_pid()" 2>$null | Out-Null
        docker exec $PostgresContainer psql -U postgres -d postgres -c "drop database if exists $database" 2>$null | Out-Null
    }
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    foreach ($name in $managedEnvironment) { [Environment]::SetEnvironmentVariable($name, $originalEnvironment[$name], "Process") }
}
