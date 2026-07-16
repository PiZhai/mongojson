param(
    [string]$DatabaseUrl = "postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable",
    [int]$ManagementPort = 18086,
    [int]$PeerPort = 18087,
    [switch]$VerifySaga
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true
$backendRoot = Split-Path $PSScriptRoot -Parent
$temp = Join-Path $env:TEMP ("steward-r41-validation-" + [guid]::NewGuid().ToString("N"))
$server = $null
$researchWorker = $null
$writerWorker = $null
$replacementWorker = $null
$suffix = [guid]::NewGuid().ToString("N").Substring(0, 10)
$managedEnvironment = @(
    "STEWARD_ORCHESTRATION_SIGNING_KEY","STEWARD_ORCHESTRATION_VERIFY_KEY","HTTP_ADDR","STEWARD_PEER_HTTP_ADDR",
    "DATABASE_URL","STORAGE_DIR","STEWARD_RUNTIME_V2","STEWARD_ORCHESTRATION_R4","STEWARD_ORCHESTRATION_WORKERS",
    "STEWARD_ORCHESTRATION_MESSAGE_LEASE","STEWARD_RUNTIME_LEASE_TTL","STEWARD_RUNTIME_INTERVAL","STEWARD_RUNTIME_LIMIT",
    "STEWARD_SYNC_INTERVAL","STEWARD_AUTONOMY_INTERVAL","STEWARD_WORKER_POLL_INTERVAL"
)
$originalEnvironment = @{}
foreach ($name in $managedEnvironment) {
    $originalEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

function Invoke-StewardJson([string]$method, [string]$path, [object]$body = $null) {
    $args = @{ Method=$method; Uri=("http://127.0.0.1:{0}/api{1}" -f $ManagementPort,$path); SkipHttpErrorCheck=$true }
    if ($null -ne $body) {
        $args.ContentType = "application/json"
        $args.Body = ($body | ConvertTo-Json -Depth 30)
    }
    $response = Invoke-WebRequest @args
    if ($response.StatusCode -ge 400) {
        throw ("{0} {1} failed with HTTP {2}: {3}" -f $method,$path,$response.StatusCode,$response.Content)
    }
    return ($response.Content | ConvertFrom-Json)
}

function Wait-Ready {
    for ($i=0; $i -lt 120; $i++) {
        try { Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/readyz" -f $ManagementPort) -TimeoutSec 2 | Out-Null; return } catch {}
        Start-Sleep -Milliseconds 250
    }
    throw "server did not become ready"
}

function Wait-Orchestration([string]$id, [int]$attempts = 160) {
    for ($i=0; $i -lt $attempts; $i++) {
        $item = (Invoke-StewardJson "GET" ("/steward/orchestrations/{0}" -f $id)).orchestration
        if ($item.status -in @("succeeded","failed","compensated","compensation_failed","blocked","cancelled")) { return $item }
        Start-Sleep -Milliseconds 250
    }
    throw "orchestration $id did not finish"
}

function Wait-MessageAcknowledged([string]$id, [int]$attempts = 80) {
    for ($i=0; $i -lt $attempts; $i++) {
        $item = (Invoke-StewardJson "GET" ("/steward/orchestrations/{0}" -f $id)).orchestration
        $messages = @($item.messages)
        if ($messages.Count -gt 0 -and @($messages | Where-Object status -ne "acknowledged").Count -eq 0) { return $item }
        Start-Sleep -Milliseconds 100
    }
    throw "orchestration $id did not acknowledge all Agent messages"
}

function Start-AgentWorker([string]$binary, [string]$agent, [string]$workerID, [string]$logPrefix) {
    return Start-Process -FilePath $binary -ArgumentList @("--agent",$agent,"--worker-id",$workerID,"--poll","50ms") `
        -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $temp ($logPrefix + ".out.log")) `
        -RedirectStandardError (Join-Path $temp ($logPrefix + ".err.log"))
}

try {
    New-Item -ItemType Directory -Path $temp | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $temp "storage") | Out-Null
    $serverBinary = Join-Path $temp "server.exe"
    $workerBinary = Join-Path $temp "steward-agent-worker.exe"
    Push-Location $backendRoot
    try {
        go build -buildvcs=false -o $serverBinary ./cmd/server
        go build -buildvcs=false -o $workerBinary ./cmd/steward-agent-worker
    }
    finally { Pop-Location }

    $seed = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($seed)
    $env:STEWARD_ORCHESTRATION_SIGNING_KEY = [Convert]::ToBase64String($seed)
    $verifyKey = (& $workerBinary --print-verify-key | Out-String).Trim()
    $env:HTTP_ADDR = "127.0.0.1:$ManagementPort"
    $env:STEWARD_PEER_HTTP_ADDR = "127.0.0.1:$PeerPort"
    $env:DATABASE_URL = $DatabaseUrl
    $env:STORAGE_DIR = Join-Path $temp "storage"
    $env:STEWARD_RUNTIME_V2 = "true"
    $env:STEWARD_ORCHESTRATION_R4 = "true"
    $env:STEWARD_ORCHESTRATION_WORKERS = "true"
    $env:STEWARD_ORCHESTRATION_MESSAGE_LEASE = "2s"
    $env:STEWARD_RUNTIME_LEASE_TTL = "1s"
    $env:STEWARD_RUNTIME_INTERVAL = "100ms"
    $env:STEWARD_RUNTIME_LIMIT = "10"
    $env:STEWARD_SYNC_INTERVAL = "0s"
    $env:STEWARD_AUTONOMY_INTERVAL = "0s"
    $server = Start-Process -FilePath $serverBinary -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $temp "server.out.log") `
        -RedirectStandardError (Join-Path $temp "server.err.log")
    Wait-Ready

    $researcher = "r41-research-$suffix"
    $writer = "r41-writer-$suffix"
    foreach ($agent in @(
        @{ id=$researcher; name="R4.1 Research Worker"; role="collector" },
        @{ id=$writer; name="R4.1 Writer Worker"; role="synthesizer" }
    )) {
        Invoke-StewardJson "PUT" "/steward/orchestration/agents" @{
            id=$agent.id; name=$agent.name; role=$agent.role
            permission_ceiling="A0"; data_level_ceiling="D0"
            tool_allowlist=@("runtime.echo"); max_concurrency=1
            max_runtime_seconds=2000; max_attempts=100; max_evidence_bytes=1048576
        } | Out-Null
    }

    # Workers receive only the Ed25519 public key; the running Server already
    # captured the private seed in its own process environment.
    Remove-Item Env:STEWARD_ORCHESTRATION_SIGNING_KEY
    $env:STEWARD_ORCHESTRATION_VERIFY_KEY = $verifyKey
    $env:STEWARD_WORKER_POLL_INTERVAL = "50ms"
    $researchWorker = Start-AgentWorker $workerBinary $researcher "research-primary-$suffix" "research-primary"
    $writerWorker = Start-AgentWorker $workerBinary $writer "writer-primary-$suffix" "writer-primary"

    $created = (Invoke-StewardJson "POST" "/steward/orchestrations" @{
        goal="R4.1 two-process Agent acceptance"; auto_start=$true
        permission_ceiling="A0"; data_level="D0"; max_parallel=2
        nodes=@(
            @{ key="collect"; agent_id=$researcher; goal="collect"; steps=@(@{key="echo";tool_name="runtime.echo";arguments=@{value="A"}}) },
            @{ key="draft"; agent_id=$writer; goal="draft"; steps=@(@{key="echo";tool_name="runtime.echo";arguments=@{value="B"}}) },
            @{ key="join"; agent_id=$writer; goal="join"; depends_on=@("collect","draft"); steps=@(@{key="echo";tool_name="runtime.echo";arguments=@{value="A+B"}}) }
        )
    }).orchestration
    $completed = Wait-Orchestration $created.id
    if ($completed.status -ne "succeeded" -or @($completed.messages | Where-Object status -ne "acknowledged").Count -ne 0) {
        throw "independent worker orchestration failed"
    }

    $saga = $null
    if ($VerifySaga) {
        $sagaCreated = (Invoke-StewardJson "POST" "/steward/orchestrations" @{
            goal="R4.2 independent worker Saga acceptance"; auto_start=$true
            failure_policy="compensate"; permission_ceiling="A0"; data_level="D0"
            nodes=@(
                @{ key="prepare"; agent_id=$researcher; goal="prepare";
                   steps=@(@{key="do";tool_name="runtime.echo";arguments=@{value="prepared"}});
                   compensation_steps=@(@{key="undo";tool_name="runtime.echo";arguments=@{value="unprepared"}}) },
                @{ key="publish"; agent_id=$writer; goal="publish"; depends_on=@("prepare");
                   steps=@(@{key="do";tool_name="runtime.echo";arguments=@{value="published"}});
                   compensation_steps=@(@{key="undo";tool_name="runtime.echo";arguments=@{value="unpublished"}}) },
                @{ key="fail"; agent_id=$researcher; goal="fail postcondition"; depends_on=@("publish");
                   steps=@(@{key="fail";tool_name="runtime.echo";arguments=@{value="actual"};expected_output=@{value="different"}}) }
            )
        }).orchestration
        $saga = Wait-Orchestration $sagaCreated.id 240
        $saga = Wait-MessageAcknowledged $sagaCreated.id 120
        $compensations = @($saga.nodes | Where-Object kind -eq "compensation")
        if ($saga.status -ne "compensated" -or $compensations.Count -ne 2 -or
            $compensations[0].key -ne "compensate-002" -or $compensations[1].key -ne "compensate-001" -or
            @($compensations | Where-Object status -ne "succeeded").Count -ne 0) {
            throw "independent workers did not complete reverse-order Saga compensation"
        }
    }

    # A 100-step run leaves enough transaction boundaries to kill the process
    # after it has durably leased the mailbox message.
    $steps = @()
    for ($i=1; $i -le 100; $i++) {
        $key = "step_$i"
        $step = @{ key=$key; tool_name="runtime.echo"; arguments=@{value=$i} }
        if ($i -gt 1) { $step.depends_on = @("step_" + ($i-1)) }
        $steps += $step
    }
    $crash = (Invoke-StewardJson "POST" "/steward/orchestrations" @{
        goal="R4.1 hard worker crash recovery"; auto_start=$true
        permission_ceiling="A0"; data_level="D0"
        nodes=@(@{key="long_run";agent_id=$researcher;goal="survive worker crash";steps=$steps})
    }).orchestration
    $leased = $false
    for ($i=0; $i -lt 200; $i++) {
        $snapshot = (Invoke-StewardJson "GET" ("/steward/orchestrations/{0}" -f $crash.id)).orchestration
        if (@($snapshot.messages | Where-Object status -eq "leased").Count -gt 0) { $leased=$true; break }
        Start-Sleep -Milliseconds 10
    }
    if (-not $leased) { throw "research worker never exposed a durable leased message" }
    Stop-Process -Id $researchWorker.Id -Force
    $researchWorker.WaitForExit()
    Start-Sleep -Milliseconds 2300
    $replacementWorker = Start-AgentWorker $workerBinary $researcher "research-replacement-$suffix" "research-replacement"
    $recovered = Wait-Orchestration $crash.id 240
    if ($recovered.status -ne "succeeded") { throw "replacement worker failed: $($recovered.failure_summary)" }
    $recovered = Wait-MessageAcknowledged $crash.id
    $message = @($recovered.messages)[0]
    if ($message.attempt -lt 2 -or $message.status -ne "acknowledged") { throw "expired message lease was not redelivered" }

    [ordered]@{
        orchestration_id=$completed.id
        status=$completed.status
        worker_processes=@($completed.workers | Select-Object -ExpandProperty process_id -Unique)
        acknowledged_messages=@($completed.messages | Where-Object status -eq "acknowledged").Count
        crash_orchestration_id=$recovered.id
        crash_recovery_status=$recovered.status
        crash_message_attempts=$message.attempt
        replacement_worker_id="research-replacement-$suffix"
        evidence_artifacts=$recovered.evidence.artifact_count
        saga_orchestration_id=$(if ($null -ne $saga) { $saga.id } else { $null })
        saga_status=$(if ($null -ne $saga) { $saga.status } else { $null })
        saga_compensation_order=$(if ($null -ne $saga) { @($saga.nodes | Where-Object kind -eq "compensation" | Select-Object -ExpandProperty key) } else { @() })
    } | ConvertTo-Json -Depth 5
}
catch {
    foreach ($name in @("server","research-primary","writer-primary","research-replacement")) {
        $path = Join-Path $temp ($name + ".err.log")
        if (Test-Path $path) { Write-Host ("--- {0} ---" -f $path); Get-Content $path -Tail 80 -ErrorAction SilentlyContinue | Write-Host }
    }
    throw
}
finally {
    foreach ($process in @($replacementWorker,$researchWorker,$writerWorker,$server)) {
        if ($null -ne $process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue; $process.WaitForExit() }
    }
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    foreach ($name in $managedEnvironment) {
        [Environment]::SetEnvironmentVariable($name, $originalEnvironment[$name], "Process")
    }
}
