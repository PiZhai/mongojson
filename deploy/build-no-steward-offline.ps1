[CmdletBinding()]
param(
    [string]$Tag = "deploy-$(Get-Date -Format 'yyyyMMdd-HHmmss')",
    [string]$Platform = 'linux/amd64',
    [string]$NginxImage = 'nginx:1.29-alpine'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$commit = (git -C $repo rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0) {
    throw 'Unable to resolve the repository commit.'
}

$buildDir = Join-Path ([System.IO.Path]::GetTempPath()) "mongojson-build-$Tag"
$releaseDir = Join-Path $repo 'output\release'
$archive = Join-Path $releaseDir "mongojson-no-steward-images-$Tag.tar"
$archiveGzip = "$archive.gz"
$checksumFile = "$archiveGzip.sha256"
$manifestFile = Join-Path $releaseDir "mongojson-no-steward-$Tag.txt"
$frontendModules = 'inspect,json,mongo-json,visualize,memo-docs,music,watch-party,canvas'
$worktreeAdded = $false

try {
    git -C $repo worktree add --detach $buildDir $commit
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to create the clean build worktree.'
    }
    $worktreeAdded = $true

    docker build --platform $Platform `
        --build-arg 'APP_DISABLED_MODULES=steward' `
        -t "mongojson-backend:$Tag" `
        (Join-Path $buildDir 'backend')
    if ($LASTEXITCODE -ne 0) { throw 'Backend image build failed.' }

    docker build --platform $Platform `
        --build-arg "VITE_INCLUDED_MODULES=$frontendModules" `
        -t "mongojson-frontend:$Tag" `
        (Join-Path $buildDir 'frontend')
    if ($LASTEXITCODE -ne 0) { throw 'Frontend image build failed.' }

    docker pull --platform $Platform $NginxImage
    if ($LASTEXITCODE -ne 0) { throw 'Nginx image pull failed.' }

    New-Item -ItemType Directory -Force -Path $releaseDir | Out-Null
    docker save -o $archive "mongojson-backend:$Tag" "mongojson-frontend:$Tag" $NginxImage
    if ($LASTEXITCODE -ne 0) { throw 'Docker image export failed.' }

    $inputStream = [System.IO.File]::OpenRead($archive)
    try {
        $outputStream = [System.IO.File]::Create($archiveGzip)
        try {
            $gzipStream = [System.IO.Compression.GzipStream]::new(
                $outputStream,
                [System.IO.Compression.CompressionLevel]::Optimal
            )
            try {
                $inputStream.CopyTo($gzipStream)
            }
            finally {
                $gzipStream.Dispose()
            }
        }
        finally {
            $outputStream.Dispose()
        }
    }
    finally {
        $inputStream.Dispose()
    }

    Remove-Item -LiteralPath $archive
    $checksum = (Get-FileHash -Algorithm SHA256 -LiteralPath $archiveGzip).Hash.ToLowerInvariant()
    Set-Content -LiteralPath $checksumFile -Encoding ascii -NoNewline `
        -Value "$checksum  $([System.IO.Path]::GetFileName($archiveGzip))"
    @(
        "TAG=$Tag"
        "COMMIT=$commit"
        "PLATFORM=$Platform"
        'APP_DISABLED_MODULES=steward'
        "VITE_INCLUDED_MODULES=$frontendModules"
        "NGINX_IMAGE=$NginxImage"
        "ARCHIVE=$([System.IO.Path]::GetFileName($archiveGzip))"
        "SHA256=$checksum"
    ) | Set-Content -LiteralPath $manifestFile -Encoding utf8

    Get-Item -LiteralPath $archiveGzip, $checksumFile, $manifestFile |
        Select-Object FullName, @{Name = 'SizeMB'; Expression = { [math]::Round($_.Length / 1MB, 2) }}
    Write-Host "TAG=$Tag"
    Write-Host "COMMIT=$commit"
}
finally {
    if ($worktreeAdded) {
        git -C $repo worktree remove $buildDir
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "Build worktree remains at $buildDir and must be removed manually."
        }
    }
}
