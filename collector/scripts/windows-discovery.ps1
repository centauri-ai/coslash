[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$OutputPath,

    [string]$Label = "snapshot",

    [int]$RecentMinutes = 240,

    [string]$HandlePath = "",

    [string]$DiscoveryRoot = "C:\coslash-discovery"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Get-ToolCommand {
    param([string]$Name)

    $command = Get-Command -Name $Name -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $command) {
        return $null
    }
    return $command.Definition
}

function Invoke-ToolCapture {
    param(
        [string]$Name,
        [string[]]$Arguments
    )

    $command = Get-ToolCommand $Name
    if ([string]::IsNullOrWhiteSpace($command)) {
        return [PSCustomObject]@{
            command = $Name
            arguments = @($Arguments)
            available = $false
            exitCode = $null
            output = ""
        }
    }

    try {
        $global:LASTEXITCODE = 0
        $previousErrorActionPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "Continue"
            $output = & $command @Arguments 2>&1 | Out-String
            $exitCode = $LASTEXITCODE
        }
        finally {
            $ErrorActionPreference = $previousErrorActionPreference
        }
        return [PSCustomObject]@{
            command = $command
            arguments = @($Arguments)
            available = $true
            exitCode = $exitCode
            output = $output.Trim()
        }
    }
    catch {
        return [PSCustomObject]@{
            command = $command
            arguments = @($Arguments)
            available = $true
            exitCode = -1
            output = $_.Exception.Message
        }
    }
}

function Get-RecentFileMetadata {
    param(
        [string]$Root,
        [string]$Filter = "*",
        [int]$Limit = 300
    )

    if ([string]::IsNullOrWhiteSpace($Root) -or !(Test-Path -LiteralPath $Root)) {
        return @()
    }

    $cutoff = (Get-Date).ToUniversalTime().AddMinutes(-$RecentMinutes)
    return @(
        Get-ChildItem -LiteralPath $Root -Filter $Filter -File -Recurse -ErrorAction SilentlyContinue |
            Where-Object { $_.LastWriteTimeUtc -ge $cutoff } |
            Sort-Object LastWriteTimeUtc -Descending |
            Select-Object -First $Limit |
            ForEach-Object {
                [PSCustomObject]@{
                    path = $_.FullName
                    size = $_.Length
                    lastWriteUtc = $_.LastWriteTimeUtc.ToString("o")
                }
            }
    )
}

function Get-ClaudeLiveSessions {
    param([string]$Root)

    if (!(Test-Path -LiteralPath $Root)) {
        return @()
    }

    return @(
        Get-ChildItem -LiteralPath $Root -Filter "*.json" -File -ErrorAction SilentlyContinue |
            ForEach-Object {
                try {
                    $record = Get-Content -LiteralPath $_.FullName -Raw | ConvertFrom-Json
                    [PSCustomObject]@{
                        path = $_.FullName
                        lastWriteUtc = $_.LastWriteTimeUtc.ToString("o")
                        pid = $record.pid
                        sessionId = $record.sessionId
                        status = $record.status
                        statusUpdatedAt = $record.statusUpdatedAt
                        parseError = ""
                    }
                }
                catch {
                    [PSCustomObject]@{
                        path = $_.FullName
                        lastWriteUtc = $_.LastWriteTimeUtc.ToString("o")
                        pid = $null
                        sessionId = ""
                        status = ""
                        statusUpdatedAt = $null
                        parseError = $_.Exception.Message
                    }
                }
            }
    )
}

function Get-RelevantProcesses {
    $all = @(Get-CimInstance Win32_Process)
    $byPid = @{}
    foreach ($process in $all) {
        $byPid[[int]$process.ProcessId] = $process
    }

    $selected = @{}
    $agentPids = @{}
    foreach ($process in $all) {
        $text = "$($process.Name) $($process.ExecutablePath) $($process.CommandLine)"
        if ($text -match "(?i)(codex|claude|opencode)") {
            $processId = [int]$process.ProcessId
            $selected[$processId] = $true
            $agentPids[$processId] = $true
        }
    }

    for ($depth = 0; $depth -lt 5; $depth++) {
        foreach ($processId in @($selected.Keys)) {
            $process = $byPid[[int]$processId]
            if ($null -ne $process -and [int]$process.ParentProcessId -gt 0) {
                $selected[[int]$process.ParentProcessId] = $true
            }
        }
    }

    for ($depth = 0; $depth -lt 3; $depth++) {
        foreach ($process in $all) {
            if ($selected.ContainsKey([int]$process.ParentProcessId)) {
                $selected[[int]$process.ProcessId] = $true
            }
        }
    }

    $processes = @(
        foreach ($processId in ($selected.Keys | Sort-Object)) {
            $process = $byPid[[int]$processId]
            if ($null -eq $process) {
                continue
            }
            $created = $null
            if ($null -ne $process.CreationDate) {
                $created = $process.CreationDate.ToUniversalTime().ToString("o")
            }
            [PSCustomObject]@{
                pid = [int]$process.ProcessId
                parentPid = [int]$process.ParentProcessId
                name = $process.Name
                executablePath = $process.ExecutablePath
                commandLine = $process.CommandLine
                creationUtc = $created
                directAgentMatch = $agentPids.ContainsKey([int]$process.ProcessId)
            }
        }
    )

    return [PSCustomObject]@{
        all = $processes
        agentPids = @($agentPids.Keys | Sort-Object)
    }
}

function Resolve-HandlePath {
    if (![string]::IsNullOrWhiteSpace($HandlePath)) {
        if (Test-Path -LiteralPath $HandlePath) {
            return (Resolve-Path -LiteralPath $HandlePath).Path
        }
        return $null
    }
    foreach ($name in @("handle64.exe", "handle.exe")) {
        $path = Get-ToolCommand $name
        if (![string]::IsNullOrWhiteSpace($path)) {
            return $path
        }
    }
    return $null
}

function Get-RelevantHandles {
    param(
        [string]$ResolvedHandlePath,
        [int[]]$ProcessIds
    )

    if ([string]::IsNullOrWhiteSpace($ResolvedHandlePath)) {
        return [PSCustomObject]@{
            available = $false
            path = ""
            processes = @()
        }
    }

    $results = @(
        foreach ($processId in $ProcessIds) {
            try {
                $global:LASTEXITCODE = 0
                $lines = @(& $ResolvedHandlePath -accepteula -nobanner -p $processId 2>&1)
                $discoveryPattern = [Regex]::Escape($DiscoveryRoot)
                $matching = @(
                    $lines |
                        ForEach-Object { "$_" } |
                        Where-Object {
                            $_ -match "(?i)(rollout-|\\.codex|\\.claude|opencode|\\.jsonl|\\.db)" -or
                            $_ -match $discoveryPattern
                        }
                )
                [PSCustomObject]@{
                    pid = $processId
                    exitCode = $LASTEXITCODE
                    matchingLines = $matching
                }
            }
            catch {
                [PSCustomObject]@{
                    pid = $processId
                    exitCode = $LASTEXITCODE
                    matchingLines = @($_.Exception.Message)
                }
            }
        }
    )

    return [PSCustomObject]@{
        available = $true
        path = $ResolvedHandlePath
        processes = $results
    }
}

$homeDirectory = [Environment]::GetFolderPath("UserProfile")
$codexRoot = Join-Path $homeDirectory ".codex\sessions"
$claudeRoot = Join-Path $homeDirectory ".claude"
$claudeSessionsRoot = Join-Path $claudeRoot "sessions"
$claudeProjectsRoot = Join-Path $claudeRoot "projects"
$claudeJobsRoot = Join-Path $claudeRoot "jobs"

$openCodeDatabaseCandidates = @(
    if (![string]::IsNullOrWhiteSpace($env:XDG_DATA_HOME)) {
        Join-Path $env:XDG_DATA_HOME "opencode\opencode.db"
    }
    Join-Path $homeDirectory ".local\share\opencode\opencode.db"
    if (![string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        Join-Path $env:LOCALAPPDATA "opencode\opencode.db"
    }
    if (![string]::IsNullOrWhiteSpace($env:APPDATA)) {
        Join-Path $env:APPDATA "opencode\opencode.db"
    }
) | Select-Object -Unique

$openCodeDatabases = @(
    foreach ($candidate in $openCodeDatabaseCandidates) {
        if (Test-Path -LiteralPath $candidate) {
            $file = Get-Item -LiteralPath $candidate
            [PSCustomObject]@{
                path = $file.FullName
                size = $file.Length
                lastWriteUtc = $file.LastWriteTimeUtc.ToString("o")
            }
        }
    }
)

$processSnapshot = Get-RelevantProcesses
$resolvedHandlePath = Resolve-HandlePath
$os = Get-CimInstance Win32_OperatingSystem
$computer = Get-CimInstance Win32_ComputerSystem
$openCodeCandidateQuery = @"
SELECT session.id, session.directory, session.time_created, message.time_created AS user_message_time
FROM session
LEFT JOIN message ON message.session_id = session.id
  AND json_extract(message.data, '$.role') = 'user'
WHERE session.parent_id IS NULL AND session.time_archived IS NULL
ORDER BY session.id, message.time_created
"@
$openCodeDatabasePath = Invoke-ToolCapture "opencode" @("db", "path")
$openCodeLiveCandidates = Invoke-ToolCapture "opencode" @(
    "db", $openCodeCandidateQuery, "--format", "json"
)

$result = [PSCustomObject]@{
    schemaVersion = 1
    label = $Label
    capturedAtUtc = (Get-Date).ToUniversalTime().ToString("o")
    recentMinutes = $RecentMinutes
    system = [PSCustomObject]@{
        computerName = $env:COMPUTERNAME
        osCaption = $os.Caption
        osVersion = $os.Version
        osBuildNumber = $os.BuildNumber
        osArchitecture = $os.OSArchitecture
        systemType = $computer.SystemType
        processorArchitecture = $env:PROCESSOR_ARCHITECTURE
        powershellVersion = $PSVersionTable.PSVersion.ToString()
        windowsTerminal = (Get-ToolCommand "wt.exe")
        userHome = $homeDirectory
        appData = $env:APPDATA
        localAppData = $env:LOCALAPPDATA
        xdgDataHome = $env:XDG_DATA_HOME
    }
    tools = @(
        Invoke-ToolCapture "codex" @("--version")
        Invoke-ToolCapture "claude" @("--version")
        Invoke-ToolCapture "opencode" @("--version")
        Invoke-ToolCapture "opencode" @("debug", "paths")
        Invoke-ToolCapture "opencode" @("db", "--help")
    )
    processes = $processSnapshot.all
    handles = (Get-RelevantHandles $resolvedHandlePath $processSnapshot.agentPids)
    codex = [PSCustomObject]@{
        sessionsRoot = $codexRoot
        rolloutFiles = @(Get-RecentFileMetadata $codexRoot "rollout-*.jsonl")
    }
    claude = [PSCustomObject]@{
        root = $claudeRoot
        liveSessions = @(Get-ClaudeLiveSessions $claudeSessionsRoot)
        projectFiles = @(Get-RecentFileMetadata $claudeProjectsRoot "*.jsonl")
        jobFiles = @(Get-RecentFileMetadata $claudeJobsRoot "state.json")
        desktopCandidates = @(
            if (![string]::IsNullOrWhiteSpace($env:APPDATA)) {
                Join-Path $env:APPDATA "Claude\claude-code-sessions"
            }
            if (![string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
                Join-Path $env:LOCALAPPDATA "Claude\claude-code-sessions"
            }
        )
    }
    opencode = [PSCustomObject]@{
        databaseCandidates = @($openCodeDatabaseCandidates)
        databases = $openCodeDatabases
        databasePath = $openCodeDatabasePath
        liveCandidateRows = $openCodeLiveCandidates
    }
}

$fullOutputPath = [IO.Path]::GetFullPath($OutputPath)
$outputDirectory = Split-Path -Parent $fullOutputPath
if (![string]::IsNullOrWhiteSpace($outputDirectory)) {
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
}
$json = $result | ConvertTo-Json -Depth 8
[IO.File]::WriteAllText($fullOutputPath, $json, (New-Object Text.UTF8Encoding($false)))
Write-Host "Wrote $fullOutputPath"
if (!$result.handles.available) {
    Write-Warning "Sysinternals Handle was not found; the Codex process-to-rollout result will be inconclusive."
}
