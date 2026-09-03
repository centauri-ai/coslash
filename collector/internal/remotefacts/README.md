# Remote family facts v2

This package is the transport-independent input to shared session composition.
It is deliberately separate from both the SSH NDJSON protocol and
`session-snapshot/v1`.

## Remote display data

Remote collection carries the session fields used by the local board and
inspector, including prompts, summaries, commands, todo items, digest entries,
file-edit summaries, subagent details, repository and working-directory facts,
and Git status. The local coSlash cache persists these details with mode 0600.

Family identity and incremental-refresh metadata remain separately bounded:

- vendor, family/session IDs, parent ID, and opaque spawn key;
- bounded name, status hint, branch, entrypoint, and model;
- start/activity/duration, in-turn/stopped, bounded counts, token totals, and
  cost converted deterministically to integer micro-USD;
- spawn completion/turn, command labels and text, approved
  name/live metadata, opaque file comparison keys with size/mtime, and bounded
  Codex key-to-session/parent header mappings used only for warm discovery.

The adapter still excludes `LogPath`, transcript rows, and fingerprint paths.
Fingerprint keys are comparison data; consumers must never open them as paths.
The reflection test makes additions to the source model require a new decision.

## Required fields and bounds

`schema_version`, `parser_version`, `vendor`, `family_id`, `state`, one or more
sessions, metadata arrays, and one or more fingerprints are required. A stale
family also requires `stale_reason`. Empty optional strings are omitted.

| Value | v1 bound |
| --- | ---: |
| IDs, parser version, opaque keys | 200 UTF-8 bytes; no whitespace/control |
| Display text | 280 UTF-8 bytes |
| Model | 120 UTF-8 bytes |
| Sessions/family | 256 |
| Fingerprints/family | 512 |
| Codex header mappings/family | 512 |
| Models/session | 32 |
| Spawns/session | 256 |
| Command labels/session | 128 |
| Count/token integer | 0–1,000,000,000 |
| Cost (micro-USD) | 0–1,000,000,000,000,000 |
| Timestamp | positive through year 3000 |

Adapter truncation keeps the longest valid UTF-8 prefix. Session IDs are sorted;
usage is sorted by model; spawns, metadata, and fingerprints are uniquely sorted
by their keys. Tail items beyond a list bound are rejected rather than silently
accepted. Producers must select deterministically before adapting a family.

Each family is one rooted tree: `family_id` identifies its only root, and every
other session must descend from that root within the nesting limit. Cycles,
unknown parents, and additional roots are rejected.

State is `complete`, `partial`, or `stale`. A validated replacement can be
published independently. A failed/unstable replacement retains the last good
facts as stale; missing data is not deletion.

IDs and opaque fingerprint keys reject `/` and `\\` as well as whitespace and
control characters. This keeps comparison keys from being mistaken for paths;
the adapter does not resolve or open them.
