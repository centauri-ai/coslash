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

function Stop-RunningCoslashServers([string]$SelectedBinary) {
    try {
        $listeners = @(Get-NetTCPConnection -State Listen -ErrorAction Stop)
    }
    catch {
        Stop-Install "could not inspect active TCP listeners; no processes were stopped"
    }

    $serverIds = @()
    $port8787Pids = @($listeners | Where-Object { [int]$_.LocalPort -eq 8787 } | Select-Object -ExpandProperty OwningProcess -Unique)
    $listenerProcessIds = @($listeners | Select-Object -ExpandProperty OwningProcess -Unique)
    foreach ($processId in $listenerProcessIds) {
        $processId = [int]$processId
        $ownsDefaultPort = $port8787Pids -contains $processId
        $process = Get-CimInstance Win32_Process -Filter "ProcessId = $processId" -ErrorAction SilentlyContinue
        if (-not $process) {
            if ($ownsDefaultPort) {
                Stop-Install "port 8787 is owned by an unverified process (PID $processId); no processes were stopped"
            }
            continue
        }

        $imageName = [IO.Path]::GetFileName([string]$process.ExecutablePath)
        if ($process.Name -ieq "coslash.exe" -and -not $process.ExecutablePath -and $ownsDefaultPort) {
            Stop-Install "could not verify the coSlash listener process (PID $processId); no processes were stopped"
        }
        $isSelectedBinary = $imageName -ieq "coslash.exe" -and [string]::Equals(
            [IO.Path]::GetFullPath([string]$process.ExecutablePath),
            $SelectedBinary,
            [StringComparison]::OrdinalIgnoreCase
        )
        if ($isSelectedBinary) {
            try {
                $owner = Invoke-CimMethod -InputObject $process -MethodName GetOwner -ErrorAction Stop
            }
            catch {
                Stop-Install "could not verify the owner of coSlash listener PID $processId; no processes were stopped"
            }
            $ownerName = "$($owner.Domain)\$($owner.User)"
            $currentOwner = [Security.Principal.WindowsIdentity]::GetCurrent().Name
            if ($owner.ReturnValue -ne 0 -or $ownerName -ine $currentOwner) {
                Stop-Install "coSlash listener PID $processId is not owned by the current user; no processes were stopped"
            }
            if ($serverIds -notcontains $processId) { $serverIds += $processId }
        }
        elseif ($ownsDefaultPort) {
            Stop-Install "port 8787 is owned by $($process.Name) (PID $processId), not a verified coSlash Local server; no processes were stopped"
        }
    }

    if ($serverIds.Count -eq 0) { return }
    $runtimePath = Join-Path $env:COSLASH_HOME "runtime.json"
    $tokenPath = Join-Path $env:COSLASH_HOME "token"
    try {
        $runtime = Get-Content -LiteralPath $runtimePath -Raw | ConvertFrom-Json -ErrorAction Stop
        $token = (Get-Content -LiteralPath $tokenPath -Raw -ErrorAction Stop).Trim()
        if (-not $runtime.baseURL -or -not $token) { throw "runtime discovery is incomplete" }
        Invoke-RestMethod -Uri ($runtime.baseURL.TrimEnd("/") + "/api/shutdown") -Method Post -Headers @{ "X-Coslash-Token" = $token } -TimeoutSec 10 | Out-Null
    }
    catch {
        Stop-Install "could not request graceful shutdown of the selected coSlash Local server; close it manually and retry"
    }

    foreach ($processId in $serverIds) {
        Write-Host "Waiting for coSlash Local to finish shutting down (PID $processId)…"
    }

    $deadline = [DateTime]::UtcNow.AddSeconds(210)
    do {
        $remaining = @($serverIds | Where-Object { Get-Process -Id $_ -ErrorAction SilentlyContinue })
        if ($remaining.Count -eq 0) { return }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)

    Stop-Install "coSlash Local did not stop; the installed binary was not replaced"
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
$installDir = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA ("coSlash\dev\" + $branchSlug)))
$targetBinary = Join-Path $installDir "coslash.exe"
if (-not $env:COSLASH_HOME) {
    $env:COSLASH_HOME = Join-Path $env:USERPROFILE (".coslash-dev\" + $branchSlug)
}
$stagedBinary = $null
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
    $stagedBinary = Join-Path $installDir (".coslash-" + [guid]::NewGuid().ToString("N") + ".tmp")
    Copy-Item -LiteralPath $binary -Destination $stagedBinary
    Stop-RunningCoslashServers $targetBinary
    if (Test-Path -LiteralPath $targetBinary) {
        [IO.File]::Replace($stagedBinary, $targetBinary, $null)
    }
    else {
        [IO.File]::Move($stagedBinary, $targetBinary)
    }
    $stagedBinary = $null
    $commit = (git -C $source rev-parse --short HEAD).Trim()
    Write-Host "Installed coSlash Local from $branch ($commit) to $installDir"

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
    if ($stagedBinary -and (Test-Path -LiteralPath $stagedBinary)) { Remove-Item -LiteralPath $stagedBinary -Force }
    if (Test-Path $workRoot) { Remove-Item -LiteralPath $workRoot -Recurse -Force }
}
