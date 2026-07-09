param(
  [string]$DatabaseURL = "",

  [string]$Package = "./internal/httpapi",

  [string]$Run = "TestSteward",

  [int]$Count = 1,

  [int]$TimeoutSeconds = 300,

  [string]$EvidenceDir = "",

  [switch]$StartPostgres
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

function Redact-DatabaseURL {
  param([string]$Value)
  if ([string]::IsNullOrWhiteSpace($Value)) {
    return ""
  }
  return ($Value -replace '://([^:@/]+):([^@/]+)@', '://$1:<redacted>@')
}

function New-Timestamp {
  return (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmss.fffffffZ")
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

function Invoke-DockerCompose {
  param([string[]]$Arguments)
  $output = & docker @Arguments 2>&1
  $exitCode = $LASTEXITCODE
  return [pscustomobject]@{
    exit_code = $exitCode
    output = @($output | ForEach-Object { "$_" })
  }
}

function Start-ComposePostgres {
  param(
    [string]$RepoRoot,
    [int]$TimeoutSeconds,
    [System.Collections.ArrayList]$Checks
  )
  Require-Command "docker"
  Push-Location $RepoRoot
  try {
    $up = Invoke-DockerCompose @("compose", "up", "-d", "postgres")
    if ($up.exit_code -ne 0) {
      Add-Check $Checks "postgres_e2e.compose_up" "error" "docker compose up -d postgres failed" @{ exit_code = $up.exit_code; output = $up.output }
      throw "docker compose up -d postgres failed with exit code $($up.exit_code)"
    }
    Add-Check $Checks "postgres_e2e.compose_up" "ok" "postgres compose service start requested" $null

    $deadline = (Get-Date).ToUniversalTime().AddSeconds($TimeoutSeconds)
    $lastOutput = @()
    while ((Get-Date).ToUniversalTime() -lt $deadline) {
      $ready = Invoke-DockerCompose @("compose", "exec", "-T", "postgres", "pg_isready", "-U", "postgres", "-d", "mongojson")
      $lastOutput = $ready.output
      if ($ready.exit_code -eq 0) {
        Add-Check $Checks "postgres_e2e.postgres_ready" "ok" "postgres compose service is ready" $null
        return
      }
      Start-Sleep -Seconds 2
    }
    Add-Check $Checks "postgres_e2e.postgres_ready" "error" "postgres compose service did not become ready before timeout" @{ output = $lastOutput }
    throw "postgres compose service did not become ready before timeout"
  } finally {
    Pop-Location
  }
}

function Invoke-GoTest {
  param(
    [string]$BackendDir,
    [string]$DatabaseURL,
    [string]$Package,
    [string]$Run,
    [int]$Count,
    [int]$TimeoutSeconds
  )
  $oldTestDatabaseURL = $env:TEST_DATABASE_URL
  Push-Location $BackendDir
  try {
    $env:TEST_DATABASE_URL = $DatabaseURL
    $timeoutArg = "$($TimeoutSeconds)s"
    $countArg = "-count=$Count"
    $output = & go test $Package -run $Run $countArg -timeout $timeoutArg -v 2>&1
    $exitCode = $LASTEXITCODE
    return [pscustomobject]@{
      exit_code = $exitCode
      output = @($output | ForEach-Object { "$_" })
      command = @("go", "test", $Package, "-run", $Run, $countArg, "-timeout", $timeoutArg, "-v")
    }
  } finally {
    if ($null -eq $oldTestDatabaseURL) {
      Remove-Item Env:\TEST_DATABASE_URL -ErrorAction SilentlyContinue
    } else {
      $env:TEST_DATABASE_URL = $oldTestDatabaseURL
    }
    Pop-Location
  }
}

Require-Command "go"

$repoRoot = Resolve-RepoPath (Join-Path $PSScriptRoot "..")
$backendDir = Join-Path $repoRoot "backend"
if ([string]::IsNullOrWhiteSpace($DatabaseURL)) {
  $DatabaseURL = $env:TEST_DATABASE_URL
}
if ([string]::IsNullOrWhiteSpace($DatabaseURL)) {
  $DatabaseURL = "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable"
}
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
  $EvidenceDir = Join-Path $backendDir "dist\steward-e2e"
}

$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDir)
Assert-ChildPath -Parent $repoRoot -Child $evidenceRoot -Label "Evidence directory"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null

$timestamp = New-Timestamp
$logPath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-postgres-e2e-$timestamp" -Suffix ".log"
$checks = New-Object System.Collections.ArrayList
$startedAt = (Get-Date).ToUniversalTime()
$goResult = $null
$errorMessage = ""

try {
  if ($StartPostgres) {
    Start-ComposePostgres -RepoRoot $repoRoot -TimeoutSeconds $TimeoutSeconds -Checks $checks
  }

  $goResult = Invoke-GoTest -BackendDir $backendDir -DatabaseURL $DatabaseURL -Package $Package -Run $Run -Count $Count -TimeoutSeconds $TimeoutSeconds
  $goResult.output | Set-Content -LiteralPath $logPath -Encoding UTF8
  if ($goResult.exit_code -eq 0) {
    Add-Check $checks "postgres_e2e.go_test" "ok" "Postgres-backed steward E2E tests passed" @{ log_path = $logPath }
  } else {
    Add-Check $checks "postgres_e2e.go_test" "error" "Postgres-backed steward E2E tests failed" @{ exit_code = $goResult.exit_code; log_path = $logPath }
  }
} catch {
  $errorMessage = $_.Exception.Message
  if ($null -eq $goResult) {
    $errorMessage | Set-Content -LiteralPath $logPath -Encoding UTF8
  }
  Add-Check $checks "postgres_e2e.runner" "error" $errorMessage $null
}

if (Test-Path -LiteralPath $logPath) {
  Add-Check $checks "postgres_e2e.log" "ok" "test log written" @{ path = $logPath }
} else {
  Add-Check $checks "postgres_e2e.log" "error" "test log was not written" $null
}

$completedAt = (Get-Date).ToUniversalTime()
$ok = ($errorMessage -eq "" -and $null -ne $goResult -and $goResult.exit_code -eq 0)
$status = "fail"
if ($ok) {
  $status = "pass"
}
$evidencePath = New-UniquePath -Directory $evidenceRoot -BaseName "steward-verify-postgres-e2e-$timestamp" -Suffix "-$status.json"

$payload = [ordered]@{
  verification = [ordered]@{
    ok = $ok
    platform = Get-HostPlatform
    package = $Package
    run = $Run
    count = $Count
    timeout_seconds = $TimeoutSeconds
    database_url = Redact-DatabaseURL $DatabaseURL
    started_at = $startedAt.ToString("o")
    completed_at = $completedAt.ToString("o")
    duration_ms = [int64]($completedAt - $startedAt).TotalMilliseconds
    log_path = $logPath
    exit_code = if ($null -ne $goResult) { $goResult.exit_code } else { $null }
    error = $errorMessage
    checks = @($checks)
  }
}

$command = @("deploy/run-steward-postgres-e2e.ps1", "-Package", $Package, "-Run", $Run, "-Count", "$Count", "-TimeoutSeconds", "$TimeoutSeconds")
if ($StartPostgres) {
  $command += "-StartPostgres"
}

$envelope = [ordered]@{
  kind = "postgres-e2e"
  ok = $ok
  command = $command
  created_at = $startedAt.ToString("o")
  payload = $payload
}
$envelope | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $evidencePath -Encoding UTF8

$summary = [ordered]@{
  ok = $ok
  platform = Get-HostPlatform
  package = $Package
  run = $Run
  evidence_path = $evidencePath
  log_path = $logPath
  exit_code = if ($null -ne $goResult) { $goResult.exit_code } else { $null }
  error = $errorMessage
}
$summary | ConvertTo-Json -Depth 5

if (-not $ok) {
  exit 1
}
