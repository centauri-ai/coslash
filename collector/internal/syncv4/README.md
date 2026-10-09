# Local v4 sync contract

**Identity:** `local-sync-v4/v1`. The wire producer is the verified
`session-backup/v1` spool. The Hub consumer is `sync-v4/v2` at server
integration SHA `b39c4e942c9f1c8ed9bc78cc835c773c630f7685`.
The additive Local check-in and command consumer is `local-device-v4/1`, pinned
to server `device-v4/2` at `e52bf98faa2b584a4ff0947358d1d7a84f2206cc`.

The scheduler discovers local Codex, Claude, OpenCode, and Cursor IDE/CLI
families. A source exporter may join this queue only after it produces a
complete, verified family through `sessionbackupproducer`; parsed cards alone
are ineligible. SSH relay discovery and the remaining vendor exporters have
separate owners. A Cursor card without an unambiguous IDE or CLI entrypoint is
ineligible. The install ID belongs to
the private v4 queue, independently of the paired credential. A credential or
Hub change clears upload and completion state while retaining the install ID.

For each family, the queue records the content revision, frozen bundle identity,
server upload ID, wire manifest and accepted revision. A change to the parsed
content revision schedules a fresh capture even when activity time is equal.
For Claude, one source-tree metadata scan also notices raw transcript and parser
sidecar changes that leave the parsed card unchanged; the producer verifies
the exact bytes before upload.
Without the Hub's `scale-import/v1` capability, recent families (activity in the newest 72 hours) are ordered first. Their
metadata creates run before their bytes. Older families drain newest first
after recent work has been attempted; a failed recent item does not indefinitely
block history. A restart reopens the verified spool and reconciles the Hub's
missing-chunk list before sending bytes. A signed direct PUT is preferred;
the authenticated proxy is the fallback when direct storage responds with a
failure. The client confirms sent chunks in batches of up to 50 chunks and
8 MiB, and treats only a completed status with an accepted revision ID as
locally done.

With `config.importPlan` delivered under `sync-policy/1` or `scale-import/v1`,
the device waits for the plan before creating uploads or listing sessions. The
plan's window and history choice set the scope. The default plan is recent
`3d` activity capped at 30 sessions; it does not start an extended backfill.
Hub Settings can explicitly start a per-device 30-day, 60-day, or all-history
import. Local receives finite choices as one-time `backfill: true`,
`history: false` plans; the all-history choice retains `history: true`. Finite
cutoffs are anchored to the plan start, and zero session caps mean unlimited
for an explicit backfill. Legacy `history: true` plans retain their
continuing-history behavior. For a bounded plan, Local selects the
newest eligible families, applies the per-agent limit, then applies the overall
`maxSessions` limit; older families remain local. The stat-only
inventory still covers the device, while discovery parses only families within
the requested window. Streamed discovery parses eight newest families per
yield, publishes the first available metadata immediately, then publishes
further metadata in groups of about 50. Warm start selects in-window sessions
within its time budget, one completed session at a time. The first selection is
at most 25 MiB. Local lists remaining metadata in batches of at most 50 before
transferring window content, with non-Cursor sources first and newest first
within that ordering; history content transfers newest first. Cursor source
preparation has a bounded attempt and retries with backoff after a timeout. A
`prioritize` command moves a
listed session to the front, including a history session while history is
paused. Live sources wait for two minutes without a change or for the session
to end before a changed revision is sent. After a one-time backfill completes,
Hub restores the default recent plan. Local does not start another catch-up; it
syncs activity observed since the current Local process started, so sessions
created while Local was offline are not uploaded by the default live plan. The
Local check-in includes the `d60` inventory bucket only after the Hub advertises
`scale-import/v1`. Deploy the Hub schema that accepts `d60` and `backfill`
before releasing this Local version, and keep the Hub's import-plan feature
gate closed until compatible clients are available. The chunk path starts with two PUT
workers per upload and adapts up to four, reducing concurrency after throttling.
It confirms only chunks the Hub marked missing; old Hubs retain serial transfer.
Check-in refreshes on phase changes and a
coalesced progress heartbeat runs about every eight seconds during active
import, including slow transfers. A heartbeat carries in-progress command
stages; the normal pass handles policy and queued commands. Outside active
import, consent refreshes before a batch once it is four minutes old.
`COSLASH_SCALE_IMPORT=0` disables the new path.

The queue's outer `version` remains 1 so the previous Local can still read
its old fields and ignore new fields. `scaleVersion: 1` identifies the additive
plan and listing state.

One upload carries a whole family: 1–4,096 artifacts, each with at most 256
chunks of 8 MiB, and at most 8,192 chunks in total. Every artifact stays
separate, so a family with one exact change body per file change syncs
whole; a larger family is not sent. The spool manifest is decoded once per
pass, not once per chunk.

The queue is private (`0700` directory and `0600` file, or current-user ACL on
Windows) and updated by sync plus atomic rename. Check-in reports
`queue.firstSync` as fallback for server events. A failed check-in, stale policy,
Hub pause, device-off, local metered hint, or low battery stops new writes.
After the Hub advertises `scale-import/v1`, check-in also reports
`queue.import` with the scheduler's phase, counts, bytes, current transfer,
progress timestamps, measured rate quantiles and wait reason. During the
inventory phase, `listed` counts files seen so far. Current queue keys and
source paths never enter that payload. Fewer than six valid rate samples, or
measurements older than two minutes, leave `rate` null. The Hub computes ETA
from a stable rate after its own minimum transfer window.
The current transfer may include its server-issued session ID after listing.
The actionable queue also reports a `queuePositions` map from server-issued
session IDs to scheduler positions, including priority and pause decisions;
parked and not-yet-ready work has no position. The map is omitted if more
than 10,000 actionable entries exist. The private source key is never sent.
The command wait request continues while Local runs, including during an
upload, a pause, and a failed pass. A changed policy or queued command wakes
check-in promptly. Retryable session failures wait 1, 5, 15, then 60 minutes
between attempts; failures parked for the same source stay parked.
Leave-outs are checked locally and at Hub create/finalize. Raw paths and
transcript text never enter sync logs.

Local discovery reads every local source through the parse cache
(`fingerprints/v1`, `internal/syncv4/fingerprints.go`): one private file per
transcript, Cursor session or OpenCode family under
`$COSLASH_HOME/fingerprints/v1/<agent>/`, keyed by the source identity, its
size and modification time, and the owning exporter's parser version
(`claude.ParserVersion`, `codex.ParserVersion`, `cursor.ParserVersion`,
`opencode.ParserVersion`). Bumping one exporter's version invalidates only its
entries. Entries are written by temporary file and rename, a file that fails
to decode is a miss that the next parse overwrites, and the inventory loop
prunes entries whose source is gone. Before each pass Local takes a stat-only
inventory of the vendor roots (`internal/inventory`, rules in its package
documentation) and records it in the queue file. The check-in reports it as
`queue.inventory` (`scale-contracts/v1`) only after the Hub has listed
`scale-import/v1` in its response `capabilities`; Local advertises the same
capability in its request. `COSLASH_SCALE_IMPORT=0` turns the cache, the
inventory and the capability off and restores full-parse discovery. Streamed
discovery (`inventory.Discover`) yields whole families newest first in
batches and persists a cursor (`discovery-cursor.json`) after each batch so a
restart resumes below it and revisits only families that changed since the
pass began.
OpenCode inventory stats the configured database path, including `OPENCODE_DB`
overrides and the standard XDG path when no override is set. A custom path
discoverable only by invoking the OpenCode CLI is omitted because that lookup
could read content. Discovery still reads the database through the normal
exporter.
When Hub leave-out rules are set, the stat-only walker cannot prove which
source files belong to an excluded repository or working directory. Local
therefore reports zero window buckets while those rules are set; aggregate
file and byte counts remain device totals.

Check-in also carries the sync log (`log`, at most 200 lines for the Tier4 Hub,
or 500 after the Hub advertises `scale-import/v1`, oldest first)
that the Hub shows on the device page and on a failed session's card. Each
unsent line stays in the queue across restarts until the Hub accepts it or its
29-day Hub retention window expires, even when more than one batch accumulates.
Each failure the queue records adds one `error` line with a code from the Hub's
closed set, the Hub session ID once the session has one, and fixed text keyed
by the code, which the Hub replaces with its own. Preparation problems map by
their first capture problem: unstable to `transcript_changed_during_read`;
invalid, unattributable or unsupported to `malformed_artifact`; otherwise
`unreadable_source`. Hub `not_found` maps to `upload_expired`,
`request_too_large` to `too_large`, and other unknown errors to
`server_error`. Back-pressure (`rate_limited`), leave-outs, sessions deleted
in the Hub, superseded or aborted uploads and a spool Local rebuilds add no
line. The same code for the same Hub session is logged once until the session
syncs or the Hub asks for a retry, and unsent lines without a session collapse
to one per code. Unsent lines leave the queue only after a check-in succeeds;
lines older than 29 days are dropped. If the Hub refuses a log batch as invalid,
Local checks in without it so sync can continue, keeps the refused lines, and
retries them after one minute. A binding change drops unsent lines.

The v4 worker starts by default, including before pairing, so Hub can start
sync without a Local restart. It never uploads until a Hub advertising
`scale-import/v1` supplies an import plan; the Local pause setting still wins.
`COSLASH_V4_SYNC=0` disables the Local scheduler and stops advertising
`sync-v4`; `COSLASH_SYNC_POLICY=0` stops advertising `sync-policy/1`.
Both switches default on. The server's separate v4 upload flag and device
policy also control whether it accepts uploads. A 409 `sync_paused` or
`device_sync_off` clears transient failure and backoff state and leaves the
worker idle until the Hub policy version changes. `device_revoked` removes the
keychain credential and stops the sync loop. v1–v3 sharing remains available.
`COSLASH_SCALE_IMPORT=0` disables the additive import and command progress
payloads while retaining the command wait fix.
The Local settings `syncPaused` switch wins over Hub pause/off and stops new
uploads and retry commands on this computer. The current Hub policy version,
manual update guidance and command-ID journal share the private queue file.
Commands are marked before execution; an interrupted command reports failure
after restart and is not launched again while its journal ID is retained.
Check-in acknowledges bounded result codes, then removes the result while
retaining the ID for deduplication. The journal retains at most 1,000
acknowledged IDs for up to seven days and keeps unacknowledged results until
the Hub accepts them. A retry command reports `landed` only after its revision is
accepted by the Hub.
The update prompt offers an HTTPS download for manual replacement. The OS
keychain credential and private install ID are not removed by replacing Local.
Owner-readable detail and artifact proof after server completion belongs to
IC-1; the current device credential has no v4 owner-read route. Live SSH,
metered-platform coverage and 500-session completion belong to
G-local-network and frozen-candidate validation.
