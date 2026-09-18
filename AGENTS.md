# coSlash agent instructions

Apply the global agent instructions, then use the closest `CLAUDE.md` for the area being changed:
`frontend/CLAUDE.md` for the web app, `collector/CLAUDE.md` for the Go collector.

## Keep changes small

- Before handoff, inspect `git diff --stat` and `git diff --numstat`. If a small request produced a large net line increase, simplify it before declaring the work done.
- Every added line should have a durable purpose. Remove temporary comparison code, generated artifacts, duplicate rules, and scaffolding after verification.
- Do not add excessive inline comments. Use an inline comment only for a non-obvious edge case or constraint that cannot be made clear by the code and is not already covered by authoritative documentation; never narrate what the code already says.
- Recheck `git status` before and after long-running tools. Preserve concurrent or unrelated changes, do not attribute them to your work, and do not clean them up without authorization.
