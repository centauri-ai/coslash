# Installation

Choose Hub setup for a connected device, or install coSlash Local without a Hub account.

- [Supported agents and platforms](#supported-agents-and-platforms)
- [Install and connect from Hub](#install-and-connect-from-hub)
- [Install coSlash Local separately](#install-coslash-local-separately)
- [First run](#first-run)
- [Linux sessions over SSH](#linux-sessions-over-ssh)
- [Install from a release archive](#install-from-a-release-archive)

## Supported agents and platforms

**Early preview · macOS, Linux (amd64, arm64) and Windows 11 amd64**

| Agent | Supported OS | Local sessions | Linux over SSH | Resume |
| --- | --- | --- | --- | --- |
| Claude Code | macOS, Windows | Desktop App, CLI | Yes | Yes |
| Codex / ChatGPT | macOS, Windows | Desktop App, CLI | Yes | Yes |
| Cursor | macOS, Windows | Desktop App, CLI | No | CLI only. **Open Cursor** reopens the folder of a Desktop App chat. |
| OpenCode | macOS, Windows | Desktop App, CLI | No | Yes |
| Pi | macOS, Windows | CLI | No | Yes |
| Grok Build | macOS, Windows | CLI | No | Yes |

On Linux (amd64, arm64), Local collects these agents' local sessions and syncs them to Hub. Pi and Grok Build synthesis, resume, and handoff stay on macOS and Windows; see [Linux](#linux).

Local collection needs no Hub account or telemetry.

## Install and connect from Hub

In Hub, open **Devices → Add device → This computer** and copy the command for
your computer. The command installs coSlash Local, starts it
in the background, and claims your one-use setup code. Return to Hub to approve
the computer.

The public Windows installer places `coslash.exe` in
`%LOCALAPPDATA%\coSlash\bin` by default (`COSLASH_INSTALL_DIR` overrides it).
It does not change `PATH`; the Hub command invokes the installed executable
directly. To run it yourself in PowerShell, use the selected install directory
when set, or the default otherwise:

```powershell
$installDir = $env:COSLASH_INSTALL_DIR
if (-not $installDir) { $installDir = Join-Path $env:LOCALAPPDATA 'coSlash\bin' }
& (Join-Path $installDir 'coslash.exe')
```

You can also add your chosen install directory to your user `PATH` before using
commands or skills that call `coslash`.

Hub shows a personalized command. These examples use a sample code and origin;
run the exact command Hub provides.

**macOS and Linux** (on a server, paste it in your own SSH session; Hub never
sees SSH credentials):

```sh
curl -fsSL https://coslash.io/install.sh | bash -s -- --connect K7QX-29PD --hub https://hub.coslash.io
```

**Homebrew:**

```sh
brew install centauri-ai/tap/coslash && coslash connect K7QX-29PD --hub https://hub.coslash.io
```

**Windows PowerShell:**

```powershell
$env:COSLASH_CONNECT='K7QX-29PD'; $env:COSLASH_HUB='https://hub.coslash.io'; irm https://coslash.io/install.ps1 | iex
```

The code expires after ten minutes and can be used once. Hub switches to the
computer's **Connect** card after Local claims the code; approving there
connects the device.

If Hub limits connect requests or is temporarily unavailable, Local reports a
retryable failure (exit code 8). Wait for the indicated delay and rerun the
command; get a new command in Hub if its code expires. Claims are not retried
automatically. With `--json`, these failures have state `rate_limited` or
`hub_temporarily_unavailable` and `retryAfterSeconds` (0 when no valid delay is
provided; delays are bounded to one day).

### Open coSlash Local

When Local is not running, Hub offers **Open coSlash Local**. Click it to start
Local on that computer and request an immediate Hub check-in. Local stays in
the background without opening its own browser tab. The first start registers a
per-user URL handler on macOS or Windows; neither requires administrator rights.
On macOS, paired release builds install their login agent automatically. Homebrew
upgrades keep the agent pointed at the stable install path. Startup preserves an
agent owned by another installation or data home, and any existing URL handler.
Dev builds, branch builds, and custom data homes never install the shared login
agent, even through `coslash service install`. A custom `COSLASH_HOME` skips URL
registration and keeps separate Hub credentials.
Closing the browser does not stop the sync worker. The macOS login agent restarts
Local if its process exits. On Windows, pairing registers Local to start at the
next sign-in; on Linux, see
[Linux](#linux). Hub asks running
Locals to check in when My space opens and shows whether they are reachable.
When uninstalling a paired Windows copy, remove its login entry with
`Remove-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'coSlash Local'`.
Settings → Sync has an **Automatically update coSlash Local** switch, on by
default. Script installations stage a checksum-verified release, checkpoint
active queue work, restart, and roll back if the new Local fails to become ready.
Homebrew and portable installs use the device's manual update action.

## Install coSlash Local separately

Use these options for local use without a Hub account, or to install Local before you connect it to Hub.

### macOS

```sh
curl -fsSL https://coslash.io/install.sh | bash
# or
brew install centauri-ai/tap/coslash
```

Then start the server and web app:

```sh
coslash
```

coSlash serves <http://127.0.0.1:8787> and opens your browser with a fresh access token. Use the URL it opens. Links from an earlier run stop working when the server restarts.

`brew upgrade coslash` updates it and `brew uninstall coslash` removes the binary. Your data stays in `~/.coslash` until you delete that directory. To install a specific version from a release archive, see [Install from a release archive](#install-from-a-release-archive).

### Linux

coSlash Local runs on Linux amd64 and arm64, including headless servers. To
connect a server, choose **Devices → Add device → A server over SSH** in
Hub, SSH into the server, and paste the command there. To install without
connecting:

```sh
curl -fsSL https://coslash.io/install.sh | bash
```

The script picks the `linux_amd64` or `linux_arm64` release archive from
`uname -m`, verifies it against `checksums.txt`, and installs `coslash` to
`~/.local/bin` (or `/usr/local/bin` when writable). It needs `curl`, `tar`, and
`sha256sum` or `shasum`; it never uses `sudo`.

After `coslash connect`, Local keeps running after you log out and starts at
boot. When your account lingers (or can turn lingering on), Local runs as the
systemd user service `coslash.service` (`systemctl --user status coslash`).
If the system does not let an SSH session enable lingering without `sudo`,
Local keeps running as a detached process and adds a `crontab` `@reboot`
entry. To use the systemd service instead, run `sudo loginctl enable-linger
"$USER"` before connecting. In a container without systemd or cron, run
`coslash --background` from the entrypoint. The device
credential is stored in `~/.coslash/hub-credentials/<hub host>` (mode `0600`)
rather than a desktop keyring, so a reboot or a locked keyring never unpairs
the host. Logs are in `~/.coslash/logs/coslash.log`. On a shared server each
account connects its own Local; if another account's Local already uses port
8787, yours listens on a free loopback port instead.

The service does not read shell profiles. It keeps the `PATH`, `COSLASH_HOME`,
`XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `GROK_HOME`, `OPENCODE_DB`, and Pi path
variables set when you connected; reconnect after changing them. To uninstall,
run `systemctl --user disable --now coslash.service`, delete
`~/.config/systemd/user/coslash.service` and the `coslash` binary, and remove
`~/.coslash` if you no longer want its data.

### Windows

Install with PowerShell:

```powershell
irm https://coslash.io/install.ps1 | iex
```

Run the startup command the installer prints. It uses your selected `COSLASH_INSTALL_DIR`, or `%LOCALAPPDATA%\coSlash\bin` by default.
The installer does not change `PATH`.

For a portable installation:

Download `coslash-windows-amd64.exe` from the newest stable [release](https://github.com/centauri-ai/coslash/releases) that includes it, or from the newest prerelease if no stable release does. Double-click it. coSlash runs as a portable application and opens its UI in your browser. It needs no administrator privileges, WSL, Go, Node, or GNU tools.

To upgrade, replace the executable with a newer one. To uninstall, delete it. Your data stays in `~\.coslash`. The executable is not code-signed. To verify its checksum, see [Install from a release archive](#windows-checksum). The supported baseline is Windows 11 24H2 amd64 ([Windows validation](windows-validation.md)).

### Connect this computer to Hub

In Hub, open **Devices → Add device → This computer**. After installing,
start coSlash Local once and return to Hub to start the pairing handoff. On
macOS, the first run registers a per-user Launch Services handler. On Windows,
the first run registers the `coslash:` URL scheme for the current Windows user;
neither requires administrator privileges. When Hub asks the browser to open
Local, allow the app handoff. Hub presents the account, workspace and computer
name and waits for an explicit approval before Local stores its device key.
Hub shows the first check-in separately from approval. After approval, Local
starts personal v4 sync by default under the Hub's device policy. The Hub-led
path does not use the manual pairing code screen or enable team sharing.

### First run

coSlash needs at least one local agent session to read. If it finds none, it shows a checklist of every source that it looked at. Take one turn in a supported agent in a repo, then run the checks again. `coslash doctor` prints the same diagnostics in the terminal.

When Pi is installed, coSlash manages an extension for live status. Restart running Pi processes after an extension update. For Pi versions, custom session directories, and status details, see [Troubleshooting](troubleshooting.md#pi-sessions-and-runtime-status).

### Linux sessions over SSH

In **Settings → Machines**, choose **Add remote host** and enter an alias from your OpenSSH configuration or a `user@host` destination. coSlash uses the system `ssh` client and never edits your SSH configuration. After you approve it, coSlash installs a small read-only helper in `~/.coslash/helpers` on the host. If the helper cannot run, coSlash falls back to SFTP.

Remote Claude Code and Codex sessions support **Resume** and **Start fresh with handoff** while the host is connected. Cached sessions stay readable while the host is offline. Synthesis and Commands are local-only. For setup errors, see [Troubleshooting](troubleshooting.md#sessions-are-missing).

## Install from a release archive

### macOS release archive

```sh
VERSION="v0.2.0" # or the desired version tag
ARCH="arm64"  # amd64 on Intel
ASSET="coslash_${VERSION}_darwin_${ARCH}.tar.gz"
BASE_URL="https://github.com/centauri-ai/coslash/releases/download/${VERSION}"
curl -fLO "${BASE_URL}/${ASSET}"
curl -fLO "${BASE_URL}/checksums.txt"
grep -F "  ${ASSET}" checksums.txt | shasum -a 256 -c -
tar -xzf "${ASSET}"
"${ASSET%.tar.gz}/coslash"
```

Release binaries are unsigned. macOS can warn about archives that a browser downloaded. The Homebrew install does not get this warning.

### Windows checksum

Download `checksums-windows.txt` from the same release as `coslash-windows-amd64.exe`. Open Windows PowerShell in the directory that contains both files, then run:

```powershell
$Asset = "coslash-windows-amd64.exe"
$Expected = (Select-String -Path checksums-windows.txt -Pattern "  $([regex]::Escape($Asset))$").Line.Split()[0]
$Actual = (Get-FileHash $Asset -Algorithm SHA256).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw "$Asset checksum does not match" }
```

Windows release binaries are not code-signed, so Windows can show a SmartScreen warning. Do not bypass the security policy of your organization.
