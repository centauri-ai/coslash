[CmdletBinding()]
param(
    [string]$OutputPath = "windows-validation.json",
    [string]$BinaryPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($env:OS -ne "Windows_NT") {
    throw "This validation script must run on Windows."
}

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$collectorRoot = Join-Path $repositoryRoot "collector"
$userProfile = [Environment]::GetFolderPath("UserProfile")

function ConvertTo-SafeText {
    param([object[]]$Lines)

    $text = ($Lines | ForEach-Object { "$_" }) -join "`n"
    if (![string]::IsNullOrWhiteSpace($repositoryRoot)) {
        foreach ($path in @($repositoryRoot, $repositoryRoot.Replace("\", "\\"), $repositoryRoot.Replace("\", "/"))) {
            $text = $text.Replace($path, "%REPOSITORY%")
        }
    }
    if (![string]::IsNullOrWhiteSpace($userProfile)) {
        foreach ($path in @($userProfile, $userProfile.Replace("\", "\\"), $userProfile.Replace("\", "/"))) {
            $text = $text.Replace($path, "%USERPROFILE%")
        }
    }
    $text = $text -replace '#t=[A-Za-z0-9_-]+', '#t=%TOKEN%'
    if ($text.Length -gt 2000) {
        $text = $text.Substring($text.Length - 2000)
    }
    return $text.Trim()
}

function Invoke-ValidationCommand {
    param(
        [string]$Id,
        [string]$FilePath,
        [string[]]$Arguments
    )

    $watch = [Diagnostics.Stopwatch]::StartNew()
    $commandErrorActionPreference = $ErrorActionPreference
    Push-Location $collectorRoot
    try {
        $global:LASTEXITCODE = 0
        $ErrorActionPreference = "Continue"
        $output = @(& $FilePath @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
        $output | ForEach-Object { Write-Host "$_" }
        $status = if ($exitCode -eq 0) { "pass" } else { "fail" }
        $detail = if ($exitCode -eq 0) { "" } else { ConvertTo-SafeText $output }
    }
    catch {
        $exitCode = -1
        $status = "fail"
        $detail = ConvertTo-SafeText @($_.Exception.Message)
        Write-Host $detail
    }
    finally {
        $ErrorActionPreference = $commandErrorActionPreference
        Pop-Location
        $watch.Stop()
    }

    return [PSCustomObject]@{
        id = $Id
        status = $status
        exitCode = $exitCode
        durationSeconds = [Math]::Round($watch.Elapsed.TotalSeconds, 2)
        detail = $detail
    }
}

function Get-ToolVersion {
    param(
        [string]$Name,
        [string[]]$Arguments = @("--version")
    )

    $command = Get-Command $Name -CommandType Application, ExternalScript -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $command) {
        return [PSCustomObject]@{ name = $Name; available = $false; version = "" }
    }
    try {
        $global:LASTEXITCODE = 0
        $output = @(& $command.Source @Arguments 2>&1)
        return [PSCustomObject]@{
            name = $Name
            available = $true
            version = (ConvertTo-SafeText $output)
        }
    }
    catch {
        return [PSCustomObject]@{
            name = $Name
            available = $true
            version = (ConvertTo-SafeText @($_.Exception.Message))
        }
    }
}

function Get-RegistryValue {
    param(
        [string]$Path,
        [string]$Name
    )

    try {
        return (Get-ItemProperty -LiteralPath $Path -Name $Name -ErrorAction Stop).$Name
    }
    catch {
        return $null
    }
}

function New-EnvironmentCheck {
    param(
        [string]$Id,
        [bool]$Passed,
        [string]$Detail
    )

    return [PSCustomObject]@{
        id = $Id
        status = $(if ($Passed) { "pass" } else { "fail" })
        detail = $Detail
    }
}

Push-Location $repositoryRoot
try {
    $commit = (& git rev-parse HEAD).Trim()
    $branch = (@(& git branch --show-current) -join "").Trim()
    $dirty = @(& git status --porcelain).Count -gt 0
}
finally {
    Pop-Location
}

$os = try {
    Get-CimInstance Win32_OperatingSystem -ErrorAction Stop
}
catch {
    $version = [Environment]::OSVersion.Version
    $productName = Get-RegistryValue "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion" "ProductName"
    $currentBuild = Get-RegistryValue "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion" "CurrentBuildNumber"
    if (![string]::IsNullOrWhiteSpace($productName) -and [int]$currentBuild -ge 22000) {
        $productName = $productName -replace 'Windows 10', 'Windows 11'
    }
    [PSCustomObject]@{
        Caption = $(if ([string]::IsNullOrWhiteSpace($productName)) { "Microsoft Windows" } else { $productName })
        Version = $version.ToString()
        BuildNumber = $(if ([string]::IsNullOrWhiteSpace($currentBuild)) { $version.Build } else { $currentBuild })
    }
}
$buildNumber = [int]$os.BuildNumber
$architecture = if (![string]::IsNullOrWhiteSpace($env:PROCESSOR_ARCHITEW6432)) {
    $env:PROCESSOR_ARCHITEW6432
} else {
    $env:PROCESSOR_ARCHITECTURE
}
$architecture = $architecture.ToLowerInvariant()
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
$isAdministrator = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
$windowsTerminalAvailable = $null -ne (Get-Command "wt.exe" -ErrorAction SilentlyContinue | Select-Object -First 1)
$windowsPowerShellAvailable = $null -ne (Get-Command "powershell.exe" -ErrorAction SilentlyContinue | Select-Object -First 1)
$supportedWindows = $os.Caption -match "Windows 11" -and
    $os.Caption -notmatch "(?i)(LTSC|multi-session)" -and
    $buildNumber -ge 26100

$environmentChecks = @(
    New-EnvironmentCheck "supported-windows" $supportedWindows "$($os.Caption), build $buildNumber"
    New-EnvironmentCheck "supported-architecture" ($architecture -eq "amd64") $architecture
    New-EnvironmentCheck "standard-user" (!$isAdministrator) $(if ($isAdministrator) { "PowerShell is elevated" } else { "PowerShell is not elevated" })
    New-EnvironmentCheck "windows-powershell-5.1" ($PSVersionTable.PSVersion.Major -eq 5 -and $PSVersionTable.PSVersion.Minor -eq 1) $PSVersionTable.PSVersion.ToString()
    New-EnvironmentCheck "terminal-launcher" $windowsPowerShellAvailable $(if ($windowsTerminalAvailable) { "Windows Terminal and Windows PowerShell are available" } elseif ($windowsPowerShellAvailable) { "Windows Terminal is unavailable; Windows PowerShell fallback is available" } else { "Windows PowerShell is unavailable" })
)

$defender = $null
try {
    $status = Get-MpComputerStatus
    $defender = [PSCustomObject]@{
        available = $true
        antivirusEnabled = [bool]$status.AntivirusEnabled
        realTimeProtectionEnabled = [bool]$status.RealTimeProtectionEnabled
        error = ""
    }
}
catch {
    $defender = [PSCustomObject]@{
        available = $false
        antivirusEnabled = $null
        realTimeProtectionEnabled = $null
        error = (ConvertTo-SafeText @($_.Exception.Message))
    }
}

$automatedChecks = @()
$automatedChecks += Invoke-ValidationCommand "windows-package-tests" "go" @(
    "test",
    "./cmd/coslash",
    "./internal/hubclient",
    "./internal/launch",
    "./internal/remote",
    "./internal/session",
    "./internal/sessionexport",
    "./internal/settings",
    "./internal/vendors/claude",
    "./internal/vendors/codex",
    "./internal/vendors/cursor",
    "./internal/vendors/opencode",
    "./snapshot/v1"
)
$automatedChecks += Invoke-ValidationCommand "go-vet" "go" @("vet", "./...")

$temporaryDirectory = Join-Path ([IO.Path]::GetTempPath()) ("coslash-windows-validation-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null
try {
    $temporaryBinary = Join-Path $temporaryDirectory "coslash.exe"
    $automatedChecks += Invoke-ValidationCommand "native-build" "go" @("build", "-o", $temporaryBinary, "./cmd/coslash")
    if (Test-Path -LiteralPath $temporaryBinary) {
        $automatedChecks += Invoke-ValidationCommand "native-build-version" $temporaryBinary @("--version")
    }
    if (![string]::IsNullOrWhiteSpace($BinaryPath) -and (Test-Path -LiteralPath $BinaryPath -PathType Leaf)) {
        $candidate = (Resolve-Path -LiteralPath $BinaryPath).Path
        $automatedChecks += Invoke-ValidationCommand "candidate-version" $candidate @("--version")
        $automatedChecks += Invoke-ValidationCommand "candidate-packaged-smoke" "powershell.exe" @(
            "-NoProfile",
            "-ExecutionPolicy", "Bypass",
            "-File", (Join-Path $PSScriptRoot "windows-smoke.ps1"),
            "-BinaryPath", $candidate
        )
        $candidateArtifact = [PSCustomObject]@{
            supplied = $true
            sha256 = (Get-FileHash -LiteralPath $candidate -Algorithm SHA256).Hash.ToLowerInvariant()
        }
    }
    elseif (![string]::IsNullOrWhiteSpace($BinaryPath)) {
        $automatedChecks += [PSCustomObject]@{
            id = "candidate-version"
            status = "fail"
            exitCode = -1
            durationSeconds = 0
            detail = "Candidate executable does not exist."
        }
        $candidateArtifact = [PSCustomObject]@{ supplied = $true; sha256 = "" }
    }
    else {
        $candidateArtifact = [PSCustomObject]@{ supplied = $false; sha256 = "" }
    }
}
finally {
    Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force -ErrorAction SilentlyContinue
}

$manualChecks = @(
    [PSCustomObject]@{ id = "claude-launch-resume-handoff"; status = "pending"; note = "" }
    [PSCustomObject]@{ id = "codex-launch-resume-handoff"; status = "pending"; note = "" }
    [PSCustomObject]@{ id = "cursor-launch-resume-handoff"; status = "pending"; note = "" }
    [PSCustomObject]@{ id = "opencode-launch-resume-handoff"; status = "pending"; note = "" }
    [PSCustomObject]@{ id = "unicode-and-handoff-cleanup"; status = "pending"; note = "" }
    [PSCustomObject]@{ id = "session-state-and-feature-parity"; status = "pending"; note = "" }
    [PSCustomObject]@{ id = "browser-and-credential-restart"; status = "pending"; note = "" }
    [PSCustomObject]@{ id = "windows-to-linux-ssh"; status = "pending"; note = "" }
    [PSCustomObject]@{ id = "standard-user-install-upgrade-defender-smartscreen"; status = "pending"; note = "" }
)

$failedAutomated = @($automatedChecks | Where-Object { $_.status -eq "fail" }).Count
$failedEnvironment = @($environmentChecks | Where-Object { $_.status -eq "fail" }).Count
$result = [PSCustomObject]@{
    schemaVersion = 1
    capturedAtUtc = (Get-Date).ToUniversalTime().ToString("o")
    status = $(if ($failedAutomated -eq 0 -and $failedEnvironment -eq 0) { "pending-manual" } else { "fail" })
    source = [PSCustomObject]@{ commit = $commit; branch = $branch; dirty = $dirty }
    system = [PSCustomObject]@{
        edition = $os.Caption
        version = $os.Version
        build = $buildNumber
        architecture = $architecture
        administrator = $isAdministrator
        powershell = $PSVersionTable.PSVersion.ToString()
    }
    security = [PSCustomObject]@{
        defender = $defender
        smartScreenMachine = (Get-RegistryValue "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer" "SmartScreenEnabled")
        smartScreenUser = (Get-RegistryValue "HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\AppHost" "EnableWebContentEvaluation")
    }
    tools = @(
        Get-ToolVersion "git"
        Get-ToolVersion "go" @("version")
        Get-ToolVersion "node"
        Get-ToolVersion "npm"
        Get-ToolVersion "claude"
        Get-ToolVersion "codex"
        Get-ToolVersion "opencode"
        Get-ToolVersion "wt.exe"
    )
    candidateArtifact = $candidateArtifact
    environmentChecks = $environmentChecks
    automatedChecks = $automatedChecks
    manualChecks = $manualChecks
}

$fullOutputPath = if ([IO.Path]::IsPathRooted($OutputPath)) {
    $OutputPath
} else {
    Join-Path $repositoryRoot $OutputPath
}
$outputDirectory = Split-Path -Parent $fullOutputPath
New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
$json = $result | ConvertTo-Json -Depth 8
[IO.File]::WriteAllText($fullOutputPath, $json, (New-Object Text.UTF8Encoding($false)))
Get-Content -LiteralPath $fullOutputPath -Raw | ConvertFrom-Json | Out-Null
Write-Host "Wrote $fullOutputPath"

if ($failedAutomated -gt 0 -or $failedEnvironment -gt 0) {
    exit 1
}
