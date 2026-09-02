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

Building from source requires **Go 1.26+** and **Node 24+** (see `collector/go.mod` and `frontend/.nvmrc`). End users should install a prebuilt binary instead; see the [README Install section](README.md#install).

Build the embedded production binary:

```sh
cd collector
make release
./bin/coslash
```

`make release` checks Go and Node versions before building. If either is missing or unsupported, install the toolchain or use a release archive / Homebrew install.

For UI development, run the API and Vite separately:

```sh
cd collector && make run
cd frontend && npm ci && npm run dev
```

The UI runs on <http://127.0.0.1:5173>, proxies `/api` to port `8787`, and reads the current development token automatically. If that port is occupied, use the fallback URL Vite prints.

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
