[CmdletBinding()]
param(
    [string]$Version = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($env:OS -ne "Windows_NT") {
    throw "This packaging script must run on Windows."
}

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$collectorRoot = Join-Path $repositoryRoot "collector"
$frontendRoot = Join-Path $repositoryRoot "frontend"
$stagedFrontend = Join-Path $collectorRoot "internal\web\dist"
$helperAssets = Join-Path $collectorRoot "internal\remote\helperassets"
$windowsDist = Join-Path $collectorRoot "dist\windows"

function Reset-CollectorDirectory {
    param([string]$Path)

    $fullPath = [IO.Path]::GetFullPath($Path)
    $collectorPrefix = $collectorRoot.TrimEnd("\") + "\"
    if (!$fullPath.StartsWith($collectorPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to replace a directory outside the collector."
    }
    if (Test-Path -LiteralPath $fullPath) {
        Remove-Item -LiteralPath $fullPath -Recurse -Force
    }
    New-Item -ItemType Directory -Path $fullPath | Out-Null
}

function Invoke-Checked {
    param(
        [string]$FilePath,
        [string[]]$Arguments
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath exited with code $LASTEXITCODE."
    }
}

$go = Get-Command go.exe -CommandType Application -ErrorAction Stop | Select-Object -First 1
$node = Get-Command node.exe -CommandType Application -ErrorAction Stop | Select-Object -First 1
$npm = Get-Command npm.cmd -CommandType Application -ErrorAction Stop | Select-Object -First 1

if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = (& git.exe -C $repositoryRoot describe --tags --always --dirty 2>$null)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($Version)) {
        $Version = "dev"
    }
}

Invoke-Checked $npm.Source @("--prefix", $frontendRoot, "ci")
Invoke-Checked $npm.Source @("--prefix", $frontendRoot, "run", "build")
$frontendIndex = Join-Path $frontendRoot "dist\index.html"
if (!(Test-Path -LiteralPath $frontendIndex -PathType Leaf)) {
    throw "The frontend build produced no dist\index.html."
}

Reset-CollectorDirectory $stagedFrontend
Copy-Item -Path (Join-Path $frontendRoot "dist\*") -Destination $stagedFrontend -Recurse -Force
[IO.File]::WriteAllText((Join-Path $stagedFrontend ".gitkeep"), "")

$environmentNames = @("CGO_ENABLED", "GOOS", "GOARCH")
$previousEnvironment = @{}
foreach ($name in $environmentNames) {
    $item = Get-Item ("Env:" + $name) -ErrorAction SilentlyContinue
    $previousEnvironment[$name] = if ($null -eq $item) { $null } else { $item.Value }
}

try {
    Reset-CollectorDirectory $helperAssets
    foreach ($architecture in @("amd64", "arm64")) {
        $env:CGO_ENABLED = "0"
        $env:GOOS = "linux"
        $env:GOARCH = $architecture
        $helper = Join-Path $helperAssets ("coslash-helper_linux_" + $architecture)
        Invoke-Checked $go.Source @(
            "-C", $collectorRoot, "build", "-trimpath",
            "-ldflags", ("-s -w -X main.build=" + $Version),
            "-o", $helper,
            "./cmd/coslash-helper"
        )
    }

    Reset-CollectorDirectory $windowsDist
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $artifact = Join-Path $windowsDist "coslash-windows-amd64.exe"
    $linkerFlags = "-s -w -X main.version=$Version -X github.com/centauri-ai/coslash/collector/internal/remote.embeddedHelperVersion=$Version"
    Invoke-Checked $go.Source @(
        "-C", $collectorRoot, "build", "-trimpath", "-tags", "embedded_helpers",
        "-ldflags", $linkerFlags,
        "-o", $artifact,
        "./cmd/coslash"
    )
    Invoke-Checked $node.Source @(
        (Join-Path $PSScriptRoot "checksums.mjs"),
        (Join-Path $windowsDist "checksums-windows.txt"),
        $artifact
    )
    Write-Host "Packaged $artifact"
}
finally {
    if (Test-Path -LiteralPath $helperAssets) {
        Remove-Item -LiteralPath $helperAssets -Recurse -Force
    }
    foreach ($name in $environmentNames) {
        if ($null -eq $previousEnvironment[$name]) {
            Remove-Item ("Env:" + $name) -ErrorAction SilentlyContinue
        }
        else {
            Set-Item ("Env:" + $name) $previousEnvironment[$name]
        }
    }
}
