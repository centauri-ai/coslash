# coSlash

The attention layer for coding agents. coSlash watches your local Claude Code and Codex sessions, reconstructs what each one was doing — goal, decisions, files, commits, next step — and shows which ones need you. Resume any session in its terminal, or copy a handoff brief and pick it up cold.

## Quick start

**Prerequisites:** Go 1.26+ and Node 22+ (Node is only needed to build).

```sh
cd collector
make release
./bin/coslash
```

That builds the frontend, embeds it, and starts one executable at [http://127.0.0.1:8787](http://127.0.0.1:8787). No Node, Vite, or second process at runtime — the browser should open on its own.

| Flag | Effect |
| --- | --- |
| `--port N` | serve on port N (default `8787`) |
| `--no-open` | do not open the browser |
| `--version` | print the version and exit |

## Develop

In two terminals:

```sh
cd collector && make run
cd frontend && npm run dev
```

The UI is at [http://localhost:5173](http://localhost:5173): Vite serves it and proxies `/api` to the Go server on port `8787`. `make run` embeds no frontend and opens no browser, so port `8787` serves the API alone.
