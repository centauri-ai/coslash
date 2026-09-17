# Windows session discovery spike

Use a Windows 11 24H2 machine with Claude Code, Codex, OpenCode, Windows Terminal,
and Windows PowerShell 5.1 installed. The output intentionally contains process
command lines and full local paths. It does not read transcript or message bodies.

## Prepare

Open Windows PowerShell in this repository and confirm the branch:

```powershell
git switch cvu/windows-support
git pull --ff-only
```

Install Microsoft Sysinternals Handle if `Get-Command handle64.exe` and
`Get-Command handle.exe` both fail. The probe still runs without it, but the
Codex result will be inconclusive. One installation option is:

```powershell
$zip = "$env:TEMP\Handle.zip"
$dir = "$env:TEMP\Handle"
Invoke-WebRequest https://download.sysinternals.com/files/Handle.zip -OutFile $zip
Expand-Archive $zip $dir -Force
Get-AuthenticodeSignature "$dir\handle64.exe"
```

The signature status must be `Valid`. Pass `-HandlePath "$dir\handle64.exe"`
to the probe commands below. If Handle is already on `PATH`, omit that argument.

## Capture active sessions

Create three distinct directories outside the repository:

```powershell
New-Item -ItemType Directory -Force C:\coslash-discovery\claude
New-Item -ItemType Directory -Force C:\coslash-discovery\codex
New-Item -ItemType Directory -Force C:\coslash-discovery\opencode
```

In separate Windows Terminal tabs, start `claude`, `codex`, and `opencode`, one
from its corresponding directory.
Send each agent one harmless, unique prompt such as
`Reply with COSLASH_DISCOVERY_CLAUDE_ACTIVE`, and leave all three sessions open.

From a fourth PowerShell tab, run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File collector\scripts\windows-discovery.ps1 `
  -Label active `
  -OutputPath docs\windows-discovery\active.json `
  -HandlePath "$env:TEMP\Handle\handle64.exe"
```

## Capture resumed sessions

Exit all three agents normally. Resume each session using that agent's recent
session picker or documented resume command. Send a second unique prompt and
leave the resumed sessions open. Then run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File collector\scripts\windows-discovery.ps1 `
  -Label resumed `
  -OutputPath docs\windows-discovery\resumed.json `
  -HandlePath "$env:TEMP\Handle\handle64.exe"
```

If the script fails, make the smallest correction needed and include that change
in the results commit. Do not add dependencies or a permanent Windows collector.

## Validate and publish

```powershell
Get-Content docs\windows-discovery\active.json -Raw | ConvertFrom-Json | Out-Null
Get-Content docs\windows-discovery\resumed.json -Raw | ConvertFrom-Json | Out-Null
git add collector\scripts\windows-discovery.ps1 docs\windows-discovery.md docs\windows-discovery\*.json
git commit -m "capture Windows agent session discovery"
git push origin cvu/windows-support
```

The result is complete when both JSON files contain:

- relevant agent processes and their parent/child process trees;
- Handle results for the matching processes;
- recent Codex rollout file metadata;
- Claude's PID-to-session metadata and recent project files;
- OpenCode database paths and live candidate rows, plus CLI diagnostic output.

Do not commit transcript contents, OpenCode message content, credentials, tokens, or
environment-variable values other than the paths already emitted by the probe.
