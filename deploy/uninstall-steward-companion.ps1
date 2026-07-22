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
$programs=[Environment]::GetFolderPath([Environment+SpecialFolder]::Programs)
$shortcutPath=if([string]::IsNullOrWhiteSpace($programs)){''}else{Join-Path $programs 'MongoJSON Steward.lnk'}
if(-not [string]::IsNullOrWhiteSpace($shortcutPath) -and (Test-Path -LiteralPath $shortcutPath -PathType Leaf)){
  $shell=New-Object -ComObject WScript.Shell
  try{$shortcut=$shell.CreateShortcut($shortcutPath);$owned=[IO.Path]::GetFullPath($shortcut.TargetPath).Equals([IO.Path]::GetFullPath($targetExe),[StringComparison]::OrdinalIgnoreCase)}finally{
    if($null -ne $shortcut){[Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut)|Out-Null}
    if($null -ne $shell){[Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell)|Out-Null}
  }
  if($owned -and $PSCmdlet.ShouldProcess($shortcutPath,'remove Steward workspace shortcut')){Remove-Item -LiteralPath $shortcutPath -Force}
}
Get-CimInstance Win32_Process -Filter "Name='steward-companion.exe'" -ErrorAction SilentlyContinue |
  Where-Object { $_.ExecutablePath -eq $targetExe } | ForEach-Object { Invoke-CimMethod -InputObject $_ -MethodName Terminate | Out-Null }
if ($RemoveData -and (Test-Path -LiteralPath $InstallDir)) {
  if ($PSCmdlet.ShouldProcess($InstallDir, "remove Session Companion data")) { Remove-Item -LiteralPath $InstallDir -Recurse -Force }
}
[ordered]@{ ok=$true; task_removed=$true; shortcut_removed=[bool]$owned; data_removed=[bool]$RemoveData } | ConvertTo-Json
