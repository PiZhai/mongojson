[CmdletBinding()]
param(
  [Parameter(Mandatory=$true)][string]$StewardBinary,
  [string]$ServiceName='MongojsonStewardR51E2E'
)
$ErrorActionPreference='Stop'
$install='C:\Program Files\MongojsonSteward-R51-E2E'
$data='C:\ProgramData\MongojsonSteward-R51-E2E'
$source=(Resolve-Path -LiteralPath $StewardBinary).Path
if(Get-Service $ServiceName -ErrorAction SilentlyContinue){throw "test service already exists: $ServiceName"}
try {
  $output=& $source service install --name $ServiceName --scope system --binary $source --workdir $data `
    --http-addr 127.0.0.1:19080 --peer-http-addr 127.0.0.1:19081 `
    --database-url 'postgres://probe:probe@127.0.0.1:55439/probe?sslmode=disable' `
    --storage-dir "$data\data" --log-dir "$data\logs" --windows-hardened `
    --windows-install-dir $install --windows-private-environment-file "$data\config\service-secrets.json" `
    --windows-service-account localservice --windows-service-sid-type restricted 2>&1|Out-String
  if($LASTEXITCODE -ne 0){throw $output}
  $service=Get-CimInstance Win32_Service -Filter "Name='$ServiceName'"
  $sidType=& sc.exe qsidtype $ServiceName 2>&1|Out-String
  $private=Get-Content "$data\config\service-secrets.json" -Raw|ConvertFrom-Json
  $registry=(Get-ItemProperty "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName").Environment
  $result=[ordered]@{
    ok=($service.StartName -eq 'NT AUTHORITY\LocalService' -and $sidType -match 'RESTRICTED' -and -not([bool](@($registry)-match '^DATABASE_URL=')))
    account=$service.StartName; sid_type=$sidType.Trim(); private_keys=@($private.PSObject.Properties.Name)
    registry_contains_database_url=[bool](@($registry)-match '^DATABASE_URL=')
    install_acl=(& icacls.exe $install 2>&1|Out-String).Trim()
    private_acl=(& icacls.exe "$data\config\service-secrets.json" 2>&1|Out-String).Trim()
  }
  $result|ConvertTo-Json -Depth 5
  if(-not $result.ok){exit 1}
} finally {
  if(Get-Service $ServiceName -ErrorAction SilentlyContinue){& $source service uninstall --name $ServiceName --scope system 2>&1|Out-Null;Start-Sleep -Seconds 1}
  foreach($target in @($install,$data)){
    $full=[IO.Path]::GetFullPath($target)
    if($full -ne $install -and $full -ne $data){throw "refusing cleanup outside R5.1 test roots: $full"}
    if(Test-Path -LiteralPath $full){Remove-Item -LiteralPath $full -Recurse -Force}
  }
}
