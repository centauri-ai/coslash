# Troubleshooting

Start with `coslash doctor`. It checks session sources, agent CLIs, and storage. Use `coslash doctor --json` for a shareable report, and check the terminal where coSlash started for logs.

## Sessions are missing

Create at least one local Claude Code, Codex, Cursor, or OpenCode session, then reload. Run `coslash doctor` for unreadable or missing sources. In the UI, select **All** vendors and time windows and clear search.

For Cursor, `coslash doctor` reports the IDE (`cursor`) and CLI (`agent`) separately. coSlash reads only local Cursor IDE and CLI sessions: Cursor SDK sessions and remote Cursor collection are unsupported. Cursor CLI token and compaction data can be unavailable because Cursor does not store them reliably; Cursor IDE exposes current context occupancy separately, not cumulative token usage.

For a remote machine, choose **Settings → Machines → Add remote host**. coSlash
uses the system's existing OpenSSH configuration or a simple `user@host`
destination. If SSH needs authentication or host-key confirmation, choose
**Authenticate in Terminal** and complete the native prompt there; coSlash never
receives the credential. A changed host key requires verification outside the
authentication flow. The Linux SSH server must enable SFTP and the SSH user
must be able to read the Claude/Codex paths listed in [Data and
privacy](data-and-privacy.md). Setup checks the connection, installs the
digest-verified helper, and verifies it.

If setup fails, **Retry setup** tries again. A host that does not answer SSH is
shown offline after a short check and retried in the background. coSlash uses
SFTP when the helper is unavailable, blocked by a `noexec` mount, unsupported,
or fails verification. Future coSlash releases automatically update only helpers
that coSlash previously installed and verified. Never paste remote stderr into a
bug report; **Copy diagnostics** contains bounded structured transport, version,
timing, byte-count, and coverage facts.

`SFTP subsystem unavailable` means ordinary SSH may work while SFTP is disabled
by `sshd_config` or account policy. `Agent data is not readable` means the SSH
account connected but lacks file permissions. `No Claude or Codex data found`
means the allowed roots are absent or empty. A stale banner keeps the last-good
cards visible; **Retry** starts an immediate bounded refresh.

To remove a remote host, choose **Remove**. It stops local monitoring even if
the host is offline and leaves its optional helper installed on the host.

## Pi sessions and runtime status

Pi support is available for local macOS and Windows sessions. Local Pi CLI reviews on both platforms work independently of collection and terminal support. Linux Pi sessions, runtime integration, synthesis, and launch actions are not supported.

Run `coslash doctor` and inspect the Pi source, CLI release, and managed-extension checks. Transcript schema 3 is supported; older and future schemas are skipped with diagnostics. Runtime hooks and terminal launch allow stable Pi releases at least 0.99.1. Pi 0.99.1, 0.99.2, and 1.0.0 are tested baselines; newer releases remain enabled with an untested-release diagnostic. Older releases, prereleases, and unrecognizable versions are excluded. An excluded runtime can have a readable schema-3 transcript while its status stays Unknown. An eligible release still needs valid runtime evidence to establish status.

Restart Pi after coSlash installs or updates `coslash-extension.ts`. An unmanaged file at that path is preserved and reported; review it before moving it aside. `/reload` is not a verified activation path. Missing extension or unverifiable process-start identity stays Unknown. Conflicting live leaves withhold active-branch evidence while status still follows verified owner activity. Waiting takes precedence over busy, then unknown evidence, then idle; verified terminated evidence permits Inactive. A hard exit is checked using process liveness, not just shutdown hooks. On macOS, process-start identity has second resolution, so same-PID reuse within the same second cannot be distinguished.

Pass the same `PI_CODING_AGENT_DIR` and `PI_CODING_AGENT_SESSION_DIR` overrides to coSlash that Pi uses. Global/project `sessionDir` and retained runtime paths are also discovered. For unobserved historical `--session-dir` locations, set `COSLASH_PI_SESSION_ROOTS` to absolute roots separated by `:` on macOS or `;` on Windows before starting coSlash. Relative project settings are resolved from that project's working directory; coSlash does not scan every repository for settings. Retained paths under `COSLASH_HOME/pi-history` survive restart. Removing that history removes discovery of otherwise unconfigured custom paths.

Pi costs are historical pricing estimates. Missing usage or ambiguous zero pricing is unavailable, not free. Verified forks exclude inherited spending while retaining inherited timeline rows. Missing, moved, conflicting, or unverifiable parents can make attributable accounting unavailable. Known tokens without model attribution remain visible separately. Pi snapshot and full-session exports are unavailable until those formats can represent branch context and accounting faithfully. Parent verification is bounded to 256 lineage links and does not guess relocated parent paths. Incomplete nested tool detail makes visible command/edit counts lower bounds; accounting availability is independent of that detail limit.

Interactive CLI, print, JSON, and RPC entrypoints load extensions on eligible Pi releases. Native Pi entrypoints report CLI, Print, JSON, or RPC from the runtime mode and verified package launcher. SDK hosts require explicit extension loading and `COSLASH_PI_ENTRYPOINT=pi-sdk`; unfamiliar hosts stay Unknown without that declaration. Native launcher evidence takes precedence over an inherited SDK declaration. The integration retains the latest terminated runtime modality, but conflicting or unverifiable live owners withhold it. Older runtime records and transcripts alone do not prove modality or a live branch. Restart existing Pi sessions after updating the integration; historical transcripts are not relabeled. Resume reopens the resolved file through the CLI; it cannot recreate an SDK host. Resume and handoff require an existing working directory and installed supported CLI. A deleted transcript, unknown identity, unsupported release, or missing working directory produces a local launch error.

Pi terminal actions, custom handoff, and local synthesis are available on macOS and Windows. Local background reviews with Pi CLI work on both platforms without the managed extension; an installed CLI must expose all required read-only and resource-disabling flags. On Windows, coSlash finds `pi.cmd` on `PATH` or in the managed `~/.pi/agent/bin` installation. Private handoff notes and staged Send prompts are removed on launch exit, signals, and failed directory changes.

Pi synthesis uses your configured provider/model by default. Pin a provider-qualified model in `settings.json` if needed. If synthesis fails, verify access to that model and refresh provider credentials, including expired AWS SSO sessions, then retry synthesis from the inspector.

Pi provider failures recorded in the selected session branch appear as an Error badge in the inspector. Hover or focus the badge to read the diagnostic, then refresh credentials or change the model in Pi and retry. A successful assistant response clears the badge. Failures before Pi creates a transcript remain visible in the Pi terminal.

## coSlash will not start

A port conflict is reported in the terminal. Stop the other process or run:

```sh
coslash --port 8888
```

## Building from source fails

`make release` in `collector/` needs supported Go and Node versions. It checks them before building and prints install hints if either is missing or unsupported.

- **End users** should not build from source. On macOS, use the install script, Homebrew, or a release archive. On Windows, download the release executable (see the README Install section).
- **Developers** need Go 1.26+ (`brew install go` or https://go.dev/dl/) and Node 24+ (`brew install node` or https://nodejs.org/). Versions are pinned in `collector/go.mod` and `frontend/.nvmrc`.
- If the binary starts but the UI is missing, it was built with `make build` instead of `make release`. Rebuild with `make release` so the frontend is embedded.

## Synthesis does not appear

Confirm that synthesis is enabled in Settings and that the selected CLI is installed, authenticated, and supports the selected model. Short sessions may not be eligible. Storage errors, timeouts, and recent failures also prevent a result; opening the inspector surfaces the current error.

## Resume or Start fresh fails

Launching requires a recorded working directory, the agent CLI, and the terminal selected in Settings. Interactive handoffs on macOS and Linux also require `expect` on `PATH`. On macOS, confirm Apple Terminal or iTerm2 is installed and allow automation under **System Settings → Privacy & Security → Automation**. On Windows, confirm Windows PowerShell 5.1 is available; coSlash uses Windows Terminal when it is installed and otherwise opens Windows PowerShell directly.

Cursor CLI **Resume** requires the `agent` command and restores the recorded session. **Open Cursor** for a Cursor IDE session requires the `cursor` command and only opens the recorded workspace; Cursor does not provide a way to restore that specific IDE chat.

**Start fresh with handoff** offers installed agents on the source host, then sends a Review or custom request to the selected CLI. If Cursor asks you to trust the workspace, answer that prompt first; coSlash sends the handoff when the agent is ready.

Remote Resume and Start fresh require a live SSH connection and a recorded
working directory. They are disabled while the host is offline; wait for it to
reconnect, then try again.

## Offline Pi integration check (source builds)

A repeatable native lifecycle check lives in `collector/internal/vendors/pi/testdata/runtime-check.mjs`. It creates temporary agent/config/session/coSlash directories and uses a deterministic local provider. It does not read your Pi credentials or send model requests. With Node 24 and an installed verified Pi release:

```sh
PI_TEST_RELEASE_DIR=/absolute/path/to/pi/install/releases/0.99.2 \
  node collector/internal/vendors/pi/testdata/runtime-check.mjs
```

The release directory must contain Pi's `node_modules`. Run separately against the minimum baseline 0.99.1 and the latest stable release before updating tested-release diagnostics. The script checks production extension events, settlement, dialogs, compaction, concurrent owners, crashes, replacement, and retained graceful-exit history. Run `node collector/internal/vendors/pi/testdata/entrypoint-check.mjs` with the same release variable to check native exact-file resume, fresh-session handoff context delivered to the provider, print, JSON, RPC, piped print, and SDK declaration behavior using the local provider. These probes do not verify coSlash HTTP launch, terminal readiness, or interactive prompt transport; also exercise dashboard Resume, fresh handoff, and Send in a real terminal before release. They do not replace real-provider testing.

## Report a bug

[Open an issue](https://github.com/centauri-ai/coslash/issues) with:

- `coslash doctor --json` output or **Copy diagnostics** from the app.
- Minimal reproduction steps and the expected and actual result.
- Relevant logs with paths, prompts, tokens, secrets, and session IDs redacted.

Do not attach a transcript unless you have reviewed and redacted it.
