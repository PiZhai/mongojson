param(
    [string]$DatabaseUrl = "postgres://postgres:postgres@127.0.0.1:55439/mongojson?sslmode=disable",
    [int]$ManagementPort = 18086,
    [int]$PeerPort = 18087
)

$ErrorActionPreference = "Stop"
& (Join-Path $PSScriptRoot "verify-r4-1-workers.ps1") `
    -DatabaseUrl $DatabaseUrl -ManagementPort $ManagementPort -PeerPort $PeerPort -VerifySaga
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
