param(
  [string]$SourceDir = $PSScriptRoot,
  [string]$ServiceName = "MongojsonStewardBrokerSession0Smoke"
)

$ErrorActionPreference = "Stop"

function Assert-Administrator {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = [Security.Principal.WindowsPrincipal]::new($identity)
  if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "This isolated Session 0 smoke test must run in an elevated PowerShell window."
  }
}

function Invoke-NativeChecked {
  param([string]$FilePath, [string[]]$Arguments)
  $output = & $FilePath @Arguments 2>&1 | Out-String
  if ($LASTEXITCODE -ne 0) {
    throw "$FilePath failed with exit code $LASTEXITCODE`: $output"
  }
  return $output
}

Assert-Administrator
$source = (Resolve-Path -LiteralPath $SourceDir).Path
$sourceBroker = Join-Path $source "steward-broker.exe"
if (-not (Test-Path -LiteralPath $sourceBroker -PathType Leaf)) {
  throw "Missing steward-broker.exe in $source"
}

$smokeDir = Join-Path $env:ProgramData "MongojsonSteward\session0-broker-smoke"
$broker = Join-Path $smokeDir "steward-broker.exe"
$resultFile = Join-Path $smokeDir "result.json"

try {
  & sc.exe stop $ServiceName *> $null
  & sc.exe delete $ServiceName *> $null
  Start-Sleep -Milliseconds 300
  if (Test-Path -LiteralPath $smokeDir) {
    [System.IO.Directory]::Delete($smokeDir, $true)
  }
  [System.IO.Directory]::CreateDirectory($smokeDir) | Out-Null
  [System.IO.File]::Copy($sourceBroker, $broker, $true)

  Invoke-NativeChecked icacls.exe @($smokeDir, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)(F)", "*S-1-5-32-544:(OI)(CI)(F)", "*S-1-5-12:(OI)(CI)(RX)") | Out-Null
  Invoke-NativeChecked icacls.exe @($broker, "/inheritance:r", "/grant:r", "*S-1-5-18:(F)", "*S-1-5-32-544:(F)", "*S-1-5-12:(RX)") | Out-Null

  $binaryPath = '"{0}" session0-self-test-service --service-name {1} --result-file "{2}"' -f $broker, $ServiceName, $resultFile
  Invoke-NativeChecked sc.exe @("create", $ServiceName, "binPath=", $binaryPath, "start=", "demand", "obj=", "LocalSystem") | Out-Null
  Invoke-NativeChecked sc.exe @("sidtype", $ServiceName, "unrestricted") | Out-Null
  Invoke-NativeChecked sc.exe @("start", $ServiceName) | Out-Null

  $deadline = [DateTime]::UtcNow.AddSeconds(30)
  while (-not (Test-Path -LiteralPath $resultFile) -and [DateTime]::UtcNow -lt $deadline) {
    Start-Sleep -Milliseconds 250
  }
  if (-not (Test-Path -LiteralPath $resultFile)) {
    $query = & sc.exe queryex $ServiceName 2>&1 | Out-String
    throw "Session 0 smoke service did not produce a result within 30 seconds. $query"
  }
  $result = Get-Content -LiteralPath $resultFile -Raw | ConvertFrom-Json
  $result | ConvertTo-Json -Depth 8
  if (-not $result.ok) {
    throw "Session 0 production capability launch failed; the diagnostic matrix above identifies the failing token layer."
  }
} finally {
  & sc.exe stop $ServiceName *> $null
  & sc.exe delete $ServiceName *> $null
  Start-Sleep -Milliseconds 300
  if (Test-Path -LiteralPath $smokeDir) {
    try { [System.IO.Directory]::Delete($smokeDir, $true) } catch { Write-Warning "Smoke directory cleanup pending: $smokeDir" }
  }
}
