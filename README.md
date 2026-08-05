# coSlash

[![CI](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml/badge.svg)](https://github.com/centauri-ai/coslash/actions/workflows/ci.yml)

The attention layer for coding agents. coSlash watches your local Claude Code and Codex sessions, reconstructs what each one was doing — goal, decisions, files, commits, next step — and shows which ones need you. Resume any session in its terminal, or copy a handoff brief and pick it up cold.

## Install

coSlash ships prebuilt binaries for macOS on Apple silicon and Intel.

```sh
brew install centauri-ai/tap/coslash
coslash
```

`brew upgrade coslash` moves to a newer release; `brew uninstall coslash` removes it.

Or download a release archive directly, substituting the version and architecture
you want:

```sh
VERSION=v0.0.1
ARCH=arm64  # or amd64 on Intel
ASSET="coslash_${VERSION}_darwin_${ARCH}.tar.gz"
BASE_URL="https://github.com/centauri-ai/coslash/releases/download/${VERSION}"
curl -fLO "${BASE_URL}/${ASSET}"
curl -fLO "${BASE_URL}/checksums.txt"
grep -F "  ${ASSET}" checksums.txt | shasum -a 256 -c -
tar -xzf "${ASSET}"
"${ASSET%.tar.gz}/coslash"
```

The binaries are unsigned, so macOS may warn about an archive you downloaded
through a browser — the Homebrew install above is the supported path and is not
affected.

Running `coslash` starts one executable at
[http://127.0.0.1:8787](http://127.0.0.1:8787) and opens a browser. There is no
Node, Vite, or second process at runtime.

| Flag | Effect |
| --- | --- |
| `--port N` | serve on port N (default `8787`) |
| `--no-open` | do not open the browser |
| `--version` | print the version and exit |

## Build from source

**Prerequisites:** Go 1.26+ and Node 24+ (Node is only needed to build).

```sh
cd collector
make release
./bin/coslash
```

That builds the frontend, embeds it into the binary, and leaves it at
`collector/bin/coslash`. `make dist` instead cross-compiles both macOS
architectures into `collector/dist/` with a `checksums.txt`, which is what the
release workflow publishes.

## Develop

In two terminals:

```sh
cd collector && make run
cd frontend && npm run dev
```

The UI is at [http://localhost:5173](http://localhost:5173): Vite serves it and proxies `/api` to the Go server on port `8787`. `make run` embeds no frontend and opens no browser, so port `8787` serves the API alone.

## Global settings

coSlash stores machine-wide preferences in `~/.coslash/settings.json`. The file is optional: synthesis is off and Apple Terminal is used until you inspect a synthesis-eligible session and save a choice in the first-run Settings dialog. That dialog initially selects synthesis On, but synthesis remains off until you save. coSlash writes the directory with mode `0700` and the file with mode `0600`.

Synthesis sends derived session facts through your selected CLI using its existing authentication and may consume account usage. Results are cached under `~/.coslash`; source transcripts are never modified. Do not put API keys, tokens, or other credentials in `settings.json`.

Version 1 supports:

- Synthesis backends: Claude Code (`claude-cli`) and Codex (`codex_exec`).
- Claude models: `claude-haiku-4-5`, `claude-sonnet-5`, and `claude-opus-5`.
- Codex models: `gpt-5.6-luna`, `gpt-5.6-terra`, and `gpt-5.6-sol`.
- Terminal apps: Apple Terminal (`terminal`) and iTerm2 (`iterm2`).

Use the top-right Settings dialog to apply synthesis and terminal changes immediately. The focused first-run dialog shown from an eligible session contains only synthesis consent and model choices. If you edit the JSON file directly, restart coSlash. The document must match [`settings.schema.json`](settings.schema.json); invalid or unsupported settings disable synthesis and block terminal launches until repaired rather than silently selecting another app.
