$ErrorActionPreference = "Stop"

$repo = "https://github.com/centauri-ai/coslash.git"
$branch = if ($env:COSLASH_SOURCE_BRANCH) { $env:COSLASH_SOURCE_BRANCH } else { "hlu/hub-support" }
$connectCode = $env:COSLASH_CONNECT
$hub = $env:COSLASH_HUB
$devVersion = "0.0.0"

function Stop-Install([string]$Message) {
    throw "coSlash branch installer: $Message"
}

function Invoke-Native([string]$Command, [string[]]$NativeArgs, [string]$WorkingDirectory) {
    Push-Location $WorkingDirectory
    try {
        & $Command @NativeArgs
        if ($LASTEXITCODE -ne 0) {
            Stop-Install "$Command failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

if ([bool]$connectCode -ne [bool]$hub) {
    Stop-Install "COSLASH_CONNECT and COSLASH_HUB must be provided together"
}

if (-not (Get-Command git -ErrorAction SilentlyContinue)) { Stop-Install "Git is required" }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Stop-Install "Go 1.26+ is required" }
if (-not (Get-Command node -ErrorAction SilentlyContinue)) { Stop-Install "Node 24+ is required" }
if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { Stop-Install "npm is required" }

$goText = (& go version 2>$null | Out-String).Trim()
if ($goText -notmatch "go version go([0-9]+\.[0-9]+(?:\.[0-9]+)?)") { Stop-Install "could not read the Go version" }
if ([version]$Matches[1] -lt [version]"1.26") { Stop-Install "Go 1.26+ is required" }
$nodeText = (& node --version 2>$null | Out-String).Trim().TrimStart("v")
if (-not $nodeText -or [version]$nodeText -lt [version]"24.0") { Stop-Install "Node 24+ is required" }

& git check-ref-format --branch $branch *> $null
if ($LASTEXITCODE -ne 0) { Stop-Install "invalid source branch name" }

$branchSlug = $branch -replace "[/\\]", "-"
$workRoot = Join-Path $env:TEMP ("coslash-branch-install-" + [guid]::NewGuid().ToString("N"))
$source = Join-Path $workRoot "source"
$installDir = Join-Path $env:LOCALAPPDATA ("coSlash\dev\" + $branchSlug)
$oldGoos = $env:GOOS
$oldGoarch = $env:GOARCH
$oldCgo = $env:CGO_ENABLED

try {
    New-Item -ItemType Directory -Path $workRoot -Force | Out-Null
    Write-Host "Building coSlash Local from branch $branch…"
    Invoke-Native "git" @("clone", "--depth", "1", "--single-branch", "--branch", $branch, $repo, $source) $workRoot

    $frontend = Join-Path $source "frontend"
    $collector = Join-Path $source "collector"
    Invoke-Native "npm" @("ci", "--prefix", $frontend) $source
    Invoke-Native "npm" @("run", "build", "--prefix", $frontend) $source

    $staged = Join-Path $collector "internal\web\dist"
    Get-ChildItem -LiteralPath $staged -Force | Remove-Item -Recurse -Force
    Copy-Item -Path (Join-Path $frontend "dist\*") -Destination $staged -Recurse -Force
    New-Item -ItemType File -Path (Join-Path $staged ".gitkeep") -Force | Out-Null

    $helperAssets = Join-Path $collector "internal\remote\helperassets"
    if (Test-Path $helperAssets) { Remove-Item -LiteralPath $helperAssets -Recurse -Force }
    New-Item -ItemType Directory -Path $helperAssets -Force | Out-Null
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    Invoke-Native "go" @("build", "-trimpath", "-ldflags", "-s -w -X main.build=$devVersion", "-o", (Join-Path $helperAssets "coslash-helper_linux_amd64"), "./cmd/coslash-helper") $collector
    $env:GOARCH = "arm64"
    Invoke-Native "go" @("build", "-trimpath", "-ldflags", "-s -w -X main.build=$devVersion", "-o", (Join-Path $helperAssets "coslash-helper_linux_arm64"), "./cmd/coslash-helper") $collector

    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $binDir = Join-Path $collector "bin"
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    $ldflags = "-s -w -X main.version=$devVersion -X github.com/centauri-ai/coslash/collector/internal/remote.embeddedHelperVersion=$devVersion"
    Invoke-Native "go" @("build", "-trimpath", "-tags", "embedded_helpers", "-ldflags", $ldflags, "-o", (Join-Path $binDir "coslash.exe"), "./cmd/coslash") $collector

    $binary = Join-Path $binDir "coslash.exe"
    $help = & $binary connect --help 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0 -or $help -notmatch "usage: coslash connect CODE --hub ORIGIN") {
        Stop-Install "branch $branch does not include the Hub connect command"
    }

    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    Copy-Item -LiteralPath $binary -Destination (Join-Path $installDir "coslash.exe") -Force
    $commit = (git -C $source rev-parse --short HEAD).Trim()
    Write-Host "Installed coSlash Local from $branch ($commit) to $installDir"

    if (-not $env:COSLASH_HOME) {
        $env:COSLASH_HOME = Join-Path $env:USERPROFILE (".coslash-dev\" + $branchSlug)
    }
    $pathEntries = [Environment]::GetEnvironmentVariable("Path", "User") -split ";"
    if ($pathEntries -notcontains $installDir) {
        $newUserPath = (@($pathEntries | Where-Object { $_ }) + $installDir) -join ";"
        [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    }
    if (($env:Path -split ";") -notcontains $installDir) { $env:Path = "$installDir;$env:Path" }

    if ($connectCode) {
        & (Join-Path $installDir "coslash.exe") connect $connectCode --hub $hub
        if ($LASTEXITCODE -ne 0) { Stop-Install "the connect command failed with exit code $LASTEXITCODE" }
    }
}
finally {
    $env:GOOS = $oldGoos
    $env:GOARCH = $oldGoarch
    $env:CGO_ENABLED = $oldCgo
    if (Test-Path $workRoot) { Remove-Item -LiteralPath $workRoot -Recurse -Force }
}
