# Local UI QA journeys

These Playwright journeys test the coSlash Local UI against a running Local that uses a synthetic home.
No AI agent is needed to run them. The QA service runs them and wakes an agent only when one fails.

## Contract

Set these environment variables before you run the journeys.

| Variable              | Required | Meaning                                                                                                        |
| --------------------- | -------- | -------------------------------------------------------------------------------------------------------------- |
| `COSLASH_LOCAL_URL`   | yes      | Base URL of Local, for example `http://127.0.0.1:51724`. Read `baseURL` from `runtime.json` in `COSLASH_HOME`. |
| `COSLASH_LOCAL_TOKEN` | yes      | Per-run token. Read the `token` file in `COSLASH_HOME`.                                                        |
| `QA_EVIDENCE_DIR`     | no       | Folder for screenshots and traces. Default: `coslash-qa-evidence` in the system temp folder.                   |
| `QA_REPORT_FILE`      | no       | When set, Playwright writes a JSON report to this file.                                                        |

Failure rules:

- A setup problem fails with an error that starts with `harness:`. Examples: a variable is missing, Local does not respond, the token is rejected, or Local has no sessions. Fix the setup.
- A failed assertion means the product or the journey is wrong.

Authentication: the browser opens `<COSLASH_LOCAL_URL>/#t=<COSLASH_LOCAL_TOKEN>`. The UI reads the fragment, keeps the token in `sessionStorage`, removes the fragment, and sends the token as `X-Coslash-Token` on each `/api/` call. This is the same link that `coslash ui-url` prints.

Each `step()` call saves a full-page PNG to `QA_EVIDENCE_DIR/<test>/<nn>-<step>.png`. Failed tests keep a trace in `QA_EVIDENCE_DIR/playwright-output`. Never commit screenshots or traces. The evidence folder must stay outside the checkout.

The dark theme journey saves the settings of the synthetic Local once, because the theme toggle stays disabled until settings exist. It restores the light theme at the end.

## Run on a laptop

Run these commands from the repository root. Node 24 or later and Go are required.

1. Build Local with the embedded UI:

   ```sh
   make -C collector release
   ```

2. Install the Chromium browser for Playwright:

   ```sh
   cd frontend
   npx playwright install chromium
   ```

3. Create the synthetic home and start Local. This script uses `collector/bin/coslash`, writes everything to a temporary folder, and prints the variables. Leave it running.

   ```sh
   node scripts/qa-local.mjs
   ```

   The last lines look like this:

   ```text
   export COSLASH_LOCAL_URL=http://127.0.0.1:51724 COSLASH_LOCAL_TOKEN=...
   ```

4. In a second terminal, in `frontend/`, paste that `export` line and run the journeys:

   ```sh
   export COSLASH_LOCAL_URL=... COSLASH_LOCAL_TOKEN=...
   export QA_EVIDENCE_DIR="$(mktemp -d)"
   npm run qa:journeys
   ```

5. Open the PNG files in `QA_EVIDENCE_DIR`. Press Ctrl-C in the first terminal to stop Local.

To run Local by hand instead of `scripts/qa-local.mjs`, run `go run ./cmd/synthetic-home --out <home> --seed 1 --now <RFC3339> --sessions-per-agent 3` in `collector/`. Then start `collector/bin/coslash --no-open --port 0` with `HOME`, `XDG_CONFIG_HOME`, and `XDG_DATA_HOME` set to the synthetic home (`<home>/.config` and `<home>/.local/share`) and `COSLASH_HOME` set to a separate folder.

## Journeys

| File                     | Checks                                                                                |
| ------------------------ | ------------------------------------------------------------------------------------- |
| `session-list.qa.mjs`    | The list shows synthetic sessions, the agent filters exist, and the board view loads. |
| `session-search.qa.mjs`  | Search and the agent filter change the list, and clearing them restores it.           |
| `session-detail.qa.mjs`  | A session opens in a detail dialog with its content and closes with Escape.           |
| `session-layouts.qa.mjs` | The 390 px layout has no page overflow, and the dark theme list is readable.          |
