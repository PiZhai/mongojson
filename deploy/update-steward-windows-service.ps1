[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$SourceDir,

  [string]$InstallDir = "C:\Program Files\MongojsonSteward",
  [string]$ServiceName = "MongojsonSteward",
  [string]$HealthURL = "http://127.0.0.1:18080/healthz",
  [int]$HealthTimeoutSeconds = 60
)

$ErrorActionPreference = "Stop"

function Test-IsAdministrator {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = [Security.Principal.WindowsPrincipal]::new($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Wait-ServiceState {
  param([string]$Name, [string]$State, [int]$TimeoutSeconds = 30)

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    $service = Get-Service -Name $Name -ErrorAction Stop
    if ([string]$service.Status -eq $State) {
      return
    }
    Start-Sleep -Milliseconds 500
  } while ((Get-Date) -lt $deadline)
  throw "service $Name did not reach state $State within $TimeoutSeconds seconds"
}

function Wait-Health {
  param([string]$URL, [int]$TimeoutSeconds)

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    try {
      $response = Invoke-RestMethod -Uri $URL -TimeoutSec 5
      if ($response.status -eq "ok") {
        return
      }
    } catch {
      if ((Get-Date) -ge $deadline) {
        throw
      }
    }
    Start-Sleep -Seconds 1
  } while ((Get-Date) -lt $deadline)
  throw "health endpoint $URL did not return status=ok within $TimeoutSeconds seconds"
}

if (-not (Test-IsAdministrator)) {
  throw "Run this script from an elevated PowerShell session."
}

$source = (Resolve-Path -LiteralPath $SourceDir).Path
$installParent = Split-Path -Parent $InstallDir
$resolvedParent = (Resolve-Path -LiteralPath $installParent).Path
$install = Join-Path $resolvedParent (Split-Path -Leaf $InstallDir)

if (-not (Test-Path -LiteralPath (Join-Path $source "steward.exe") -PathType Leaf)) {
  throw "source release does not contain steward.exe: $source"
}
if (-not (Test-Path -LiteralPath (Join-Path $source "ui\index.html") -PathType Leaf)) {
  throw "source release does not contain ui/index.html: $source"
}
if (-not (Test-Path -LiteralPath $install -PathType Container)) {
  throw "installed release directory does not exist: $install"
}

$service = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'"
if ($null -eq $service) {
  throw "Windows service does not exist: $ServiceName"
}

$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$backup = "$install.backup-$timestamp"
$failed = "$install.failed-$timestamp"
$movedCurrent = $false
$installedNew = $false

try {
  if ($service.State -ne "Stopped") {
    Stop-Service -Name $ServiceName -Force
    Wait-ServiceState -Name $ServiceName -State "Stopped"
  }

  Move-Item -LiteralPath $install -Destination $backup
  $movedCurrent = $true
  New-Item -ItemType Directory -Path $install | Out-Null
  $installedNew = $true
  Copy-Item -Path (Join-Path $source "*") -Destination $install -Recurse -Force

  $versionOutput = & (Join-Path $install "steward.exe") version 2>&1 | Out-String
  if ($LASTEXITCODE -ne 0) {
    throw "new steward binary version probe failed: $versionOutput"
  }

  Start-Service -Name $ServiceName
  Wait-ServiceState -Name $ServiceName -State "Running"
  Wait-Health -URL $HealthURL -TimeoutSeconds $HealthTimeoutSeconds

  $current = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'"
  [ordered]@{
    ok = $true
    service_name = $ServiceName
    state = $current.State
    start_mode = $current.StartMode
    account = $current.StartName
    install_dir = $install
    backup_dir = $backup
    health_url = $HealthURL
    version = $versionOutput.Trim()
  } | ConvertTo-Json -Depth 4
} catch {
  $failure = $_.Exception.Message
  try {
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    if ($installedNew -and (Test-Path -LiteralPath $install)) {
      Move-Item -LiteralPath $install -Destination $failed
    }
    if ($movedCurrent -and (Test-Path -LiteralPath $backup)) {
      Move-Item -LiteralPath $backup -Destination $install
      Start-Service -Name $ServiceName
      Wait-ServiceState -Name $ServiceName -State "Running"
    }
  } catch {
    throw "update failed: $failure; rollback also failed: $($_.Exception.Message)"
  }
  throw "update failed and previous release was restored: $failure"
}
