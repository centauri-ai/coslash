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

With `scale-import/v1`, a device waits for `config.importPlan` before creating
uploads or listing sessions. The plan's window and history choice set the scope.
Warm start selects in-window sessions within its time budget, one completed
session at a time. The first selection is at most 25 MiB. Local then lists
remaining metadata in batches of at most 50 before transferring window content
newest first and history content newest first. A `prioritize` command moves a
listed session to the front, including a history session while history is
paused. Live sources wait for two minutes without a change or for the session
to end before a changed revision is sent. The chunk path sends at most four
PUTs per upload concurrently and confirms only chunks the Hub marked missing;
old Hubs retain serial transfer. Consent is refreshed before a batch once it
is four minutes old. `COSLASH_SCALE_IMPORT=0` disables the new path.

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
Leave-outs are checked locally and at Hub create/finalize. Raw paths and
transcript text never enter sync logs.

Check-in also carries the sync log (`log`, at most 200 lines, oldest first)
that the Hub shows on the device page and on a failed session's card. Each
failure the queue records adds one `error` line with a code from the Hub's
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
to one per code. Unsent lines stay in the queue file, at most 1,000 with the
oldest dropped first, and leave it only after a check-in succeeds; lines older
than 29 days are dropped. If the Hub refuses a check-in
as invalid, Local checks in without the log and drops that batch, so the log
never stops sync. A binding change drops unsent lines.

`COSLASH_V4_SYNC_ENABLED=1` is a development activation flag and defaults off.
The server's separate v4 upload flag must also be enabled. Disable the Local
flag and restart to stop this scheduler; v1–v3 sharing remains available.
The Local settings `syncPaused` switch wins over Hub pause/off and stops new
uploads and retry commands on this computer. The current Hub policy version,
manual update guidance and command-ID journal share the private queue file.
Commands are marked before execution; an interrupted command reports failure
after restart and is never launched twice. Check-in acknowledges bounded result
codes, then removes the result while retaining the ID for durable deduplication.
The update prompt offers an HTTPS download for manual replacement. The OS
keychain credential and private install ID are not removed by replacing Local.
Owner-readable detail and artifact proof after server completion belongs to
IC-1; the current device credential has no v4 owner-read route. Live SSH,
metered-platform coverage and 500-session completion belong to
G-local-network and frozen-candidate validation.
