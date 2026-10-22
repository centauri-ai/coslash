[![coSlash](frontend/public/brand/coslash-logo.svg)](https://coslash.io)

**Run more agents, lose less context.**

[![CI](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml/badge.svg)](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml)
[![Go 1.26.5](https://img.shields.io/badge/Go-1.26.5-00ADD8?logo=go)](collector/go.mod)
[![Node 24.4.1](https://img.shields.io/badge/Node-24.4.1-339933?logo=node.js&logoColor=white)](frontend/.nvmrc)
[![Latest release](https://img.shields.io/github/v/release/centauri-ai/coslash)](https://github.com/centauri-ai/coslash/releases)
[![License](https://img.shields.io/github/license/centauri-ai/coslash)](LICENSE)

coSlash brings your coding-agent sessions into one board. See which agents need you,
catch up on what changed, and resume work with context.

![Current table and board views with sample sessions, followed by a repository search](docs/media/list-and-board.gif)

## Why coSlash?

- **Know where to look.** See which sessions are active, waiting for your answer,
  or no longer running, across agents, repositories, and branches.
- **Catch up without reading the whole transcript.** Open a session for its goal,
  decisions, changed files, subagents, and token and cost breakdowns.
- **Pick up where you left off.** Resume the original session, start fresh with a
  handoff, or send the work to another installed agent.

**Supported agents:** Claude Code, Codex / ChatGPT, Cursor, OpenCode, Pi, and Grok Build.

**Early preview:** macOS, Linux (amd64 and arm64), and Windows 11 amd64. Agent capabilities vary by platform. See the [support table](docs/install.md#supported-agents-and-platforms) for desktop, CLI, and SSH support, including resume limitations.

## Get started

Local use needs no Hub account, Go, or Node installation.

**macOS / Linux**

```sh
curl -fsSL https://coslash.io/install.sh | bash
coslash
```

**Windows**

```powershell
irm https://coslash.io/install.ps1 | iex
```

On Windows, run the startup command the installer prints.
coSlash opens its local UI in your browser.
See the [installation guide](docs/install.md) for Homebrew, headless servers, and updates.

### Connect to Hub

1. Open [Hub](https://hub.coslash.io) and choose **Devices → Add device → This computer**.
2. Run the command Hub provides. It installs coSlash Local and starts it in the background.
3. Return to Hub and approve the computer to connect it and enable personal sync.

For a Linux server, choose **A server over SSH** instead.
Run its setup command in your own SSH session. Hub never receives your SSH credentials.

coSlash needs at least one session from a supported agent.
If no sessions appear, take one turn in an agent inside a repository.
Then run the checks again, or use `coslash doctor` for diagnostics.
See [troubleshooting](docs/troubleshooting.md) for setup errors.

## Your data

Local collection needs no Hub account and uses no telemetry.
coSlash reads transcripts without changing them.
Pairing a device enables automatic personal sync under Hub policy.
Eligible session data can upload without another preview.
Pause sync in **Settings → Sync**.
Workspace sharing and remote MCP access require separate authorization.

AI synthesis is off by default. If you enable it, coSlash sends derived session facts
through your chosen agent CLI and its account. Paid models can incur charges.
See [data and privacy](docs/data-and-privacy.md) for what leaves your machine,
local storage, and removal controls.

## More

- [Installation, updates, and uninstall](docs/install.md)
- [Agent skills, CLI commands, and Hub MCP](docs/agents.md)
- [AI synthesis and model options](docs/synthesis.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Contributing and source builds](CONTRIBUTING.md)
- [Security policy](SECURITY.md) · [MIT license](LICENSE)

Bug reports are welcome through [Issues](https://github.com/centauri-ai/coslash/issues).
Redact transcripts, prompts, and private paths before posting.
Unsolicited pull requests are not accepted. See [Contributing](CONTRIBUTING.md) for the project policy.
