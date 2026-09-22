# Windows validation

The supported Windows baseline is Windows 11 24H2 (build 26100 or later) on
amd64, running as a standard user with Windows PowerShell 5.1. Windows Terminal
is preferred but optional. WSL is not required.

Automated pull-request checks run the complete Go test and vet suites on a
native `windows-2025` runner, verify the SHA-256 checksum of the release-built
executable with `Get-FileHash`, and smoke-test that exact executable without
contacting an SSH host.

Before releasing, perform this manual check on the packaged
`coslash-windows-amd64.exe`:

1. Start it without elevation from a path containing spaces. Confirm the
   browser opens, Settings can be saved, and diagnostics complete. Repeat from
   one non-ASCII path when practical.
2. For Claude Code, Codex, OpenCode, and Cursor IDE/CLI, confirm local session
   discovery, resume, and Start fresh with handoff. Confirm a missing agent is
   reported as unavailable rather than as a collector failure.
3. With a configured Linux OpenSSH destination, inspect `Get-Service ssh-agent`
   and `ssh-add.exe -l` without changing either. Authenticate through the UI,
   complete the native key-based prompt in Windows Terminal or Windows
   PowerShell, refresh sessions, resume one session, and start fresh with a
   handoff. Cancel a separate authentication attempt and confirm it exits
   cleanly.
4. Repeat discovery with `%APPDATA%` and `%LOCALAPPDATA%` redirected to validate
   known-folder handling. Where a multi-user test account is available, confirm
   coSlash neither trusts nor reports another user's private state or processes.
5. Download the executable through the same browser used by users. Record any
   Microsoft Defender or SmartScreen warning. The executable is currently
   unsigned, so do not bypass organization policy merely to complete this test.

Password-only SSH may work in the interactive terminal but cannot be reused by
background refresh. Do not persist a password; reusable authentication requires
`ssh-agent` or another already-configured key agent.

Sanitize reports before sharing them. Remove host names, user names, filesystem
paths, session IDs, tokens, prompts, and SSH output. The manual result should
record the commit SHA, Windows edition/build/architecture, PowerShell version,
administrator status, packaged executable checksum, and pass/fail results for
each item above.
