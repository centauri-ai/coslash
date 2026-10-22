# Contributing

coSlash is developed primarily by its maintainers. Its source is public for
transparency, local builds, and product feedback, but unsolicited pull requests
and patches are not accepted. A maintainer may invite a contribution when a
specific change is a good fit for the product roadmap.

Bug reports are welcome through GitHub Issues. Do not include agent transcripts,
prompts, credentials, personal paths, private URLs, or other sensitive data.
Report vulnerabilities privately using the process in [SECURITY.md](SECURITY.md).

Use of this source, and any invited contribution, is governed by the repository's
[MIT License](LICENSE).

The Go collector and local API live in `collector/`; the React, Vite, and Tailwind UI lives in `frontend/`.

## Develop

Building from source requires **Go 1.26+** and **Node 24+** (see `collector/go.mod` and `frontend/.nvmrc`). For a prebuilt binary, see the [installation guide](docs/install.md).

Build the embedded production binary:

```sh
cd collector
make release
./bin/coslash
```

`make release` checks Go and Node versions before building. If either is missing or unsupported, install the toolchain or use a release archive / Homebrew install.

Release packaging runs `make dist` on macOS. It builds both macOS archives and the native Windows executable from one staged frontend and helper build. On Windows, use `go build` for local collector development.

For UI development, run the API and Vite separately:

```sh
cd collector && make run
cd frontend && npm ci && npm run dev
```

The UI runs on <http://127.0.0.1:5173>, proxies `/api` to port `8787`, and reads the current development token automatically. If that port is occupied, use the fallback URL Vite prints.

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

## Check changes

```sh
cd collector
make test
make check

cd ../frontend
npm test
npm run lint
npm run format:check
```

Run `make release && make smoke` from `collector/` when changing startup, embedded assets, or packaging. In a pull request, describe the user-visible change and list only checks that actually ran.

## Extension points

- Add agent vendors under `collector/internal/vendors/` and register them in the collector and UI.
- Add verified Claude context windows in `collector/internal/session/models.go`; Codex models normally self-report them.
- Add verified list pricing in `frontend/src/pages/coslash/lib/pricing.ts`; unknown models must remain visibly excluded.

Keep documentation coarse and tied to current behavior. Avoid copying internal constants or schemas that already have a source of truth in code.
