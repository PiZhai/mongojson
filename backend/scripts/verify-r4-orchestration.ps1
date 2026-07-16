param(
    [string]$DatabaseUrl = "postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable",
    [int]$ManagementPort = 18084,
    [int]$PeerPort = 18085
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true
$backendRoot = Split-Path $PSScriptRoot -Parent
$temp = Join-Path $env:TEMP ("steward-r40-validation-" + [guid]::NewGuid().ToString("N"))
$serverProcess = $null
$suffix = [guid]::NewGuid().ToString("N").Substring(0, 10)
$managedEnvironment = @(
    "HTTP_ADDR", "STEWARD_PEER_HTTP_ADDR", "DATABASE_URL", "STORAGE_DIR",
    "STEWARD_RUNTIME_V2", "STEWARD_ORCHESTRATION_R4", "STEWARD_ORCHESTRATION_SIGNING_KEY",
    "STEWARD_ORCHESTRATION_DELEGATION_TTL", "STEWARD_RUNTIME_INTERVAL", "STEWARD_RUNTIME_LIMIT",
    "STEWARD_SYNC_INTERVAL", "STEWARD_AUTONOMY_INTERVAL"
)
$savedEnvironment = @{}
foreach ($name in $managedEnvironment) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

function Invoke-StewardJson([string]$method, [string]$path, [object]$body = $null) {
    $parameters = @{
        Method = $method
        Uri = ("http://127.0.0.1:{0}/api{1}" -f $ManagementPort, $path)
        SkipHttpErrorCheck = $true
    }
    if ($null -ne $body) {
        $parameters.ContentType = "application/json"
        $parameters.Body = ($body | ConvertTo-Json -Depth 20)
    }
    $response = Invoke-WebRequest @parameters
    if ($response.StatusCode -ge 400) {
        throw ("Steward {0} {1} failed with HTTP {2}: {3}" -f $method, $path, $response.StatusCode, $response.Content)
    }
    return ($response.Content | ConvertFrom-Json)
}

function Wait-Steward {
    for ($attempt = 0; $attempt -lt 120; $attempt++) {
        try {
            Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/readyz" -f $ManagementPort) -TimeoutSec 2 | Out-Null
            return
        }
        catch {
            Start-Sleep -Milliseconds 250
        }
    }
    throw "Steward server did not become ready"
}

function Wait-Orchestration([string]$id) {
    for ($attempt = 0; $attempt -lt 120; $attempt++) {
        $current = (Invoke-StewardJson "GET" ("/steward/orchestrations/{0}" -f $id)).orchestration
        if ($current.status -in @("succeeded", "failed", "blocked", "cancelled")) {
            return $current
        }
        Start-Sleep -Milliseconds 250
    }
    throw "Orchestration $id did not reach a terminal state"
}

try {
    New-Item -ItemType Directory -Path $temp | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $temp "storage") | Out-Null
    $serverBinary = Join-Path $temp "server.exe"
    Push-Location $backendRoot
    try {
        go build -buildvcs=false -o $serverBinary ./cmd/server
    }
    finally {
        Pop-Location
    }

    $key = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($key)
    $env:HTTP_ADDR = "127.0.0.1:$ManagementPort"
    $env:STEWARD_PEER_HTTP_ADDR = "127.0.0.1:$PeerPort"
    $env:DATABASE_URL = $DatabaseUrl
    $env:STORAGE_DIR = Join-Path $temp "storage"
    $env:STEWARD_RUNTIME_V2 = "true"
    $env:STEWARD_ORCHESTRATION_R4 = "true"
    $env:STEWARD_ORCHESTRATION_SIGNING_KEY = [Convert]::ToBase64String($key)
    $env:STEWARD_ORCHESTRATION_DELEGATION_TTL = "5m"
    $env:STEWARD_RUNTIME_INTERVAL = "100ms"
    $env:STEWARD_RUNTIME_LIMIT = "10"
    $env:STEWARD_SYNC_INTERVAL = "0s"
    $env:STEWARD_AUTONOMY_INTERVAL = "0s"

    $serverProcess = Start-Process -FilePath $serverBinary `
        -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $temp "server.out.log") `
        -RedirectStandardError (Join-Path $temp "server.err.log")
    Wait-Steward

    $researcher = "research-$suffix"
    $writer = "writer-$suffix"
    Invoke-StewardJson "PUT" "/steward/orchestration/agents" @{
        id = $researcher
        name = "R4 Acceptance Research Agent"
        role = "parallel collector"
        permission_ceiling = "A0"
        data_level_ceiling = "D0"
        tool_allowlist = @("runtime.echo")
        max_concurrency = 1
    } | Out-Null
    Invoke-StewardJson "PUT" "/steward/orchestration/agents" @{
        id = $writer
        name = "R4 Acceptance Writer Agent"
        role = "bounded synthesizer"
        permission_ceiling = "A0"
        data_level_ceiling = "D0"
        tool_allowlist = @("runtime.echo")
        max_concurrency = 1
    } | Out-Null

    $created = (Invoke-StewardJson "POST" "/steward/orchestrations" @{
        goal = "R4.0 real-process fan-out and join acceptance"
        idempotency_key = "r40-process-$suffix"
        auto_start = $true
        permission_ceiling = "A0"
        data_level = "D0"
        max_parallel = 2
        nodes = @(
            @{
                key = "collect_a"
                agent_id = $researcher
                goal = "collect A"
                steps = @(@{ key = "echo"; tool_name = "runtime.echo"; arguments = @{ value = "A" }; expected_output = @{ value = "A" } })
            },
            @{
                key = "collect_b"
                agent_id = $writer
                goal = "collect B"
                steps = @(@{ key = "echo"; tool_name = "runtime.echo"; arguments = @{ value = "B" }; expected_output = @{ value = "B" } })
            },
            @{
                key = "join"
                agent_id = $writer
                goal = "join A and B"
                depends_on = @("collect_a", "collect_b")
                steps = @(@{ key = "echo"; tool_name = "runtime.echo"; arguments = @{ value = "A+B" }; expected_output = @{ value = "A+B" } })
            }
        )
    }).orchestration
    $completed = Wait-Orchestration $created.id

    if ($completed.status -ne "succeeded") {
        throw "R4.0 orchestration ended as $($completed.status): $($completed.failure_summary)"
    }
    if ($completed.nodes.Count -ne 3 -or @($completed.nodes | Where-Object status -eq "succeeded").Count -ne 3) {
        throw "Not every R4.0 node succeeded"
    }
    if (@($completed.nodes | Select-Object -ExpandProperty agent_id -Unique).Count -ne 2) {
        throw "Acceptance did not exercise two distinct Agent identities"
    }
    if ($completed.evidence.child_run_count -ne 3 -or $completed.evidence.artifact_count -lt 3 -or [string]::IsNullOrWhiteSpace($completed.evidence.manifest_sha256)) {
        throw "Evidence lineage was not aggregated"
    }
    if (@($completed.events | Where-Object type -eq "orchestration.succeeded").Count -ne 1) {
        throw "Terminal orchestration audit event is missing"
    }

    [ordered]@{
        orchestration_id = $completed.id
        status = $completed.status
        agent_ids = @($completed.nodes | Select-Object -ExpandProperty agent_id -Unique)
        child_run_count = $completed.evidence.child_run_count
        artifact_count = $completed.evidence.artifact_count
        evidence_manifest_sha256 = $completed.evidence.manifest_sha256
        event_count = $completed.events.Count
        database = $DatabaseUrl
    } | ConvertTo-Json -Depth 5
}
catch {
    if (Test-Path (Join-Path $temp "server.err.log")) {
        Get-Content (Join-Path $temp "server.err.log") -Tail 100 -ErrorAction SilentlyContinue | Write-Error
    }
    throw
}
finally {
    if ($null -ne $serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
        $serverProcess.WaitForExit()
    }
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    foreach ($name in $managedEnvironment) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], "Process")
    }
}
