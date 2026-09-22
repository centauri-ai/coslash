[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BinaryPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$binary = (Resolve-Path -LiteralPath $BinaryPath).Path
$smokeHome = Join-Path ([IO.Path]::GetTempPath()) ("coslash-windows-smoke-" + [Guid]::NewGuid().ToString("N"))
$stdoutPath = Join-Path $smokeHome "stdout.log"
$stderrPath = Join-Path $smokeHome "stderr.log"
$process = $null

function Get-SafeServerLog {
    $lines = @()
    foreach ($path in @($stdoutPath, $stderrPath)) {
        if (Test-Path -LiteralPath $path) {
            $lines += Get-Content -LiteralPath $path
        }
    }
    return (($lines -join "`n") -replace '#t=[A-Za-z0-9_-]+', '#t=%TOKEN%').Trim()
}

function Invoke-SmokeRequest {
    param(
        [string]$BaseURL,
        [string]$Token,
        [string]$Path
    )

    return Invoke-WebRequest -UseBasicParsing -Uri ($BaseURL + $Path) -Headers @{ "X-Coslash-Token" = $Token } -TimeoutSec 5
}

New-Item -ItemType Directory -Path $smokeHome | Out-Null
try {
    $environment = @{
        COSLASH_HOME = $smokeHome
        HOME = $smokeHome
        USERPROFILE = $smokeHome
        APPDATA = (Join-Path $smokeHome "AppData\Roaming")
        LOCALAPPDATA = (Join-Path $smokeHome "AppData\Local")
        XDG_CONFIG_HOME = (Join-Path $smokeHome ".config")
    }
    $previousEnvironment = @{}
    try {
        foreach ($name in $environment.Keys) {
            $item = Get-Item ("Env:" + $name) -ErrorAction SilentlyContinue
            $previousEnvironment[$name] = if ($null -eq $item) { $null } else { $item.Value }
            Set-Item ("Env:" + $name) $environment[$name]
        }
        $process = Start-Process -FilePath $binary -ArgumentList @("--no-open", "--port", "0") -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
    }
    finally {
        foreach ($name in $environment.Keys) {
            if ($null -eq $previousEnvironment[$name]) {
                Remove-Item ("Env:" + $name) -ErrorAction SilentlyContinue
            }
            else {
                Set-Item ("Env:" + $name) $previousEnvironment[$name]
            }
        }
    }

    $baseURL = ""
    $token = ""
    $root = $null
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        if ($process.HasExited) {
            throw "Candidate exited during startup. $(Get-SafeServerLog)"
        }
        $log = Get-SafeServerLog
        $match = [regex]::Match($log, 'listening on (http://127\.0\.0\.1:[0-9]+)')
        if ($match.Success) {
            $baseURL = $match.Groups[1].Value
        }
        $tokenPath = Join-Path $smokeHome "token"
        if (Test-Path -LiteralPath $tokenPath) {
            $token = (Get-Content -LiteralPath $tokenPath -Raw).Trim()
        }
        if (![string]::IsNullOrWhiteSpace($baseURL) -and ![string]::IsNullOrWhiteSpace($token)) {
            try {
                $root = Invoke-SmokeRequest $baseURL $token "/api/sessions"
                break
            }
            catch {
            }
        }
        Start-Sleep -Milliseconds 250
    }
    if ($null -eq $root) {
        throw "Candidate did not become ready. $(Get-SafeServerLog)"
    }
    try {
        $root = Invoke-SmokeRequest $baseURL $token "/"
    }
    catch {
        throw "Candidate does not serve the embedded coSlash frontend."
    }
    if ($root.StatusCode -ne 200 -or $root.Content -notmatch '<div id="root"' -or $root.Content -notmatch '<title>coSlash') {
        throw "Candidate does not contain the embedded coSlash frontend."
    }

    $assetMatch = [regex]::Match($root.Content, '/assets/[^"'']+\.js')
    if (!$assetMatch.Success) {
        throw "Candidate frontend does not reference a JavaScript asset."
    }
    $asset = Invoke-SmokeRequest $baseURL $token $assetMatch.Value
    if ($asset.StatusCode -ne 200) {
        throw "Candidate frontend JavaScript asset returned HTTP $($asset.StatusCode)."
    }

    $sessions = Invoke-SmokeRequest $baseURL $token "/api/sessions"
    if ($sessions.StatusCode -ne 200) {
        throw "Candidate sessions API returned HTTP $($sessions.StatusCode)."
    }
    $sessions.Content | ConvertFrom-Json | Out-Null

    $settings = @{
        '$schema' = "https://raw.githubusercontent.com/centauri-ai/coslash/main/settings.schema.json"
        version = 1
        synthesis = @{ enabled = $false; backend = "claude-cli"; model = "claude-haiku-4-5" }
        appearance = @{ theme = "light" }
        launch = @{ terminal = "windows-terminal" }
        remote = @{ id = "r_0123456789abcdef"; sshAlias = "coslash-smoke-invalid"; enabled = $true }
    } | ConvertTo-Json -Depth 5
    $configured = Invoke-WebRequest -UseBasicParsing -Uri ($baseURL + "/api/settings") -Method Put -Headers @{ "X-Coslash-Token" = $token } -ContentType "application/json" -Body $settings -TimeoutSec 5
    if ($configured.StatusCode -ne 200) {
        throw "Candidate settings API returned HTTP $($configured.StatusCode)."
    }
    $remote = Invoke-SmokeRequest $baseURL $token "/api/remote/status"
    if ($remote.StatusCode -ne 200 -or $remote.Content -notmatch '"helperInstallationAvailable":true') {
        throw "Candidate does not expose both embedded Linux helper assets."
    }

    Write-Host "Windows packaged candidate smoke test passed."
}
finally {
    if ($null -ne $process -and !$process.HasExited) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        $process.WaitForExit()
    }
    Remove-Item -LiteralPath $smokeHome -Recurse -Force -ErrorAction SilentlyContinue
}
