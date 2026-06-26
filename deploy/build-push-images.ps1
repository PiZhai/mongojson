param(
  [Parameter(Mandatory = $true)]
  [string]$Registry,

  [string]$Tag = $(git rev-parse --short HEAD),

  [string]$Platform = "linux/amd64",

  [string]$BackendName = "mongojson-backend",

  [string]$FrontendName = "mongojson-frontend"
)

$ErrorActionPreference = "Stop"

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
  throw "Missing required command: docker"
}

if ([string]::IsNullOrWhiteSpace($Tag)) {
  throw "Missing image tag. Pass -Tag explicitly or run from a git worktree."
}

$registryPrefix = $Registry.TrimEnd("/")
$backendImage = "$registryPrefix/$BackendName`:$Tag"
$frontendImage = "$registryPrefix/$FrontendName`:$Tag"

Write-Host "[deploy] Building and pushing backend image: $backendImage"
docker buildx build --platform $Platform -t $backendImage --push ./backend
if ($LASTEXITCODE -ne 0) {
  throw "Backend image build/push failed with exit code $LASTEXITCODE"
}

Write-Host "[deploy] Building and pushing frontend image: $frontendImage"
docker buildx build --platform $Platform -t $frontendImage --push ./frontend
if ($LASTEXITCODE -ne 0) {
  throw "Frontend image build/push failed with exit code $LASTEXITCODE"
}

Write-Host ""
Write-Host "Add or update these lines in /opt/personal-tooling/env/prod.env:"
Write-Host "BACKEND_IMAGE=$backendImage"
Write-Host "FRONTEND_IMAGE=$frontendImage"
