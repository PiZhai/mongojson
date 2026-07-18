[CmdletBinding(SupportsShouldProcess)]
param(
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "MongojsonSteward"),
  [string]$TaskName = "MongojsonStewardCompanion",
  [switch]$RemoveData
)

$ErrorActionPreference = "Stop"
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  if ($PSCmdlet.ShouldProcess($TaskName, "unregister Session Companion task")) {
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
  }
}
$targetExe = Join-Path $InstallDir "steward-companion.exe"
Get-CimInstance Win32_Process -Filter "Name='steward-companion.exe'" -ErrorAction SilentlyContinue |
  Where-Object { $_.ExecutablePath -eq $targetExe } | ForEach-Object { Invoke-CimMethod -InputObject $_ -MethodName Terminate | Out-Null }
if ($RemoveData -and (Test-Path -LiteralPath $InstallDir)) {
  if ($PSCmdlet.ShouldProcess($InstallDir, "remove Session Companion data")) { Remove-Item -LiteralPath $InstallDir -Recurse -Force }
}
[ordered]@{ ok=$true; task_removed=$true; data_removed=[bool]$RemoveData } | ConvertTo-Json
