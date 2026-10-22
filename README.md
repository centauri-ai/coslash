[![coSlash](frontend/public/brand/coslash-logo.svg)](https://coslash.io)

**The attention layer for coding agents. Run more agents, lose less context.**

[coslash.io](https://coslash.io)

[![CI](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml/badge.svg)](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml)
[![Go 1.26.5](https://img.shields.io/badge/Go-1.26.5-00ADD8?logo=go)](collector/go.mod)
[![Node 24.4.1](https://img.shields.io/badge/Node-24.4.1-339933?logo=node.js&logoColor=white)](frontend/.nvmrc)
[![License](https://img.shields.io/github/license/centauri-ai/coslash)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/centauri-ai/coslash)](https://github.com/centauri-ai/coslash/releases)

Three agents are running. One finished twenty minutes ago, one is waiting on a question you never saw, and one has been quietly compacting its context on a branch whose name you've forgotten. coSlash reads their transcripts straight off your disk and turns them into a single board: what each session set out to do, what it decided, what it changed, and which ones need you next.

Then it gets you back in: resume a session in its own terminal with full context, or copy a handoff brief and pick it up cold somewhere else.

Local collection runs on your machine. Session data leaves it through Hub V4
sync when the paired device and Hub policy allow uploads; Local Settings can
pause sync.
A separately authorized remote Hub MCP connection lets your agent query Hub
sessions; synthesis uses the selected agent CLI. See [data and
privacy](docs/data-and-privacy.md).

**Early preview · macOS, Linux (amd64, arm64) and Windows 11 amd64**

See [supported agents and platforms](docs/install.md#supported-agents-and-platforms) for desktop, CLI, SSH, and resume support.

Local collection needs no Hub account or telemetry.

## Install

See the [installation guide](docs/install.md) for Hub setup, standalone installations, updates, and uninstall instructions.

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

On a headless host, `coslash hub login --hub <MCP URL>` connects the Local CLI
with device-code authorization. Copy the MCP URL from Hub Settings → Coding
agents. It requests `hub:read` by default. Repeat
`--scope SCOPE` to request additional access, for example
`coslash hub login --scope hub:write --scope hub:share`; review the requested
scopes in Hub before the CLI stores its separate Hub credential. Search session
text with `coslash hub search "retry backoff"`; use `coslash hub call` for other
tools. On macOS, the CLI stores its encryption key in Keychain and the encrypted
credential in a private `0600` file under `$COSLASH_HOME/hub-credentials`.

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

Sessions from every supported agent land in the same place, whether they came from a desktop app or a CLI. **Table view** gives you a compact, title-first row per session with vendor, activity, machine, resume readiness, update time, and cost aligned for comparison. **Board view** groups sessions by repository and branch, with a column per state, so a repo with four parallel branches reads as four rows instead of a scroll.

Search by title, repo, branch, agent, prompt, recap, summary, goal, or synthesis. Filter by state, machine, vendor, folder, and time window, and sort by recency, title, or estimated cost. The list refreshes itself every minute, so statuses and "3 min ago" stay honest without a reload.

![Switching from table view to board view, then searching to filter sessions to one repository](docs/media/list-and-board.gif)

### States that tell you where to look

- **Active**: the agent is working right now.
- **Waiting**: it stopped and needs an answer from you.
- **Idle**: the session is live but nothing is happening.
- **Inactive**: no live process. This is history you can still mine.
- **Unknown**: coSlash has no reliable runtime evidence.

![Header strip showing session counts by vendor, estimated cost at list API prices, and Active / Waiting badges](docs/media/attention-header.png)

### An inspector that saves you from reading the transcript

Open any session and you get a reconstruction instead of a log:

- **Debrief**: the goal, the outcome, and up to five key decisions. A badge tells you where the goal came from: *declared* by you, *inferred* from the session, or the raw *first prompt* as a floor.
- **Timeline**: first prompt, your turns, questions the agent asked, todo updates, recaps, compactions, and subagent spawns, each stamped with its turn. Click a category chip to show or hide that kind of event.
- **Artifacts**: files changed with per-file `+/−` and edit counts, commits, PRs, open and completed todos, and every shell command the session ran.
- **Header facts**: model (and any model it switched from), turns, tool uses, errors, runtime, token breakdown, and whether the run was interactive or an autonomous SDK/exec run.

![Toggling timeline category chips to show or hide questions, todos, and other event types](docs/media/inspector.gif)

### Resume readiness, before you commit to resuming

Picking a session back up is not always the cheap option. Before you decide, coSlash shows five things side by side:

- **Context used**: how full the window is, color-coded as it approaches the ceiling.
- **Compactions**: how many times this session has already been squeezed.
- **Branch**: commits ahead of and behind the base branch.
- **Working tree**: how long since anything was edited.
- **Prompt cache**: warm or cold, with the 5-minute and 1-hour windows marked.

A session that's 90% full, compacted twice, and 40 commits behind `main` is telling you to start fresh. One that's warm and 30% full is telling you to just resume.

![Five-cell resume readiness strip: context used 85% in red, zero compactions, branch 7 ahead of main, working tree, and a cold prompt cache](docs/media/readiness.png)

### Three ways back in

- **Resume** reopens the exact session in its own CLI, in its working directory, in your terminal of choice, with its full context intact.
- **Start fresh with handoff** opens the original agent with the generated notes, ready for your next message. For a review or a custom request, choose an installed destination agent. coSlash sends the brief and the task, then tracks the new session and its result.
- **Copy handoff** puts the same brief on your clipboard for a PR description, a standup, a ticket, or another machine entirely.

Interactive handoffs on macOS and Linux need the system `expect` command. On macOS and Windows, Grok Build is also a handoff and review destination after you log in to it. On Windows, a Grok review can read files outside the worktree. Read [Data and privacy](docs/data-and-privacy.md) before you use it.

![Clicking Start fresh with handoff opens a new Claude Code terminal with the session brief loaded](docs/media/handoff.gif)

### Subagents, not just sessions

Subagents appear on a rail under the parent that spawned them, with their model, status, tokens, and cost. Open one to see the task it was handed, the commands it ran, and the result it returned to its parent: the part that usually disappears into a single collapsed line in the transcript.

![Subagent dialog showing the task, steps, and result returned to the parent session](docs/media/subagents.png)

### Tokens and cost you can actually audit

Per-model token breakdowns including cache reads and writes, estimated cost at list API prices, and totals rolled up per branch, per repo, and across the whole window. Models with no verified price are excluded from the total and flagged rather than guessed at, so the number is never quietly wrong. OpenCode sessions report their recorded cost instead of an estimate.

**Insights** shows one month at a time: the agents and models in use, the top repositories, and sessions per day. Its cost panel separates the coding cost of sessions last active in that month from the cost of synthesis runs that started in that month, and adds the known amounts together. In the inspector, **Synthesis rounds** lists each synthesis run for that session with its model, tokens, and cost. Synthesis cost history stays under `COSLASH_HOME` (default `~/.coslash`) and covers local runs only.

![Board rollup of token and cost totals per repository and branch](docs/media/cost.png)

### Optional AI synthesis

Debriefs work without any model: goals, outcomes, timelines, and artifacts are derived deterministically from the transcript. Turning on synthesis sharpens them.

When enabled, coSlash passes no more than 12 KB of derived facts to a local agent CLI: Claude Code, Codex, OpenCode, Cursor, Pi, or Grok Build. Each CLI uses its existing account, and a paid model bills that account. Only substantial sessions qualify, so short runs do not use a model. Results are cached under `~/.coslash`. For the model options of each backend, see [Synthesis backends](docs/synthesis.md).

It is **off until you explicitly enable and save it**.

![Settings dialog with AI synthesis enabled, OpenCode selected, and What synthesis sends expanded](docs/media/settings-synthesis.png)

## Settings and data

Settings are stored machine-wide in `~/.coslash/settings.json` ([schema](settings.schema.json)). Use the top-right theme controls for light or dark mode. Use **Settings** for the synthesis backend and model, the launch terminal, and remote hosts. On macOS, the terminal is Apple Terminal or iTerm2. On Windows, coSlash uses Windows Terminal when it is available, otherwise Windows PowerShell.

Transcripts are read-only: coSlash never modifies local or remote agent data. Cached summaries, temporary handoffs, and normalized remote facts live under `~/.coslash`. Raw remote transcripts are not stored. The server listens only on loopback and protects every API request with an access token that it creates at start.

Read [Data and privacy](docs/data-and-privacy.md) before you point coSlash at sensitive transcripts.

## Command reference

| Command | Effect |
| --- | --- |
| `coslash` | Start the server and open the app. |
| `coslash --port N` | Use another loopback port. |
| `coslash --no-open` | Do not open the browser. |
| `coslash --version` | Print the version. |
| `coslash --help` / `coslash help` | List the CLI commands and their purpose. |
| `coslash version` | Print the version. |
| `coslash ui-url` | Print the authenticated web UI URL of the running app, also after `--background` or `--no-open` startup. Use the same `COSLASH_HOME` as the running app. |
| `coslash sessions [query] --json` | List local sessions as JSON. The query filters by title, repository, branch, or agent. An exact session ID or `<agent>:<session>` selector returns only that session. |
| `coslash handoff <agent>:<session>` | Print the handoff Markdown for a selector from `coslash sessions`. |
| `coslash send <agent>:<session> --to claude\|codex\|opencode\|cursor\|pi\|grok [message]` | Start the target agent in the working directory of the session, with the handoff and an optional first task. For Cursor, coSlash copies both to the clipboard. Paste them into the new Cursor CLI session. |
| `coslash review <agent>:<session> --with claude\|codex\|opencode\|cursor\|pi\|grok` | Start a review of a local session with an installed agent. |
| `coslash doctor` / `coslash status` | Check session sources, agent CLIs, local storage, Hub sync health, and the `coslash:` link handler. |
| `coslash doctor --json` / `coslash status --json` | Print the same diagnostics as JSON, including Hub origin, pairing, last check-in, content-free queue counts, and link-handler state. |
| `coslash doctor --fix-links [--yes]` | On macOS, list stale URL handlers; add `--yes` to unregister the listed bundles. |

`sessions`, `handoff`, `send`, and `review` need the app to be running. Results go to standard output. Failures write `Error: <message>` to standard error and exit non-zero. The commands find the access token of the running app locally and never print it. Only `coslash ui-url` prints the token, as part of the URL on standard output. It does not write the URL to the app logs.

### Agent skills

Install the five coSlash skills for Claude Code:

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

The skills call the `coslash` executable, so it must be on `PATH`. The `sessions`, `handoff`, `send`, and `review` skills start the app when it is stopped. `doctor` works without starting it. The `stable` branch moves with each stable release. When you update coSlash, update the skills too: `claude plugin marketplace update centauri-ai` then `claude plugin update coslash@centauri-ai`, `codex plugin marketplace upgrade`, `grok plugin update coslash`, or `npx skills@latest update -g`. On Windows, if `npx skills` fails on a symlink, rerun the same command with `--copy`.

If you use a custom `COSLASH_HOME` with Codex, a `shell_environment_policy` in `~/.codex/config.toml` can drop it. Set it under `[shell_environment_policy.set]` as `COSLASH_HOME = "<path>"`. `coslash doctor --json` shows the `storage.home` that the skills use.

## Development

Building from source needs **Go 1.26+** and **Node 24+** (see `collector/go.mod` and `frontend/.nvmrc`). The Go collector and local API live in `collector/`. The React, Vite, and Tailwind UI lives in `frontend/`.

```sh
cd collector
make release
./bin/coslash
```

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

See [Contributing](CONTRIBUTING.md) for the development loop and checks.

## Project status

coSlash is an early preview built by its maintainers. The source is public for transparency, local builds, and product feedback. Bug reports are welcome through [Issues](https://github.com/centauri-ai/coslash/issues). Redact transcripts, prompts, and paths first. Unsolicited pull requests are not accepted. See [Contributing](CONTRIBUTING.md).

## Help

- [Troubleshooting](docs/troubleshooting.md)
- [Data and privacy](docs/data-and-privacy.md)
- [Current product baseline](docs/current-product-baseline.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## License

[MIT](LICENSE)
