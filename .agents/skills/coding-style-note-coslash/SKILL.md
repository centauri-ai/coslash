---
name: coding-style-note-coslash
description: Code style conventions for the coslash repo — comment discipline, build-target hygiene, and docs that match real behavior. Use when writing, reviewing, or editing Go, TypeScript, Makefiles, or docs in the coslash repository.
---

# coSlash coding style

## Comments

- Comment only what the code cannot say: a TODO, a constraint the reader cannot infer, or a deliberate special case.
- Never narrate logic or restate a plan. If a comment describes what the next lines do, delete it.
- Keep package and exported doc comments to one or two lines.
- Prefer a clearer name or a small named helper over a comment that explains a block.

```go
// Bad — narrates the code below.
// Loop over sessions, read each transcript mtime, and attach cached synthesis.

// Good — records a constraint that is not visible here.
// Go cannot embed a path outside its own module, so `make release` stages it first.
```

## Build and dev targets

- A target must not inherit generated state from another target. Clear what you consume: `build`, `run`, and `clean` empty the staged embed directory so they never pick up a `release` artifact.
- Keep the dev path honest. A dev target must not open or advertise a URL that only a release build serves.

## Docs

- Document the command, port, and URL that actually work for the mode being described, and run them before claiming they do.
- Keep the README to the shortest path that works. Flags and their effects go in a table, not prose.

## Before pushing

- From `collector/`: `gofmt -l ./cmd ./internal`, `go vet ./...`, `go test ./...`.
- From `frontend/`: `npm run lint`, `npm test`, `npm run format:check`.
- Reproduce a reported bug, and re-run the reporter's exact repro after fixing it, rather than reasoning it away.
