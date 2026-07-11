param(
  [string]$BinaryPath = "",

  [string]$EvidenceDir = "",

  [int]$BaseManagementPort = 19180,

  [int]$BasePeerPort = 19181,

  [int]$BaseDiscoveryPort = 19281,

  [int]$PostgresHostPort = 5432,

  [int]$StartupTimeoutSeconds = 60,

  [string]$SyncKeyID = "local-mesh-sync-v1",

  [switch]$SkipStartPostgres,

  [switch]$KeepDatabases
)

$ErrorActionPreference = "Stop"
$PathSeparators = @([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)

function Require-Command {
  param([string]$Name)
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "Missing required command: $Name"
  }
}

function Resolve-RepoPath {
  param([string]$Path)
  return (Resolve-Path -LiteralPath $Path).Path
}

function Assert-ChildPath {
  param(
    [string]$Parent,
    [string]$Child,
    [string]$Label
  )
  $parentFull = [System.IO.Path]::GetFullPath($Parent).TrimEnd($PathSeparators)
  $childFull = [System.IO.Path]::GetFullPath($Child).TrimEnd($PathSeparators)
  $comparison = [System.StringComparison]::OrdinalIgnoreCase
  if (-not ($childFull.StartsWith($parentFull + [System.IO.Path]::DirectorySeparatorChar, $comparison) -or $childFull.StartsWith($parentFull + [System.IO.Path]::AltDirectorySeparatorChar, $comparison))) {
    throw "$Label is outside repository: $childFull"
  }
}

function Get-HostPlatform {
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
    return "windows"
  }
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)) {
    return "darwin"
  }
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)) {
    return "linux"
  }
  return "unknown"
}

function Get-HostArch {
  $arch = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString().ToLowerInvariant()
  switch ($arch) {
    "x64" { return "amd64" }
    "arm64" { return "arm64" }
    default { return $arch }
  }
}

function New-Timestamp {
  return (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmss.fffffffZ")
}

function New-Secret {
  $bytes = New-Object byte[] 32
  [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
  return [Convert]::ToBase64String($bytes)
}

function New-UniquePath {
  param(
    [string]$Directory,
    [string]$BaseName,
    [string]$Suffix
  )
  $path = Join-Path $Directory ($BaseName + $Suffix)
  for ($attempt = 2; Test-Path -LiteralPath $path; $attempt++) {
    $path = Join-Path $Directory ("$BaseName-$('{0:D2}' -f $attempt)$Suffix")
  }
  return $path
}

function Quote-Arg {
  param([string]$Value)
  if ($Value -notmatch '[\s"]') {
    return $Value
  }
  return '"' + ($Value -replace '"', '\"') + '"'
}

function Add-Check {
  param(
    [System.Collections.ArrayList]$Checks,
    [string]$ID,
    [string]$Status,
    [string]$Message,
    [object]$Detail = $null
  )
  $check = [ordered]@{
    id = $ID
    status = $Status
    message = $Message
  }
  if ($null -ne $Detail) {
    $check.detail = $Detail
  }
  [void]$Checks.Add([pscustomobject]$check)
}

function Invoke-DockerCompose {
  param([string[]]$Arguments)
  $output = & docker @Arguments 2>&1
  return [pscustomobject]@{
    exit_code = $LASTEXITCODE
    output = @($output | ForEach-Object { "$_" })
  }
}

function Start-ComposePostgres {
  param(
    [string]$RepoRoot,
    [int]$TimeoutSeconds,
    [System.Collections.ArrayList]$Checks
  )
  Require-Command "docker"
  Push-Location $RepoRoot
  try {
    $up = Invoke-DockerCompose @("compose", "up", "-d", "postgres")
    if ($up.exit_code -ne 0) {
      Add-Check $Checks "local_mesh.compose_up" "error" "docker compose up -d postgres failed" @{ exit_code = $up.exit_code; output = $up.output }
      throw "docker compose up -d postgres failed with exit code $($up.exit_code)"
    }
    Add-Check $Checks "local_mesh.compose_up" "ok" "postgres compose service start requested" $null

    $deadline = (Get-Date).ToUniversalTime().AddSeconds($TimeoutSeconds)
    $lastOutput = @()
    while ((Get-Date).ToUniversalTime() -lt $deadline) {
      $ready = Invoke-DockerCompose @("compose", "exec", "-T", "postgres", "pg_isready", "-U", "postgres", "-d", "mongojson")
      $lastOutput = $ready.output
      if ($ready.exit_code -eq 0) {
        Add-Check $Checks "local_mesh.postgres_ready" "ok" "postgres compose service is ready" $null
        return
      }
      Start-Sleep -Seconds 2
    }
    Add-Check $Checks "local_mesh.postgres_ready" "error" "postgres compose service did not become ready before timeout" @{ output = $lastOutput }
    throw "postgres compose service did not become ready before timeout"
  } finally {
    Pop-Location
  }
}

function Invoke-PostgresSQL {
  param(
    [string]$RepoRoot,
    [string]$SQL
  )
  Push-Location $RepoRoot
  try {
    $output = & docker compose exec -T postgres psql -U postgres -d postgres -v ON_ERROR_STOP=1 -c $SQL 2>&1
    if ($LASTEXITCODE -ne 0) {
      throw "psql failed with exit code $LASTEXITCODE`: $($output -join "`n")"
    }
  } finally {
    Pop-Location
  }
}

function Initialize-MeshDatabases {
  param(
    [string]$RepoRoot,
    [object[]]$Nodes,
    [System.Collections.ArrayList]$Checks
  )
  foreach ($node in $Nodes) {
    Invoke-PostgresSQL -RepoRoot $RepoRoot -SQL "drop database if exists $($node.database) with (force);"
    Invoke-PostgresSQL -RepoRoot $RepoRoot -SQL "create database $($node.database);"
  }
  Add-Check $Checks "local_mesh.databases" "ok" "temporary Postgres databases created" @{
    databases = @($Nodes | ForEach-Object { $_.database })
  }
}

function Remove-MeshDatabases {
  param(
    [string]$RepoRoot,
    [object[]]$Nodes
  )
  foreach ($node in $Nodes) {
    try {
      Invoke-PostgresSQL -RepoRoot $RepoRoot -SQL "drop database if exists $($node.database) with (force);"
    } catch {
    }
  }
}

function Invoke-StewardCommand {
  param(
    [string]$BinaryPath,
    [string[]]$Arguments
  )
  $output = & $BinaryPath @Arguments 2>&1
  return [pscustomobject]@{
    exit_code = $LASTEXITCODE
    output = @($output | ForEach-Object { "$_" })
    text = (($output | ForEach-Object { "$_" }) -join "`n")
  }
}

function Invoke-StewardJSON {
  param(
    [string]$BinaryPath,
    [string[]]$Arguments
  )
  $result = Invoke-StewardCommand -BinaryPath $BinaryPath -Arguments $Arguments
  if ($result.exit_code -ne 0) {
    throw "steward $($Arguments -join ' ') failed with exit code $($result.exit_code): $($result.text)"
  }
  try {
    return $result.text | ConvertFrom-Json
  } catch {
    throw "steward $($Arguments -join ' ') did not return JSON: $($result.text)"
  }
}

function Get-OrBuild-Binary {
  param(
    [string]$RepoRoot,
    [string]$BackendDir,
    [string]$EvidenceRoot,
    [string]$BinaryPath
  )
  if (-not [string]::IsNullOrWhiteSpace($BinaryPath)) {
    return (Resolve-Path -LiteralPath $BinaryPath).Path
  }

  Require-Command "go"
  $extension = ""
  if ((Get-HostPlatform) -eq "windows") {
    $extension = ".exe"
  }
  $binaryDir = Join-Path $EvidenceRoot "bin"
  New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null
  $outputPath = Join-Path $binaryDir ("steward-local-mesh-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)
  Assert-ChildPath -Parent $RepoRoot -Child $outputPath -Label "Local mesh binary"

  Push-Location $BackendDir
  try {
    go build -trimpath -o $outputPath ./cmd/steward
    if ($LASTEXITCODE -ne 0) {
      throw "go build ./cmd/steward failed with exit code $LASTEXITCODE"
    }
  } finally {
    Pop-Location
  }
  return $outputPath
}

function New-MeshNodes {
  param(
    [string]$RunID,
    [int]$BaseManagementPort,
    [int]$BasePeerPort,
    [int]$BaseDiscoveryPort,
    [int]$PostgresHostPort,
    [string]$EvidenceRoot
  )
  $definitions = @(
    @{ id = "windows-main"; name = "Windows Main"; logical_platform = "windows"; offset = 0; local_key_id = "windows-local-v1" },
    @{ id = "macbook-main"; name = "MacBook Main"; logical_platform = "darwin"; offset = 10; local_key_id = "macbook-local-v1" },
    @{ id = "linux-lab"; name = "Linux Lab"; logical_platform = "linux"; offset = 20; local_key_id = "linux-local-v1" }
  )
  $nodes = @()
  foreach ($definition in $definitions) {
    $managementPort = $BaseManagementPort + [int]$definition.offset
    $peerPort = $BasePeerPort + [int]$definition.offset
    $discoveryPort = $BaseDiscoveryPort + [int]$definition.offset
    $nodeRoot = Join-Path $EvidenceRoot ("node-" + $definition.id)
    $storageDir = Join-Path $nodeRoot "data"
    $logDir = Join-Path $nodeRoot "logs"
    New-Item -ItemType Directory -Force -Path $storageDir, $logDir | Out-Null
    $database = ("steward_mesh_" + $RunID + "_" + ($definition.id -replace "-", "_")).ToLowerInvariant()
    $nodes += [pscustomobject]@{
      id = $definition.id
      name = $definition.name
      logical_platform = $definition.logical_platform
      management_port = $managementPort
      peer_port = $peerPort
      api_base = "http://127.0.0.1:$managementPort/api"
      ready_url = "http://127.0.0.1:$managementPort/readyz"
      peer_api_base = "http://127.0.0.1:$peerPort/api"
      http_addr = "127.0.0.1:$managementPort"
      peer_http_addr = "127.0.0.1:$peerPort"
      discovery_listen_addr = "127.0.0.1:$discoveryPort"
      database = $database
      database_url = "postgres://postgres:postgres@localhost:$PostgresHostPort/$database`?sslmode=disable"
      root = $nodeRoot
      storage_dir = $storageDir
      log_dir = $logDir
      local_key_id = $definition.local_key_id
      process = $null
      stdout_task = $null
      stderr_task = $null
      public_key = ""
      private_key = ""
      local_key = ""
    }
  }
  return $nodes
}

function Start-StewardNode {
  param(
    [string]$BinaryPath,
    [object]$Node,
    [hashtable]$SharedEnv
  )
  $arguments = @("run", "--workdir", $Node.root, "--log-dir", $Node.log_dir, "--service-name", $Node.id)
  $psi = [System.Diagnostics.ProcessStartInfo]::new()
  $psi.FileName = $BinaryPath
  $psi.Arguments = (($arguments | ForEach-Object { Quote-Arg $_ }) -join " ")
  $psi.WorkingDirectory = $Node.root
  $psi.UseShellExecute = $false
  $psi.CreateNoWindow = $true
  $psi.RedirectStandardOutput = $true
  $psi.RedirectStandardError = $true

  $env = $psi.EnvironmentVariables
  foreach ($key in $SharedEnv.Keys) {
    $env[$key] = [string]$SharedEnv[$key]
  }
  $env["HTTP_ADDR"] = $Node.http_addr
  $env["STEWARD_PEER_HTTP_ADDR"] = $Node.peer_http_addr
  $env["DATABASE_URL"] = $Node.database_url
  $env["STORAGE_DIR"] = $Node.storage_dir
  $env["STEWARD_AGENT_ID"] = $Node.id
  $env["STEWARD_PUBLIC_API_BASE"] = $Node.peer_api_base
  $env["STEWARD_DEVICE_NAME"] = $Node.name
  $env["STEWARD_DISCOVERY_LISTEN_ADDR"] = $Node.discovery_listen_addr
  $env["STEWARD_DEVICE_PRIVATE_KEY"] = $Node.private_key
  $env["STEWARD_DEVICE_PUBLIC_KEY"] = $Node.public_key
  $env["STEWARD_LOCAL_ENCRYPTION_KEY"] = $Node.local_key
  $env["STEWARD_LOCAL_ENCRYPTION_KEY_ID"] = $Node.local_key_id

  $process = [System.Diagnostics.Process]::Start($psi)
  $Node.process = $process
  $Node.stdout_task = $process.StandardOutput.ReadToEndAsync()
  $Node.stderr_task = $process.StandardError.ReadToEndAsync()
}

function Save-StewardNodeOutput {
  param([object]$Node)
  foreach ($stream in @("stdout", "stderr")) {
    $taskProperty = $stream + "_task"
    $task = $Node.$taskProperty
    if ($null -eq $task) {
      continue
    }
    try {
      $content = $task.GetAwaiter().GetResult()
      if (-not [string]::IsNullOrWhiteSpace($content)) {
        Add-Content -LiteralPath (Join-Path $Node.log_dir ($Node.id + "." + $stream + ".log")) -Value $content.TrimEnd() -Encoding UTF8
      }
    } catch {
    } finally {
      $Node.$taskProperty = $null
    }
  }
}

function Stop-StewardNode {
  param([object]$Node)
  $process = $Node.process
  if ($null -eq $process) {
    return
  }
  try {
    if (-not $process.HasExited) {
      $process.Kill()
    }
    $process.WaitForExit(5000) | Out-Null
  } catch {
  } finally {
    Save-StewardNodeOutput -Node $Node
    $process.Dispose()
    $Node.process = $null
  }
}

function Stop-StewardNodes {
  param([object[]]$Nodes)
  foreach ($node in $Nodes) {
    Stop-StewardNode -Node $node
  }
}

function Get-NodeLogTail {
  param([object]$Node)
  $path = Join-Path $Node.log_dir ($Node.id + ".log")
  if (-not (Test-Path -LiteralPath $path)) {
    return @()
  }
  return @(Get-Content -LiteralPath $path -Tail 30)
}

function Wait-NodeReady {
  param(
    [object]$Node,
    [int]$TimeoutSeconds
  )
  $deadline = (Get-Date).ToUniversalTime().AddSeconds($TimeoutSeconds)
  $lastError = ""
  while ((Get-Date).ToUniversalTime() -lt $deadline) {
    if ($null -ne $Node.process -and $Node.process.HasExited) {
      throw "node $($Node.id) exited before ready; log tail: $((Get-NodeLogTail $Node) -join " | ")"
    }
    try {
      $response = Invoke-RestMethod -Method Get -Uri $Node.ready_url -TimeoutSec 2
      if ($response.status -eq "ok" -or $response.status -eq "ready") {
        return
      }
      $lastError = ($response | ConvertTo-Json -Depth 5)
    } catch {
      $lastError = $_.Exception.Message
    }
    Start-Sleep -Milliseconds 500
  }
  throw "node $($Node.id) did not become ready before timeout: $lastError"
}

function Invoke-API {
  param(
    [string]$Method,
    [string]$URL,
    [object]$Body = $null
  )
  if ($null -eq $Body) {
    return Invoke-RestMethod -Method $Method -Uri $URL -TimeoutSec 10
  }
  $json = $Body | ConvertTo-Json -Depth 8
  return Invoke-RestMethod -Method $Method -Uri $URL -ContentType "application/json" -Body $json -TimeoutSec 10
}

function Register-MeshPeers {
  param(
    [object[]]$Nodes,
    [System.Collections.ArrayList]$Checks
  )
  foreach ($node in $Nodes) {
    foreach ($peer in $Nodes) {
      if ($node.id -eq $peer.id) {
        continue
      }
      $body = @{
        id = $peer.id
        device_name = $peer.name
        platform = $peer.logical_platform
        role = "peer"
        api_base_url = $peer.peer_api_base
        public_key = $peer.public_key
        sync_enabled = $true
        permission_level = "A3"
      }
      Invoke-API -Method Post -URL ($node.api_base + "/steward/devices") -Body $body | Out-Null
    }
  }
  Add-Check $Checks "local_mesh.peer_registration" "ok" "all local mesh nodes registered each other as trusted peers" $null
}

function Run-MeshVerification {
  param(
    [string]$BinaryPath,
    [object[]]$Nodes,
    [string]$SyncKeyID,
    [string]$EvidenceDir
  )
  $args = @("verify", "mesh", "--strict-security", "--strict", "--require-peers", "--sync", "--write-probes", "--evidence-dir", $EvidenceDir)
  foreach ($node in $Nodes) {
    $args += @("--node", $node.api_base)
  }
  foreach ($node in $Nodes) {
    $args += @("--expect-agent-id", $node.id)
  }
  $args += @("--expect-agent-platform", (Get-HostPlatform))
  $args += @("--expect-sync-key-id", $SyncKeyID)
  foreach ($node in $Nodes) {
    $args += @("--expect-local-key-id", $node.local_key_id)
  }
  return Invoke-StewardCommand -BinaryPath $BinaryPath -Arguments $args
}

function Wait-MeshDiscovery {
  param(
    [object[]]$Nodes,
    [int]$TimeoutSeconds
  )
  $deadline = (Get-Date).ToUniversalTime().AddSeconds($TimeoutSeconds)
  $lastState = @{}
  while ((Get-Date).ToUniversalTime() -lt $deadline) {
    $allReady = $true
    foreach ($node in $Nodes) {
      try {
        $response = Invoke-API -Method Get -URL ($node.api_base + "/steward/sync/status")
        $discovery = $response.sync.discovery
        $candidates = @($response.sync.discovered_peers)
        $candidateIDs = @($candidates | ForEach-Object { $_.device_id } | Sort-Object -Unique)
        $verified = @($candidates | Where-Object { -not $_.signature_verified }).Count -eq 0
        $lastState[$node.id] = @{
          running = $discovery.running
          candidate_ids = $candidateIDs
          rejected = $discovery.rejected_announcements
        }
        if (-not $discovery.enabled -or -not $discovery.running -or -not $verified -or $candidateIDs.Count -lt ($Nodes.Count - 1)) {
          $allReady = $false
        }
      } catch {
        $allReady = $false
        $lastState[$node.id] = @{ error = $_.Exception.Message }
      }
    }
    if ($allReady) {
      return [pscustomobject]@{ nodes = $lastState }
    }
    Start-Sleep -Milliseconds 200
  }
  throw "mesh discovery did not converge before timeout: $($lastState | ConvertTo-Json -Depth 8 -Compress)"
}

function Run-OfflineCatchUpProbe {
  param(
    [string]$BinaryPath,
    [object]$SourceNode,
    [object]$OfflineNode,
    [hashtable]$SharedEnv,
    [int]$StartupTimeoutSeconds
  )

  Stop-StewardNode -Node $OfflineNode
  Start-Sleep -Milliseconds 500

  $taskResponse = Invoke-API -Method Post -URL ($SourceNode.api_base + "/steward/tasks") -Body @{
    type = "verification_offline_catch_up"
    title = "local mesh offline catch-up probe"
    description = "created while $($OfflineNode.id) was offline"
    priority = "normal"
    source = "manual"
    data_level = "D0"
    permission_level = "A3"
    risk_level = "low"
    user_confirmed = $true
  }
  $taskID = $taskResponse.task.id
  if ([string]::IsNullOrWhiteSpace($taskID)) {
    throw "offline catch-up task response did not include task.id"
  }

  Start-StewardNode -BinaryPath $BinaryPath -Node $OfflineNode -SharedEnv $SharedEnv
  Wait-NodeReady -Node $OfflineNode -TimeoutSeconds $StartupTimeoutSeconds

  $syncResponse = Invoke-API -Method Post -URL ($SourceNode.api_base + "/steward/devices/$($OfflineNode.id)/sync")
  $syncErrors = @($syncResponse.sync.errors)
  if ($syncErrors.Count -gt 0) {
    throw "offline catch-up sync reported errors: $($syncErrors -join '; ')"
  }

  $tasksResponse = Invoke-API -Method Get -URL ($OfflineNode.api_base + "/steward/tasks?limit=100")
  $replicatedTask = @($tasksResponse.tasks | Where-Object { $_.id -eq $taskID } | Select-Object -First 1)
  if ($replicatedTask.Count -ne 1) {
    throw "offline catch-up task $taskID was not visible on recovered node $($OfflineNode.id)"
  }

  return [pscustomobject]@{
    source_agent_id = $SourceNode.id
    recovered_agent_id = $OfflineNode.id
    task_id = $taskID
    pulled = $syncResponse.sync.pulled
    pushed = $syncResponse.sync.pushed
    imported = $syncResponse.sync.imported
    applied = $syncResponse.sync.applied
    remote_visible = $true
  }
}

function Run-AutonomyExecutionProbe {
  param([object]$Node)
  $proposalBody = @{
    action = "create_local_task"
    title = "local mesh S4 execution probe"
    summary = "created by local mesh runner"
    trigger_reason = "verify real-process S4 execution"
    suggested_action = "create one low-risk local task"
    impact_summary = "creates one local task only"
    risk_level = "low"
    permission_level = "A3"
    data_level = "D0"
    policy = "auto"
    source_entity_type = "verification"
  }
  $proposalResponse = Invoke-API -Method Post -URL ($Node.api_base + "/steward/autonomy/proposals") -Body $proposalBody
  $proposalID = $proposalResponse.proposal.id
  if ([string]::IsNullOrWhiteSpace($proposalID)) {
    throw "autonomy proposal response did not include proposal.id"
  }
  $simulate = Invoke-API -Method Post -URL ($Node.api_base + "/steward/autonomy/proposals/$proposalID/simulate")
  if ($simulate.run.status -ne "success") {
    throw "autonomy simulation did not succeed: $($simulate | ConvertTo-Json -Depth 8)"
  }
  $execute = Invoke-API -Method Post -URL ($Node.api_base + "/steward/autonomy/proposals/$proposalID/execute")
  if ($execute.run.status -ne "success") {
    throw "autonomy execution did not succeed: $($execute | ConvertTo-Json -Depth 8)"
  }
  return [pscustomobject]@{
    proposal_id = $proposalID
    simulation_status = $simulate.run.status
    execution_status = $execute.run.status
    impact_summary = $execute.run.impact_summary
  }
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-local-mesh"
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$timestamp = New-Timestamp
$runID = ($timestamp -replace '[^0-9]', '').Substring(0, 14)
$startedAt = (Get-Date).ToUniversalTime()
$checks = New-Object System.Collections.ArrayList
$nodes = @()
$binary = ""
$meshResult = $null
$discoveryProbe = $null
$offlineCatchUpProbe = $null
$autonomyProbe = $null
$errorMessage = ""

try {
  if (-not $SkipStartPostgres) {
    Start-ComposePostgres -RepoRoot $repoRoot -TimeoutSeconds $StartupTimeoutSeconds -Checks $checks
  }

  $binary = Get-OrBuild-Binary -RepoRoot $repoRoot -BackendDir $backendDir -EvidenceRoot $evidenceRoot -BinaryPath $BinaryPath
  Add-Check $checks "local_mesh.binary" "ok" "steward local mesh binary is available" @{ path = $binary }
  Invoke-StewardJSON -BinaryPath $binary -Arguments @("version") | Out-Null
  Add-Check $checks "local_mesh.version" "ok" "steward binary returned version metadata" $null

  $nodes = @(New-MeshNodes -RunID $runID -BaseManagementPort $BaseManagementPort -BasePeerPort $BasePeerPort -BaseDiscoveryPort $BaseDiscoveryPort -PostgresHostPort $PostgresHostPort -EvidenceRoot $evidenceRoot)
  Initialize-MeshDatabases -RepoRoot $repoRoot -Nodes $nodes -Checks $checks

  $syncKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $SyncKeyID)
  $sharedEnv = @{
    "STEWARD_SYNC_SECRET" = New-Secret
    "STEWARD_SYNC_REQUIRE_AUTH" = "true"
    "STEWARD_SYNC_ALLOW_INSECURE" = "false"
    "STEWARD_SYNC_ENCRYPTION_KEY" = $syncKey.key
    "STEWARD_SYNC_ENCRYPTION_KEY_ID" = $SyncKeyID
    "STEWARD_HEARTBEAT_INTERVAL" = "1s"
    "STEWARD_SYNC_INTERVAL" = "2s"
    "STEWARD_AUTONOMY_INTERVAL" = "2s"
    "STEWARD_AUTONOMY_LIMIT" = "12"
    "STEWARD_LLM_PROVIDER" = "disabled"
    "STEWARD_DISCOVERY_ENABLED" = "true"
    "STEWARD_DISCOVERY_TARGETS" = (($nodes | ForEach-Object { $_.discovery_listen_addr }) -join ",")
    "STEWARD_DISCOVERY_INTERVAL" = "500ms"
    "STEWARD_DISCOVERY_TTL" = "3s"
  }

  foreach ($node in $nodes) {
    $keypair = Invoke-StewardJSON -BinaryPath $binary -Arguments @("keygen", "--prefix", $node.id)
    $localKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $node.local_key_id)
    $node.public_key = $keypair.public_key
    $node.private_key = $keypair.private_key
    $node.local_key = $localKey.key
    Start-StewardNode -BinaryPath $binary -Node $node -SharedEnv $sharedEnv
  }
  Add-Check $checks "local_mesh.processes_started" "ok" "three steward run processes started" @{
    nodes = @($nodes | ForEach-Object { @{ id = $_.id; api_base = $_.api_base; peer_api_base = $_.peer_api_base } })
  }

  foreach ($node in $nodes) {
    Wait-NodeReady -Node $node -TimeoutSeconds $StartupTimeoutSeconds
  }
  Add-Check $checks "local_mesh.ready" "ok" "all local mesh management APIs reported ready" $null

  $discoveryProbe = Wait-MeshDiscovery -Nodes $nodes -TimeoutSeconds $StartupTimeoutSeconds
  Add-Check $checks "local_mesh.peer_discovery" "ok" "all local mesh nodes discovered the other signed candidates before manual registration" $discoveryProbe

  Register-MeshPeers -Nodes $nodes -Checks $checks

  $meshEvidenceDir = Join-Path $evidenceRoot "mesh-evidence"
  New-Item -ItemType Directory -Force -Path $meshEvidenceDir | Out-Null
  $meshResult = Run-MeshVerification -BinaryPath $binary -Nodes $nodes -SyncKeyID $SyncKeyID -EvidenceDir $meshEvidenceDir
  if ($meshResult.exit_code -eq 0) {
    Add-Check $checks "local_mesh.verify_mesh" "ok" "steward verify mesh passed against real local processes" @{ evidence_dir = $meshEvidenceDir }
  } else {
    Add-Check $checks "local_mesh.verify_mesh" "error" "steward verify mesh failed" @{ exit_code = $meshResult.exit_code; output = $meshResult.output }
  }

  $offlineCatchUpProbe = Run-OfflineCatchUpProbe -BinaryPath $binary -SourceNode $nodes[0] -OfflineNode $nodes[2] -SharedEnv $sharedEnv -StartupTimeoutSeconds $StartupTimeoutSeconds
  Add-Check $checks "local_mesh.offline_catch_up" "ok" "a task created while one peer was offline became visible after that peer restarted and synchronized" $offlineCatchUpProbe

  $autonomyProbe = Run-AutonomyExecutionProbe -Node $nodes[0]
  Add-Check $checks "local_mesh.autonomy_execute" "ok" "S4 low-risk autonomy proposal simulated and executed through real management API" $autonomyProbe
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "local_mesh.runner" "error" $errorMessage $null
} finally {
  Stop-StewardNodes -Nodes $nodes
  if (-not $KeepDatabases -and $nodes.Count -gt 0) {
    Remove-MeshDatabases -RepoRoot $repoRoot -Nodes $nodes
  }
}

$completedAt = (Get-Date).ToUniversalTime()
$hasFailingCheck = $false
foreach ($check in $checks) {
  if ($check.status -ne "ok") {
    $hasFailingCheck = $true
  }
}
$ok = ($errorMessage -eq "" -and -not $hasFailingCheck)
$status = "fail"
if ($ok) {
  $status = "pass"
}
$evidencePath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-verify-local-mesh-$timestamp" -Suffix "-$status.json"

$payload = [ordered]@{
  verification = [ordered]@{
    ok = $ok
    platform = Get-HostPlatform
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    binary_path = $binary
    sync_key_id = $SyncKeyID
    error = $errorMessage
    nodes = @($nodes | ForEach-Object {
      [ordered]@{
        agent_id = $_.id
        logical_platform = $_.logical_platform
        api_base = $_.api_base
        peer_api_base = $_.peer_api_base
        discovery_listen_addr = $_.discovery_listen_addr
        database = $_.database
        local_key_id = $_.local_key_id
        log_dir = $_.log_dir
      }
    })
    mesh_exit_code = if ($null -ne $meshResult) { $meshResult.exit_code } else { $null }
    mesh_output = if ($null -ne $meshResult) { $meshResult.output } else { $null }
    discovery_probe = $discoveryProbe
    offline_catch_up_probe = $offlineCatchUpProbe
    autonomy_probe = $autonomyProbe
    checks = @($checks)
  }
}

$command = @(
  "deploy/run-steward-local-mesh.ps1",
  "-BaseManagementPort", "$BaseManagementPort",
  "-BasePeerPort", "$BasePeerPort",
  "-BaseDiscoveryPort", "$BaseDiscoveryPort",
  "-SyncKeyID", $SyncKeyID
)

$envelope = [ordered]@{
  kind = "local-mesh"
  ok = $ok
  command = $command
  created_at = $startedAt.ToString("o")
  payload = $payload
}
$envelope | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $evidencePath -Encoding UTF8

$summary = [ordered]@{
  ok = $ok
  platform = Get-HostPlatform
  evidence_path = $evidencePath
  binary_path = $binary
  nodes = @($nodes | ForEach-Object { $_.api_base })
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 6

if (-not $ok) {
  exit 1
}
