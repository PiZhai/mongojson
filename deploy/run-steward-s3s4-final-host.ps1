param(
  [string]$BinaryPath = "",

  [string]$EvidenceDir = "",

  [string]$ServiceName = "",

  [string]$ServiceScope = "",

  [string]$APIBase = "http://127.0.0.1:18080/api",

  [string[]]$Node = @(),

  [string]$LocalAgentID = "",

  [string]$LocalAgentVersion = "",

  [string]$LocalPlatform = "",

  [string]$LocalSyncKeyID = "",

  [string]$LocalLocalKeyID = "",

  [string[]]$ExpectedAgentIDs = @(),

  [string[]]$ExpectedPlatforms = @("windows", "darwin", "linux"),

  [string[]]$ExpectedAgentVersions = @(),

  [string[]]$ExpectedSyncKeyIDs = @(),

  [string[]]$ExpectedLocalKeyIDs = @(),

  [string]$ExpectedAdvisorProvider = "",

  [string]$ExpectedAdvisorModel = "",

  [string]$ExpectedAdvisorMaxDataLevel = "",

  [string]$WatchDuration = "24h",

  [string]$WatchInterval = "5m",

  [switch]$AdvisorProbeEachSample,

  [switch]$SkipAdvisorProbe,

  [switch]$SkipAdvisorPrivacyProbe,

  [switch]$SkipService,

  [switch]$SkipMesh,

  [switch]$SkipLocalManifest,

  [switch]$AllowIncompleteMesh,

  [switch]$AllowUserServiceScope,

  [switch]$PlanOnly
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

function Get-DefaultServiceName {
  param([string]$Platform)
  switch ($Platform) {
    "windows" { return "MongojsonSteward" }
    "darwin" { return "com.mongojson.steward" }
    "linux" { return "mongojson-steward" }
    default { return "mongojson-steward" }
  }
}

function Get-DefaultServiceScope {
  param([string]$Platform)
  if ($Platform -eq "windows") {
    return "system"
  }
  return "user"
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

function Add-Arg {
  param(
    [System.Collections.ArrayList]$List,
    [string]$Name,
    [string]$Value = $null
  )
  [void]$List.Add($Name)
  if (-not [string]::IsNullOrEmpty($Value)) {
    [void]$List.Add($Value)
  }
}

function Add-RepeatArgs {
  param(
    [System.Collections.ArrayList]$List,
    [string]$Name,
    [string[]]$Values
  )
  foreach ($value in $Values) {
    if (-not [string]::IsNullOrWhiteSpace($value)) {
      Add-Arg $List $Name $value.Trim()
    }
  }
}

function Normalize-NonEmptyValues {
  param(
    [string[]]$Values,
    [switch]$Lowercase
  )
  $normalized = @()
  foreach ($value in $Values) {
    if ([string]::IsNullOrWhiteSpace($value)) {
      continue
    }
    $item = $value.Trim()
    if ($Lowercase) {
      $item = $item.ToLowerInvariant()
    }
    $normalized += $item
  }
  return [string[]]$normalized
}

function Resolve-ExpectedValueForLocalPlatform {
  param(
    [string]$CurrentValue,
    [string[]]$Values,
    [string[]]$Platforms,
    [string]$LocalPlatform
  )
  if (-not [string]::IsNullOrWhiteSpace($CurrentValue)) {
    return $CurrentValue.Trim()
  }
  if ($Values.Count -eq 1 -and -not [string]::IsNullOrWhiteSpace($Values[0])) {
    return $Values[0].Trim()
  }
  $local = $LocalPlatform.Trim().ToLowerInvariant()
  for ($i = 0; $i -lt $Values.Count -and $i -lt $Platforms.Count; $i++) {
    if ([string]::IsNullOrWhiteSpace($Platforms[$i]) -or [string]::IsNullOrWhiteSpace($Values[$i])) {
      continue
    }
    if ($Platforms[$i].Trim().ToLowerInvariant() -eq $local) {
      return $Values[$i].Trim()
    }
  }
  return ""
}

function Convert-CommandJson {
  param([string]$Text)
  if ([string]::IsNullOrWhiteSpace($Text)) {
    return $null
  }
  try {
    return $Text | ConvertFrom-Json
  } catch {
    return $null
  }
}

function Invoke-StewardCommand {
  param(
    [string]$BinaryPath,
    [string[]]$Arguments
  )
  $startedAt = (Get-Date).ToUniversalTime()
  $output = & $BinaryPath @Arguments 2>&1
  $completedAt = (Get-Date).ToUniversalTime()
  $text = ($output | ForEach-Object { "$_" }) -join "`n"
  return [pscustomobject]@{
    exit_code = $LASTEXITCODE
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    command = @($BinaryPath) + @($Arguments)
    output = @($output | ForEach-Object { "$_" })
    json = Convert-CommandJson $text
  }
}

function Get-OrBuild-Binary {
  param(
    [string]$RepoRoot,
    [string]$BackendDir,
    [string]$RunRoot,
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
  $binaryDir = Join-Path $RunRoot "bin"
  New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null
  $outputPath = Join-Path $binaryDir ("steward-s3s4-final-host-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)

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

function New-ServiceVerifyArgs {
  param(
    [string]$APIBase,
    [string]$ServiceName,
    [string]$ServiceScope,
    [string]$EvidenceDir,
    [string]$LocalAgentID,
    [string]$LocalPlatform,
    [string]$LocalAgentVersion,
    [string]$LocalSyncKeyID,
    [string]$LocalLocalKeyID,
    [string]$ExpectedAdvisorProvider,
    [string]$ExpectedAdvisorModel,
    [string]$ExpectedAdvisorMaxDataLevel,
    [string]$WatchDuration,
    [string]$WatchInterval
  )
  $commandArgs = New-Object System.Collections.ArrayList
  Add-Arg $commandArgs "--api" $APIBase
  Add-Arg $commandArgs "verify"
  Add-Arg $commandArgs "service"
  if (-not [string]::IsNullOrWhiteSpace($ServiceName)) {
    Add-Arg $commandArgs "--name" $ServiceName
  }
  if (-not [string]::IsNullOrWhiteSpace($ServiceScope)) {
    Add-Arg $commandArgs "--scope" $ServiceScope
  }
  Add-Arg $commandArgs "--strict-security"
  Add-Arg $commandArgs "--watch-duration" $WatchDuration
  Add-Arg $commandArgs "--watch-interval" $WatchInterval
  Add-Arg $commandArgs "--evidence-dir" $EvidenceDir
  if (-not $SkipAdvisorProbe) {
    Add-Arg $commandArgs "--advisor-probe"
  }
  if ($AdvisorProbeEachSample) {
    Add-Arg $commandArgs "--advisor-probe-each-sample"
  }
  if (-not $SkipAdvisorPrivacyProbe) {
    Add-Arg $commandArgs "--advisor-privacy-probe"
  }
  if (-not [string]::IsNullOrWhiteSpace($LocalAgentID)) {
    Add-Arg $commandArgs "--expect-agent-id" $LocalAgentID
  }
  if (-not [string]::IsNullOrWhiteSpace($LocalPlatform)) {
    Add-Arg $commandArgs "--expect-agent-platform" $LocalPlatform
  }
  if (-not [string]::IsNullOrWhiteSpace($LocalAgentVersion)) {
    Add-Arg $commandArgs "--expect-agent-version" $LocalAgentVersion
  }
  if (-not [string]::IsNullOrWhiteSpace($LocalSyncKeyID)) {
    Add-Arg $commandArgs "--expect-sync-key-id" $LocalSyncKeyID
  }
  if (-not [string]::IsNullOrWhiteSpace($LocalLocalKeyID)) {
    Add-Arg $commandArgs "--expect-local-key-id" $LocalLocalKeyID
  }
  if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorProvider)) {
    Add-Arg $commandArgs "--expect-advisor-provider" $ExpectedAdvisorProvider
  }
  if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorModel)) {
    Add-Arg $commandArgs "--expect-advisor-model" $ExpectedAdvisorModel
  }
  if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorMaxDataLevel)) {
    Add-Arg $commandArgs "--expect-advisor-max-data-level" $ExpectedAdvisorMaxDataLevel
  }
  return [string[]]$commandArgs
}

function New-MeshVerifyArgs {
  param(
    [string[]]$Nodes,
    [string]$EvidenceDir,
    [string[]]$ExpectedAgentIDs,
    [string[]]$ExpectedPlatforms,
    [string[]]$ExpectedAgentVersions,
    [string[]]$ExpectedSyncKeyIDs,
    [string[]]$ExpectedLocalKeyIDs,
    [string]$ExpectedAdvisorProvider,
    [string]$ExpectedAdvisorModel,
    [string]$ExpectedAdvisorMaxDataLevel,
    [string]$WatchDuration,
    [string]$WatchInterval
  )
  $commandArgs = New-Object System.Collections.ArrayList
  Add-Arg $commandArgs "verify"
  Add-Arg $commandArgs "mesh"
  Add-RepeatArgs $commandArgs "--node" $Nodes
  Add-Arg $commandArgs "--strict-security"
  Add-Arg $commandArgs "--strict"
  Add-Arg $commandArgs "--require-peers"
  Add-Arg $commandArgs "--sync"
  Add-Arg $commandArgs "--write-probes"
  Add-Arg $commandArgs "--watch-duration" $WatchDuration
  Add-Arg $commandArgs "--watch-interval" $WatchInterval
  Add-Arg $commandArgs "--evidence-dir" $EvidenceDir
  if (-not $SkipAdvisorProbe) {
    Add-Arg $commandArgs "--advisor-probe"
  }
  if ($AdvisorProbeEachSample) {
    Add-Arg $commandArgs "--advisor-probe-each-sample"
  }
  if (-not $SkipAdvisorPrivacyProbe) {
    Add-Arg $commandArgs "--advisor-privacy-probe"
  }
  Add-RepeatArgs $commandArgs "--expect-agent-id" $ExpectedAgentIDs
  Add-RepeatArgs $commandArgs "--expect-agent-platform" $ExpectedPlatforms
  Add-RepeatArgs $commandArgs "--expect-agent-version" $ExpectedAgentVersions
  Add-RepeatArgs $commandArgs "--expect-sync-key-id" $ExpectedSyncKeyIDs
  Add-RepeatArgs $commandArgs "--expect-local-key-id" $ExpectedLocalKeyIDs
  if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorProvider)) {
    Add-Arg $commandArgs "--expect-advisor-provider" $ExpectedAdvisorProvider
  }
  if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorModel)) {
    Add-Arg $commandArgs "--expect-advisor-model" $ExpectedAdvisorModel
  }
  if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorMaxDataLevel)) {
    Add-Arg $commandArgs "--expect-advisor-max-data-level" $ExpectedAdvisorMaxDataLevel
  }
  return [string[]]$commandArgs
}

function New-LocalManifestArgs {
  param(
    [string]$RunRoot,
    [string]$OutputPath,
    [string]$LocalPlatform,
    [string]$LocalAgentID,
    [string]$ServiceName,
    [string]$ServiceScope,
    [string[]]$ExpectedAgentIDs,
    [string[]]$ExpectedPlatforms,
    [string]$LocalSyncKeyID,
    [string]$LocalLocalKeyID,
    [string[]]$ExpectedSyncKeyIDs,
    [string[]]$ExpectedLocalKeyIDs,
    [string]$ExpectedAdvisorProvider,
    [string]$ExpectedAdvisorModel,
    [string]$ExpectedAdvisorMaxDataLevel,
    [string]$WatchDuration
  )
  $commandArgs = New-Object System.Collections.ArrayList
  Add-Arg $commandArgs "verify"
  Add-Arg $commandArgs "evidence"
  Add-Arg $commandArgs "--dir" $RunRoot
  Add-Arg $commandArgs "--output" $OutputPath
  Add-Arg $commandArgs "--require-passing"
  Add-Arg $commandArgs "--min-watch-duration" $WatchDuration
  Add-Arg $commandArgs "--min-watch-duration-per-platform"
  if (-not $SkipService) {
    Add-Arg $commandArgs "--require-kind" "service"
    Add-Arg $commandArgs "--require-platform" $LocalPlatform
    Add-Arg $commandArgs "--require-kind-platform" ("service:" + $LocalPlatform)
    foreach ($check in @("service.status", "service.runtime", "service.watch", "service.watch.heartbeat", "daemon.loops.status", "s3.device.policy_contract", "s3.sync.change_contract", "s3.sync.security.strict", "s4.autonomy.status", "s4.autonomy.policy_contract", "s4.autonomy.policy_gate", "s4.autonomy.retry_policy", "s4.advisor.probe", "s4.advisor.privacy_probe")) {
      Add-Arg $commandArgs "--require-kind-check-platform" ("service:" + $check + ":" + $LocalPlatform)
    }
    if (-not [string]::IsNullOrWhiteSpace($LocalAgentID)) {
      Add-Arg $commandArgs "--require-agent-id" $LocalAgentID
      Add-Arg $commandArgs "--require-kind-platform-agent" ("service:" + $LocalPlatform + ":" + $LocalAgentID)
    }
    if (-not [string]::IsNullOrWhiteSpace($ServiceScope)) {
      Add-Arg $commandArgs "--require-kind-platform-service-scope" ("service:" + $LocalPlatform + ":" + $ServiceScope)
    }
    if (-not [string]::IsNullOrWhiteSpace($ServiceName)) {
      Add-Arg $commandArgs "--require-kind-platform-service-name" ("service:" + $LocalPlatform + ":" + $ServiceName)
    }
    if (-not [string]::IsNullOrWhiteSpace($LocalSyncKeyID)) {
      Add-Arg $commandArgs "--require-kind-check-platform" ("service:s3.sync.security.expected_sync_key:" + $LocalPlatform)
    }
    if (-not [string]::IsNullOrWhiteSpace($LocalLocalKeyID)) {
      Add-Arg $commandArgs "--require-kind-check-platform" ("service:s3.sync.security.expected_local_key:" + $LocalPlatform)
    }
    if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorProvider)) {
      Add-Arg $commandArgs "--require-kind-platform-advisor-provider" ("service:" + $LocalPlatform + ":" + $ExpectedAdvisorProvider)
    }
    if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorModel)) {
      Add-Arg $commandArgs "--require-kind-platform-advisor-model" ("service:" + $LocalPlatform + ":" + $ExpectedAdvisorModel)
    }
    if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorMaxDataLevel)) {
      Add-Arg $commandArgs "--require-kind-platform-advisor-max-data-level" ("service:" + $LocalPlatform + ":" + $ExpectedAdvisorMaxDataLevel)
    }
  }
  if (-not $SkipMesh) {
    Add-Arg $commandArgs "--require-kind" "mesh"
    foreach ($platform in $ExpectedPlatforms) {
      if ([string]::IsNullOrWhiteSpace($platform)) {
        continue
      }
      Add-Arg $commandArgs "--require-platform" $platform
      Add-Arg $commandArgs "--require-kind-platform" ("mesh:" + $platform)
      foreach ($check in @("mesh.watch", "mesh.watch.heartbeat", "daemon.loops.status", "s3.device.policy_contract", "s3.sync.change_contract", "s3.peers.present", "s3.peers.status", "s3.sync.security.strict", "s3.peer_probe.task", "s3.peer_probe.source_ref", "s3.peer_probe.data_tag", "s3.peer_probe.entity_tag", "s3.peer_probe.event", "s3.peer_probe.timeline_segment", "s3.peer_probe.relations", "s4.autonomy.status", "s4.autonomy.policy_contract", "s4.autonomy.policy_gate", "s4.autonomy.retry_policy", "s4.advisor.probe", "s4.advisor.privacy_probe")) {
        Add-Arg $commandArgs "--require-kind-check-platform" ("mesh:" + $check + ":" + $platform)
      }
      if ((Normalize-NonEmptyValues -Values $ExpectedSyncKeyIDs).Count -gt 0) {
        Add-Arg $commandArgs "--require-kind-check-platform" ("mesh:s3.sync.security.expected_sync_key:" + $platform)
      }
      if ((Normalize-NonEmptyValues -Values $ExpectedLocalKeyIDs).Count -gt 0) {
        Add-Arg $commandArgs "--require-kind-check-platform" ("mesh:s3.sync.security.expected_local_key:" + $platform)
      }
      if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorProvider)) {
        Add-Arg $commandArgs "--require-kind-platform-advisor-provider" ("mesh:" + $platform + ":" + $ExpectedAdvisorProvider)
      }
      if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorModel)) {
        Add-Arg $commandArgs "--require-kind-platform-advisor-model" ("mesh:" + $platform + ":" + $ExpectedAdvisorModel)
      }
      if (-not [string]::IsNullOrWhiteSpace($ExpectedAdvisorMaxDataLevel)) {
        Add-Arg $commandArgs "--require-kind-platform-advisor-max-data-level" ("mesh:" + $platform + ":" + $ExpectedAdvisorMaxDataLevel)
      }
    }
    for ($i = 0; $i -lt $ExpectedPlatforms.Count -and $i -lt $ExpectedAgentIDs.Count; $i++) {
      if (-not [string]::IsNullOrWhiteSpace($ExpectedPlatforms[$i]) -and -not [string]::IsNullOrWhiteSpace($ExpectedAgentIDs[$i])) {
        Add-Arg $commandArgs "--require-agent-id" $ExpectedAgentIDs[$i]
        Add-Arg $commandArgs "--require-kind-platform-agent" ("mesh:" + $ExpectedPlatforms[$i] + ":" + $ExpectedAgentIDs[$i])
      }
    }
  }
  return [string[]]$commandArgs
}

function Write-FinalHostEvidence {
  param(
    [string]$RunRoot,
    [object]$Payload,
    [bool]$OK
  )
  $path = New-UniquePath -Directory $RunRoot -BaseName ("steward-verify-s3s4-final-host-" + (New-Timestamp)) -Suffix ($(if ($OK) { "-pass.json" } else { "-fail.json" }))
  $envelope = [ordered]@{
    kind = "s3s4-final-host"
    ok = $OK
    command = @($PSCommandPath)
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    payload = [ordered]@{
      verification = $Payload
    }
  }
  $envelope | ConvertTo-Json -Depth 40 | Set-Content -LiteralPath $path -Encoding ASCII
  return $path
}

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-s3s4-final-host"
}
if ([string]::IsNullOrWhiteSpace($LocalPlatform)) {
  $LocalPlatform = Get-HostPlatform
}
if ([string]::IsNullOrWhiteSpace($ServiceName)) {
  $ServiceName = Get-DefaultServiceName -Platform $LocalPlatform
}
if ([string]::IsNullOrWhiteSpace($ServiceScope)) {
  $ServiceScope = Get-DefaultServiceScope -Platform $LocalPlatform
}
$ServiceScope = $ServiceScope.Trim().ToLowerInvariant()
if ($ServiceScope -ne "system" -and $ServiceScope -ne "user") {
  throw "S3/S4 final service verification requires -ServiceScope to be 'system' or 'user'."
}
if ([string]::IsNullOrWhiteSpace($LocalAgentVersion) -and $ExpectedAgentVersions.Count -eq 1) {
  $LocalAgentVersion = $ExpectedAgentVersions[0]
}
if ([string]::IsNullOrWhiteSpace($LocalSyncKeyID) -and $ExpectedSyncKeyIDs.Count -eq 1) {
  $LocalSyncKeyID = $ExpectedSyncKeyIDs[0]
}
if ([string]::IsNullOrWhiteSpace($LocalSyncKeyID)) {
  $LocalSyncKeyID = Resolve-ExpectedValueForLocalPlatform -CurrentValue $LocalSyncKeyID -Values $ExpectedSyncKeyIDs -Platforms $ExpectedPlatforms -LocalPlatform $LocalPlatform
}
if ([string]::IsNullOrWhiteSpace($LocalLocalKeyID)) {
  $LocalLocalKeyID = Resolve-ExpectedValueForLocalPlatform -CurrentValue $LocalLocalKeyID -Values $ExpectedLocalKeyIDs -Platforms $ExpectedPlatforms -LocalPlatform $LocalPlatform
}
if ($Node.Count -eq 0) {
  $Node = @($APIBase)
}
$finalMeshMode = (-not $SkipMesh) -and (-not $AllowIncompleteMesh)
if ($finalMeshMode -and $Node.Count -lt 3) {
  throw "S3/S4 final mesh verification requires at least three --Node values, or pass -AllowIncompleteMesh for a non-final smoke run."
}
if ((-not $SkipService) -and [string]::IsNullOrWhiteSpace($LocalAgentID)) {
  throw "S3/S4 final service verification requires -LocalAgentID so service evidence is tied to the expected device identity; pass -SkipService only for a non-final smoke run."
}
if ((-not $SkipService) -and $ServiceScope -ne "system" -and (-not $AllowUserServiceScope)) {
  throw "S3/S4 final high-permission service verification requires -ServiceScope system so evidence can satisfy the s3s4-final-system gate; pass -AllowUserServiceScope only for an intentional user-scope s3s4-final smoke or non-system run."
}
if (-not $SkipService) {
  $missingServiceKeyExpectations = @()
  if ([string]::IsNullOrWhiteSpace($LocalSyncKeyID)) {
    $missingServiceKeyExpectations += "-LocalSyncKeyID"
  }
  if ([string]::IsNullOrWhiteSpace($LocalLocalKeyID)) {
    $missingServiceKeyExpectations += "-LocalLocalKeyID"
  }
  if ($missingServiceKeyExpectations.Count -gt 0) {
    throw ("S3/S4 final service verification requires explicit key id expectations: " + ($missingServiceKeyExpectations -join ", ") + ". Provide them for final evidence, or pass -SkipService only for a non-final smoke run.")
  }
}
if ($finalMeshMode) {
  $normalizedExpectedPlatforms = Normalize-NonEmptyValues -Values $ExpectedPlatforms -Lowercase
  $normalizedExpectedAgentIDs = Normalize-NonEmptyValues -Values $ExpectedAgentIDs
  $normalizedExpectedSyncKeyIDs = Normalize-NonEmptyValues -Values $ExpectedSyncKeyIDs
  $normalizedExpectedLocalKeyIDs = Normalize-NonEmptyValues -Values $ExpectedLocalKeyIDs
  foreach ($requiredPlatform in @("windows", "darwin", "linux")) {
    if ($normalizedExpectedPlatforms -notcontains $requiredPlatform) {
      throw "S3/S4 final mesh verification requires -ExpectedPlatforms to include windows,darwin,linux; pass -AllowIncompleteMesh for a non-final smoke run."
    }
  }
  if ($normalizedExpectedPlatforms.Count -ne 3) {
    throw "S3/S4 final mesh verification requires exactly three non-empty -ExpectedPlatforms values: windows,darwin,linux; pass -AllowIncompleteMesh for a non-final smoke run."
  }
  if ($normalizedExpectedAgentIDs.Count -ne $normalizedExpectedPlatforms.Count) {
    throw "S3/S4 final mesh verification requires one non-empty -ExpectedAgentIDs value for each expected platform; pass -AllowIncompleteMesh for a non-final smoke run."
  }
  if ($normalizedExpectedSyncKeyIDs.Count -ne 1 -and $normalizedExpectedSyncKeyIDs.Count -ne $Node.Count) {
    throw "S3/S4 final mesh verification requires -ExpectedSyncKeyIDs once for all nodes or once per node; pass -AllowIncompleteMesh for a non-final smoke run."
  }
  if ($normalizedExpectedLocalKeyIDs.Count -ne 1 -and $normalizedExpectedLocalKeyIDs.Count -ne $Node.Count) {
    throw "S3/S4 final mesh verification requires -ExpectedLocalKeyIDs once for all nodes or once per node; pass -AllowIncompleteMesh for a non-final smoke run."
  }
  if ($normalizedExpectedPlatforms -notcontains $LocalPlatform.ToLowerInvariant()) {
    throw "S3/S4 final mesh verification requires -ExpectedPlatforms to include the local platform '$LocalPlatform'; pass -AllowIncompleteMesh for a non-final smoke run."
  }
  if ((-not [string]::IsNullOrWhiteSpace($LocalAgentID)) -and ($normalizedExpectedAgentIDs -notcontains $LocalAgentID.Trim())) {
    throw "S3/S4 final mesh verification requires -ExpectedAgentIDs to include the local agent id '$LocalAgentID'; pass -AllowIncompleteMesh for a non-final smoke run."
  }
}
if ($AdvisorProbeEachSample -and $SkipAdvisorProbe) {
  throw "-AdvisorProbeEachSample requires advisor probe to be enabled; remove -SkipAdvisorProbe."
}
$advisorVerificationEnabled = (-not $SkipAdvisorProbe) -or (-not $SkipAdvisorPrivacyProbe)
if ($advisorVerificationEnabled) {
  $missingAdvisorExpectations = @()
  if ([string]::IsNullOrWhiteSpace($ExpectedAdvisorProvider)) {
    $missingAdvisorExpectations += "-ExpectedAdvisorProvider"
  }
  if ([string]::IsNullOrWhiteSpace($ExpectedAdvisorModel)) {
    $missingAdvisorExpectations += "-ExpectedAdvisorModel"
  }
  if ([string]::IsNullOrWhiteSpace($ExpectedAdvisorMaxDataLevel)) {
    $missingAdvisorExpectations += "-ExpectedAdvisorMaxDataLevel"
  }
  if ($missingAdvisorExpectations.Count -gt 0) {
    throw ("S3/S4 final advisor verification requires explicit target model expectations: " + ($missingAdvisorExpectations -join ", ") + ". Provide them for final evidence, or pass -SkipAdvisorProbe -SkipAdvisorPrivacyProbe for a non-final smoke run.")
  }
}

$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null
$runRoot = Join-Path $evidenceRoot ("run-" + (New-Timestamp) + "-" + $LocalPlatform)
New-Item -ItemType Directory -Force -Path $runRoot | Out-Null
$serviceEvidenceDir = Join-Path $runRoot "service"
$meshEvidenceDir = Join-Path $runRoot "mesh"
New-Item -ItemType Directory -Force -Path $serviceEvidenceDir | Out-Null
New-Item -ItemType Directory -Force -Path $meshEvidenceDir | Out-Null

$checks = New-Object System.Collections.ArrayList
$startedAt = (Get-Date).ToUniversalTime()
$binary = ""
$serviceResult = $null
$meshResult = $null
$manifestResult = $null
$manifestArgs = @()
$errorMessage = ""

try {
  if ($PlanOnly -and [string]::IsNullOrWhiteSpace($BinaryPath)) {
    $binary = "steward"
    Add-Check $checks "s3s4_final_host.binary" "ok" "plan-only mode uses steward from PATH placeholder" @{ path = $binary }
  } else {
    $binary = Get-OrBuild-Binary -RepoRoot $repoRoot -BackendDir $backendDir -RunRoot $runRoot -BinaryPath $BinaryPath
    Add-Check $checks "s3s4_final_host.binary" "ok" "steward verifier binary is available" @{ path = $binary }
  }

  $serviceArgs = New-ServiceVerifyArgs `
    -APIBase $APIBase `
    -ServiceName $ServiceName `
    -ServiceScope $ServiceScope `
    -EvidenceDir $serviceEvidenceDir `
    -LocalAgentID $LocalAgentID `
    -LocalPlatform $LocalPlatform `
    -LocalAgentVersion $LocalAgentVersion `
    -LocalSyncKeyID $LocalSyncKeyID `
    -LocalLocalKeyID $LocalLocalKeyID `
    -ExpectedAdvisorProvider $ExpectedAdvisorProvider `
    -ExpectedAdvisorModel $ExpectedAdvisorModel `
    -ExpectedAdvisorMaxDataLevel $ExpectedAdvisorMaxDataLevel `
    -WatchDuration $WatchDuration `
    -WatchInterval $WatchInterval

  $meshArgs = New-MeshVerifyArgs `
    -Nodes $Node `
    -EvidenceDir $meshEvidenceDir `
    -ExpectedAgentIDs $ExpectedAgentIDs `
    -ExpectedPlatforms $ExpectedPlatforms `
    -ExpectedAgentVersions $ExpectedAgentVersions `
    -ExpectedSyncKeyIDs $ExpectedSyncKeyIDs `
    -ExpectedLocalKeyIDs $ExpectedLocalKeyIDs `
    -ExpectedAdvisorProvider $ExpectedAdvisorProvider `
    -ExpectedAdvisorModel $ExpectedAdvisorModel `
    -ExpectedAdvisorMaxDataLevel $ExpectedAdvisorMaxDataLevel `
    -WatchDuration $WatchDuration `
    -WatchInterval $WatchInterval

  $manifestPath = Join-Path $runRoot "host-manifest.json"
  $manifestArgs = New-LocalManifestArgs `
    -RunRoot $runRoot `
    -OutputPath $manifestPath `
    -LocalPlatform $LocalPlatform `
    -LocalAgentID $LocalAgentID `
    -ServiceName $ServiceName `
    -ServiceScope $ServiceScope `
    -ExpectedAgentIDs $ExpectedAgentIDs `
    -ExpectedPlatforms $ExpectedPlatforms `
    -LocalSyncKeyID $LocalSyncKeyID `
    -LocalLocalKeyID $LocalLocalKeyID `
    -ExpectedSyncKeyIDs $ExpectedSyncKeyIDs `
    -ExpectedLocalKeyIDs $ExpectedLocalKeyIDs `
    -ExpectedAdvisorProvider $ExpectedAdvisorProvider `
    -ExpectedAdvisorModel $ExpectedAdvisorModel `
    -ExpectedAdvisorMaxDataLevel $ExpectedAdvisorMaxDataLevel `
    -WatchDuration $WatchDuration

  if ($PlanOnly) {
    Add-Check $checks "s3s4_final_host.plan" "ok" "commands rendered without execution" $null
  } else {
    if (-not $SkipService) {
      $serviceResult = Invoke-StewardCommand -BinaryPath $binary -Arguments $serviceArgs
      if ($serviceResult.exit_code -eq 0) {
        Add-Check $checks "s3s4_final_host.service" "ok" "service final-host verification passed" @{ duration_ms = $serviceResult.duration_ms; output = $serviceResult.output }
      } else {
        Add-Check $checks "s3s4_final_host.service" "error" "service final-host verification failed" @{ exit_code = $serviceResult.exit_code; output = $serviceResult.output }
      }
    }
    if (-not $SkipMesh) {
      $meshResult = Invoke-StewardCommand -BinaryPath $binary -Arguments $meshArgs
      if ($meshResult.exit_code -eq 0) {
        Add-Check $checks "s3s4_final_host.mesh" "ok" "mesh final-host verification passed" @{ duration_ms = $meshResult.duration_ms; output = $meshResult.output }
      } else {
        Add-Check $checks "s3s4_final_host.mesh" "error" "mesh final-host verification failed" @{ exit_code = $meshResult.exit_code; output = $meshResult.output }
      }
    }
    if (-not $SkipLocalManifest) {
      $manifestResult = Invoke-StewardCommand -BinaryPath $binary -Arguments $manifestArgs
      if ($manifestResult.exit_code -eq 0) {
        Add-Check $checks "s3s4_final_host.local_manifest" "ok" "local final-host evidence manifest passed" @{ manifest_path = $manifestPath }
      } else {
        Add-Check $checks "s3s4_final_host.local_manifest" "error" "local final-host evidence manifest failed" @{ manifest_path = $manifestPath; exit_code = $manifestResult.exit_code; output = $manifestResult.output }
      }
    }
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "s3s4_final_host.error" "error" $errorMessage $null
}

$completedAt = (Get-Date).ToUniversalTime()
$ok = $true
foreach ($check in $checks) {
  if ($check.status -ne "ok") {
    $ok = $false
    break
  }
}

$payload = [ordered]@{
  ok = $ok
  plan_only = [bool]$PlanOnly
  platform = $LocalPlatform
  agent_id = $LocalAgentID
  started_at = $startedAt.ToString("o")
  completed_at = $completedAt.ToString("o")
  duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
  host = [ordered]@{
    platform = $LocalPlatform
    agent_id = $LocalAgentID
    api_base = $APIBase
    service_name = $ServiceName
    service_scope = $ServiceScope
    local_agent_id = $LocalAgentID
    local_agent_version = $LocalAgentVersion
    local_sync_key_id = $LocalSyncKeyID
    local_local_key_id = $LocalLocalKeyID
  }
  mesh = [ordered]@{
    nodes = $Node
    expected_agent_ids = $ExpectedAgentIDs
    expected_platforms = $ExpectedPlatforms
  }
  evidence_dir = $runRoot
  binary = $binary
  commands = [ordered]@{
    service = @($binary) + @($serviceArgs)
    mesh = @($binary) + @($meshArgs)
    local_manifest = @($binary) + @($manifestArgs)
  }
  checks = @($checks)
  service_result = $serviceResult
  mesh_result = $meshResult
  local_manifest_result = $manifestResult
  error = $errorMessage
}

$evidencePath = Write-FinalHostEvidence -RunRoot $runRoot -Payload $payload -OK $ok
$payload.evidence_path = $evidencePath
$payload | ConvertTo-Json -Depth 40

if (-not $ok) {
  exit 1
}
