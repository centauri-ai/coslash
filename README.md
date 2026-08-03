# coSlash
The attention layer for coding agents. coSlash watches your local Claude Code and Codex sessions, reconstructs what each one was doing — goal, decisions, files, commits, next step — and shows which ones need you. Resume any session in its terminal, or copy a handoff brief and pick it up cold.

## Build

Requires Go 1.26+ and Node 22+. Node builds the frontend; the executable it produces needs neither.

```sh
cd collector
make release
```

That builds the frontend, stages it into the Go module, embeds it, and writes `collector/bin/coslash` — one executable that serves the app and the API from the same loopback origin, with no second process.

## Run

```sh
./bin/coslash
```

Serves `http://127.0.0.1:8787` and opens it in your default browser.

| Flag | Effect |
| --- | --- |
| `--port N` | serve on port N instead of 8787 |
| `--no-open` | leave the browser alone |
| `--version` | print the version and exit |

## Develop

Run `make run` in `collector/` and `npm run dev` in `frontend/`; Vite serves the frontend and proxies `/api` to the Go server. `make build` skips the frontend, so that binary serves the API only and says so at `/`.
