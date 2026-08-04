# coSlash

[![CI](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml/badge.svg)](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.26.5-00ADD8?logo=go)](collector/go.mod)
[![Node](https://img.shields.io/badge/Node-24.4.1-339933?logo=node.js&logoColor=white)](frontend/.nvmrc)
[![License](https://img.shields.io/github/license/centauri-ai/coslash)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/centauri-ai/coslash)](https://github.com/centauri-ai/coslash/releases)

The attention layer for coding agents. coSlash watches your local Claude Code and Codex sessions, reconstructs what each one was doing—goal, decisions, files, commits, next step—and shows which ones need you. Resume any session in its terminal, or copy a handoff brief and pick it up cold.

**Early preview · macOS only**

## Install

```sh
brew install centauri-ai/tap/coslash
coslash
```

Use `brew upgrade coslash` to update and `brew uninstall coslash` to remove the binary. Uninstalling keeps coSlash data in `~/.coslash`; delete that directory separately if you no longer need it.

<details>
<summary>Install a release archive manually</summary>

```sh
VERSION=v0.0.1
ARCH=arm64  # amd64 on Intel
ASSET="coslash_${VERSION}_darwin_${ARCH}.tar.gz"
BASE_URL="https://github.com/centauri-ai/coslash/releases/download/${VERSION}"
curl -fLO "${BASE_URL}/${ASSET}"
curl -fLO "${BASE_URL}/checksums.txt"
grep -F "  ${ASSET}" checksums.txt | shasum -a 256 -c -
tar -xzf "${ASSET}"
"${ASSET%.tar.gz}/coslash"
```

Release binaries are unsigned. macOS may warn about archives downloaded through a browser; the supported Homebrew install is unaffected.

</details>

`coslash` serves <http://127.0.0.1:8787> and opens a browser with a new access token. Use the opened URL; old links expire when the server restarts.

| Command | Effect |
| --- | --- |
| `coslash --port N` | Use another loopback port. |
| `coslash --no-open` | Do not open the browser. |
| `coslash --version` | Print the version. |
| `coslash doctor` | Check session sources, agent CLIs, and local storage. |
| `coslash doctor --json` | Print the same diagnostics as JSON. |

## What you get

- Attention states, search, filters, sorting, and board or list views across Claude Code and Codex sessions.
- Goals, outcomes, decisions, todos, changes, commits, context use, and estimated cost in one inspector.
- Actions to resume a session or start fresh with a copyable handoff.

## Settings and data

Settings are available in the top-right menu and stored in `~/.coslash/settings.json`. Synthesis is off until you save a backend and model; light and dark themes, Apple Terminal, and iTerm2 are supported. See [`settings.schema.json`](settings.schema.json) for the file format.

Transcripts are read-only. When enabled, synthesis sends bounded session facts through your selected local Claude Code or Codex CLI and may consume account usage. Cached summaries and temporary handoffs live under `~/.coslash`.

The server is loopback-only and protects API requests with a per-start access token. Read [Data and privacy](docs/data-and-privacy.md) before using coSlash with sensitive transcripts.

## Develop

Requires Go 1.26+ and Node 24+.

```sh
cd collector
make release
./bin/coslash
```

See [Contributing](CONTRIBUTING.md) for the development loop and checks.

## Help

- [Troubleshooting](docs/troubleshooting.md)
- [Data and privacy](docs/data-and-privacy.md)
- [Contributing](CONTRIBUTING.md)

## License

[MIT](LICENSE)
