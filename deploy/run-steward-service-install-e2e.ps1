param(
  [string]$BinaryPath = "",

  [string]$ConfigFile = "",

  [switch]$UseProcessEnvironment,

  [string]$EvidenceDir = "",

  [string]$ServiceName = "",

  [string]$ServiceScope = "system",

  [string]$WorkDir = "",

  [string]$WatchDuration = "24h",

  [string]$WatchInterval = "5m",

  [string]$VerifyStartupTimeout = "45s",

  [switch]$AdvisorProbeEachSample,

  [switch]$SkipAdvisorProbe,

  [switch]$SkipAdvisorPrivacyProbe,

  [switch]$AllowUserServiceScope,

  [switch]$AllowInsecureConfigFile,

  [switch]$PlanOnly,

  [switch]$ConfirmInstall
)

$ErrorActionPreference = "Stop"

$ServiceEnvironmentKeys = @(
  "HTTP_ADDR",
  "STEWARD_PEER_HTTP_ADDR",
  "DATABASE_URL",
  "STORAGE_DIR",
  "STEWARD_UI_DIR",
  "STEWARD_AGENT_ID",
  "STEWARD_PUBLIC_API_BASE",
  "STEWARD_SYNC_SECRET",
  "STEWARD_DEVICE_PRIVATE_KEY",
  "STEWARD_DEVICE_PUBLIC_KEY",
  "STEWARD_SYNC_ENCRYPTION_KEY",
  "STEWARD_SYNC_ENCRYPTION_KEY_ID",
  "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS",
  "STEWARD_LOCAL_ENCRYPTION_KEY",
  "STEWARD_LOCAL_ENCRYPTION_KEY_ID",
  "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS",
  "STEWARD_HEARTBEAT_INTERVAL",
  "STEWARD_SYNC_INTERVAL",
  "STEWARD_AUTONOMY_INTERVAL",
  "STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS",
  "STEWARD_AUTONOMY_RETRY_BACKOFF",
  "STEWARD_AUTONOMY_RETRY_MAX_BACKOFF",
  "STEWARD_LOG_DIR",
  "STEWARD_LLM_PROVIDER",
  "STEWARD_LLM_BASE_URL",
  "STEWARD_LLM_MODEL",
  "STEWARD_LLM_API_KEY",
  "STEWARD_LLM_ALLOW_NO_API_KEY",
  "STEWARD_LLM_TIMEOUT",
  "STEWARD_LLM_MAX_DATA_LEVEL",
  "STEWARD_LLM_FAILURE_THRESHOLD",
  "STEWARD_LLM_FAILURE_COOLDOWN",
  "STEWARD_DISCOVERY_ENABLED",
  "STEWARD_DEVICE_NAME",
  "STEWARD_DISCOVERY_LISTEN_ADDR",
  "STEWARD_DISCOVERY_TARGETS",
  "STEWARD_DISCOVERY_INTERVAL",
  "STEWARD_DISCOVERY_TTL"
)

function Require-Command {
  param([string]$Name)
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "Missing required command: $Name"
  }
}

function Resolve-ExistingPath {
  param(
    [string]$Path,
    [string]$Label
  )
  if ([string]::IsNullOrWhiteSpace($Path)) {
    throw "$Label is required."
  }
  try {
    return (Resolve-Path -LiteralPath $Path -ErrorAction Stop).Path
  } catch {
    throw "$Label does not exist: $Path"
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

function Get-DefaultServiceName {
  param([string]$Platform)
  switch ($Platform) {
    "windows" { return "MongojsonSteward" }
    "darwin" { return "com.mongojson.steward" }
    "linux" { return "mongojson-steward" }
    default { throw "Unsupported host platform: $Platform" }
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

function Test-IsSensitiveKey {
  param([string]$Key)
  $upper = $Key.Trim().ToUpperInvariant()
  if ($upper -eq "STEWARD_LLM_ALLOW_NO_API_KEY") {
    return $false
  }
  return $upper -eq "DATABASE_URL" -or
    $upper.Contains("SECRET") -or
    $upper.Contains("TOKEN") -or
    $upper.Contains("PASSWORD") -or
    $upper.Contains("API_KEY") -or
    ($upper.Contains("ENCRYPTION_KEY") -and -not $upper.Contains("ENCRYPTION_KEY_ID")) -or
    $upper.Contains("PREVIOUS_KEYS") -or
    $upper.Contains("PRIVATE_KEY")
}

function Read-ServiceEnvironment {
  param([string]$Path)
  $raw = Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
  if ($null -eq $raw -or $raw -is [System.Array]) {
    throw "Service config must be a JSON object whose values are strings."
  }
  $allowed = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
  foreach ($key in $ServiceEnvironmentKeys) {
    [void]$allowed.Add($key)
  }
  $environment = [ordered]@{}
  foreach ($property in $raw.PSObject.Properties) {
    $key = [string]$property.Name
    if (-not $allowed.Contains($key)) {
      throw "Unsupported service environment key in config: $key"
    }
    if ($null -eq $property.Value -or $property.Value -isnot [string]) {
      throw "Service environment value $key must be a string."
    }
    $environment[$key] = [string]$property.Value
  }
  if ($environment.Count -eq 0) {
    throw "Service config must contain at least one supported environment value."
  }
  return $environment
}

function Read-ProcessServiceEnvironment {
  $environment = [ordered]@{}
  foreach ($key in $ServiceEnvironmentKeys) {
    $value = [Environment]::GetEnvironmentVariable($key, [EnvironmentVariableTarget]::Process)
    if ($null -ne $value) {
      $environment[$key] = $value
    }
  }
  if ($environment.Count -eq 0) {
    throw "No supported steward service values are set in the current process environment."
  }
  return $environment
}

function Assert-SecureConfigFile {
  param([string]$Path)
  $platform = Get-HostPlatform
  if ($platform -eq "windows") {
    $broadSIDs = @(
      "S-1-1-0",
      "S-1-5-11",
      "S-1-5-32-545"
    )
    $unsafeRules = @(
      (Get-Acl -LiteralPath $Path).Access | Where-Object {
        $identitySID = try {
          $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value
        } catch {
          ""
        }
        $_.AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Allow -and
        $broadSIDs -contains $identitySID -and
        ($_.FileSystemRights.ToString() -match "Read|FullControl|Modify")
      }
    )
    if ($unsafeRules.Count -gt 0) {
      throw "Service config is readable by a broad Windows identity. Restrict its ACL before installation."
    }
    return
  }

  $mode = [System.IO.File]::GetUnixFileMode($Path)
  $unsafeMask = [System.IO.UnixFileMode]::GroupRead -bor
    [System.IO.UnixFileMode]::GroupWrite -bor
    [System.IO.UnixFileMode]::GroupExecute -bor
    [System.IO.UnixFileMode]::OtherRead -bor
    [System.IO.UnixFileMode]::OtherWrite -bor
    [System.IO.UnixFileMode]::OtherExecute
  if (([int]$mode -band [int]$unsafeMask) -ne 0) {
    throw "Service config permissions are too broad ($mode). Use owner-only mode 0600 or stricter."
  }
}

function Assert-SystemScopePrivileges {
  param(
    [string]$Platform,
    [string]$Scope
  )
  if ($Scope -ne "system") {
    return
  }
  if ($Platform -eq "windows") {
    $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [System.Security.Principal.WindowsPrincipal]::new($identity)
    if (-not $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)) {
      throw "System-scope Windows Service installation requires an elevated Administrator shell."
    }
    return
  }
  Require-Command "id"
  $uid = (& id -u 2>&1 | Out-String).Trim()
  if ($LASTEXITCODE -ne 0 -or $uid -ne "0") {
    throw "System-scope service installation requires root privileges on $Platform."
  }
}

function Get-OrBuild-Binary {
  param(
    [string]$BackendDir,
    [string]$RunRoot,
    [string]$RequestedPath,
    [bool]$AllowBuild
  )
  if (-not [string]::IsNullOrWhiteSpace($RequestedPath)) {
    return Resolve-ExistingPath -Path $RequestedPath -Label "BinaryPath"
  }
  if (-not $AllowBuild) {
    throw "BinaryPath is required for real installation so the native service points at a stable release binary."
  }
  Require-Command "go"
  $extension = ""
  if ((Get-HostPlatform) -eq "windows") {
    $extension = ".exe"
  }
  $binaryDir = Join-Path $RunRoot "bin"
  New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null
  # Avoid Windows installer-name heuristics: an executable containing "install"
  # can request elevation before the steward dry-run code is reached.
  $outputPath = Join-Path $binaryDir ("steward-plan-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)
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

function Protect-CommandResult {
  param(
    [object]$Result,
    [System.Collections.IDictionary]$Environment
  )
  $safeText = [string]$Result.text
  $leakedKeys = @()
  foreach ($key in $Environment.Keys) {
    if (-not (Test-IsSensitiveKey -Key $key)) {
      continue
    }
    $value = [string]$Environment[$key]
    if ([string]::IsNullOrEmpty($value)) {
      continue
    }
    if ($safeText.Contains($value)) {
      $leakedKeys += $key
      $safeText = $safeText.Replace($value, "<redacted>")
    }
  }
  return [pscustomobject]@{
    exit_code = $Result.exit_code
    started_at = $Result.started_at
    completed_at = $Result.completed_at
    duration_ms = $Result.duration_ms
    output = $safeText
    leaked_sensitive_keys = @($leakedKeys)
  }
}

function Invoke-StewardCommand {
  param(
    [string]$Executable,
    [string[]]$Arguments
  )
  $startedAt = (Get-Date).ToUniversalTime()
  $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
  $startInfo.FileName = $Executable
  $startInfo.UseShellExecute = $false
  $startInfo.CreateNoWindow = $true
  $startInfo.RedirectStandardOutput = $true
  $startInfo.RedirectStandardError = $true
  foreach ($argument in $Arguments) {
    [void]$startInfo.ArgumentList.Add($argument)
  }
  $process = [System.Diagnostics.Process]::new()
  $process.StartInfo = $startInfo
  try {
    if (-not $process.Start()) {
      throw "Failed to start steward process."
    }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $process.WaitForExit()
    $stdout = $stdoutTask.GetAwaiter().GetResult().TrimEnd()
    $stderr = $stderrTask.GetAwaiter().GetResult().TrimEnd()
    $exitCode = $process.ExitCode
  } finally {
    $process.Dispose()
  }
  $completedAt = (Get-Date).ToUniversalTime()
  $streams = @()
  if (-not [string]::IsNullOrWhiteSpace($stdout)) {
    $streams += $stdout
  }
  if (-not [string]::IsNullOrWhiteSpace($stderr)) {
    $streams += $stderr
  }
  return [pscustomobject]@{
    exit_code = $exitCode
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    text = ($streams -join "`n")
  }
}

function Set-TemporaryProcessEnvironment {
  param([System.Collections.IDictionary]$Environment)
  $previous = [ordered]@{}
  foreach ($key in $Environment.Keys) {
    $previous[$key] = [Environment]::GetEnvironmentVariable($key, [EnvironmentVariableTarget]::Process)
    [Environment]::SetEnvironmentVariable($key, [string]$Environment[$key], [EnvironmentVariableTarget]::Process)
  }
  return $previous
}

function Restore-ProcessEnvironment {
  param([System.Collections.IDictionary]$Previous)
  foreach ($key in $Previous.Keys) {
    [Environment]::SetEnvironmentVariable($key, $Previous[$key], [EnvironmentVariableTarget]::Process)
  }
}

function Write-Evidence {
  param(
    [string]$RunRoot,
    [object]$Payload,
    [bool]$OK
  )
  $suffix = if ($OK) { "-pass.json" } else { "-fail.json" }
  $path = New-UniquePath -Directory $RunRoot -BaseName ("steward-verify-service-install-e2e-" + (New-Timestamp)) -Suffix $suffix
  $envelope = [ordered]@{
    kind = "service-install-e2e"
    ok = $OK
    command = @($PSCommandPath)
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    payload = [ordered]@{
      verification = $Payload
    }
  }
  $envelope | ConvertTo-Json -Depth 30 | Set-Content -LiteralPath $path -Encoding UTF8
  return $path
}

$platform = Get-HostPlatform
$productionVerifier = Join-Path $PSScriptRoot "test-steward-production.ps1"
if ($platform -eq "windows" -and $ConfirmInstall) {
  throw "The legacy direct-SCM Windows install E2E is retired. Install or migrate with install-steward-production.ps1/migrate-steward-production.ps1, then run $productionVerifier. This prevents bypassing the R5.1 LocalService, Restricted Service SID, Broker and Companion isolation path."
}
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-service-install-e2e"
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null
$runRoot = Join-Path $evidenceRoot ("run-" + (New-Timestamp) + "-" + $platform)
New-Item -ItemType Directory -Force -Path $runRoot | Out-Null

if ($PlanOnly -and $ConfirmInstall) {
  throw "PlanOnly and ConfirmInstall are mutually exclusive."
}
if (-not $PlanOnly -and -not $ConfirmInstall) {
  throw "Real native service installation requires the explicit -ConfirmInstall switch; use -PlanOnly for a no-write check."
}
if (-not [string]::IsNullOrWhiteSpace($ConfigFile) -and $UseProcessEnvironment) {
  throw "Choose exactly one secret source: -ConfigFile or -UseProcessEnvironment."
}
if ([string]::IsNullOrWhiteSpace($ConfigFile) -and -not $UseProcessEnvironment) {
  throw "Choose a secret source with -ConfigFile or -UseProcessEnvironment."
}
if ($ConfirmInstall -and $AllowInsecureConfigFile) {
  throw "AllowInsecureConfigFile is limited to PlanOnly and cannot bypass real installation safeguards."
}

$ServiceScope = $ServiceScope.Trim().ToLowerInvariant()
if ($ServiceScope -ne "system" -and $ServiceScope -ne "user") {
  throw "ServiceScope must be 'system' or 'user'."
}
if ($platform -eq "windows" -and $ServiceScope -ne "system") {
  throw "Windows Service only supports system scope."
}
if ($ServiceScope -eq "user" -and -not $AllowUserServiceScope) {
  throw "Portable non-Windows installation defaults to system scope. Pass -AllowUserServiceScope to make an intentional user-scope installation."
}
if ([string]::IsNullOrWhiteSpace($ServiceName)) {
  $ServiceName = Get-DefaultServiceName -Platform $platform
}
if ($AdvisorProbeEachSample -and $SkipAdvisorProbe) {
  throw "AdvisorProbeEachSample requires advisor probing to remain enabled."
}

$configPath = ""
$environment = $null
if ($UseProcessEnvironment) {
  $environment = Read-ProcessServiceEnvironment
} else {
  $configPath = Resolve-ExistingPath -Path $ConfigFile -Label "ConfigFile"
  if (-not $AllowInsecureConfigFile) {
    Assert-SecureConfigFile -Path $configPath
  }
  $environment = Read-ServiceEnvironment -Path $configPath
}

if ($ConfirmInstall) {
  Assert-SystemScopePrivileges -Platform $platform -Scope $ServiceScope
  $WorkDir = Resolve-ExistingPath -Path $WorkDir -Label "WorkDir"
}

$checks = New-Object System.Collections.ArrayList
$startedAt = (Get-Date).ToUniversalTime()
$binary = ""
$commandArgs = @()
$safeResult = $null
$errorMessage = ""
$previousEnvironment = $null

try {
  $binary = Get-OrBuild-Binary -BackendDir $backendDir -RunRoot $runRoot -RequestedPath $BinaryPath -AllowBuild ([bool]$PlanOnly)
  Add-Check $checks "service_install_e2e.binary" "ok" "steward binary is available" @{ path = $binary; stable_required = [bool]$ConfirmInstall }

  if ([string]::IsNullOrWhiteSpace($WorkDir)) {
    $WorkDir = $backendDir
  } else {
    $WorkDir = Resolve-ExistingPath -Path $WorkDir -Label "WorkDir"
  }

  if (-not $UseProcessEnvironment) {
    $previousEnvironment = Set-TemporaryProcessEnvironment -Environment $environment
  }

  $commandArgs = @(
    "service", "install",
    "--name", $ServiceName,
    "--scope", $ServiceScope,
    "--binary", $binary,
    "--workdir", $WorkDir,
    "--strict-security"
  )
  if ($PlanOnly) {
    $commandArgs += "--dry-run"
  } else {
    $verificationDir = Join-Path $runRoot "service"
    New-Item -ItemType Directory -Force -Path $verificationDir | Out-Null
    $commandArgs += @(
      "--start",
      "--verify",
      "--verify-startup-timeout", $VerifyStartupTimeout,
      "--verify-watch-duration", $WatchDuration,
      "--verify-watch-interval", $WatchInterval,
      "--verify-evidence-dir", $verificationDir
    )
    if (-not $SkipAdvisorProbe) {
      $commandArgs += "--verify-advisor-probe"
    }
    if ($AdvisorProbeEachSample) {
      $commandArgs += "--verify-advisor-probe-each-sample"
    }
    if (-not $SkipAdvisorPrivacyProbe) {
      $commandArgs += "--verify-advisor-privacy-probe"
    }
  }

  $result = Invoke-StewardCommand -Executable $binary -Arguments $commandArgs
  $safeResult = Protect-CommandResult -Result $result -Environment $environment
  if ($safeResult.leaked_sensitive_keys.Count -gt 0) {
    Add-Check $checks "service_install_e2e.redaction" "error" "steward command output contained sensitive config values; evidence was redacted" @{ keys = $safeResult.leaked_sensitive_keys }
  } else {
    Add-Check $checks "service_install_e2e.redaction" "ok" "steward command output did not expose sensitive config values" $null
  }
  if ($result.exit_code -eq 0) {
    $message = if ($PlanOnly) { "strict native service install plan passed without writing service manager state" } else { "native service installed, started, and verified" }
    Add-Check $checks "service_install_e2e.command" "ok" $message @{ duration_ms = $result.duration_ms }
    if ($PlanOnly) {
      Add-Check $checks "service_install_e2e.plan" "ok" "plan-only mode completed without native service installation" $null
    } else {
      Add-Check $checks "service_install_e2e.install" "ok" "confirmed native service installation and post-install verification completed" $null
    }
  } else {
    Add-Check $checks "service_install_e2e.command" "error" "native service install command failed" @{ exit_code = $result.exit_code; output = $safeResult.output }
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "service_install_e2e.error" "error" $errorMessage $null
} finally {
  if ($null -ne $previousEnvironment) {
    Restore-ProcessEnvironment -Previous $previousEnvironment
  }
}

$completedAt = (Get-Date).ToUniversalTime()
$ok = $errorMessage -eq ""
foreach ($check in $checks) {
  if ($check.status -ne "ok") {
    $ok = $false
  }
}

$payload = [ordered]@{
  ok = $ok
  plan_only = [bool]$PlanOnly
  confirmed_install = [bool]$ConfirmInstall
  platform = $platform
  agent_id = [string]$environment["STEWARD_AGENT_ID"]
  started_at = $startedAt.ToString("o")
  completed_at = $completedAt.ToString("o")
  duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
  service = [ordered]@{
    platform = $platform
    name = $ServiceName
    scope = $ServiceScope
    binary = $binary
    workdir = $WorkDir
    agent_id = [string]$environment["STEWARD_AGENT_ID"]
    sync_key_id = [string]$environment["STEWARD_SYNC_ENCRYPTION_KEY_ID"]
    local_key_id = [string]$environment["STEWARD_LOCAL_ENCRYPTION_KEY_ID"]
  }
  config = [ordered]@{
    source = $(if ($UseProcessEnvironment) { "process-environment" } else { "protected-json" })
    path = $configPath
    keys = @($environment.Keys)
  }
  verification = [ordered]@{
    watch_duration = $WatchDuration
    watch_interval = $WatchInterval
    advisor_probe = (-not $SkipAdvisorProbe)
    advisor_probe_each_sample = [bool]$AdvisorProbeEachSample
    advisor_privacy_probe = (-not $SkipAdvisorPrivacyProbe)
  }
  command = @($binary) + @($commandArgs)
  result = $safeResult
  checks = @($checks)
  error = $errorMessage
}

$evidencePath = Write-Evidence -RunRoot $runRoot -Payload $payload -OK $ok
$summary = [ordered]@{
  ok = $ok
  plan_only = [bool]$PlanOnly
  platform = $platform
  service_name = $ServiceName
  service_scope = $ServiceScope
  evidence_path = $evidencePath
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 8

if (-not $ok) {
  exit 1
}
