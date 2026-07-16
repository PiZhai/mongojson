param(
    [string]$DatabaseUrl = "postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable",
    [int]$BrokerPort = 18102,
    [int]$ManagementPort = 18082,
    [int]$PeerPort = 18083
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true
$backendRoot = Split-Path $PSScriptRoot -Parent
$temp = Join-Path $env:TEMP ("steward-r33-validation-" + [guid]::NewGuid().ToString("N"))
$brokerProcess = $null
$serverProcess = $null
$summary = $null

function Wait-Broker([string]$binary) {
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        try {
            $raw = & $binary status 2>$null
            if ($LASTEXITCODE -eq 0) {
                return ($raw | Out-String | ConvertFrom-Json)
            }
        }
        catch {
            # The process is still starting.
        }
        Start-Sleep -Milliseconds 250
    }
    throw "Privilege Broker did not become ready"
}

function Wait-Steward {
    for ($attempt = 0; $attempt -lt 80; $attempt++) {
        try {
            Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/readyz" -f $ManagementPort) -TimeoutSec 2 | Out-Null
            return
        }
        catch {
            # Migrations or the HTTP listener are still starting.
        }
        Start-Sleep -Milliseconds 250
    }
    throw "Steward server did not become ready"
}

function Invoke-StewardPost([string]$path, [object]$body) {
    $response = Invoke-WebRequest -Method Post `
        -Uri ("http://127.0.0.1:{0}/api{1}" -f $ManagementPort, $path) `
        -ContentType "application/json" `
        -Body ($body | ConvertTo-Json -Depth 10) `
        -SkipHttpErrorCheck
    if ($response.StatusCode -ge 400) {
        throw ("Steward POST {0} failed with HTTP {1}: {2}" -f $path, $response.StatusCode, $response.Content)
    }
    return ($response.Content | ConvertFrom-Json)
}

function Assert-StewardPostRejected([string]$path, [object]$body) {
    $response = Invoke-WebRequest -Method Post `
        -Uri ("http://127.0.0.1:{0}/api{1}" -f $ManagementPort, $path) `
        -ContentType "application/json" `
        -Body ($body | ConvertTo-Json -Depth 10) `
        -SkipHttpErrorCheck
    if ($response.StatusCode -lt 400) {
        throw "Expected Steward to reject $path but got HTTP $($response.StatusCode)"
    }
}

try {
    New-Item -ItemType Directory -Path $temp | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $temp "storage") | Out-Null
    $brokerBinary = Join-Path $temp "steward-broker.exe"
    $approvalBinary = Join-Path $temp "steward-approval.exe"
    $serverBinary = Join-Path $temp "server.exe"

    Push-Location $backendRoot
    try {
        go build -buildvcs=false -o $brokerBinary ./cmd/steward-broker
        go build -buildvcs=false -o $approvalBinary ./cmd/steward-approval
        go build -buildvcs=false -o $serverBinary ./cmd/server
    }
    finally {
        Pop-Location
    }

    $generated = (& $brokerBinary keygen | Out-String | ConvertFrom-Json)
    $keys = $generated.keys
    $approvalKeys = ((& $approvalBinary keygen) | Out-String | ConvertFrom-Json).keys
    $executable = Join-Path $env:WINDIR "System32\whoami.exe"
    $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $executable).Hash.ToLowerInvariant()
    $policyPath = Join-Path $temp "policy.json"
    @{
        version = 2
        approval_authorities = @(@{
            name = "r31-validation-operator"
            public_key = $approvalKeys.public_key
            enabled = $true
        })
        capabilities = @(@{
            name = "tool:whoami"
            description = "Return the Broker process identity"
            permission_level = "A4"
            risk_level = "high"
            executable = $executable
            executable_sha256 = $digest
            arguments = @()
            working_directory = Split-Path $executable
            timeout_seconds = 15
            max_output_bytes = 4096
            enabled = $true
        })
    } | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $policyPath -Encoding utf8
    $validated = (& $brokerBinary validate-policy --policy $policyPath | Out-String | ConvertFrom-Json)

    $env:STEWARD_BROKER_LISTEN = "127.0.0.1:$BrokerPort"
    $env:STEWARD_BROKER_POLICY = $policyPath
    $env:STEWARD_BROKER_STATE = Join-Path $temp "state.json"
    $env:STEWARD_BROKER_AUDIT = Join-Path $temp "audit.jsonl"
    $env:STEWARD_BROKER_CHECKPOINT = Join-Path $temp "checkpoint.json"
    $env:STEWARD_BROKER_DATA_DIR = $temp
    $env:STEWARD_BROKER_CLIENT_KEY = $keys.client_key
    $env:STEWARD_BROKER_CONTROL_KEY = $keys.control_key
    $env:STEWARD_BROKER_SIGNING_PRIVATE_KEY = $keys.signing_private_key
    $env:STEWARD_BROKER_PUBLIC_KEY = $keys.signing_public_key
    $env:STEWARD_BROKER_URL = "http://127.0.0.1:$BrokerPort"
    & $brokerBinary initialize-checkpoint | Out-Null
    $brokerProcess = Start-Process -FilePath $brokerBinary `
        -ArgumentList @("run", "--workdir", $temp) `
        -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $temp "broker.out.log") `
        -RedirectStandardError (Join-Path $temp "broker.err.log")
    $brokerInitial = Wait-Broker $brokerBinary

    # The ordinary Steward process must never inherit Broker-only authority.
    Remove-Item Env:STEWARD_BROKER_SIGNING_PRIVATE_KEY
    Remove-Item Env:STEWARD_BROKER_CONTROL_KEY
    Remove-Item Env:STEWARD_BROKER_LISTEN
    Remove-Item Env:STEWARD_BROKER_POLICY
    Remove-Item Env:STEWARD_BROKER_STATE
    Remove-Item Env:STEWARD_BROKER_AUDIT
    Remove-Item Env:STEWARD_BROKER_CHECKPOINT
    Remove-Item Env:STEWARD_BROKER_DATA_DIR
    $env:HTTP_ADDR = "127.0.0.1:$ManagementPort"
    $env:STEWARD_PEER_HTTP_ADDR = "127.0.0.1:$PeerPort"
    $env:DATABASE_URL = $DatabaseUrl
    $env:STORAGE_DIR = Join-Path $temp "storage"
    $env:STEWARD_RUNTIME_R3 = "true"
    $env:STEWARD_RUNTIME_V2 = "true"
    $env:STEWARD_RUNTIME_INTERVAL = "200ms"
    $env:STEWARD_RUNTIME_LIMIT = "5"
    $env:STEWARD_SYNC_INTERVAL = "0s"
    $env:STEWARD_AUTONOMY_INTERVAL = "0s"
    $serverProcess = Start-Process -FilePath $serverBinary `
        -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $temp "server.out.log") `
        -RedirectStandardError (Join-Path $temp "server.err.log")
    Wait-Steward

    # The acceptance database is intentionally persistent, while every test
    # creates a fresh Broker state. Align the fresh Broker to the durable local
    # generation using the independent control identity before approving work.
    $localControl = (Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/api/steward/execution/control" -f $ManagementPort)).control
    if ($brokerInitial.generation -ne $localControl.generation -or $brokerInitial.stopped -ne $localControl.stopped) {
        $controlAction = if ($localControl.stopped) { "stop" } else { "resume" }
        if (-not $localControl.stopped) {
            $env:STEWARD_BROKER_CONTROL_KEY = $keys.control_key
        }
        try {
            & $brokerBinary control $controlAction `
                --generation $localControl.generation `
                --reason "R3.3 acceptance initial generation alignment" `
                --changed-by "r33-control-authority" | Out-Null
        }
        finally {
            Remove-Item Env:STEWARD_BROKER_CONTROL_KEY -ErrorAction SilentlyContinue
        }
        $brokerInitial = (& $brokerBinary status | Out-String | ConvertFrom-Json)
    }

    $created = (Invoke-StewardPost "/steward/runs" @{
        goal = "R3.1 real cross-process privilege execution with independent approval proof"
        auto_start = $true
        requested_by = "r31-runtime-proof"
        permission_ceiling = "A7"
        data_level = "D2"
        steps = @(@{
            key = "broker-whoami"
            title = "Execute fixed whoami capability"
            tool_name = "privilege.execute"
            arguments = @{ capability = "tool:whoami" }
            expected_output = @{}
            timeout_seconds = 20
            requires_approval = $true
        })
    }).run
    if ($created.status -ne "awaiting_approval") {
        throw "created status was $($created.status)"
    }
    $approvalReason = "real independently signed isolated broker acceptance"
    Assert-StewardPostRejected ("/steward/runs/{0}/approve" -f $created.id) @{
        plan_hash = $created.plan_hash
        granted_by = "r31-runtime-proof"
        reason = $approvalReason
    }
    $env:STEWARD_APPROVAL_PRIVATE_KEY = $approvalKeys.private_key
    try {
        $approvalProof = (& $approvalBinary issue --approve `
            --subject ("runtime:{0}" -f $created.id) `
            --plan-hash $created.plan_hash `
            --capability "tool:whoami" `
            --generation $brokerInitial.generation `
            --granted-by "r31-runtime-proof" `
            --reason $approvalReason | Out-String | ConvertFrom-Json)
    }
    finally {
        Remove-Item Env:STEWARD_APPROVAL_PRIVATE_KEY -ErrorAction SilentlyContinue
    }
    $tamperedProof = ($approvalProof | ConvertTo-Json -Depth 10 | ConvertFrom-Json)
    $tamperedProof.claims.capability = "tool:tampered"
    Assert-StewardPostRejected ("/steward/runs/{0}/approve" -f $created.id) @{
        plan_hash = $created.plan_hash
        granted_by = "r31-runtime-proof"
        reason = $approvalReason
        approval_proof = $tamperedProof
    }
    Invoke-StewardPost ("/steward/runs/{0}/approve" -f $created.id) @{
        plan_hash = $created.plan_hash
        granted_by = "r31-runtime-proof"
        reason = $approvalReason
        approval_proof = $approvalProof
    } | Out-Null

    $final = $null
    for ($attempt = 0; $attempt -lt 80; $attempt++) {
        $current = (Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/api/steward/runs/{1}" -f $ManagementPort, $created.id)).run
        if ($current.status -in @("succeeded", "failed", "blocked", "cancelled")) {
            $final = $current
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if ($null -eq $final -or $final.status -ne "succeeded") {
        throw "R3 run did not succeed: $($final.status) $($final.failure_summary)"
    }

    $receiptMetadata = $final.steps[0].evidence |
        Where-Object { $_.kind -eq "privilege_broker_receipt" } |
        Select-Object -First 1
    if ($null -eq $receiptMetadata -or $receiptMetadata.payload_available -ne $true -or $null -ne $receiptMetadata.payload) {
        throw "governed receipt metadata was invalid or leaked an inline payload"
    }
    $revealed = (Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/api/steward/runs/{1}/evidence/{2}" -f $ManagementPort, $created.id, $receiptMetadata.id)).evidence
    $receipt = $revealed.payload.receipt.payload
    if ($receipt.plan_hash -ne $created.plan_hash -or
        $receipt.approval_ref -ne $approvalProof.claims.proof_id -or
        $receipt.approval_proof_id -ne $approvalProof.claims.proof_id -or
        $receipt.approval_key_id -ne $approvalProof.key_id -or
        $receipt.capability -ne "tool:whoami") {
        throw "signed receipt bindings did not match the approved run"
    }

    $stopped = (Invoke-StewardPost "/steward/execution/control/stop" @{
        reason = "R3.1 real emergency stop proof"
        changed_by = "r31-runtime-proof"
    }).control
    if (-not $stopped.stopped -or -not $stopped.broker.reachable -or
        -not $stopped.broker.stopped -or $stopped.broker.generation -ne $stopped.generation) {
        throw "unified stop did not synchronize Broker"
    }
    $brokerStopped = (& $brokerBinary status | Out-String | ConvertFrom-Json)
    Assert-StewardPostRejected "/steward/execution/control/resume" @{
        reason = "R3.1 proof complete"
        changed_by = "r31-runtime-proof"
    }
    $nextGeneration = $stopped.generation + 1
    $env:STEWARD_BROKER_CONTROL_KEY = $keys.control_key
    try {
        $independentResume = (& $brokerBinary control resume `
            --generation $nextGeneration `
            --reason "R3.3 independent operator resume" `
            --changed-by "r33-control-authority" | Out-String | ConvertFrom-Json)
    }
    finally {
        Remove-Item Env:STEWARD_BROKER_CONTROL_KEY -ErrorAction SilentlyContinue
    }
    if ($independentResume.stopped -or $independentResume.generation -ne $nextGeneration) {
        throw "independent control authority did not resume Broker"
    }
    $resumed = (Invoke-StewardPost "/steward/execution/control/resume" @{
        reason = "R3.3 independently authorized resume complete"
        changed_by = "r31-runtime-proof"
    }).control
    if ($resumed.stopped -or $resumed.broker.stopped -or
        $resumed.broker.generation -ne $resumed.generation) {
        throw "unified resume did not synchronize Broker"
    }

    $auditRecords = @(Get-Content -LiteralPath (Join-Path $temp "audit.jsonl") |
        ForEach-Object { $_ | ConvertFrom-Json })
    $lastAudit = $auditRecords[-1]
    if ($auditRecords.Count -lt 6 -or
        [string]::IsNullOrWhiteSpace($lastAudit.hash) -or
        [string]::IsNullOrWhiteSpace($lastAudit.signature)) {
        throw "Broker audit chain evidence was incomplete"
    }
    $summary = [ordered]@{
        policy_digest = $validated.policy_digest
        broker_instance = $brokerInitial.instance_id
        broker_key_id = $brokerInitial.key_id
        broker_capabilities = $brokerInitial.capabilities.Count
        approval_authorities = $brokerInitial.approval_authorities.Count
        approval_key_id = $approvalProof.key_id
        approval_proof_id = $approvalProof.claims.proof_id
        run_id = $final.id
        run_status = $final.status
        approval_ref = $receipt.approval_ref
        receipt_execution_id = $receipt.execution_id
        receipt_signature_present = -not [string]::IsNullOrWhiteSpace($revealed.payload.receipt.signature)
        receipt_stdout_sha256 = $receipt.stdout_sha256
        stop_generation = $stopped.generation
        broker_stopped = $brokerStopped.stopped
        resume_generation = $resumed.generation
        independent_resume_generation = $independentResume.generation
        checkpoint_present = Test-Path (Join-Path $temp "checkpoint.json")
        audit_records = $auditRecords.Count
        audit_chain_tail = $lastAudit.hash
    }
}
catch {
    $brokerError = if (Test-Path (Join-Path $temp "broker.err.log")) {
        Get-Content (Join-Path $temp "broker.err.log") -Raw
    } else { "" }
    $serverError = if (Test-Path (Join-Path $temp "server.err.log")) {
        Get-Content (Join-Path $temp "server.err.log") -Raw
    } else { "" }
    throw ("{0}`nBroker log: {1}`nServer log: {2}" -f $_.Exception.Message, $brokerError, $serverError)
}
finally {
    if ($null -ne $serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -Force
    }
    if ($null -ne $brokerProcess -and -not $brokerProcess.HasExited) {
        Stop-Process -Id $brokerProcess.Id -Force
    }
    $resolvedTemp = [IO.Path]::GetFullPath($temp)
    $tempRoot = [IO.Path]::GetFullPath($env:TEMP)
    if ($resolvedTemp.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path $resolvedTemp -Leaf).StartsWith("steward-r33-validation-")) {
        Remove-Item -LiteralPath $resolvedTemp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$summary | ConvertTo-Json -Depth 5
