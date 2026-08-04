# Contributing

The Go collector and local API live in `collector/`; the React, Vite, and Tailwind UI lives in `frontend/`.

## Develop

Requires Go 1.26+ and Node 24+.

Build the embedded production binary:

```sh
cd collector
make release
./bin/coslash
```

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
