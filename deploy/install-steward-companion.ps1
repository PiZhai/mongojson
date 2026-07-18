[CmdletBinding(SupportsShouldProcess)]
param(
  [Parameter(Mandatory = $true)][string]$SourceDir,
  [Parameter(Mandatory = $true)][string]$LocalEncryptionKey,
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "MongojsonSteward"),
  [string]$TaskName = "MongojsonStewardCompanion",
  [string]$ServiceName = "MongojsonSteward",
  [switch]$Start
)

$ErrorActionPreference = "Stop"

function Protect-CurrentUserPath([string]$Path) {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  $userGrant="*${identity}:F"; $systemGrant="*S-1-5-18:F"
  if(Test-Path -LiteralPath $Path -PathType Container){$userGrant="*${identity}:(OI)(CI)F";$systemGrant="*S-1-5-18:(OI)(CI)F"}
  & icacls.exe $Path /inheritance:r /grant:r $userGrant $systemGrant | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to protect companion path: $Path" }
}
function Write-Utf8NoBom([string]$Path,[string]$Value){[IO.File]::WriteAllText($Path,$Value,[Text.UTF8Encoding]::new($false))}

if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) { throw "LOCALAPPDATA is unavailable" }
$source = (Resolve-Path -LiteralPath $SourceDir).Path
$sourceExe = Join-Path $source "steward-companion.exe"
if (-not (Test-Path -LiteralPath $sourceExe -PathType Leaf)) { throw "missing steward-companion.exe in $source" }
if ($LocalEncryptionKey -notmatch '^[A-Za-z0-9+/]{43}=$') { throw "LocalEncryptionKey must be a base64 encoded 32-byte key" }

if ($PSCmdlet.ShouldProcess($InstallDir, "install Steward Session Companion")) {
  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  Protect-CurrentUserPath $InstallDir
  Copy-Item -LiteralPath $sourceExe -Destination (Join-Path $InstallDir "steward-companion.exe") -Force
  $secretPath = Join-Path $InstallDir "companion-secrets.json"
  Write-Utf8NoBom $secretPath (@{ STEWARD_LOCAL_ENCRYPTION_KEY = $LocalEncryptionKey } | ConvertTo-Json -Compress)
  Protect-CurrentUserPath $secretPath

  $exe = Join-Path $InstallDir "steward-companion.exe"
  $arguments = "--service-name `"$ServiceName`" --private-environment-file `"$secretPath`""
  $action = New-ScheduledTaskAction -Execute $exe -Argument $arguments -WorkingDirectory $InstallDir
  $trigger = New-ScheduledTaskTrigger -AtLogOn -User ([Security.Principal.WindowsIdentity]::GetCurrent().Name)
  $principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
  $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
  Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
  if ($Start) { Start-ScheduledTask -TaskName $TaskName }
}

[ordered]@{ ok=$true; task_name=$TaskName; install_dir=$InstallDir; service_name=$ServiceName; run_level="Limited" } | ConvertTo-Json
