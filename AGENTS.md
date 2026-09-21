# coSlash agent instructions

Apply the global agent instructions, then read the closest `AGENTS.md` before editing an area:
`frontend/AGENTS.md` for the web app, `collector/AGENTS.md` for the Go collector. Read it even if
your agent did not load it automatically; not every agent reads nested instruction files from the
repository root.

Keep project instructions in `AGENTS.md` files. Claude Code, Codex, Cursor, and opencode all read
them; only Claude Code reads `CLAUDE.md`, and Claude Code skips this file when a `CLAUDE.md` exists
in the working directory or above it. Claude Code also ignores `AGENTS.override.md` and `.agents/`.

## Keep changes small

- Before handoff, inspect `git diff --stat` and `git diff --numstat`. If a small request produced a large net line increase, simplify it before declaring the work done.
- Every added line should have a durable purpose. Remove temporary comparison code, generated artifacts, duplicate rules, and scaffolding after verification.
- Do not add excessive inline comments. Use an inline comment only for a non-obvious edge case or constraint that cannot be made clear by the code and is not already covered by authoritative documentation; never narrate what the code already says.
- Recheck `git status` before and after long-running tools. Preserve concurrent or unrelated changes, do not attribute them to your work, and do not clean them up without authorization.

## Close the behavior, not just the reported line

- Treat review feedback as a failure scenario to verify. Group comments that exercise the same invariant, then inspect equivalent paths and representations before editing.
- Prefer the smallest fix that closes the underlying invariant. Afterward, compare the complete repair diff with the reviewed snapshot for removed guards, changed defaults, stale asynchronous work, and newly inconsistent callers.
- Add a focused regression test for the reported trigger and the nearest meaningful boundary or negative case. Run the area's canonical formatter and checks; if the exact formatter is unavailable, do not guess at its output or alternate layouts.

## Keep tests honest

- Do not delete or weaken an existing test to make a change pass. When a test pins behavior you are replacing, port its assertion to the new code path and say so in the pull request; if the pinned behavior is genuinely wrong, state why rather than removing the test quietly.
- A rewrite must carry over the invariants the replaced code guarded, not just its visible output. Name those invariants before starting, then check each one against the new implementation.
- Tests must not depend on the developer's machine, installed agent CLIs, network access, wall-clock timing, or map iteration order.
