# Windows validation handoff

Use this procedure on a Windows 11 24H2 machine as a standard, non-administrator
user. Run it once on amd64 and once on arm64 when both machines are available.
Enterprise LTSC, Windows multi-session, and Azure Virtual Desktop are outside
the supported boundary.

The validation output contains versions, test results, the Git commit, Windows
edition and build, architecture, Defender state, and SmartScreen settings. It
does not intentionally collect usernames, computer names, credentials,
transcripts, session content, environment-variable values, or full paths.

## Codex agent prompt

Give the Windows Codex agent this prompt from a checkout of this repository:

```text
Validate the current coSlash Windows implementation by following
docs/windows-validation.md exactly. Run every automated check as a standard
user, then complete every manual check with Claude Code, Codex, and OpenCode.
Do not change product code. Record pass, fail, or skip plus a short factual note
for every manual check in the generated JSON. Inspect the JSON for secrets or
personal data, validate that it parses, and commit only the result JSON. Do not
commit binaries, transcripts, logs, screenshots, credentials, or environment
dumps. If a check fails, preserve the JSON result and report the exact
reproduction; do not fix the failure unless asked.
```

## Prepare

Open Windows PowerShell 5.1 in the repository. Do not use an elevated window.
Confirm that Windows Terminal and the three supported agent CLIs are installed:

```powershell
git status --short
powershell.exe -NoProfile -Command '$PSVersionTable.PSVersion'
wt.exe --version
claude --version
codex --version
opencode --version
```

Check out the exact validation branch or commit supplied by the reviewer. Do not
pull or rebase unless explicitly instructed; the recorded commit must identify
the code under test.

If a release-candidate executable is available, verify its published checksum
first and pass its path as `-BinaryPath`. Otherwise omit that argument; the
script still builds and checks the current source tree.

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File collector\scripts\windows-validation.ps1 `
  -OutputPath docs\windows-validation-results\windows-amd64.json `
  -BinaryPath C:\path\to\coslash-windows-amd64.exe
```

Use `windows-arm64.json` on an Arm machine. The script exits nonzero if the
machine is outside the supported boundary or an automated check fails. It does
not modify Defender, SmartScreen, or system configuration. The credential test
creates and removes one disposable Windows Credential Manager entry.

## Complete the manual checks

Run the candidate executable as the same standard user. Use Windows Terminal
with Windows PowerShell 5.1. For each check below, change the matching
`manualChecks` entry in the result JSON from `pending` to `pass`, `fail`, or
`skip`, and add a short factual `note`. A supported-machine failure is `fail`,
not `skip`.

1. `claude-launch-resume-handoff`
   - Create a Claude Code session in a directory containing spaces.
   - Confirm coSlash discovers it as live with the correct project directory.
   - Resume it from coSlash and confirm the same session opens.
   - Start fresh with handoff and confirm a new Claude session receives the
     handoff.

2. `codex-launch-resume-handoff`
   - Repeat the launch, discovery, resume, and handoff checks for Codex.
   - Confirm the running process maps to the correct rollout rather than another
     recent Codex session.

3. `opencode-launch-resume-handoff`
   - Repeat the launch, discovery, resume, and handoff checks for OpenCode.
   - Confirm the session maps to the correct project directory.
   - Confirm an existing coSlash-managed OpenCode plugin is upgraded and an
     unmanaged plugin is not overwritten.

4. `unicode-and-handoff-cleanup`
   - Use a project path and prompt containing non-ASCII text and emoji.
   - Confirm all three agents receive the handoff without corrupted text.
   - After each launched agent reads its handoff, confirm the temporary handoff
     file is removed.

5. `session-state-and-feature-parity`
   - Confirm active, idle, waiting, and inactive state changes appear correctly.
   - Open the inspector and check timeline, artifacts, costs/tokens, readiness,
     search, filtering, and board/list views against the same session data used
     on macOS.

6. `browser-and-credential-restart`
   - Start coSlash and confirm the default browser opens the authenticated local
     URL.
   - Pair any test account allowed for this validation, restart coSlash, and
     confirm the credential is still available through Windows Credential
     Manager. Remove the test credential afterward.

7. `windows-to-linux-ssh`
   - Add a test Linux host through the normal settings flow.
   - Confirm setup, refresh, session discovery, resume, and handoff work from
     Windows without Unix SSH multiplexing options.

8. `standard-user-install-upgrade-defender-smartscreen`
   - Install the candidate under the current user's LocalAppData without
     administrator rights, start it, stop it, and replace it with a second copy.
   - Record the unmodified Defender and SmartScreen behavior for the unsigned
     executable. Do not weaken either protection to make the check pass.

## Validate and commit the result

Set the top-level `status` to `pass` only when every required manual and
automated check passes. Leave it as `fail` when any check fails. Then inspect the
file before committing it:

```powershell
$Result = "docs\windows-validation-results\windows-amd64.json"
$Data = Get-Content $Result -Raw | ConvertFrom-Json
$Data.manualChecks | Format-Table id, status, note -AutoSize
if ($Data.manualChecks.status -contains "pending") { throw "manual checks remain pending" }
Get-Content $Result
git add -- $Result
git commit -m "test: record Windows amd64 validation"
```

Commit only the JSON result. Send the commit SHA to the reviewer. Screenshots or
additional logs are only needed when a failed check cannot be understood from
its reproduction note.
