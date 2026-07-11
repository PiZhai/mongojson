param(
  [Parameter(Mandatory = $true)]
  [string]$InventoryFile,

  [string]$BinaryPath = "",

  [string]$EvidenceDir = "",

  [switch]$PlanOnly
)

$ErrorActionPreference = "Stop"

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
    [System.Collections.ArrayList]$Arguments,
    [string]$Name,
    [string]$Value = $null
  )
  [void]$Arguments.Add($Name)
  if ($PSBoundParameters.ContainsKey("Value")) {
    [void]$Arguments.Add($Value)
  }
}

function Require-String {
  param(
    [object]$Value,
    [string]$Label
  )
  $text = [string]$Value
  if ([string]::IsNullOrWhiteSpace($text)) {
    throw "$Label is required."
  }
  return $text.Trim()
}

function Assert-AllowedProperties {
  param(
    [object]$Value,
    [string[]]$Allowed,
    [string]$Label
  )
  $allowedSet = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
  foreach ($name in $Allowed) {
    [void]$allowedSet.Add($name)
  }
  foreach ($property in $Value.PSObject.Properties) {
    if (-not $allowedSet.Contains($property.Name)) {
      throw "$Label contains unsupported property '$($property.Name)'. Final-system inventory accepts identifiers and paths only."
    }
  }
}

function Get-DefaultServiceName {
  param([string]$Platform)
  switch ($Platform) {
    "windows" { return "MongojsonSteward" }
    "darwin" { return "com.mongojson.steward" }
    "linux" { return "mongojson-steward" }
    default { throw "Unsupported S3/S4 host platform: $Platform" }
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

function Read-Inventory {
  param([string]$Path)
  $resolved = (Resolve-Path -LiteralPath $Path -ErrorAction Stop).Path
  $inventory = Get-Content -LiteralPath $resolved -Raw | ConvertFrom-Json
  if ($null -eq $inventory -or $inventory -is [System.Array]) {
    throw "S3/S4 final-system inventory must be a JSON object."
  }
  Assert-AllowedProperties -Value $inventory -Allowed @("schema_version", "advisor", "hosts") -Label "inventory"
  if ([int]$inventory.schema_version -ne 1) {
    throw "S3/S4 final-system inventory requires schema_version 1."
  }

  Assert-AllowedProperties -Value $inventory.advisor -Allowed @("provider", "model", "max_data_level") -Label "advisor"
  $provider = Require-String -Value $inventory.advisor.provider -Label "advisor.provider"
  $model = Require-String -Value $inventory.advisor.model -Label "advisor.model"
  $maxDataLevel = (Require-String -Value $inventory.advisor.max_data_level -Label "advisor.max_data_level").ToUpperInvariant()
  if ($maxDataLevel -notin @("D0", "D1", "D2", "D3")) {
    throw "advisor.max_data_level must be one of D0, D1, D2, or D3."
  }

  $hosts = @($inventory.hosts)
  if ($hosts.Count -ne 3) {
    throw "S3/S4 final-system inventory requires exactly three hosts."
  }
  $normalizedHosts = @()
  $seenPlatforms = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
  $seenAgentIDs = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
  $seenEvidenceDirs = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
  foreach ($hostEntry in $hosts) {
    Assert-AllowedProperties -Value $hostEntry -Allowed @("platform", "agent_id", "service_name", "service_scope", "evidence_dir") -Label "hosts[]"
    $platform = (Require-String -Value $hostEntry.platform -Label "hosts[].platform").ToLowerInvariant()
    if ($platform -notin @("windows", "darwin", "linux")) {
      throw "hosts[].platform must be windows, darwin, or linux; got '$platform'."
    }
    if (-not $seenPlatforms.Add($platform)) {
      throw "Duplicate host platform in inventory: $platform"
    }
    $agentID = Require-String -Value $hostEntry.agent_id -Label "hosts[$platform].agent_id"
    if (-not $seenAgentIDs.Add($agentID)) {
      throw "Duplicate host agent_id in inventory: $agentID"
    }
    $serviceScope = (Require-String -Value $hostEntry.service_scope -Label "hosts[$platform].service_scope").ToLowerInvariant()
    if ($serviceScope -ne "system") {
      throw "S3/S4 final-system requires hosts[$platform].service_scope to be system."
    }
    $serviceName = [string]$hostEntry.service_name
    if ([string]::IsNullOrWhiteSpace($serviceName)) {
      $serviceName = Get-DefaultServiceName -Platform $platform
    } else {
      $serviceName = $serviceName.Trim()
    }
    $evidencePath = Require-String -Value $hostEntry.evidence_dir -Label "hosts[$platform].evidence_dir"
    if (-not $PlanOnly) {
      $evidencePath = (Resolve-Path -LiteralPath $evidencePath -ErrorAction Stop).Path
      if (-not (Test-Path -LiteralPath $evidencePath -PathType Container)) {
        throw "hosts[$platform].evidence_dir is not a directory: $evidencePath"
      }
    }
    if (-not $seenEvidenceDirs.Add($evidencePath)) {
      throw "Each host must use a distinct evidence_dir; duplicate: $evidencePath"
    }
    $normalizedHosts += [pscustomobject]@{
      platform = $platform
      agent_id = $agentID
      service_name = $serviceName
      service_scope = $serviceScope
      evidence_dir = $evidencePath
    }
  }
  foreach ($requiredPlatform in @("windows", "darwin", "linux")) {
    if (-not $seenPlatforms.Contains($requiredPlatform)) {
      throw "S3/S4 final-system inventory is missing platform: $requiredPlatform"
    }
  }
  return [pscustomobject]@{
    path = $resolved
    advisor = [pscustomobject]@{
      provider = $provider
      model = $model
      max_data_level = $maxDataLevel
    }
    hosts = @($normalizedHosts | Sort-Object platform)
  }
}

function Get-HostArch {
  $arch = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString().ToLowerInvariant()
  switch ($arch) {
    "x64" { return "amd64" }
    "arm64" { return "arm64" }
    default { return $arch }
  }
}

function Get-OrBuild-Binary {
  param(
    [string]$BackendDir,
    [string]$RunRoot,
    [string]$RequestedPath
  )
  if (-not [string]::IsNullOrWhiteSpace($RequestedPath)) {
    return (Resolve-Path -LiteralPath $RequestedPath -ErrorAction Stop).Path
  }
  if ($PlanOnly) {
    return "steward"
  }
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Missing required command: go"
  }
  $extension = if ((Get-HostPlatform) -eq "windows") { ".exe" } else { "" }
  $binaryDir = Join-Path $RunRoot "bin"
  New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null
  $outputPath = Join-Path $binaryDir ("steward-s3s4-final-system-" + (Get-HostArch) + $extension)
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

function Invoke-Steward {
  param(
    [string]$Executable,
    [string[]]$Arguments
  )
  $output = & $Executable @Arguments 2>&1
  return [pscustomobject]@{
    exit_code = $LASTEXITCODE
    output = @($output | ForEach-Object { "$_" })
  }
}

function New-HostPreflightArgs {
  param(
    [object]$HostEntry,
    [object]$Advisor,
    [string]$OutputPath
  )
  $args = New-Object System.Collections.ArrayList
  foreach ($pair in @(
    @("--dir", $HostEntry.evidence_dir),
    @("--require-kind", "service-install-e2e"),
    @("--require-kind", "service"),
    @("--require-kind", "mesh"),
    @("--require-kind", "s3s4-final-host"),
    @("--require-platform", $HostEntry.platform),
    @("--require-agent-id", $HostEntry.agent_id),
    @("--require-kind-platform-agent", "service-install-e2e:$($HostEntry.platform):$($HostEntry.agent_id)"),
    @("--require-kind-platform-agent", "service:$($HostEntry.platform):$($HostEntry.agent_id)"),
    @("--require-kind-platform-agent", "mesh:$($HostEntry.platform):$($HostEntry.agent_id)"),
    @("--require-kind-platform-agent", "s3s4-final-host:$($HostEntry.platform):$($HostEntry.agent_id)"),
    @("--require-kind-platform-service-scope", "service-install-e2e:$($HostEntry.platform):system"),
    @("--require-kind-platform-service-scope", "service:$($HostEntry.platform):system"),
    @("--require-kind-platform-service-name", "service-install-e2e:$($HostEntry.platform):$($HostEntry.service_name)"),
    @("--require-kind-platform-service-name", "service:$($HostEntry.platform):$($HostEntry.service_name)"),
    @("--require-kind-platform-advisor-provider", "service:$($HostEntry.platform):$($Advisor.provider)"),
    @("--require-kind-platform-advisor-model", "service:$($HostEntry.platform):$($Advisor.model)"),
    @("--require-kind-platform-advisor-max-data-level", "service:$($HostEntry.platform):$($Advisor.max_data_level)"),
    @("--require-kind-platform-advisor-provider", "mesh:$($HostEntry.platform):$($Advisor.provider)"),
    @("--require-kind-platform-advisor-model", "mesh:$($HostEntry.platform):$($Advisor.model)"),
    @("--require-kind-platform-advisor-max-data-level", "mesh:$($HostEntry.platform):$($Advisor.max_data_level)"),
    @("--min-watch-duration", "24h"),
    @("--output", $OutputPath)
  )) {
    Add-Arg $args $pair[0] $pair[1]
  }
  Add-Arg $args "--require-passing"
  Add-Arg $args "--min-watch-duration-per-platform"
  return [string[]](@("verify", "evidence") + @($args))
}

function New-FinalManifestArgs {
  param(
    [string]$ImportedEvidenceDir,
    [object[]]$Hosts,
    [object]$Advisor,
    [string]$OutputPath
  )
  $args = New-Object System.Collections.ArrayList
  Add-Arg $args "--dir" $ImportedEvidenceDir
  Add-Arg $args "--preset" "s3s4-final-system"
  foreach ($hostEntry in $Hosts) {
    Add-Arg $args "--require-agent-id" $hostEntry.agent_id
    foreach ($kind in @("service-install-e2e", "service", "mesh", "s3s4-final-host")) {
      Add-Arg $args "--require-kind-platform-agent" "$kind`:$($hostEntry.platform):$($hostEntry.agent_id)"
    }
    foreach ($kind in @("service-install-e2e", "service", "s3s4-final-host")) {
      Add-Arg $args "--require-kind-platform-service-name" "$kind`:$($hostEntry.platform):$($hostEntry.service_name)"
    }
    foreach ($kind in @("service", "mesh")) {
      Add-Arg $args "--require-kind-platform-advisor-provider" "$kind`:$($hostEntry.platform):$($Advisor.provider)"
      Add-Arg $args "--require-kind-platform-advisor-model" "$kind`:$($hostEntry.platform):$($Advisor.model)"
      Add-Arg $args "--require-kind-platform-advisor-max-data-level" "$kind`:$($hostEntry.platform):$($Advisor.max_data_level)"
    }
  }
  Add-Arg $args "--output" $OutputPath
  return [string[]](@("verify", "evidence") + @($args))
}

function Copy-EvidencePackage {
  param(
    [string]$SourceDir,
    [string]$DestinationDir
  )
  $sourceRoot = [System.IO.Path]::GetFullPath($SourceDir).TrimEnd([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
  $files = @(Get-ChildItem -LiteralPath $sourceRoot -Recurse -File -Filter "steward-verify-*.json")
  if ($files.Count -eq 0) {
    throw "No steward verification evidence files found in: $sourceRoot"
  }
  $copied = @()
  foreach ($file in $files) {
    try {
      $relativePath = [System.IO.Path]::GetRelativePath($sourceRoot, $file.FullName)
    } catch {
      $sourceURI = [Uri]($sourceRoot + [System.IO.Path]::DirectorySeparatorChar)
      $fileURI = [Uri]$file.FullName
      $relativePath = [Uri]::UnescapeDataString($sourceURI.MakeRelativeUri($fileURI).ToString()).Replace('/', [System.IO.Path]::DirectorySeparatorChar)
    }
    $destination = Join-Path $DestinationDir $relativePath
    $destinationParent = Split-Path -Parent $destination
    New-Item -ItemType Directory -Force -Path $destinationParent | Out-Null
    Copy-Item -LiteralPath $file.FullName -Destination $destination
    $copied += [pscustomobject]@{
      relative_path = $relativePath
      bytes = $file.Length
      sha256 = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
    }
  }
  return @($copied)
}

function Write-SystemEvidence {
  param(
    [string]$RunRoot,
    [object]$Payload,
    [bool]$OK
  )
  $status = if ($OK) { "pass" } else { "fail" }
  $path = New-UniquePath -Directory $RunRoot -BaseName ("steward-verify-s3s4-final-system-" + (New-Timestamp)) -Suffix "-$status.json"
  $envelope = [ordered]@{
    kind = "s3s4-final-system"
    ok = $OK
    command = @($PSCommandPath, "-InventoryFile", $InventoryFile)
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    payload = [ordered]@{
      verification = $Payload
    }
  }
  $json = $envelope | ConvertTo-Json -Depth 40
  [System.IO.File]::WriteAllText($path, $json + [Environment]::NewLine, [System.Text.UTF8Encoding]::new($false))
  return $path
}

$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-s3s4-final-system"
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null
$runRoot = Join-Path $evidenceRoot ("run-" + (New-Timestamp))
$reportsDir = Join-Path $runRoot "reports"
$hostsDir = Join-Path $runRoot "hosts"
New-Item -ItemType Directory -Force -Path $reportsDir | Out-Null
New-Item -ItemType Directory -Force -Path $hostsDir | Out-Null

$checks = New-Object System.Collections.ArrayList
$startedAt = (Get-Date).ToUniversalTime()
$inventory = $null
$binary = ""
$hostResults = @()
$finalArgs = @()
$finalResult = $null
$errorMessage = ""

try {
  $inventory = Read-Inventory -Path $InventoryFile
  Add-Check $checks "final_system.inventory" "ok" "three-host final-system inventory is valid" @{ path = $inventory.path }
  $binary = Get-OrBuild-Binary -BackendDir $backendDir -RunRoot $runRoot -RequestedPath $BinaryPath
  Add-Check $checks "final_system.binary" "ok" "steward verifier binary is available" @{ path = $binary; plan_only = [bool]$PlanOnly }

  foreach ($hostEntry in $inventory.hosts) {
    $preflightPath = Join-Path $reportsDir ("preflight-$($hostEntry.platform).json")
    $preflightArgs = New-HostPreflightArgs -HostEntry $hostEntry -Advisor $inventory.advisor -OutputPath $preflightPath
    $hostResult = [ordered]@{
      platform = $hostEntry.platform
      agent_id = $hostEntry.agent_id
      source_dir = $hostEntry.evidence_dir
      preflight_command = @($binary) + @($preflightArgs)
      preflight_manifest = $preflightPath
      imported_files = @()
    }
    if ($PlanOnly) {
      Add-Check $checks "final_system.source.$($hostEntry.platform)" "ok" "host evidence package planned" @{ source_dir = $hostEntry.evidence_dir }
    } else {
      $preflightResult = Invoke-Steward -Executable $binary -Arguments $preflightArgs
      $hostResult.preflight_result = $preflightResult
      if ($preflightResult.exit_code -ne 0) {
        Add-Check $checks "final_system.preflight.$($hostEntry.platform)" "error" "host evidence package preflight failed" @{ output = $preflightResult.output; manifest = $preflightPath }
        $hostResults += [pscustomobject]$hostResult
        continue
      }
      Add-Check $checks "final_system.preflight.$($hostEntry.platform)" "ok" "host evidence package identity and policy preflight passed" @{ manifest = $preflightPath }
      $destinationDir = Join-Path $hostsDir $hostEntry.platform
      $hostResult.imported_files = Copy-EvidencePackage -SourceDir $hostEntry.evidence_dir -DestinationDir $destinationDir
      Add-Check $checks "final_system.import.$($hostEntry.platform)" "ok" "host evidence package imported with SHA-256 inventory" @{ files = $hostResult.imported_files.Count; destination = $destinationDir }
    }
    $hostResults += [pscustomobject]$hostResult
  }

  $finalManifestPath = Join-Path $runRoot "final-manifest.json"
  $finalArgs = New-FinalManifestArgs -ImportedEvidenceDir $hostsDir -Hosts $inventory.hosts -Advisor $inventory.advisor -OutputPath $finalManifestPath
  if ($PlanOnly) {
    Add-Check $checks "final_system.plan" "ok" "per-host preflight and final manifest commands rendered without reading evidence" $null
  } elseif ((@($checks | Where-Object { $_.status -ne "ok" })).Count -eq 0) {
    $finalResult = Invoke-Steward -Executable $binary -Arguments $finalArgs
    if ($finalResult.exit_code -eq 0) {
      Add-Check $checks "final_system.manifest" "ok" "three-host S3/S4 final-system evidence gate passed" @{ manifest = $finalManifestPath }
    } else {
      Add-Check $checks "final_system.manifest" "error" "three-host S3/S4 final-system evidence gate failed" @{ output = $finalResult.output; manifest = $finalManifestPath }
    }
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "final_system.error" "error" $errorMessage $null
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
  started_at = $startedAt.ToString("o")
  completed_at = $completedAt.ToString("o")
  duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
  inventory = $inventory
  evidence_dir = $runRoot
  binary = $binary
  hosts = @($hostResults)
  final_command = @($binary) + @($finalArgs)
  final_result = $finalResult
  checks = @($checks)
  error = $errorMessage
}
$evidencePath = Write-SystemEvidence -RunRoot $runRoot -Payload $payload -OK $ok
$summary = [ordered]@{
  ok = $ok
  plan_only = [bool]$PlanOnly
  evidence_dir = $runRoot
  evidence_path = $evidencePath
  final_manifest = if ($PlanOnly) { $null } else { Join-Path $runRoot "final-manifest.json" }
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 8

if (-not $ok) {
  exit 1
}
