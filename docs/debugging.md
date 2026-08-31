# Debugging and recording issues

Use this when something in coSlash looks wrong — missing sessions, synthesis failures, launch problems, remote SSH hangs, or unexplained UI states. Evidence stays on the Mac (terminal, diagnostics export, optional log files). Do not paste transcripts, prompts, tokens, or secrets into tickets.

## Quick capture (any area)

1. Keep the terminal where `coslash` is running visible.
2. Reproduce once, noting the exact UI path and expected vs actual result.
3. Collect a safe snapshot:

   ```sh
   coslash doctor --json > /tmp/coslash-doctor.json
   ```

   Or in the app: open diagnostics and use **Copy diagnostics**.

4. Grab nearby terminal / log lines (`issue.*` and `remote.*`).
5. File or share with:

   - short reproduction steps
   - expected vs actual
   - `doctor --json` or **Copy diagnostics**
   - redacted terminal / log excerpts
   - coSlash version (`coslash --version`) and macOS version if relevant

See also [Troubleshooting](troubleshooting.md) for common fixes before escalating.

### What is safe to share

| Include | Exclude |
| --- | --- |
| Version, platform, doctor/diagnostics JSON | Transcripts, prompts, session names |
| Classified reasons (`connection_failed`, `cli_missing`, timeouts) | SSH aliases that embed secrets or customer hostnames if sensitive |
| Timings, counts, outcome lines (`issue.*`, `remote.*`) | Tokens, cookies, API keys, raw stderr host banners |
| UI copy the user saw | Full remote paths under another user's home |

## Product areas

| Area | First checks | Extra evidence |
| --- | --- | --- |
| Local sessions missing | Vendor/time filters; `coslash doctor` sources | `issue.collect.*`; doctor JSON |
| Synthesis | Settings enabled; CLI auth/model | `issue.synthesis.failed`; inspector error text |
| Resume / Start Fresh | macOS Automation; terminal choice; local-only | `issue.launch.failed`; doctor |
| Sharing / Hub | Hub URL/env; credential state | `issue.hub.failed`; no tokens |
| Settings | Valid JSON; permissions on `~/.coslash` | `issue.api.error route=settings` |
| Remote Machines / SSH | `ssh <alias>` once; Test connection | `remote.*` lines (below) |

## Local issue logs (this testing branch)

On `test/remote-ssh-observability`, coSlash records concise product issue lines plus remote SSH steps. Logging defaults **on**. Startup should print:

`issue logging on → <COSLASH_HOME>/logs …`

```sh
COSLASH_DEBUG=0 ./bin/coslash          # disable all issue/remote step logs
COSLASH_DEBUG=1 ./bin/coslash          # force on
COSLASH_REMOTE_DEBUG=0 ./bin/coslash   # also disables (fallback if COSLASH_DEBUG unset)
```

Lines go to the coSlash terminal and `$COSLASH_HOME/logs/issues-YYYYMMDD.log` (default home `~/.coslash`). Files are mode `0600`; do not commit them.

```sh
grep -E 'issue\.|remote\.' ~/.coslash/logs/issues-*.log
```

### Useful `issue.*` names

| Line | Meaning |
| --- | --- |
| `issue.api.error route=… reason=… status=…` | API handler returned an error |
| `issue.collect.vendor_failed agent=…` | One local vendor failed; others may still serve |
| `issue.collect.all_failed` | Every vendor failed |
| `issue.synthesis.failed reason=…` | Debrief CLI/run failed |
| `issue.launch.failed reason=…` | Resume / Start Fresh failed |
| `issue.hub.failed action=…` | Hub destination/pairing/share failed |
| `issue.httpsec.reject reason=…` | Loopback auth/host/origin rejected |
| `issue.startup.soft_fail component=…` | Non-fatal startup problem |

Fields stay low-cardinality (reasons, statuses, agent ids) — not session ids, paths, prompts, or tokens.

## Remote SSH step logs

Same switch as above. Use when **Settings → Machines → Test connection** hangs, times out (~95s), or needs phase-by-phase proof.

Healthy test order:

1. `remote.test phase=start`
2. `remote.control_master result=hit|started …`
3. `remote.sftp_open result=ok …`
4. `remote.test outcome=ok|limited …`

| Signal | Likely meaning | Next check |
| --- | --- | --- |
| UI timeout, few/no `remote.*` lines | Request never hit the handler, or stuck early | Confirm this build is running; watch the terminal while retrying |
| `phase=start` then silence | Blocked in SSH / ControlMaster / SFTP open | `ssh <alias>` in Terminal; confirm SFTP |
| `control_master … start_failed` | Master could not start | Host key, auth, `BatchMode`, alias config |
| `sftp_open … error` | SFTP failed after SSH | Host `Subsystem sftp`; account policy |
| `test outcome=error reason=…` | Classified failure shown in UI | Match reason to [troubleshooting](troubleshooting.md) |
| `test phase=reject` | Invalid alias/body | Alias format |

## Operations notes (this branch)

- Prefer doctor/diagnostics first; add `issue.*` / `remote.*` logs when reproducing.
- Delete old `logs/issues-*.log` freely; they are local-only under `COSLASH_HOME`.
- Before promoting observability to `main`, set `defaultOn` to `false` in `collector/internal/observe/observe.go` so release builds stay quiet unless `COSLASH_DEBUG=1`.
- Land real product fixes on the merge path to `main`; keep dump-only noise temporary on this branch.
