# Local v4 sync contract

**Identity:** `local-sync-v4/v1`. The wire producer is the verified
`session-backup/v1` spool. The Hub consumer is `sync-v4/v2` at server
integration SHA `b39c4e942c9f1c8ed9bc78cc835c773c630f7685`.
The additive Local check-in and command consumer is `local-device-v4/1`, pinned
to server `device-v4/2` at `e52bf98faa2b584a4ff0947358d1d7a84f2206cc`.

The scheduler currently discovers local Codex families. A source exporter may
join this queue only after it produces a complete, verified family through
`sessionbackupproducer`; parsed cards alone are ineligible. SSH relay discovery
and the other vendor exporters have separate owners. The install ID belongs to
the private v4 queue, independently of the paired credential. A credential or
Hub change clears upload and completion state while retaining the install ID.

`coslash host pair` and `coslash host run` reuse this local queue on a paired
Linux host and upload directly to Hub over HTTPS. The host has its own device
credential and installation ID; no Mac relay participates. See
[`docs/host-agent.md`](../../docs/host-agent.md). New direct-host sync does not
change the old relay cache or synthesize its session identity.

For each family, the queue records the content revision, frozen bundle identity,
server upload ID, wire manifest and accepted revision. A change to the parsed
content revision schedules a fresh capture even when activity time is equal.
Recent families (activity in the newest 72 hours) are ordered first. Their
metadata creates run before their bytes. Older families drain newest first
after recent work has been attempted; a failed recent item does not indefinitely
block history. A restart reopens the verified spool and reconciles the Hub's
missing-chunk list before sending bytes. A signed direct PUT is preferred;
the authenticated proxy is the fallback when direct storage responds with a
failure. The client confirms each chunk and treats only a completed status
with an accepted revision ID as locally done.

The queue is private (`0700` directory and `0600` file, or current-user ACL on
Windows) and updated by sync plus atomic rename. Check-in reports
`queue.firstSync` as fallback for server events. A failed check-in, stale policy,
Hub pause, device-off, local metered hint, or low battery stops new writes.
Leave-outs are checked locally and at Hub create/finalize. Raw paths and
transcript text never enter sync logs.

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
