# SSH collection with a Linux helper

Status: recommended architecture; implementation gated on Phase 0 contracts

Last updated: 2026-09-01

## Recommendation

Adopt an installed Linux collection helper as the long-term architecture for
remote Machines. Run the parser beside the transcripts and send bounded,
versioned session facts back to the Mac. Keep SFTP as a compatibility and
degraded fallback, but do not make full-transcript SFTP parsing the path that
must scale to agent-box workloads.

Before switching transports, fix the existing SFTP correctness boundaries:
start collection with the requested UI window, skip failures per file or family,
isolate agent budgets, and retain every valid result. These changes provide a
reliable fallback and establish the failure semantics the helper must preserve.

Do not begin the cache migration or production helper transport until Phase 0
locks down four implementation contracts:

1. the normalized family schema and feature-parity field allowlist;
2. the refresh state machine, especially when deltas and tombstones may commit;
3. consistency rules for transcripts that change while they are being parsed;
4. artifact authentication and symlink-safe installation and execution.

These are design gates, not rollout hardening. Getting them wrong could silently
delete cached families, publish internally inconsistent facts, or weaken the
existing SSH/SFTP security boundary.

## Problem statement

Remote v1 presents an SFTP tree through the same `ReadSource` interface used for
local files. That preserves parser reuse, but it places filesystem-sized work on
the wrong side of the network boundary.

For each refresh, the Mac currently:

1. Walks the remote Claude and Codex trees.
2. Reads Codex headers to reconstruct parent and child relationships.
3. Stats selected files to build fingerprints.
4. Transfers each selected transcript and parses it locally.
5. Repeats most of that work on the next stale refresh.

Remote `Stat`, `ReadDir`, and `Open` operations also validate paths with SFTP
operations such as `lstat` and `realpath`. Multiple discovery and parsing passes
therefore multiply network round trips before accounting for transcript bytes.

The measured agent-box workload makes this a structural limit:

| Source | Files | Transcript bytes | Consequence |
| --- | ---: | ---: | --- |
| Claude | 131 | 92.57 MiB | Slow but can fit the current limits. |
| Codex | 296 | 1,127.3 MiB | Cannot fit a complete SFTP refresh. |
| Largest Codex file | 1 | 135.73 MiB | Cannot pass the 32 MiB per-file limit. |

A recent window reduces which transcripts are parsed in full, but Codex must
still inspect headers across the tree to recover session families. An active
large transcript also changes repeatedly, so fingerprint-only incremental SFTP
would still need to transfer that file again.

If coSlash must derive complete transcript facts, either the transcript bytes
cross SSH or the parser runs next to the transcripts. Moving parsing to Linux is
the scalable boundary.

## Current failure amplification

The transport cost is compounded by several correctness issues:

- `ApplySettings` can start collection with `since=0` before `ListView` provides
  the selected UI window.
- A file rejected during Codex header discovery is conservatively retained and
  can fail again during strict parsing.
- Strict remote parsing discards all valid files for an agent when any selected
  file is corrupt, oversized, or interrupted.
- Claude and Codex collect concurrently through one remote `Source`; its total
  byte counter is shared, so the agents race for the same aggregate allowance.
- Fingerprints are saved in the snapshot but are not used to reuse parsed facts.
- The cache stores composed root sessions rather than file- or family-level
  inputs, making safe incremental recomposition difficult.

Partial-agent publication and retry backoff prevent the earlier connecting
loop, but they do not remove these underlying limits.

## Target architecture

```text
Mac coSlash                                     Linux machine

remote manager
    |
    | SSH exec: versioned collect request
    | - requested time window
    | - parser/schema version
    | - known family fingerprints
    v
transport client  -------------------------->  coslash-helper collect
                                                    |
                                                    | local filesystem reads
                                                    v
                                              Claude/Codex parsers
                                                    |
    bounded normalized deltas                       |
    <-----------------------------------------------+
    |
    v
validate -> merge per-family cache -> compose -> API/UI
```

The helper is a narrow collection program, not a remote UI, daemon, or complete
coSlash installation. It reads supported agent data using the SSH user's normal
permissions and writes normalized records to stdout. The Mac remains the owner
of settings, health state, durable session cache, filtering, and UI behavior.

### Why the helper should be installed

An installed, versioned executable is more predictable than piping scripts or
depending on Go, Python, or another runtime already being available. It also
allows the existing Go parsers and tests to be reused without implementing a
second parser.

The installation flow should be explicit. The user authorizes coSlash to place
the helper in a user-owned location on the selected machine. A likely layout is:

```text
~/.local/lib/coslash/helpers/<version-or-sha>/coslash-helper
```

Installation must use a temporary file followed by an atomic rename, verify the
expected digest, set user-only executable permissions, and never require root.
The exact location is a product decision and must account for `noexec` home
mounts. coSlash should detect the remote OS and architecture before choosing an
artifact.

The helper should not self-update. The Mac chooses and verifies the compatible
version, and retains an older compatible helper until the replacement succeeds.
Compatible installed helpers are reused rather than installed on every
connection. The compatibility policy should support the current and immediately
previous protocol/schema for at least one stable Mac release, warn while an old
helper is still compatible, and require an upgrade before executing an
incompatible or revoked helper. Degraded SFTP remains available but must be
visible rather than silently hiding the upgrade state.

Disabling a host should leave the helper installed because disable is not consent
to a remote deletion. Removing a host should first offer explicit “remove and
uninstall helper” and “remove host only” choices. Uninstall must finish before
the local alias/settings are forgotten, or the UI must retain enough information
to retry and disclose that the helper remains installed.

## Collection protocol

Use a dedicated, versioned protocol over SSH stdin/stdout. NDJSON is a good
initial framing choice because records can be decoded incrementally and a
completed family can survive a later interruption.

### Request

The request should include:

- protocol version;
- parser build/schema version;
- requested `since_ms` and collection time;
- enabled vendors;
- known per-family fingerprints from the Mac cache;
- explicit input and output limits;
- a request ID used only for diagnostics.

Do not send SSH configuration, credentials, raw cached session content, or
arbitrary remote paths. The helper owns its fixed source allowlist relative to
the remote user's home. Cached fingerprint keys are comparison inputs only: the
helper must never resolve or open a path supplied in the request. Prefer opaque,
helper-derived file keys; if relative names are retained for identity, validate
them as data and match them only against the helper's newly discovered manifest.

### Response

The response should contain independently valid records:

- handshake and helper capabilities;
- one normalized family delta per changed or new family;
- deleted-family tombstones;
- per-vendor candidate, selected, skipped, and truncated coverage;
- structured skip and failure reasons;
- a final completion record with counts and timing.

Every record should carry the protocol version, request ID, and a monotonic
sequence number, plus the vendor where applicable. Family records should also
carry the family ID, parser/schema version, the prior fingerprint set they
replace, and the new fingerprint set. This lets the Mac reject a response that
was replayed, mixed between requests, reordered in a conflicting way, or
computed against a different cached baseline.

Family deltas need enough parser output to let the Mac perform shared session
composition without reopening remote files. They should exclude raw transcript
rows and unrestricted tool output. Absolute remote paths should be omitted or
replaced by opaque identifiers unless a specific remote feature requires them.

The response contract should be separate from the cloud
`session-snapshot/v1` contract. SSH collection needs parser/composition facts and
incremental deletion semantics; a consented cloud snapshot has a different
privacy boundary and lifecycle.

### Refresh state machine and cache commit rules

NDJSON framing makes individual family results recoverable, but it does not make
an interrupted response an authoritative snapshot. The protocol must distinguish
family-level commits from vendor-level enumeration completeness.

Use these commit rules:

1. A valid changed-family record may replace that one cached family after the
   entire record is received and validated.
2. An unchanged-family record confirms presence only; it never removes another
   family.
3. A skipped, failed, or unstable changed family retains its last good value and
   is marked stale with a structured reason.
4. A tombstone is provisional until the same vendor emits a successful
   `vendor_complete` record proving that its allowlisted tree was fully
   enumerated. Scan truncation, permission errors, disappearing directories, or
   timeout invalidate all tombstones for that vendor.
5. A missing family is never interpreted as deleted without such a completed
   enumeration. Missing optional vendor roots produce empty coverage only when
   absence itself was observed successfully.
6. A missing final request-completion record makes the refresh partial. Already
   validated changed families may remain publishable, but coverage is incomplete
   and no uncommitted tombstone is applied.
7. The Mac writes one atomic cache generation containing committed family
   changes, retained stale families, committed tombstones, and coverage. It does
   not mutate the durable cache once per NDJSON line.

The protocol fixtures must cover at least: interruption before and after a
family record, interruption before `vendor_complete`, a changed family that
fails parsing, incomplete enumeration, duplicate/conflicting records, a stale
baseline, and deletion during a broader or narrower requested window.

### Normalized family schema

Phase 0 should define a concrete schema rather than serializing Go structs
directly. The schema must contain the stable facts required by shared
composition, including:

- agent, family ID, source session ID, and parent/spawn relationships;
- session timing, activity, source status hints, and stopped/in-turn state;
- bounded names and other approved display fields;
- bounded counts, model/token/cost facts, spawn state, and subagent command
  labels required for existing cards and composition;
- approved vendor metadata required for name, status, and workflow resolution;
- file fingerprints and a family completeness/staleness state.

Each string, list, nesting level, numeric value, family record, and total response
needs a specified limit and deterministic truncation behavior. Raw transcript
rows, unrestricted prompts or tool output, absolute paths, environment values,
and fields used only by unsupported remote features are excluded. Unknown fields
are rejected unless the negotiated protocol explicitly marks them optional.

Schema fixtures should compare helper and SFTP normalized family facts and their
composed root sessions against local collection for the same source data. Exact
ordering and canonical identity rules must be specified so the comparison is
deterministic.

### Validation and limits

Treat helper output as untrusted even though it arrived over an authenticated
SSH connection:

- cap stdin, stdout, stderr, request size, fingerprint count, record size,
  record count, nesting, and total duration;
- reject unknown required protocol versions;
- validate vendor names, identifiers, timestamps, numeric ranges, and nesting;
- reject duplicate or conflicting family records;
- publish completed families when a later family fails;
- commit the new cache snapshot atomically;
- preserve the last good family when its replacement is incomplete.

stderr is diagnostic-only and must remain bounded and redacted before it is
exposed through diagnostics.

The Mac transport must drain bounded stdout and stderr concurrently, cancel the
local SSH process and its process group on timeout, and classify a clean helper
failure separately from SSH transport failure. The remote command and helper
path are selected from fixed, validated values rather than interpolated shell
input.

## Incremental cache model

Store normalized inputs by agent and session family rather than only storing the
final composed root cards.

Each cache entry should include:

- agent and family ID;
- parser/schema version;
- fingerprints for every contributing file;
- normalized `ParsedSession`-equivalent facts needed for composition;
- last successful collection time;
- whether the family was complete, skipped, or retained from a previous result.

On refresh, the helper scans local directory metadata and compares families with
the fingerprints supplied by the Mac. It parses only new or changed families,
emits provisional tombstones for removed families, and reports unchanged
families without resending their facts. Tombstones become authoritative only
under the completed-enumeration rules above. The helper can therefore remain
stateless after installation.

For Codex, the Mac cache must retain the stable path-to-session and parent-header
mapping used to construct families. An unchanged directory entry can reuse that
mapping; a new or changed entry requires a local helper header read or an SFTP
header read. A family fingerprint alone is not enough to avoid rereading every
Codex header during SFTP discovery.

Fingerprint identity should include the relative path, size, and modification
time. The parser version is part of cache validity: changing parser behavior
forces affected families to be recollected even when files did not change.

Fingerprint equality is an optimization, not a proof that content is immutable.
The implementation must document filesystem timestamp-resolution assumptions.
For every parsed file, capture metadata before and after the read. If size or
modification time changes, retry within a small fixed budget or mark the family
unstable and retain its last good value. A disappearing file is a skip during
that generation, not a deletion, unless a later complete enumeration confirms
its absence.

The known-fingerprint request is itself bounded. Phase 0 must choose a maximum
request size and deterministic fallback when the cache is larger, such as
vendor inventory digests, paging, or recollecting a bounded window. The helper
must not interpret omitted fingerprints as deleted families.

For protocol v1, prefer an explicit `baseline_mode=none` fallback that performs
a bounded recollection of the requested window. Deletion still requires a
bounded authoritative family inventory following complete vendor enumeration.
If that inventory cannot fit, report partial coverage and apply no tombstones.
Digest or paging complexity can be added later if measurements show the bounded
fallback is too costly.

An active append-only file may still require a full local reparse initially.
That consumes Linux disk and CPU rather than SSH bandwidth. Incremental parser
checkpoints can be considered later only if measurement shows local reparsing is
too expensive.

## Failure semantics

Both helper and SFTP transports should implement the same user-visible rules:

- Failure is isolated per file or family whenever composition remains safe.
- One vendor cannot consume another vendor's resource budget.
- Any successfully collected family is publishable.
- A failed changed family retains its last good cached value and is marked stale.
- Missing optional vendor data is empty coverage, not a host failure.
- A truncated window is explicit and never presented as complete history.
- Existing cards remain visible while a refresh is running.
- Manual retry bypasses backoff but does not bypass safety limits.

Health should distinguish transport, helper compatibility, source data, and
resource failures. At minimum, diagnostics need stable reasons for:

- SSH connection or authentication failure;
- helper missing;
- helper installation or verification failure;
- unsupported remote OS or architecture;
- protocol or parser version mismatch;
- refresh timeout;
- output limit;
- source file skipped because it is unreadable or invalid;
- partial family or partial vendor data.

## Security and privacy boundary

Installing the helper intentionally changes the current promise that Linux runs
no coSlash code and receives no coSlash package. Documentation and setup consent
must say this plainly before release.

The design should preserve these constraints:

- no root access or privilege escalation;
- no agent transcript mutation;
- no arbitrary command or path supplied through the protocol;
- fixed read allowlist beneath the SSH user's home, plus narrowly defined
  process-liveness checks;
- user-only helper and directory permissions;
- signed artifact or release-metadata verification against a public key shipped
  with the Mac application, plus digest verification before execution;
- bounded stdout and stderr;
- no raw transcript persistence on the Mac;
- no network access required by the helper;
- uninstall removes only known helper paths after resolving exact targets.

The threat model must cover a compromised remote account returning malicious
protocol data, a replaced helper binary, symlink attacks during installation,
unexpected executable formats, and an interrupted upgrade.

The filesystem implementation must define how it prevents time-of-check to
time-of-use path substitution. Allowlisted reads and install, upgrade, rollback,
and uninstall operations should use no-follow, directory-relative traversal (or
an equivalent race-resistant primitive), reject unsafe ownership or permissions,
and operate only on an exact helper version path. The artifact trust design must
also cover signing-key rotation/revocation and what happens when a previously
installed helper no longer verifies.

## SFTP fallback

Keep SFTP for hosts where the helper is unavailable, installation is declined,
or execution is blocked. The fallback should be reliable rather than complete
at arbitrary scale.

Required SFTP improvements:

1. Wait for the first requested UI window before initial refresh.
2. Build one manifest from directory-entry metadata and reuse it across
   selection, fingerprinting, and parsing.
3. Skip known oversized files before header reads and strict parsing.
4. Return valid per-file or per-family results alongside structured skips.
5. Give Claude and Codex independent byte accounting.
6. Cache family-level parsed facts plus Codex path-to-parent header mappings, and
   avoid rereading unchanged transcript bodies or headers.
7. Reduce redundant `lstat` and `realpath` operations without weakening the
   symlink and allowlist boundary.
8. Preserve timeout classification through SFTP close and EOF failures.

If a transcript is too large for SFTP, the UI should show partial coverage and
recommend installing the helper. Raising the aggregate limit is not a durable
solution.

## Delivery plan

### Phase 0: establish measurements and contracts

- Capture operation counts, bytes, selected family sizes, parser duration, and
  response size for the known agent-box fixture.
- Define the normalized family schema, field privacy allowlist, and deterministic
  parity fixtures for helper, SFTP, and local collection.
- Define a transport-independent collection result and versioned family cache
  model, including path/header discovery metadata and cache migration behavior.
- Specify the refresh state machine and atomic cache commit rules for complete,
  partial, stale, unstable, skipped, and deleted families.
- Specify protocol records, compatibility rules, request/response limits,
  canonical ordering, and hostile/interruption fixtures.
- Decide supported Linux architectures and libc strategy, helper installation
  location, `noexec` behavior, and fixed bootstrap/exec commands.
- Choose signed artifact authentication, its embedded root of trust, and key
  rotation/revocation behavior.
- Define first-refresh and repeat-refresh performance targets from measurements.

Exit condition: reviewed schemas and state-machine fixtures can express complete,
partial, skipped, stale, unstable, and deleted families without depending on
SFTP-specific errors; interrupted or incomplete enumeration cannot delete a
cached family; artifact and filesystem trust boundaries are documented.

### Phase 1: correct and incremental SFTP

- Implement the SFTP improvements listed above.
- Migrate the cache from composed-session-only storage to family-level facts.
- Verify that one oversized Codex file no longer removes other Codex or Claude
  sessions.

Exit condition: SFTP always provides deterministic best-effort results; repeat
refreshes do not transfer unchanged transcript bodies or reread unchanged Codex
headers; incomplete scans cannot commit deletion.

### Phase 2: helper prototype

- Add a Linux-only `coslash-helper collect` command around existing vendor
  parsers.
- Implement the request/response protocol and Mac-side subprocess transport.
- Initially install the helper manually on the benchmark host.
- Compare first and repeat refreshes with SFTP using the same window and data.
- Confirm that the largest Codex transcript returns normalized facts without
  transferring raw transcript bytes.
- Exercise transcript mutation during parsing and interrupted NDJSON responses.

Exit condition: the prototype produces parity with local parser facts for the
fixture, bounded input/output, safe partial cache commits, and materially better
agent-box latency.

### Phase 3: managed installation and compatibility

- Build and publish signed Linux artifacts and checksummed release metadata for
  supported targets.
- Add explicit setup consent, remote platform detection, atomic installation,
  verification, upgrade, rollback, and uninstall.
- Add capability negotiation and fall back to SFTP on incompatibility.
- Update privacy, troubleshooting, diagnostics, and Machines UI copy.

Exit condition: a user can install, verify, upgrade, diagnose, and remove the
helper without logging into Linux to repair coSlash state.

### Phase 4: hardening and rollout

- Test hostile protocol output, symlink races, interrupted installation,
  timeouts, output floods, malformed transcripts, and disappearing files.
- Test slow disks, many small files, giant active files, and missing vendors.
- Roll out behind an explicit feature flag before making the helper the preferred
  transport.
- Retain SFTP fallback telemetry without collecting transcript contents or
  session names.

Exit condition: helper collection is the preferred path on supported hosts and
fallback behavior remains usable and observable.

## Decisions required by the end of Phase 0

1. Is helper installation optional or required for remote Machines?
2. Which Linux architectures and libc variants are supported initially?
3. Where is the helper installed, and how are `noexec` home directories handled?
4. Which signing and release-metadata scheme authenticates artifacts, and how
   are trust-root rotation and revocation handled?
5. Which normalized fields are required for remote feature parity?
6. Are working directories or edited paths ever returned, and how are they
   represented without leaking unnecessary absolute paths?
7. What cache migration and parser-version support window is required?
8. What measured latency and response-size targets define success?
9. What bounded-request fallback is used when all cached fingerprints do not fit?
10. Which filesystem primitives enforce no-follow reads and symlink-safe install,
    upgrade, rollback, and uninstall operations?
11. What protocol/schema support window and prompt behavior apply to deprecated,
    incompatible, and revoked helpers?
12. Does host removal uninstall the helper or leave it installed, and how is that
    choice made before the local SSH alias is forgotten?

## Non-goals

- Running a persistent daemon on Linux.
- Exposing a network listener on the Linux machine.
- Uploading raw transcripts to a cloud service.
- Providing remote Resume, Start Fresh, synthesis, or terminal control as part
  of this transport change.
- Solving local/cloud snapshot consent with the SSH collection protocol.
- Implementing incremental JSONL parser checkpoints before local helper parsing
  is measured as a bottleneck.

## Final recommendation

Proceed with the helper architecture, with the Mac-owned family cache and a
stateless, installed Linux parser as the target. Treat Phase 0 as an
implementation gate: approve the normalized schema, refresh/cache state machine,
file-consistency rules, and artifact/filesystem trust model before migrating the
cache or adding the production transport. Then correct and incrementally cache
SFTP behind that shared boundary before introducing helper collection.

This sequencing prevents the helper from inheriting the current all-or-nothing
behavior, keeps an honest fallback for unsupported machines, and makes the
performance comparison meaningful. It also recognizes the fundamental limit:
complete facts for a 100+ MiB active transcript cannot be both timely and
bandwidth-light while parsing remains on the Mac.

The helper should remain optional initially and be preferred on supported hosts
only after the managed installation flow is hardened. Unsupported, declined, or
blocked hosts continue on explicit best-effort SFTP with honest partial coverage.

## Related references

- `ssh_task/implementation-master-plan/implementation-master-plan.md`
- `ssh_task/mix-plan/workload-context-for-debug.md`
- `collector/internal/remote/manager.go`
- `collector/internal/remote/source.go`
- `collector/internal/remote/cache.go`
- `collector/internal/vendors/codex/source.go`
- `collector/internal/vendors/codex/discovery.go`
- `collector/internal/vendors/claude/source.go`
- `docs/data-and-privacy.md`
