$repoRoot=(Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$buildScript=Join-Path $repoRoot 'deploy\build-steward.ps1'

function Invoke-AtomicBuildFixture {
  param(
    [string]$OutputDir,
    [string]$Fault='',
    [switch]$NoClean
  )

  $fakeBin=Join-Path ([IO.Path]::GetTempPath()) ('steward-fake-go-'+[guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $fakeBin|Out-Null
  if([Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([Runtime.InteropServices.OSPlatform]::Windows)){
    @'
@echo off
if /I "%~1"=="env" goto env
if /I "%~1"=="build" goto scanbuild
if /I "%~1"=="test" exit /b 0
exit /b 2
:env
echo go1.99.0-fixture
exit /b 0
:scanbuild
shift
:scanarg
if "%~1"=="" exit /b 3
if /I "%~1"=="-o" goto writeoutput
shift
goto scanarg
:writeoutput
shift
> "%~1" echo fixture-binary
exit /b 0
'@|Set-Content -LiteralPath (Join-Path $fakeBin 'go.cmd') -Encoding ASCII
  }else{
    @'
#!/bin/sh
if [ "$1" = "env" ]; then
  printf '%s\n' 'go1.99.0-fixture'
  exit 0
fi
if [ "$1" != "build" ]; then
  exit 2
fi
shift
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    printf '%s\n' 'fixture-binary' > "$1"
    exit 0
  fi
  shift
done
exit 3
'@|Set-Content -LiteralPath (Join-Path $fakeBin 'go') -Encoding utf8NoBOM
    & chmod 700 -- (Join-Path $fakeBin 'go')
  }

  $oldPath=$env:PATH
  $oldFault=$env:STEWARD_BUILD_TEST_FAULT_INJECTION
  $result=[ordered]@{succeeded=$false;error=''}
  try{
    $env:PATH=$fakeBin+[IO.Path]::PathSeparator+$oldPath
    if([string]::IsNullOrWhiteSpace($Fault)){
      Remove-Item Env:STEWARD_BUILD_TEST_FAULT_INJECTION -ErrorAction SilentlyContinue
    }else{
      $env:STEWARD_BUILD_TEST_FAULT_INJECTION=$Fault
    }
    try{
      $buildArgs=@{
        Targets='linux/amd64'
        OutputDir=$OutputDir
        Version='atomic-fixture'
        SkipTests=$true
        SkipUI=$true
        AllowDirtyWorktree=$true
        Clean=(-not $NoClean)
      }
      & $buildScript @buildArgs|Out-Null
      $result.succeeded=$true
    }catch{
      $result.error=$_.Exception.Message
    }
  }finally{
    $env:PATH=$oldPath
    if($null -eq $oldFault){
      Remove-Item Env:STEWARD_BUILD_TEST_FAULT_INJECTION -ErrorAction SilentlyContinue
    }else{
      $env:STEWARD_BUILD_TEST_FAULT_INJECTION=$oldFault
    }
    Remove-Item -LiteralPath $fakeBin -Recurse -Force -ErrorAction SilentlyContinue
  }
  return [pscustomobject]$result
}

Describe 'Steward atomic release publication' {
  It 'publishes a complete staged package and explicit target/UI metadata' {
    $output=Join-Path $repoRoot ('backend\dist\atomic-publish-success-'+[guid]::NewGuid().ToString('N'))
    try{
      New-Item -ItemType Directory -Path $output|Out-Null
      Set-Content -LiteralPath (Join-Path $output 'stale-untrusted-payload.exe') -Value 'must not survive'
      $result=Invoke-AtomicBuildFixture -OutputDir $output -NoClean
      $result.succeeded|Should Be $true
      (Test-Path -LiteralPath (Join-Path $output 'stale-untrusted-payload.exe'))|Should Be $false
      (Test-Path -LiteralPath (Join-Path $output 'manifest.json') -PathType Leaf)|Should Be $true
      $manifest=Get-Content -LiteralPath (Join-Path $output 'manifest.json') -Raw|ConvertFrom-Json
      $manifest.ui_included|Should Be $false
      $manifest.artifacts[0].package_target|Should Be 'linux/amd64'
      $manifest.artifacts[0].ui_included|Should Be $false
      $package=Get-Content -LiteralPath (Join-Path $output 'steward-atomic-fixture-linux-amd64\release-manifest.json') -Raw|ConvertFrom-Json
      $package.package_target|Should Be 'linux/amd64'
      $package.ui_included|Should Be $false
    }finally{
      Remove-Item -LiteralPath $output -Recurse -Force -ErrorAction SilentlyContinue
    }
  }

  It 'does not expose a formal package when a fault occurs before publication' {
    $output=Join-Path $repoRoot ('backend\dist\atomic-publish-fault-'+[guid]::NewGuid().ToString('N'))
    $parent=Split-Path -Parent $output
    $leaf=Split-Path -Leaf $output
    try{
      $result=Invoke-AtomicBuildFixture -OutputDir $output -Fault 'before_publish'
      $result.succeeded|Should Be $false
      $result.error|Should Match 'Injected release build failure before atomic publication'
      (Test-Path -LiteralPath $output)|Should Be $false
      @(Get-ChildItem -LiteralPath $parent -Force -Filter '.stg-*' -ErrorAction SilentlyContinue).Count|Should Be 0
    }finally{
      Remove-Item -LiteralPath $output -Recurse -Force -ErrorAction SilentlyContinue
      Get-ChildItem -LiteralPath $parent -Force -Filter '.stg-*' -ErrorAction SilentlyContinue|Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
    }
  }
}
