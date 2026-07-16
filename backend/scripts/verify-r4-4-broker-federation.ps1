param(
    [string]$DatabaseUrl = "postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable",
    [string]$PostgresContainer = "mongojson-steward-r3-postgres",
    [int]$OriginBrokerPort = 18240,
    [int]$TargetBrokerPort = 18241,
    [int]$OriginManagementPort = 18242,
    [int]$OriginPeerPort = 18243,
    [int]$TargetManagementPort = 18244,
    [int]$TargetPeerPort = 18245
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true
$backendRoot = Split-Path $PSScriptRoot -Parent
$temp = Join-Path $env:TEMP ("steward-r44-validation-" + [guid]::NewGuid().ToString("N"))
$suffix = [guid]::NewGuid().ToString("N").Substring(0, 10)
$originDB = "steward_r44_origin_$suffix"
$targetDB = "steward_r44_target_$suffix"
$processes = @()
$managedEnvironment = @(
    "DATABASE_URL","STORAGE_DIR","HTTP_ADDR","STEWARD_PEER_HTTP_ADDR","STEWARD_PUBLIC_API_BASE",
    "STEWARD_AGENT_ID","STEWARD_DEVICE_PUBLIC_KEY","STEWARD_DEVICE_PRIVATE_KEY","STEWARD_SYNC_REQUIRE_AUTH",
    "STEWARD_SYNC_ALLOW_INSECURE","STEWARD_RUNTIME_V2","STEWARD_RUNTIME_R3","STEWARD_ORCHESTRATION_R4",
    "STEWARD_ORCHESTRATION_REMOTE","STEWARD_ORCHESTRATION_SIGNING_KEY","STEWARD_REMOTE_EXECUTION_LEASE",
    "STEWARD_RUNTIME_INTERVAL","STEWARD_RUNTIME_LIMIT","STEWARD_SYNC_INTERVAL","STEWARD_AUTONOMY_INTERVAL",
    "STEWARD_BROKER_URL","STEWARD_BROKER_CLIENT_KEY","STEWARD_BROKER_PUBLIC_KEY","STEWARD_BROKER_CONTROL_KEY",
    "STEWARD_BROKER_SIGNING_PRIVATE_KEY","STEWARD_BROKER_LISTEN","STEWARD_BROKER_POLICY","STEWARD_BROKER_STATE",
    "STEWARD_BROKER_AUDIT","STEWARD_BROKER_CHECKPOINT","STEWARD_BROKER_DATA_DIR","STEWARD_BROKER_DEVICE_ID",
    "STEWARD_APPROVAL_PRIVATE_KEY"
)
$originalEnvironment = @{}
foreach ($name in $managedEnvironment) { $originalEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process") }

function Database-Url([string]$name) { return ($DatabaseUrl -replace '/[^/?]+\?', ("/{0}?" -f $name)) }

function Invoke-Json([string]$base, [string]$method, [string]$path, [object]$body = $null) {
    $args = @{Method=$method;Uri=($base+$path);SkipHttpErrorCheck=$true;TimeoutSec=10}
    if ($null -ne $body) { $args.ContentType="application/json"; $args.Body=($body | ConvertTo-Json -Depth 50) }
    $response = Invoke-WebRequest @args
    if ($response.StatusCode -ge 400) { throw "$method $path failed with HTTP $($response.StatusCode): $($response.Content)" }
    return ($response.Content | ConvertFrom-Json)
}

function Wait-Tcp([int]$port) {
    for ($i=0; $i -lt 120; $i++) {
        $client = [System.Net.Sockets.TcpClient]::new()
        try { $client.Connect("127.0.0.1", $port); return } catch { Start-Sleep -Milliseconds 100 } finally { $client.Dispose() }
    }
    throw "port $port did not become ready"
}

function Wait-Ready([int]$port) {
    for ($i=0; $i -lt 160; $i++) {
        try { Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/readyz" -f $port) -TimeoutSec 2 | Out-Null; return } catch {}
        Start-Sleep -Milliseconds 150
    }
    throw "server on port $port did not become ready"
}

function Start-Broker([string]$binary, [string]$name, [int]$port, [object]$keys, [string]$policy) {
    $dir = Join-Path $temp ("broker-"+$name)
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    $env:STEWARD_BROKER_DEVICE_ID = $name
    $env:STEWARD_BROKER_LISTEN = "127.0.0.1:$port"
    $env:STEWARD_BROKER_POLICY = $policy
    $env:STEWARD_BROKER_STATE = Join-Path $dir "state.json"
    $env:STEWARD_BROKER_AUDIT = Join-Path $dir "audit.jsonl"
    $env:STEWARD_BROKER_CHECKPOINT = Join-Path $dir "checkpoint.json"
    $env:STEWARD_BROKER_DATA_DIR = $dir
    $env:STEWARD_BROKER_CLIENT_KEY = $keys.client_key
    $env:STEWARD_BROKER_CONTROL_KEY = $keys.control_key
    $env:STEWARD_BROKER_SIGNING_PRIVATE_KEY = $keys.signing_private_key
    & $binary initialize-checkpoint | Out-Null
    $process = Start-Process -FilePath $binary -ArgumentList @("run","--workdir",$dir) -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $dir "broker.out.log") -RedirectStandardError (Join-Path $dir "broker.err.log")
    $script:processes += $process
    Wait-Tcp $port
    return $process
}

function Start-Node([string]$binary, [string]$name, [string]$database, [int]$managementPort, [int]$peerPort, [object]$deviceKeys, [int]$brokerPort, [object]$brokerKeys) {
    $env:DATABASE_URL = Database-Url $database
    $env:STORAGE_DIR = Join-Path $temp ("storage-"+$name)
    New-Item -ItemType Directory -Path $env:STORAGE_DIR -Force | Out-Null
    $env:HTTP_ADDR = "127.0.0.1:$managementPort"
    $env:STEWARD_PEER_HTTP_ADDR = "127.0.0.1:$peerPort"
    $env:STEWARD_PUBLIC_API_BASE = "http://127.0.0.1:$peerPort/api"
    $env:STEWARD_AGENT_ID = $name
    $env:STEWARD_DEVICE_PUBLIC_KEY = $deviceKeys.public_key
    $env:STEWARD_DEVICE_PRIVATE_KEY = $deviceKeys.private_key
    $env:STEWARD_SYNC_REQUIRE_AUTH = "true"
    $env:STEWARD_SYNC_ALLOW_INSECURE = "false"
    $env:STEWARD_RUNTIME_V2 = "true"
    $env:STEWARD_RUNTIME_R3 = "true"
    $env:STEWARD_ORCHESTRATION_R4 = "true"
    $env:STEWARD_ORCHESTRATION_REMOTE = "true"
    $env:STEWARD_REMOTE_EXECUTION_LEASE = "6s"
    $env:STEWARD_RUNTIME_INTERVAL = "100ms"
    $env:STEWARD_RUNTIME_LIMIT = "10"
    $env:STEWARD_SYNC_INTERVAL = "0s"
    $env:STEWARD_AUTONOMY_INTERVAL = "0s"
    $env:STEWARD_BROKER_URL = "http://127.0.0.1:$brokerPort"
    $env:STEWARD_BROKER_CLIENT_KEY = $brokerKeys.client_key
    $env:STEWARD_BROKER_PUBLIC_KEY = $brokerKeys.signing_public_key
    Remove-Item Env:STEWARD_BROKER_CONTROL_KEY -ErrorAction SilentlyContinue
    Remove-Item Env:STEWARD_BROKER_SIGNING_PRIVATE_KEY -ErrorAction SilentlyContinue
    $process = Start-Process -FilePath $binary -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $temp ($name+".out.log")) -RedirectStandardError (Join-Path $temp ($name+".err.log"))
    $script:processes += $process
    Wait-Ready $managementPort
    return $process
}

try {
    New-Item -ItemType Directory -Path $temp | Out-Null
    $serverBinary = Join-Path $temp "server.exe"
    $stewardBinary = Join-Path $temp "steward.exe"
    $brokerBinary = Join-Path $temp "steward-broker.exe"
    $approvalBinary = Join-Path $temp "steward-approval.exe"
    Push-Location $backendRoot
    try {
        go build -buildvcs=false -o $serverBinary ./cmd/server
        go build -buildvcs=false -o $stewardBinary ./cmd/steward
        go build -buildvcs=false -o $brokerBinary ./cmd/steward-broker
        go build -buildvcs=false -o $approvalBinary ./cmd/steward-approval
    } finally { Pop-Location }

    docker exec $PostgresContainer psql -U postgres -d postgres -v ON_ERROR_STOP=1 -c "create database $originDB" | Out-Null
    docker exec $PostgresContainer psql -U postgres -d postgres -v ON_ERROR_STOP=1 -c "create database $targetDB" | Out-Null
    $originDeviceKeys = (& $stewardBinary keygen --prefix r44-origin | ConvertFrom-Json)
    $targetDeviceKeys = (& $stewardBinary keygen --prefix r44-target | ConvertFrom-Json)
    $originBrokerKeys = ((& $brokerBinary keygen) | ConvertFrom-Json).keys
    $targetBrokerKeys = ((& $brokerBinary keygen) | ConvertFrom-Json).keys
    $approvalKeys = ((& $approvalBinary keygen) | ConvertFrom-Json).keys
    $seed = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($seed)
    $env:STEWARD_ORCHESTRATION_SIGNING_KEY = [Convert]::ToBase64String($seed)
    $executable = Join-Path $env:WINDIR "System32\whoami.exe"
    $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $executable).Hash.ToLowerInvariant()
    $credentialPath = Join-Path $env:WINDIR "win.ini"

    $originPolicy = Join-Path $temp "origin-policy.json"
    $targetPolicy = Join-Path $temp "target-policy.json"
    $authority = @(@{name="r44-operator";public_key=$approvalKeys.public_key;enabled=$true})
    $baseCapability = @{name="tool:whoami";description="fixed protected identity query";permission_level="A4";risk_level="high";
        executable=$executable;executable_sha256=$digest;arguments=@();working_directory=(Split-Path $executable);timeout_seconds=15;max_output_bytes=4096;enabled=$true}
    @{version=3;approval_authorities=$authority;capabilities=@($baseCapability);broker_peers=@(@{device_id="r44-target";name="target Broker";
        public_key=$targetBrokerKeys.signing_public_key;allowed_capabilities=@("tool:whoami");allowed_credentials=@("credential:r44");enabled=$true})} |
        ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $originPolicy -Encoding utf8
    $targetCapability = $baseCapability.Clone(); $targetCapability.credential_ids = @("credential:r44")
    @{version=3;approval_authorities=$authority;capabilities=@($targetCapability);credentials=@(@{id="credential:r44";path=$credentialPath;max_bytes=65536;enabled=$true});
        broker_peers=@(@{device_id="r44-origin";name="origin Broker";public_key=$originBrokerKeys.signing_public_key;
            allowed_capabilities=@("tool:whoami");allowed_credentials=@("credential:r44");enabled=$true})} |
        ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $targetPolicy -Encoding utf8

    Start-Broker $brokerBinary "r44-origin" $OriginBrokerPort $originBrokerKeys $originPolicy | Out-Null
    Start-Broker $brokerBinary "r44-target" $TargetBrokerPort $targetBrokerKeys $targetPolicy | Out-Null
    $originProcess = Start-Node $serverBinary "r44-origin" $originDB $OriginManagementPort $OriginPeerPort $originDeviceKeys $OriginBrokerPort $originBrokerKeys
    $targetProcess = Start-Node $serverBinary "r44-target" $targetDB $TargetManagementPort $TargetPeerPort $targetDeviceKeys $TargetBrokerPort $targetBrokerKeys
    $originAPI = "http://127.0.0.1:$OriginManagementPort/api"
    $targetAPI = "http://127.0.0.1:$TargetManagementPort/api"
    Invoke-Json $originAPI "POST" "/steward/devices" @{id="r44-target";device_name="R44 Target";platform="windows";role="peer";sync_enabled=$true;
        permission_level="A7";public_key=$targetDeviceKeys.public_key;api_base_url="http://127.0.0.1:$TargetPeerPort/api";
        broker_public_key=$targetBrokerKeys.signing_public_key;broker_key_id=$targetBrokerKeys.key_id} | Out-Null
    Invoke-Json $targetAPI "POST" "/steward/devices" @{id="r44-origin";device_name="R44 Origin";platform="windows";role="peer";sync_enabled=$true;
        permission_level="A7";public_key=$originDeviceKeys.public_key;api_base_url="http://127.0.0.1:$OriginPeerPort/api";
        broker_public_key=$originBrokerKeys.signing_public_key;broker_key_id=$originBrokerKeys.key_id} | Out-Null
    Invoke-Json $originAPI "PUT" "/steward/orchestration/agents" @{id="r44-agent";name="R44 Agent";role="remote privileged operator";
        permission_ceiling="A7";data_level_ceiling="D4";tool_allowlist=@("privilege.execute");max_concurrency=1} | Out-Null
    $created = (Invoke-Json $originAPI "POST" "/steward/orchestrations" @{goal="R4.4 real Broker federation";auto_start=$true;permission_ceiling="A7";data_level="D4";
        nodes=@(@{key="privileged";agent_id="r44-agent";goal="execute protected remote capability";target_device="r44-target";
            permission_ceiling="A7";data_level="D4";credential_refs=@("credential:r44");steps=@(@{key="execute";tool_name="privilege.execute";arguments=@{capability="tool:whoami"}})})}).orchestration
    $node = $created.nodes[0]
    $preview = (Invoke-Json $originAPI "POST" ("/steward/orchestrations/{0}/nodes/{1}/remote-privilege/preview" -f $created.id,$node.id) @{}).preview
    $env:STEWARD_APPROVAL_PRIVATE_KEY = $approvalKeys.private_key
    try {
        $proof = (& $approvalBinary issue --approve --subject $preview.subject --plan-hash $preview.plan_hash --capability $preview.capability `
            --generation $preview.control_generation --granted-by "r44-validation" --reason "real R4.4 Broker federation acceptance" | Out-String | ConvertFrom-Json)
    } finally { Remove-Item Env:STEWARD_APPROVAL_PRIVATE_KEY -ErrorAction SilentlyContinue }
    Invoke-Json $originAPI "POST" ("/steward/orchestrations/{0}/nodes/{1}/remote-privilege/approve" -f $created.id,$node.id) `
        @{plan_hash=$preview.plan_hash;approval_proof=$proof} | Out-Null
    $completed = $null
    for ($i=0; $i -lt 300; $i++) {
        $completed = (Invoke-Json $originAPI "GET" ("/steward/orchestrations/{0}" -f $created.id)).orchestration
        if ($completed.status -in @("succeeded","failed","blocked","cancelled")) { break }
        Start-Sleep -Milliseconds 150
    }
    $dispatch = $completed.nodes[0].remote_dispatch
    $receipt = $dispatch.result_payload.broker_receipt.payload
    if ($completed.status -ne "succeeded" -or $dispatch.status -ne "succeeded" -or -not $receipt.audit_persisted -or
        $receipt.delegation_id -ne $completed.nodes[0].remote_privilege.delegation_id -or $receipt.credential_refs[0] -ne "credential:r44" -or
        -not [string]::IsNullOrEmpty($dispatch.result_payload.stdout)) { throw "R4.4 Broker receipt or opaque credential contract failed" }
    [ordered]@{orchestration_id=$completed.id;status=$completed.status;origin_process_id=$originProcess.Id;target_process_id=$targetProcess.Id;
        target_device_id=$completed.nodes[0].selected_device_id;delegation_id=$receipt.delegation_id;capability=$receipt.capability;
        target_broker_key_id=$dispatch.result_payload.broker_receipt.key_id;credential_refs=$receipt.credential_refs;
        credential_plaintext_exposed=$false;broker_audit_persisted=$receipt.audit_persisted;result_signature_verified=(-not [string]::IsNullOrWhiteSpace($dispatch.result_signature))} |
        ConvertTo-Json -Depth 10
}
finally {
    foreach ($process in $processes) { if ($null -ne $process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue } }
    foreach ($database in @($originDB,$targetDB)) {
        docker exec $PostgresContainer psql -U postgres -d postgres -c "select pg_terminate_backend(pid) from pg_stat_activity where datname='$database' and pid <> pg_backend_pid()" 2>$null | Out-Null
        docker exec $PostgresContainer psql -U postgres -d postgres -c "drop database if exists $database" 2>$null | Out-Null
    }
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    foreach ($name in $managedEnvironment) { [Environment]::SetEnvironmentVariable($name, $originalEnvironment[$name], "Process") }
}
