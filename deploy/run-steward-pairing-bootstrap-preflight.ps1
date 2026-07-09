param(
  [string]$BinaryPath = "",

  [string]$EvidenceDir = "",

  [string]$ServiceName = "MongojsonStewardPairingBootstrapPreflight",

  [string]$ServiceScope = "",

  [string]$SenderID = "bootstrap-sender",

  [string]$SenderPlatform = "windows",

  [string]$SenderAPIBase = "http://127.0.0.1:19681/api",

  [string]$RecipientID = "bootstrap-recipient",

  [string]$RecipientHTTPAddr = "127.0.0.1:19680",

  [string]$RecipientPeerHTTPAddr = "127.0.0.1:19681",

  [string]$RecipientPublicAPIBase = "http://127.0.0.1:19681/api",

  [string]$DatabaseURL = "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable",

  [string]$SyncKeyID = "bootstrap-sync-v1",

  [string]$PreviousSyncKeyID = "bootstrap-sync-v0",

  [string]$LocalKeyID = "bootstrap-local-v1"
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
  $outputPath = Join-Path $binaryDir ("steward-pairing-bootstrap-preflight-" + (Get-HostPlatform) + "-" + (Get-HostArch) + $extension)
  Assert-ChildPath -Parent $RepoRoot -Child $outputPath -Label "Pairing bootstrap preflight binary"

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

function Write-CurrentEnvFile {
  param(
    [string]$Path,
    [hashtable]$Environment
  )
  $ordered = [ordered]@{}
  foreach ($key in ($Environment.Keys | Sort-Object)) {
    $ordered[$key] = [string]$Environment[$key]
  }
  $ordered | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $Path -Encoding UTF8
}

function Contains-Value {
  param(
    [object[]]$Values,
    [string]$Expected
  )
  foreach ($value in $Values) {
    if ([string]$value -eq $Expected) {
      return $true
    }
  }
  return $false
}

function Contains-Arg {
  param(
    [object[]]$ArgList,
    [string]$Expected
  )
  foreach ($arg in $ArgList) {
    if ([string]$arg -eq $Expected) {
      return $true
    }
  }
  return $false
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-pairing-bootstrap-preflight"
}
if ([string]::IsNullOrWhiteSpace($ServiceScope)) {
  if ((Get-HostPlatform) -eq "windows") {
    $ServiceScope = "system"
  } else {
    $ServiceScope = "user"
  }
}
$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$timestamp = New-Timestamp
$startedAt = (Get-Date).ToUniversalTime()
$checks = New-Object System.Collections.ArrayList
$binary = ""
$bundlePath = ""
$currentEnvPath = ""
$bootstrapResult = $null
$bootstrapOutput = $null
$errorMessage = ""
$secretValues = @()

try {
  $binary = Get-OrBuild-Binary -RepoRoot $repoRoot -BackendDir $backendDir -EvidenceRoot $evidenceRoot -BinaryPath $BinaryPath
  Add-Check $checks "pairing_bootstrap_preflight.binary" "ok" "steward pairing bootstrap preflight binary is available" @{ path = $binary }
  Invoke-StewardJSON -BinaryPath $binary -Arguments @("version") | Out-Null
  Add-Check $checks "pairing_bootstrap_preflight.version" "ok" "steward binary returned version metadata" $null

  $senderDeviceKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("keygen", "--prefix", $SenderID)
  $recipientDeviceKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("keygen", "--prefix", $RecipientID)
  $recipientPairingKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("pairing", "keygen", "--label", $RecipientID)
  $syncKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $SyncKeyID)
  $previousSyncKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $PreviousSyncKeyID)
  $localKey = Invoke-StewardJSON -BinaryPath $binary -Arguments @("sync-keygen", "--key-id", $LocalKeyID)
  $syncSecret = New-Secret
  $previousSyncKeys = $PreviousSyncKeyID + ":" + $previousSyncKey.key

  $secretValues = @(
    $senderDeviceKey.private_key,
    $recipientDeviceKey.private_key,
    $recipientPairingKey.private_key,
    $syncSecret,
    $syncKey.key,
    $previousSyncKeys,
    $localKey.key
  ) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }

  $scratchDir = Join-Path $evidenceRoot "scratch"
  New-Item -ItemType Directory -Force -Path $scratchDir | Out-Null
  $bundlePath = Join-Path $scratchDir ($SenderID + ".encrypted.pairing.json")
  $currentEnvPath = Join-Path $scratchDir "recipient-current-env.json"

  $exportResult = Invoke-StewardCommand -BinaryPath $binary -Arguments @(
    "pairing", "export",
    "--id", $SenderID,
    "--name", ($SenderID + " Device"),
    "--platform", $SenderPlatform,
    "--api-base-url", $SenderAPIBase,
    "--public-key", $senderDeviceKey.public_key,
    "--private-key", $senderDeviceKey.private_key,
    "--permission-level", "A3",
    "--include-sync-secret",
    "--sync-secret", $syncSecret,
    "--include-sync-encryption-key",
    "--sync-encryption-key", $syncKey.key,
    "--sync-encryption-key-id", $SyncKeyID,
    "--include-sync-encryption-previous-keys",
    "--sync-encryption-previous-keys", $previousSyncKeys,
    "--encrypt-shared-sync-for", $recipientPairingKey.public_key,
    "--output", $bundlePath
  )
  if ($exportResult.exit_code -ne 0) {
    Add-Check $checks "pairing_bootstrap_preflight.bundle" "error" "pairing export failed" @{ exit_code = $exportResult.exit_code; output = $exportResult.output }
    throw "pairing export failed with exit code $($exportResult.exit_code)"
  }

  $bundle = Get-Content -LiteralPath $bundlePath -Raw | ConvertFrom-Json
  if ($bundle.signature.algorithm -eq "ed25519.pairing-bundle.v1" -and
      $bundle.shared_sync_encrypted.algorithm -eq "nacl.box.seal_anonymous.x25519-xsalsa20-poly1305" -and
      $null -eq $bundle.shared_sync) {
    Add-Check $checks "pairing_bootstrap_preflight.bundle" "ok" "pairing bundle is signed and shared sync material is encrypted" @{
      device_id = $bundle.device.id
      encrypted = $true
      signed = $true
    }
  } else {
    Add-Check $checks "pairing_bootstrap_preflight.bundle" "error" "pairing bundle was not signed and encrypted as expected" $bundle
  }

  $currentEnv = @{
    "HTTP_ADDR" = $RecipientHTTPAddr
    "STEWARD_PEER_HTTP_ADDR" = $RecipientPeerHTTPAddr
    "DATABASE_URL" = $DatabaseURL
    "STORAGE_DIR" = (Join-Path $evidenceRoot "recipient-data")
    "STEWARD_AGENT_ID" = $RecipientID
    "STEWARD_PUBLIC_API_BASE" = $RecipientPublicAPIBase
    "STEWARD_SYNC_REQUIRE_AUTH" = "true"
    "STEWARD_SYNC_ALLOW_INSECURE" = "false"
    "STEWARD_DEVICE_PRIVATE_KEY" = $recipientDeviceKey.private_key
    "STEWARD_DEVICE_PUBLIC_KEY" = $recipientDeviceKey.public_key
    "STEWARD_LOCAL_ENCRYPTION_KEY" = $localKey.key
    "STEWARD_LOCAL_ENCRYPTION_KEY_ID" = $LocalKeyID
    "STEWARD_HEARTBEAT_INTERVAL" = "1m"
    "STEWARD_SYNC_INTERVAL" = "5m"
    "STEWARD_AUTONOMY_INTERVAL" = "15m"
    "STEWARD_LLM_PROVIDER" = "disabled"
  }
  Write-CurrentEnvFile -Path $currentEnvPath -Environment $currentEnv

  $bootstrapArgs = @(
    "pairing", "bootstrap",
    "--file", $bundlePath,
    "--service-name", $ServiceName,
    "--service-scope", $ServiceScope,
    "--decrypt-shared-sync-key", $recipientPairingKey.private_key,
    "--require-signature",
    "--current-env-file", $currentEnvPath,
    "--strict-security"
  )
  $bootstrapResult = Invoke-StewardCommand -BinaryPath $binary -Arguments $bootstrapArgs
  if ($bootstrapResult.exit_code -ne 0) {
    Add-Check $checks "pairing_bootstrap_preflight.bootstrap" "error" "pairing bootstrap failed" @{ exit_code = $bootstrapResult.exit_code; output = $bootstrapResult.output }
    throw "pairing bootstrap failed with exit code $($bootstrapResult.exit_code)"
  }
  $bootstrapOutput = $bootstrapResult.text | ConvertFrom-Json
  if ($bootstrapOutput.ok -eq $true -and $bootstrapOutput.device.id -eq $SenderID) {
    Add-Check $checks "pairing_bootstrap_preflight.bootstrap" "ok" "pairing bootstrap decrypted and validated the signed bundle" @{
      device_id = $bootstrapOutput.device.id
      platform = $bootstrapOutput.device.platform
    }
  } else {
    Add-Check $checks "pairing_bootstrap_preflight.bootstrap" "error" "pairing bootstrap output did not include expected sender device" $bootstrapOutput.device
  }

  $suggestedKeys = @($bootstrapOutput.suggested_env_keys)
  $hasSuggestedKeys = (Contains-Value -Values $suggestedKeys -Expected "STEWARD_SYNC_SECRET") -and
    (Contains-Value -Values $suggestedKeys -Expected "STEWARD_SYNC_ENCRYPTION_KEY") -and
    (Contains-Value -Values $suggestedKeys -Expected "STEWARD_SYNC_ENCRYPTION_KEY_ID") -and
    (Contains-Value -Values $suggestedKeys -Expected "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS")
  $redacted = $bootstrapOutput.suggested_env_redacted
  $redactedOK = $redacted.STEWARD_SYNC_SECRET -eq "<redacted>" -and
    $redacted.STEWARD_SYNC_ENCRYPTION_KEY -eq "<redacted>" -and
    $redacted.STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS -eq "<redacted>" -and
    $redacted.STEWARD_SYNC_ENCRYPTION_KEY_ID -eq $SyncKeyID
  if ($hasSuggestedKeys -and $redactedOK) {
    Add-Check $checks "pairing_bootstrap_preflight.suggested_env" "ok" "suggested service environment keys are present and redacted" @{
      keys = $suggestedKeys
      sync_key_id = $redacted.STEWARD_SYNC_ENCRYPTION_KEY_ID
    }
  } else {
    Add-Check $checks "pairing_bootstrap_preflight.suggested_env" "error" "suggested service environment keys or redaction were not as expected" @{
      keys = $suggestedKeys
      redacted = $redacted
    }
  }

  if ($null -ne $bootstrapOutput.service_env_plan -and
      $bootstrapOutput.service_env_plan.environment.STEWARD_SYNC_ENCRYPTION_KEY_ID -eq $SyncKeyID -and
      $bootstrapOutput.service_env_plan.environment.STEWARD_SYNC_ENCRYPTION_KEY -eq "<redacted>" -and
      $bootstrapOutput.service_env_plan.environment.STEWARD_DEVICE_PRIVATE_KEY -eq "<redacted>") {
    Add-Check $checks "pairing_bootstrap_preflight.service_env_plan" "ok" "bootstrap rendered a redacted non-mutating service env plan" @{
      sync_key_id = $bootstrapOutput.service_env_plan.environment.STEWARD_SYNC_ENCRYPTION_KEY_ID
    }
  } else {
    Add-Check $checks "pairing_bootstrap_preflight.service_env_plan" "error" "bootstrap did not render the expected redacted service env plan" $bootstrapOutput.service_env_plan
  }

  $runtimeArgs = @($bootstrapOutput.verification.runtime_args)
  $verificationOK = (Contains-Arg -ArgList $runtimeArgs -Expected "--strict-security") -and
    (Contains-Arg -ArgList $runtimeArgs -Expected "--expect-agent-id") -and
    (Contains-Arg -ArgList $runtimeArgs -Expected $RecipientID) -and
    (Contains-Arg -ArgList $runtimeArgs -Expected "--expect-sync-key-id") -and
    (Contains-Arg -ArgList $runtimeArgs -Expected $SyncKeyID) -and
    (Contains-Arg -ArgList $runtimeArgs -Expected "--expect-local-key-id") -and
    (Contains-Arg -ArgList $runtimeArgs -Expected $LocalKeyID)
  if ($verificationOK) {
    Add-Check $checks "pairing_bootstrap_preflight.verification_advice" "ok" "bootstrap verification advice includes target agent and key expectations" $bootstrapOutput.verification
  } else {
    Add-Check $checks "pairing_bootstrap_preflight.verification_advice" "error" "bootstrap verification advice did not include expected assertions" $bootstrapOutput.verification
  }

  $applyArgs = @($bootstrapOutput.commands.service_env_apply)
  $planArgs = @($bootstrapOutput.commands.service_env_plan)
  $commandsOK = (Contains-Arg -ArgList $planArgs -Expected "--current-env-file") -and
    (Contains-Arg -ArgList $planArgs -Expected "--scope") -and
    (Contains-Arg -ArgList $planArgs -Expected $ServiceScope) -and
    (Contains-Arg -ArgList $planArgs -Expected "--strict-security") -and
    (Contains-Arg -ArgList $applyArgs -Expected "--scope") -and
    (Contains-Arg -ArgList $applyArgs -Expected $ServiceScope) -and
    (Contains-Arg -ArgList $applyArgs -Expected "--confirm") -and
    (Contains-Arg -ArgList $applyArgs -Expected "--restart") -and
    (Contains-Arg -ArgList $applyArgs -Expected "--verify") -and
    (Contains-Arg -ArgList $applyArgs -Expected "<recipient pairing private_key>")
  if ($commandsOK) {
    Add-Check $checks "pairing_bootstrap_preflight.command_advice" "ok" "bootstrap command advice includes review, apply, restart, verify, and private-key placeholders" $bootstrapOutput.commands
  } else {
    Add-Check $checks "pairing_bootstrap_preflight.command_advice" "error" "bootstrap command advice did not include expected safety flags or placeholder" $bootstrapOutput.commands
  }

  $leaked = @()
  foreach ($secret in $secretValues) {
    if ($bootstrapResult.text.Contains($secret)) {
      $leaked += $secret
    }
  }
  if ($leaked.Count -eq 0) {
    Add-Check $checks "pairing_bootstrap_preflight.redaction" "ok" "bootstrap output did not leak pairing private keys or sync secret material" $null
  } else {
    Add-Check $checks "pairing_bootstrap_preflight.redaction" "error" "bootstrap output leaked sensitive values" @{ leaked_count = $leaked.Count }
  }

  $notesText = (@($bootstrapOutput.notes) -join "`n").ToLowerInvariant()
  if ($notesText.Contains("non-mutating plan") -and $notesText.Contains("does not register") -and $notesText.Contains("does not") -and $notesText.Contains("model endpoint")) {
    Add-Check $checks "pairing_bootstrap_preflight.no_mutation" "ok" "bootstrap output explicitly states that it does not mutate service, device, API, or model state" $bootstrapOutput.notes
  } else {
    Add-Check $checks "pairing_bootstrap_preflight.no_mutation" "error" "bootstrap output did not preserve the non-mutating safety note" $bootstrapOutput.notes
  }
} catch {
  $errorMessage = $_.Exception.Message
  Add-Check $checks "pairing_bootstrap_preflight.runner" "error" $errorMessage $null
} finally {
  if (-not [string]::IsNullOrWhiteSpace($currentEnvPath) -and (Test-Path -LiteralPath $currentEnvPath)) {
    Remove-Item -LiteralPath $currentEnvPath -Force
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
$evidencePath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-verify-pairing-bootstrap-preflight-$timestamp" -Suffix "-$status.json"

$payload = [ordered]@{
  verification = [ordered]@{
    ok = $ok
    platform = Get-HostPlatform
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    binary_path = $binary
    service_name = $ServiceName
    service_scope = $ServiceScope
    sender_id = $SenderID
    recipient_id = $RecipientID
    sync_key_id = $SyncKeyID
    local_key_id = $LocalKeyID
    bundle_path = $bundlePath
    bootstrap_exit_code = if ($null -ne $bootstrapResult) { $bootstrapResult.exit_code } else { $null }
    bootstrap_output = if ($null -ne $bootstrapResult) { $bootstrapResult.output } else { $null }
    error = $errorMessage
    checks = @($checks)
  }
}

$command = @(
  "deploy/run-steward-pairing-bootstrap-preflight.ps1",
  "-ServiceName", $ServiceName,
  "-ServiceScope", $ServiceScope,
  "-SenderID", $SenderID,
  "-RecipientID", $RecipientID,
  "-SyncKeyID", $SyncKeyID,
  "-LocalKeyID", $LocalKeyID
)

$envelope = [ordered]@{
  kind = "pairing-bootstrap-preflight"
  ok = $ok
  command = $command
  created_at = $startedAt.ToString("o")
  payload = $payload
}
$envelope | ConvertTo-Json -Depth 14 | Set-Content -LiteralPath $evidencePath -Encoding UTF8

$summary = [ordered]@{
  ok = $ok
  platform = Get-HostPlatform
  evidence_path = $evidencePath
  binary_path = $binary
  service_name = $ServiceName
  service_scope = $ServiceScope
  sender_id = $SenderID
  recipient_id = $RecipientID
  sync_key_id = $SyncKeyID
  local_key_id = $LocalKeyID
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 6

if (-not $ok) {
  exit 1
}
