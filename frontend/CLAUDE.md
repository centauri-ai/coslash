# Frontend conventions

## Styling

- Prefer Tailwind spacing/size classnames over pixel values (e.g. `size-6`, not `w-[24px]`).
- Prefer padding over margins for spacing.
- Prefer whole-number scale steps over fractional ones (e.g. `p-4`, not `p-3.5`).
- Prefer `justify-between` over `ml-auto` for pushing flex children apart.
- Prefer plain `div`s over semantic HTML elements (`main`, `header`, `footer`).
- No slash opacity syntax on colors (e.g. `border-border/40`) — use a solid scale step like `border-neutral-100` instead.
- No unicode glyphs as UI icons (☑, ☐, ▸, ●, ⧉) — use icon components (lucide-react) or styled elements instead. Typographic characters in text (·, —, /) are fine.
- If a component becomes too big, consider breaking it down into child or sub components.
- Name things semantically to make reading code easier.
- Prefer padding over hardcoding heights. (h-8 vs p-2)
- For conditional classes with `cn()`, prefer object syntax (`"class": condition`) over `condition && "class"`.
- Prefer fail-fast code over fail-safe behavior, unless the fallback case is fully expected.

## Stylesheets and tokens

- Use the existing design tokens before introducing new ones. Keep one-off typography, spacing, color, and responsive rules with the component.
- Add or expand a stylesheet only when shared selectors, cross-element state, pseudo-elements, or behavior that is materially clearer in CSS requires it. Do not add CSS variables or named selectors merely to restate one component's utility classes.

## Working from designs

- Treat HTML mockups as visual references, not implementation templates. Match the requested visual contract without copying prototype chrome, sample data, or interactions that are outside the task.
- For pixel-matching work, render the reference and implementation at the same viewport. Verify computed dimensions and the relevant responsive breakpoints in addition to inspecting screenshots.
- After the first correct render, perform a simplification pass before running checks. Pay particular attention to net-new CSS and one-use abstractions.

## Verification

- Verify frontend changes with `npm run lint`, `npm test`, `npm run build`, and `npm run format:check` from `frontend/`.
