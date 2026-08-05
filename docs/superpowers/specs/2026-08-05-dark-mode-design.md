# Dark mode

## Scope

Add a light/dark control to the full Settings dialog. The first visit is light.

## Design

- Keep the existing `.dark` CSS tokens and Tailwind variant.
- Store only a user-selected value in browser `localStorage` under `theme`.
- Apply the selected theme to the document root immediately; do not wait for the settings Save button.
- Keep the theme out of `~/.coslash/settings.json` and the backend API.

## Check

Unit-test the helper that resolves the stored value and applies the root class.
