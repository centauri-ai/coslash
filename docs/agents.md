# Agent integrations

Use coSlash from an agent through local CLI skills or a separately authorized Hub MCP connection.
Local commands need an [installed coSlash executable](install.md) on `PATH`.
Hub MCP access uses its own authorization, separate from device pairing.

- [Agent skills](#agent-skills)
- [Remote Hub MCP](#remote-hub-mcp-for-local-agents)
- [Command reference](#command-reference)

## Agent skills

Install the coSlash skills for Claude Code:

```sh
claude plugin marketplace add centauri-ai/coslash#stable
claude plugin install coslash@centauri-ai
```

Or for Codex:

```sh
codex plugin marketplace add centauri-ai/coslash --ref stable
codex plugin add coslash@centauri-ai
```

Or for Grok:

```sh
grok plugin install centauri-ai/coslash@stable#plugins/coslash --trust
grok plugin enable coslash
```

Or for Cursor CLI, OpenCode, and Pi:

```sh
npx skills@latest add centauri-ai/coslash -g -a cursor -a opencode -a pi --skill '*' -y
```

If the app is stopped, the `sessions`, `handoff`, `send`, and `review` skills start it. The `doctor` skill works without the app.

The `stable` branch moves with each stable release. When you update coSlash, update the skills too:

- Claude Code: `claude plugin marketplace update centauri-ai`, then `claude plugin update coslash@centauri-ai`.
- Codex: `codex plugin marketplace upgrade`.
- Grok: `grok plugin update coslash`.
- Cursor CLI, OpenCode, and Pi: `npx skills@latest update -g`.

On Windows, if `npx skills` fails on a symlink, rerun the same command with `--copy`.

If you use a custom `COSLASH_HOME` with Codex, a `shell_environment_policy` in `~/.codex/config.toml` can drop it. Set it under `[shell_environment_policy.set]` as `COSLASH_HOME = "<path>"`. `coslash doctor --json` shows the `storage.home` that the skills use.

## Remote Hub MCP for local agents

When your Hub has enabled its remote MCP service and your account has **Agent
knowledge** enabled, configure an installed agent with `coslash mcp setup
claude|codex|cursor|opencode`. Choose one agent name per command. Then run
`coslash mcp login <agent>` and approve its `mcp:read` and `mcp:ask` access in
the coSlash browser. The endpoint is `https://mcp.hub.coslash.io/mcp`; use
`--url <https://host/mcp>` on `setup` only for another deployment. Pairing a
Local device does not authorize MCP.
OpenCode setup preserves an existing `opencode.jsonc`, including comments and
other servers.

On a headless host, `coslash hub login --hub <MCP URL>` connects the Local CLI
with device-code authorization. Copy the MCP URL from Hub Settings → Coding
agents. It requests `hub:read` by default. Repeat
`--scope SCOPE` to request additional access, for example
`coslash hub login --scope hub:write --scope hub:share`; review the requested
scopes in Hub before the CLI stores its separate Hub credential. Search session
text with `coslash hub search "retry backoff"`; use `coslash hub call` for other
tools. On macOS, the CLI stores its encryption key in Keychain and the encrypted
credential in a private `0600` file under `$COSLASH_HOME/hub-credentials`.

`coslash hub auth status [--json]` checks the saved connection without starting a
login. It sends an authenticated MCP `ping`, refreshing an expired token first.
It reports the Hub URL, the scopes Hub granted, the token expiry, and, when the
grant includes `hub:read`, the account ID, name and email. A login saved by an
older coslash shows unknown scopes until its next token refresh. Reading the
credential can still show an OS Keychain or Credential Manager prompt.

| Exit | `status` | Next step |
| --- | --- | --- |
| 0 | `authenticated` | None. `identity_status` is `available`, `insufficient_scope` (no `hub:read`) or `unavailable` (the account read failed after a successful ping). |
| 1 | `error` | The Hub rejected the request or returned an invalid response; see `error_code`. |
| 2 | (no output) | Fix the command arguments. |
| 3 | `login_required` | Run `recovery_command`, for example `coslash hub login --hub https://hub.example.test/mcp`. |
| 4 | `unreachable` | Check the network and rerun the status command. Do not log in again. |
| 5 | `credential_error` | The credential store is unreadable or a refreshed token could not be saved; follow `recovery_command`. For `invalid_lock_file`, delete `hub-credentials/agent-access.lock` while no coslash command runs; it is under `$COSLASH_HOME` on Linux and under `~/.coslash` on macOS and Windows, whatever `COSLASH_HOME` is. |
| 6 | `busy` | Another coslash command holds the credentials or the connection changed; rerun the status command. |

`--json` prints exactly one object with the fields `status`, `hub_url`,
`scopes`, `expires_at`, `account`, `identity_status`, `error_code` and
`recovery_command`. `scopes` is `null` when unknown and `[]` when Hub granted no
scopes. Status never prints tokens or deletes credentials.

Hub CLI commands that share a credential store take a file lock before they
read, refresh or save it, so concurrent commands rotate a refresh token only
once. Update every coslash binary that uses the same credentials: an older
binary ignores the lock, and a refresh token replayed by two processes makes
Hub revoke the login.

The agent stores its own OAuth credential. To switch Hub accounts for Claude
Code, Codex, or OpenCode, run `coslash mcp switch <agent>`, change the account
in the coSlash browser, then run `coslash mcp login <agent>`. For Cursor,
disconnect coSlash in Cursor's MCP settings first, change the browser account,
and run `coslash mcp login cursor`; Cursor's CLI does not expose MCP credential
logout. Existing tokens also stop working when the Hub revokes access or turns
off Agent knowledge. Ask the agent to use a `coslash.*` tool to confirm that
the intended account can read the expected sessions.

## Command reference

Run `coslash --help` for the full command list. These commands support local session workflows:

| Command | Effect |
| --- | --- |
| `coslash` | Start the server and open the app. |
| `coslash ui-url` | Print the authenticated web UI URL of the running app, also after `--background` or `--no-open` startup. Use the same `COSLASH_HOME` as the running app. |
| `coslash sessions [query] --json` | List local sessions as JSON. The query filters by title, repository, branch, or agent. An exact session ID or `<agent>:<session>` selector returns only that session. |
| `coslash handoff <agent>:<session>` | Print the handoff Markdown for a selector from `coslash sessions`. |
| `coslash send <agent>:<session> --to claude\|codex\|opencode\|cursor\|pi\|grok [--access read-only\|workspace-write] [--] [message]` | Start the target agent in the working directory of the session, with the handoff. Linux supports Claude/Codex with an explicit task, defaults to read-only, and prints the result; `--access workspace-write` permits edits within the source directory. On macOS and Windows, sends open the configured terminal with an optional first task. For Cursor, coSlash copies both to the clipboard. Paste them into the new Cursor CLI session. |
| `coslash review <agent>:<session> --with claude\|codex\|opencode\|cursor\|pi\|grok` | Start a review of a local session with an installed agent. |
| `coslash doctor` / `coslash status` | Check session sources, agent CLIs, local storage, Hub sync health, and the `coslash:` link handler. |
| `coslash doctor --json` / `coslash status --json` | Print the same diagnostics as JSON, including Hub origin, pairing, last check-in, content-free queue counts, and link-handler state. |
| `coslash doctor --fix-links [--yes]` | On macOS, list stale URL handlers; add `--yes` to unregister the listed bundles. |

`sessions`, `handoff`, `send`, and `review` need the app to be running. Results go to standard output. Failures write `Error: <message>` to standard error and exit non-zero. The commands find the access token of the running app locally and never print it. Only `coslash ui-url` prints the token, as part of the URL on standard output. It does not write the URL to the app logs.

On Linux, `send` runs Claude Code or Codex without a terminal, requires an explicit
task, and waits up to 30 minutes for its result. Access defaults to read-only;
use `--access workspace-write` before the task to authorize edits in the source
working directory. For example: `coslash send '<agent>:<session>' --to claude --access workspace-write -- 'Review plan.md and append findings.'`
Use `--` before flag-looking task text. Stopping the command or Local cancels its
agent and descendants; results are returned directly, without a job store or
status/log commands. After a connection failure, inspect any edits before
retrying: an interrupted task can have made changes.

Interactive handoffs on macOS need the system `expect` command. On macOS and Windows, Grok Build is also a handoff and review destination after you log in to it. On Windows, a Grok review can read files outside the worktree. Read [Data and privacy](data-and-privacy.md) before you use it.
