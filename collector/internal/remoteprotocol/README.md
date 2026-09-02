# SSH collection protocol v1

The helper reads one bounded request and emits strict NDJSON. Every response line
has `protocol_version`, `request_id`, and a gapless `sequence`; vendor records
also identify `claude` or `codex`. The first line is a handshake. Negotiation is
by intersecting protocol and schema ranges, not matching build strings.

Record order is:

1. `handshake` with baseline identity, selected schema/parser, capabilities;
2. zero or more `changed_family`, `unchanged_family`, `skipped_family`, and
   `provisional_tombstone` records;
3. `vendor_complete` after complete enumeration of each vendor;
4. `request_complete` with final counts/timing.

Changed facts validate and become publishable immediately. Skips retain the last
good facts intact and attach a transient stale reason; a later unchanged result
clears that reason. Tombstones remain provisional until `vendor_complete`
provides a complete bounded authoritative inventory proving absence. Interruption
before that point cannot delete anything. Missing `request_complete` makes the
refresh partial but does not discard already validated changes.

Records may name only requested vendors. Family actions for a vendor end at its
single `vendor_complete`, and `request_complete` requires every requested vendor
to have completed. A proposal uses the request ID as its new baseline identity;
the request's baseline ID identifies the prior committed generation.

The request is at most 256 KiB and 1,024 known families. A full known set that
exceeds either ceiling is replaced atomically with `baseline_mode=none`; a
partial baseline is never sent. Baseline-free responses must return a complete
inventory (at most the negotiated ceiling, no more than 2,048). If it cannot fit,
the vendor cannot report complete inventory and no tombstone commits. Omitted
fingerprints never imply deletion. Changed records in baseline-free mode omit
`prior_fingerprint`, because the helper was intentionally given no comparison
state; the complete inventory remains the deletion authority.

Defaults cap a record at 1 MiB, a response at 32 MiB, and a response at 4,096
records. Unknown fields, unknown required versions, mixed/replayed IDs, sequence
gaps, stale baselines, duplicate/conflicting family actions, oversized input,
and content following `request_complete` are rejected. The package performs no
SSH, filesystem, or durable cache I/O.
