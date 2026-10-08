[![coSlash](frontend/public/brand/coslash-logo.svg)](https://coslash.io)

**The attention layer for coding agents. Run more agents, lose less context.**

[coslash.io](https://coslash.io)

[![CI](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml/badge.svg)](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml)
[![Go 1.26.5](https://img.shields.io/badge/Go-1.26.5-00ADD8?logo=go)](collector/go.mod)
[![Node 24.4.1](https://img.shields.io/badge/Node-24.4.1-339933?logo=node.js&logoColor=white)](frontend/.nvmrc)
[![License](https://img.shields.io/github/license/centauri-ai/coslash)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/centauri-ai/coslash)](https://github.com/centauri-ai/coslash/releases)

Three agents are running. One finished twenty minutes ago, one is waiting on a question you never saw, and one has been quietly compacting its context on a branch whose name you've forgotten. coSlash reads their transcripts straight off your disk and turns them into a single board: what each session set out to do, what it decided, what it changed, and which ones need you next.

Then it gets you back in — resume a session in its own terminal with full context, or copy a handoff brief and pick it up cold somewhere else.

Local collection runs on your machine. Session data leaves it when you
explicitly approve a Hub share or enable experimental v4 sync.
A separately authorized remote Hub MCP connection lets your agent query Hub
sessions; synthesis uses the selected agent CLI. See [data and
privacy](docs/data-and-privacy.md).

**Early preview · macOS and Windows 11 amd64**

| | |
| --- | --- |
| **Supported agents** | Claude Code · Codex / ChatGPT · Cursor · OpenCode · Pi (macOS, local) |
| **Works with** | The desktop apps and the CLIs of each agent |
| **Reads** | Local transcripts and, optionally, one Linux host over read-only SFTP. Local collection needs no Hub account or telemetry. |

## Install and connect from Hub

In Hub, open **Devices → Add device → This computer** and copy the command for
your computer. The command installs coSlash Local, adds it to `PATH`, starts it
in the background, and claims your one-use setup code. Return to Hub to approve
the computer.

Hub shows a personalized command. These examples use a sample code and origin;
run the exact command Hub provides.

**macOS:**

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

### Open coSlash Local

When Local is not running, Hub offers **Open coSlash Local**. Click it to start
Local on that computer and request an immediate Hub check-in. Local stays in
the background without opening its own browser tab. The first start registers a
per-user URL handler on macOS or Windows; neither requires administrator rights.

## Install coSlash Local separately

Use these options when you want to install Local before connecting it to Hub.

### macOS

macOS on Apple Silicon or Intel.

```sh
curl -fsSL https://coslash.io/install.sh | bash
```

Or with Homebrew:

```sh
brew install centauri-ai/tap/coslash
```

Then start the server and web app:

```sh
coslash
```

coSlash serves <http://127.0.0.1:8787> and opens your browser with a fresh access token. Use the URL it opens — links from an earlier run stop working once the server restarts.

`brew upgrade coslash` updates it and `brew uninstall coslash` removes the binary. Uninstalling leaves your data in `~/.coslash`; delete that directory separately if you no longer want it.

#### Install a release archive manually

```sh
VERSION="v0.0.1" # or the desired version tag
ARCH="arm64"  # amd64 on Intel
ASSET="coslash_${VERSION}_darwin_${ARCH}.tar.gz"
BASE_URL="https://github.com/centauri-ai/coslash/releases/download/${VERSION}"
curl -fLO "${BASE_URL}/${ASSET}"
curl -fLO "${BASE_URL}/checksums.txt"
grep -F "  ${ASSET}" checksums.txt | shasum -a 256 -c -
tar -xzf "${ASSET}"
"${ASSET%.tar.gz}/coslash"
```

Release binaries are unsigned. macOS may warn about archives downloaded through a browser; the supported Homebrew install is unaffected.

### Windows

Open the [coSlash releases](https://github.com/centauri-ai/coslash/releases)
page and download `coslash-windows-amd64.exe` from the newest stable release
that includes it. If none does, use the newest prerelease with that executable.
Double-click the executable. coSlash runs as a portable application and opens
its UI in your browser; no administrator privileges, WSL, Go, Node, or GNU
tools are required.

Move the executable to a permanent folder if desired. To upgrade, download the
new release and replace the previous executable. Removing the executable
uninstalls coSlash but leaves its data in `~\.coslash`.

#### Verify the Windows download checksum

Download `checksums-windows.txt` from the same release as the executable. Open
Windows PowerShell in the directory containing both files, then run:

```powershell
$Asset = "coslash-windows-amd64.exe"
$Expected = (Select-String -Path checksums-windows.txt -Pattern "  $([regex]::Escape($Asset))$").Line.Split()[0]
$Actual = (Get-FileHash $Asset -Algorithm SHA256).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw "$Asset checksum does not match" }
```

Windows release binaries are not currently code-signed. Windows may display a
SmartScreen warning; do not bypass an
organization's security policy.

Windows support is validated on Windows 11 24H2 (build 26100 or later), amd64,
as a standard user with Windows PowerShell 5.1. WSL is not required. See the
[Windows validation checklist](docs/windows-validation.md) for the release
contract and remaining manual checks.

### First run

coSlash needs at least one local agent session to read. If it finds none, it says so and runs a checklist of every source it looked at — run Claude Code, Codex, Cursor (IDE or `agent` CLI), or OpenCode in a repo, take one turn, and re-run the checks. `coslash doctor` prints the same diagnostics from the terminal.

### Local Pi sessions

Pi collection, runtime integration, synthesis, resume, and handoff are supported only for local macOS sessions. Windows and Linux Pi support is deferred until device validation; their coSlash builds and other supported agents remain available.

The reader supports Pi transcript schema **3**. Schema 1, 2, and future schemas are reported as unsupported; synthetic migration research does not establish older-format support. Runtime integration and local launch are tested with **Pi 0.99.1 and 0.99.2**, independently of the transcript schema.

On startup, coSlash installs its managed extension in `PI_CODING_AGENT_DIR/extensions/coslash-extension.ts` (default `~/.pi/agent/extensions/`). It preserves Pi settings and other extensions and refuses to overwrite an unmanaged file. Restart existing Pi processes after installation or update. Missing integration or an unverified Pi runtime release produces **Unknown**, not Inactive. Print, JSON, and RPC modes load the extension on the verified release; SDK hosts must load it explicitly.

Default and configured Pi storage is read locally. `PI_CODING_AGENT_DIR` selects the agent directory; `PI_CODING_AGENT_SESSION_DIR` selects a session root. Pi also supports global/project `sessionDir` settings and CLI `--session-dir`. Start coSlash with the same overrides. For historical custom locations never observed by the integration, set `COSLASH_PI_SESSION_ROOTS` to an OS path-list (colon-separated on macOS). Integrated runtimes retain learned exact paths under `COSLASH_HOME`, so those sessions remain discoverable after Pi and coSlash restart. Project settings are discovered from coSlash's startup directory and already-discovered session working directories; arbitrary projects are not searched.

Pi Resume opens the backend-resolved transcript path, including custom session IDs. Start fresh with handoff preserves Pi's normal appended instructions and adds the brief as background on the first user turn. These terminal actions are local only and require Pi 0.99.1 or 0.99.2; SDK sessions reopen through the CLI rather than reconstructing their host. See [Troubleshooting](docs/troubleshooting.md#pi-sessions-and-runtime-status) for setup and accounting limits.

### Optional Linux session monitoring

In **Settings → Machines**, use **Add remote host** with an alias from your
system's existing OpenSSH configuration or a simple `user@host` destination.
coSlash checks SSH and SFTP; if native SSH authentication or host-key
confirmation is needed, choose **Authenticate in Terminal** and setup resumes
when it succeeds. coSlash then offers to install and verify the matching Linux
collector. The helper lives in the SSH user's private `~/.coslash/helpers`
directory, has no root or network access, and reads only supported agent paths.
Future coSlash updates replace a helper that it previously installed and
verified; first-time setup always requires this action.

coSlash uses the system `ssh` client and may reuse a control socket under
`~/.coslash/ssh`; it never edits your SSH config. SFTP remains the visible
fallback if a helper cannot run. An offline host is checked promptly and retried
in the background; use **Retry setup** for an immediate attempt. **Remove**
stops monitoring locally even when the host is offline and leaves its helper on
the remote machine.

Remote sessions support **Resume** and **Start fresh with handoff** while their
SSH host is connected. Cards, transcript-derived facts, costs, tokens,
file-edit summaries, and **Copy handoff** are also available. Complete cached
Claude and Codex sessions use the same inspector and ordered file-diff view as local
sessions, including after restart or while the SSH host is offline. A complete
cached Codex SSH revision—including its recorded file-change bodies—can also be
reviewed in full and explicitly uploaded when the paired Hub advertises the v2
full-session contract. Claude exact revisions are available for inspection but
remain outside that legacy sharing path; other remote sessions retain their
bounded summary view. Synthesis and Commands remain local-only.
A remote session can show recent transcript activity while process liveness is
unknown; coSlash labels those facts separately.

### Remote Hub MCP for local agents

When your Hub has enabled its remote MCP service and your account has **Agent
knowledge** enabled, configure an installed agent with `coslash mcp setup
claude|codex|cursor|opencode`. Choose one agent name per command. Then run
`coslash mcp login <agent>` and approve its `mcp:read` and `mcp:ask` access in
the coSlash browser. The endpoint is `https://mcp.hub.coslash.io/mcp`; use
`--url <https://host/mcp>` on `setup` only for another deployment. Pairing a
Local device does not authorize MCP.
OpenCode setup preserves an existing `opencode.jsonc`, including comments and
other servers.

The agent stores its own OAuth credential. To switch Hub accounts for Claude
Code, Codex, or OpenCode, run `coslash mcp switch <agent>`, change the account
in the coSlash browser, then run `coslash mcp login <agent>`. For Cursor,
disconnect coSlash in Cursor's MCP settings first, change the browser account,
and run `coslash mcp login cursor`; Cursor's CLI does not expose MCP credential
logout. Existing tokens also stop working when the Hub revokes access or turns
off Agent knowledge. Ask the agent to use a `coslash.*` tool to confirm that
the intended account can read the expected sessions.

## What you get

### One board for every agent

Claude Code, Codex, Cursor, and OpenCode sessions land in the same place, whether they came from a desktop app or a CLI. **List view** gives you a compact, title-first row per session with vendor, activity, machine, resume readiness, update time, and cost aligned for comparison; **board view** groups sessions by repository and branch, with a column per state, so a repo with four parallel branches reads as four rows instead of a scroll.

Search local sessions by title, repo, branch, agent, prompt, recap, summary, goal, or synthesis. Remote sessions remain searchable by saved name, repo, branch, and agent. Filter by state, machine, vendor, and folder, and narrow to a time window (today, this week, 7 days, 30 days, all time). Sort by recency, title, or estimated cost. The list refreshes itself every minute, so statuses and "3 min ago" stay honest without a reload.

### Cursor support

coSlash reads local Cursor IDE and Cursor CLI (`agent`) sessions. Cursor SDK sessions and remote Cursor collection are not supported.

**Resume** restores a Cursor CLI session exactly. For a Cursor IDE session, **Open Cursor** reopens its working directory but cannot restore a specific chat. **Start fresh with handoff** can launch Cursor CLI and send the brief with a Review or custom request. Cursor IDE exposes current context occupancy, not cumulative token usage; Cursor CLI does not persist reliable token or compaction data.

![Switching from list view to board view, then searching to filter sessions to one repository](docs/media/list-and-board.gif)

### States that tell you where to look

Sessions use four evidenced states, plus Unknown when runtime evidence is missing or unverifiable. Pi without the managed integration stays Unknown.

- **Active** — the agent is working right now.
- **Waiting** — it stopped and needs an answer from you.
- **Idle** — the session is live but nothing is happening.
- **Inactive** — no live process; this is history you can still mine.

![Header strip showing session counts by vendor, estimated cost at list API prices, and Active / Waiting badges](docs/media/attention-header.png)

### An inspector that saves you from reading the transcript

Open any session and you get a reconstruction instead of a log:

- **Debrief** — the goal, the outcome, and up to five key decisions. A badge tells you where the goal came from: *declared* by you, *inferred* from the session, or the raw *first prompt* as a floor.
- **Timeline** — first prompt, your turns, questions the agent asked, todo updates, recaps, compactions, and subagent spawns, each stamped with its turn. Click a category chip to show or hide that kind of event.
- **Artifacts** — files changed with per-file `+/−` and edit counts, commits, PRs, open and completed todos, and every shell command the session ran.
- **Header facts** — model (and any model it switched from), turns, tool uses, errors, runtime, token breakdown, and whether the run was interactive or an autonomous SDK/exec run.

![Toggling timeline category chips to show or hide questions, todos, and other event types](docs/media/inspector.gif)

### Resume readiness, before you commit to resuming

Picking a session back up is not always the cheap option. Before you decide, coSlash shows five things side by side:

- **Context used** — how full the window is, color-coded as it approaches the ceiling.
- **Compactions** — how many times this session has already been squeezed.
- **Branch** — commits ahead of and behind the base branch.
- **Working tree** — how long since anything was edited.
- **Prompt cache** — warm or cold, with the 5-minute and 1-hour windows marked.

A session that's 90% full, compacted twice, and 40 commits behind `main` is telling you to start fresh. One that's warm and 30% full is telling you to just resume.

![Five-cell resume readiness strip: context used 85% in red, zero compactions, branch 7 ahead of main, working tree, and a cold prompt cache](docs/media/readiness.png)

### Three ways back in

- **Resume** reopens the exact session in its own CLI, in its working directory, in your terminal of choice, with its full context intact.
- **Start fresh with handoff** offers Fresh handoff to open the original agent with the generated notes, ready for your next message. For Review or a custom request, choose an installed destination agent on the source host; coSlash sends the brief and task and tracks the new session and result.
- **Copy handoff** puts the same brief on your clipboard for a PR description, a standup, a ticket, or another machine entirely.

Terminal launches use Windows Terminal with Windows PowerShell when Windows
Terminal is available, otherwise standalone Windows PowerShell. On macOS they
use Apple Terminal or iTerm2, as configured in Settings.
Interactive handoffs on macOS and Linux require the system `expect` command.

![Clicking Start fresh with handoff opens a new Claude Code terminal with the session brief loaded](docs/media/handoff.gif)

### Subagents, not just sessions

Subagents appear on a rail under the parent that spawned them, with their model, status, tokens, and cost. Open one to see the task it was handed, the commands it ran, and the result it returned to its parent — the part that usually disappears into a single collapsed line in the transcript.

![Subagent dialog showing the task, steps, and result returned to the parent session](docs/media/subagents.png)

### Tokens and cost you can actually audit

Per-model token breakdowns including cache reads and writes, estimated cost at list API prices, and totals rolled up per branch, per repo, and across the whole window. Models with no verified price are excluded from the total and flagged rather than guessed at, so the number is never quietly wrong. OpenCode sessions report their recorded cost instead of an estimate.

![Board rollup of token and cost totals per repository and branch](docs/media/cost.png)

### Optional AI synthesis

Debriefs work without any model: goals, outcomes, timelines, and artifacts are derived deterministically from the transcript. Turning on synthesis sharpens them.

When enabled, coSlash passes no more than 12 KB of derived facts to a local agent CLI. Facts can include goals, digest entries, todos, filenames, commits, and statistics. Supported CLIs are Claude Code, Codex, OpenCode, Cursor, and Pi (macOS only). Each CLI uses its existing account. Results are cached under `~/.coslash`. Only substantial sessions qualify, so short runs do not use a model.

For OpenCode, the model list includes *OpenCode default for a new run* and, when the installed CLI supports listing them, free OpenCode Zen models. The default option passes no model. OpenCode v2 selects its current catalog default in a fresh, isolated process; v1 may instead use a model from the user's configuration. Either may differ from the model shown in an existing OpenCode session. If the resolved model is paid, it will bill your account per debrief.

On macOS, Pi synthesis is verified with Pi 0.99.2. The default option uses Pi's configured provider and model for a new run, with your existing authentication and provider environment. To pin a model, set `synthesis.backend` to `pi-cli` and `synthesis.model` to a provider-qualified ID such as `amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0` in `settings.json`. Pi runs without tools, extensions, skills, prompt templates, context files, or session persistence. Provider access errors and expired credentials appear as synthesis failures in the inspector; refresh credentials (for example, `aws sso login --profile <profile>`) and retry synthesis. The resolved model may consume paid account usage.

It is **off until you explicitly enable and save it**.

![Settings dialog with AI synthesis enabled, OpenCode selected, and What synthesis sends expanded](docs/media/settings-synthesis.png)

## Settings and data

Settings are stored machine-wide in `~/.coslash/settings.json`. Use the top-right
theme controls for light or dark mode, and **Settings** for the synthesis backend
and model, launch terminal (Windows Terminal with Windows PowerShell when
available, otherwise standalone Windows PowerShell; or Apple Terminal or iTerm2
on macOS), and optional SSH alias or simple `user@host` destination.
See [`settings.schema.json`](settings.schema.json) for the file format.

The dialog offers a short model list per backend, but the model is not restricted to it. Editing `settings.json` directly accepts any model the selected CLI can actually reach — including one served through an API proxy such as `ANTHROPIC_BASE_URL`, or a third-party provider — so long as that CLI is set up to resolve it.

Transcripts are read-only; coSlash never modifies local or remote agent data. Cached summaries, temporary local handoffs, and normalized remote facts live under `~/.coslash`. Raw remote transcript bytes are not persisted. The server is loopback-only and protects every API request with an access token minted at start.

Read [Data and privacy](docs/data-and-privacy.md) before pointing coSlash at sensitive transcripts.

## Command reference

| Command | Effect |
| --- | --- |
| `coslash` | Start the server and open the app. |
| `coslash --port N` | Use another loopback port. |
| `coslash --no-open` | Do not open the browser. |
| `coslash --version` | Print the version. |
| `coslash sessions [query] --json` | List local sessions as JSON, optionally filtering by title, repository, branch, or agent. A query that is an exact session ID or `<agent>:<session>` selector returns only that session without a full list. Requires the app to be running. |
| `coslash handoff <agent>:<session>` | Print canonical handoff Markdown for the selector returned by `coslash sessions`. Requires the app to be running. |
| `coslash send <agent>:<session> --to claude\|codex\|opencode\|cursor\|pi [message]` | Start the target agent in the selected session working directory with its handoff and optional initial task. Cursor copies the handoff and task to the clipboard; paste them into the new Cursor CLI session. Requires the app to be running. |
| `coslash review <agent>:<session> --with claude\|codex\|opencode\|cursor` | Start a review of the selected local session with the selected installed agent. Requires the app to be running. |
| `coslash doctor` | Check session sources, agent CLIs, and local storage. |
| `coslash doctor --json` | Print the same diagnostics as JSON — a shareable report. |

Successful data commands write their result to standard output. Failures write `Error: <message>` to standard error and exit non-zero; run `coslash doctor --json` for diagnostics. Authentication for the running app is discovered locally and is never printed.

### Agent skills

Install the five coSlash skills for Claude Code:

```sh
claude plugin marketplace add centauri-ai/coslash#stable
claude plugin install coslash@centauri-ai
```

Or install them for Codex:

```sh
codex plugin marketplace add centauri-ai/coslash --ref stable
codex plugin add coslash@centauri-ai
```

For Cursor CLI and OpenCode, install the same skills from this repository:

```sh
npx skills@latest add centauri-ai/coslash -g -a cursor -a opencode --skill '*' -y
```

Use `npx skills@latest ls -g -a cursor -a opencode` to list them, `npx skills@latest update -g` to update them, or `npx skills@latest remove -g` to remove selected skills. The `coslash` executable must also be installed on `PATH`.

The `sessions`, `handoff`, `send`, and `review` skills require the coSlash app to be running. `doctor` works while the app is stopped.

The `stable` branch moves with each stable release, so the skills match the coSlash version that Homebrew and the install script ship. Update coSlash and the skills together. For Claude Code, run `claude plugin marketplace update centauri-ai`, then `claude plugin update coslash@centauri-ai`. For Codex, run `codex plugin marketplace upgrade`. If you added the marketplace without `stable`, remove it and add it again with the commands above.

Codex runs skill commands with its own environment. A `shell_environment_policy` in `~/.codex/config.toml`, for example `inherit = "core"`, can drop a custom `COSLASH_HOME`. The skills then use `~/.coslash`: `doctor` reports that home without an error, and the other skills do not find an app that runs with the custom home, so they report `coSlash app is not running` or start a second app for `~/.coslash`. To keep a custom home, set it under `[shell_environment_policy.set]` as `COSLASH_HOME = "<path>"`. `coslash doctor --json` reports the `version` and `storage.home` that the skills use.

## Development

Building from source requires **Go 1.26+** and **Node 24+** (see `collector/go.mod` and `frontend/.nvmrc`). End users do not need these tools — use [Install](#install) above for a prebuilt binary.

The Go collector and local API live in `collector/`; the React, Vite, and Tailwind UI lives in `frontend/`.

```sh
cd collector
make release
./bin/coslash
```

If `make release` reports a missing or unsupported Go or Node version, install the toolchain first or switch to the curl/Homebrew install path.

### Connect to a Hub from a source branch

For local Hub testing, `collector/scripts/install-branch.sh` and
`collector/scripts/install-branch.ps1` build the selected source branch directly;
they do not download a release or require a release tag. Both require Git, Go
1.26+, Node 24+, and npm. The macOS script also requires `make`.

```sh
curl -fsSL https://raw.githubusercontent.com/centauri-ai/coslash/hlu/hub-support/collector/scripts/install-branch.sh | bash -s -- --branch hlu/hub-support --connect CODE --hub http://localhost:8080
```

```powershell
$env:COSLASH_SOURCE_BRANCH='hlu/hub-support'; $env:COSLASH_CONNECT='CODE'; $env:COSLASH_HUB='http://localhost:8080'; irm https://raw.githubusercontent.com/centauri-ai/coslash/hlu/hub-support/collector/scripts/install-branch.ps1 | iex
```

Replace `CODE` and the Hub origin with the values shown in Hub. To build another
branch, change `--branch` on macOS or `COSLASH_SOURCE_BRANCH` on Windows (for
example, `main`). A branch without `coslash connect` stops before replacing an
installed binary. Branch builds report version `0.0.0`; set the local Hub's
minimum and recommended Local versions to `0.0.0` while using them. Each source
branch gets its own install directory and `COSLASH_HOME`, so a dev build does
not replace the regular coSlash install or its data.

Release packaging uses `make dist` on macOS to build both macOS archives and
the native Windows executable from one staged frontend/helper build. Windows
contributors can use `go build` for local collector development; end users
should download the prebuilt executable above.

See [Contributing](CONTRIBUTING.md) for the development loop and checks.

## Project status

coSlash is an early preview built by its maintainers. The source is public for transparency, local builds, and product feedback. Bug reports are welcome through [Issues](https://github.com/centauri-ai/coslash/issues) — please redact transcripts, prompts, and paths first. Unsolicited pull requests are not accepted; see [Contributing](CONTRIBUTING.md).

The [current product baseline](docs/current-product-baseline.md) inventories the
implemented local capabilities, API, privacy contracts, storage, setup, and
explicitly deferred work for the next product iteration.

## Help

- [Troubleshooting](docs/troubleshooting.md)
- [Data and privacy](docs/data-and-privacy.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## License

[MIT](LICENSE)
