[CmdletBinding()]
param(
    # Verified copy of the official v0.4.2 asset to reuse instead of downloading.
    [string]$LegacyBinaryPath = ""
)

# Native Windows proof against the real published v0.4.2 update helper.
#
# Preparation (this script, never `go test`):
#   1. fetch the official immutable v0.4.2 Windows asset from the fixed release URL,
#   2. require its SHA-256 to equal the value pinned in the repository
#      (collector/cmd/coslash/testdata/legacy-helper-0.4.2-windows-amd64.sha256) and
#      the release's own checksums-windows.txt,
#   3. build the new target from this checkout with version 0.4.3.
# Then run the opt-in `realhelper` tests, which launch the real helper (three
# arguments: target, staged, ready) and every `--version` check with owned
# HOME/USERPROFILE/COSLASH_HOME/LOCALAPPDATA/TEMP and no credentials, Hub or
# network model calls. Safe to run by hand on a disposable Windows machine.

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$collector = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$pinFile = Join-Path $collector "cmd\coslash\testdata\legacy-helper-0.4.2-windows-amd64.sha256"
$pin = (Get-Content -LiteralPath $pinFile -Raw).Trim().Split(" ", [StringSplitOptions]::RemoveEmptyEntries)
if ($pin.Count -ne 2 -or $pin[0] -notmatch '^[0-9a-f]{64}$' -or $pin[1] -ne "coslash-windows-amd64.exe") {
    throw "pinned checksum file is malformed"
}
$pinnedHash = $pin[0]
$releaseBase = "https://github.com/centauri-ai/coslash/releases/download/v0.4.2"
$work = Join-Path ([IO.Path]::GetTempPath()) ("coslash-real-helper-prep-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $work | Out-Null
$exitCode = 1
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $legacy = Join-Path $work "coslash-v0.4.2-windows-amd64.exe"
    if ($LegacyBinaryPath -ne "") {
        Copy-Item -LiteralPath $LegacyBinaryPath -Destination $legacy
    }
    else {
        Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/coslash-windows-amd64.exe" -OutFile $legacy
    }
    $actual = (Get-FileHash $legacy -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $pinnedHash) {
        throw "v0.4.2 Windows asset sha256 $actual does not match pinned $pinnedHash"
    }
    $sums = Join-Path $work "checksums-windows.txt"
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/checksums-windows.txt" -OutFile $sums
    $published = (Select-String -Path $sums -Pattern "  coslash-windows-amd64\.exe$").Line.Split(" ")[0].ToLowerInvariant()
    if ($published -ne $pinnedHash) {
        throw "release checksums-windows.txt ($published) disagrees with pinned $pinnedHash"
    }
    Write-Host "Official v0.4.2 asset verified: $pinnedHash"

    $next = Join-Path $work "coslash-new-0.4.3.exe"
    Push-Location $collector
    try {
        & go build -trimpath -ldflags "-X main.version=0.4.3" -o $next ./cmd/coslash
        if ($LASTEXITCODE -ne 0) { throw "go build of the new target failed" }
        $env:COSLASH_REAL_HELPER_V042 = $legacy
        $env:COSLASH_REAL_HELPER_NEW = $next
        # Native stderr from go must not become a terminating error.
        $ErrorActionPreference = "Continue"
        & go test -tags realhelper -count=1 -timeout 10m -parallel 6 -v -run '^TestRealLegacyHelper$' ./cmd/coslash
        $exitCode = $LASTEXITCODE
        $ErrorActionPreference = "Stop"
    }
    finally {
        Remove-Item Env:COSLASH_REAL_HELPER_V042 -ErrorAction SilentlyContinue
        Remove-Item Env:COSLASH_REAL_HELPER_NEW -ErrorAction SilentlyContinue
        Pop-Location
    }
}
finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
if ($exitCode -ne 0) {
    Write-Output "::error title=Windows real legacy helper proof failed::go test exited $exitCode"
}
exit $exitCode
