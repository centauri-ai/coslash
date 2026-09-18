# coSlash agent instructions

Apply the global agent instructions, then use the closest `CLAUDE.md` for the area being changed.

## Keep changes small

- Before handoff, inspect `git diff --stat` and `git diff --numstat`. If a small request produced a large net line increase, simplify it before declaring the work done.
- Every added line should have a durable purpose. Remove temporary comparison code, generated artifacts, duplicate rules, and scaffolding after verification.
- Do not add excessive inline comments. Use an inline comment only for a non-obvious edge case or constraint that cannot be made clear by the code and is not already covered by authoritative documentation; never narrate what the code already says.
- Recheck `git status` before and after long-running tools. Preserve concurrent or unrelated changes, do not attribute them to your work, and do not clean them up without authorization.

## Frontend

- Follow `frontend/CLAUDE.md` and use the existing Tailwind utilities and design tokens first. Keep one-off typography, spacing, color, and responsive rules with the component.
- Add or expand a stylesheet only when shared selectors, cross-element state, pseudo-elements, or behavior that is materially clearer in CSS requires it. Do not add CSS variables or named selectors merely to restate one component's utility classes.
- Treat HTML mockups as visual references, not implementation templates. Match the requested visual contract without copying prototype chrome, sample data, or interactions that are outside the task.
- For pixel-matching work, render the reference and implementation at the same viewport. Verify computed dimensions and the relevant responsive breakpoints in addition to inspecting screenshots.
- After the first correct render, perform a simplification pass before running checks. Pay particular attention to net-new CSS and one-use abstractions.
- Verify frontend changes with `npm run lint`, `npm test`, `npm run build`, and `npm run format:check` from `frontend/`.
