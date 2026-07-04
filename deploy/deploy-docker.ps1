param(
  [switch]$NoBuild,
  [switch]$Pull,
  [int]$FrontendPort = 80,
  [int]$BackendPort = 18080,
  [int]$PostgresPort = 5432
)

$ErrorActionPreference = "Stop"

$ProjectDir = Resolve-Path (Join-Path $PSScriptRoot "..")
$ComposeFile = Join-Path $ProjectDir "docker-compose.yml"

function Assert-Command($Name) {
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "Command '$Name' was not found. Please install Docker Desktop and ensure it is available in PATH."
  }
}

function Invoke-Checked($FilePath, [string[]]$Arguments) {
  & $FilePath @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "Command failed with exit code $LASTEXITCODE`: $FilePath $($Arguments -join ' ')"
  }
}

function Get-LocalUrl($Port) {
  if ($Port -eq 80) {
    return "http://127.0.0.1/"
  }
  return "http://127.0.0.1:$Port/"
}

Write-Host "Project: $ProjectDir"
Write-Host "Compose: $ComposeFile"
Write-Host "Ports: frontend=$FrontendPort backend=$BackendPort postgres=$PostgresPort"

Assert-Command "docker"
Invoke-Checked "docker" @("version")
Invoke-Checked "docker" @("compose", "version")

Push-Location $ProjectDir
try {
  $env:FRONTEND_HOST_PORT = [string]$FrontendPort
  $env:BACKEND_HOST_PORT = [string]$BackendPort
  $env:POSTGRES_HOST_PORT = [string]$PostgresPort

  if ($Pull) {
    Write-Host "Pulling base images..."
    Invoke-Checked "docker" @("compose", "-f", $ComposeFile, "pull")
  }

  $composeArgs = @("compose", "-f", $ComposeFile, "up", "-d", "--remove-orphans")
  if (-not $NoBuild) {
    $composeArgs += "--build"
  }

  Write-Host "Building and starting containers..."
  Invoke-Checked "docker" $composeArgs

  Write-Host "Waiting for services..."
  Start-Sleep -Seconds 8

  Write-Host "Container status:"
  Invoke-Checked "docker" @("compose", "-f", $ComposeFile, "ps")

  Write-Host ""
  Write-Host "Done."
  Write-Host "Frontend: $(Get-LocalUrl $FrontendPort)"
  Write-Host "Backend:  $(Get-LocalUrl $BackendPort)"
}
finally {
  Pop-Location
}
