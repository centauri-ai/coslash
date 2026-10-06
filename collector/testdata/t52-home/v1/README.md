# T52 synthetic agent home

`manifest.json` is the fixture contract. `generate.py --lane a|b|c --out DIR`
creates a private, disposable home containing native Codex, Claude Code, and
OpenCode data. It never reads or overwrites the caller's real home. Lane A
contains D-A; lane B contains D-B including one live Claude session; lane C
has no session data and names the clean installer account. `append-turn.py`
adds another synthetic turn to D-B's `claude-live` session.

Run the generator from any directory, then point a Local build at `DIR/home`
with `HOME=DIR/home`, `XDG_DATA_HOME=DIR/home/.local/share`, and
`COSLASH_HOME` set to a separate disposable path. Do not use a real
`COSLASH_HOME` or real credentials for a lane. Verify generated files with
`python3 generate.py --verify DIR` before seeding a stack.

The manifest distinguishes source rows from the sessions eligible for the
10-day import window and 45-session catch-up cap. The two `~/personal/`
Codex rows are always excluded. Cursor is intentionally empty.
