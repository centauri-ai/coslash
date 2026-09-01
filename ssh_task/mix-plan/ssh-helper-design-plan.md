# SSH collection with a Linux helper

Status: proposed design reference

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
the remote user's home.

### Response

The response should contain independently valid records:

- handshake and helper capabilities;
- one normalized family delta per changed or new family;
- deleted-family tombstones;
- per-vendor candidate, selected, skipped, and truncated coverage;
- structured skip and failure reasons;
- a final completion record with counts and timing.

Family deltas need enough parser output to let the Mac perform shared session
composition without reopening remote files. They should exclude raw transcript
rows and unrestricted tool output. Absolute remote paths should be omitted or
replaced by opaque identifiers unless a specific remote feature requires them.

The response contract should be separate from the cloud
`session-snapshot/v1` contract. SSH collection needs parser/composition facts and
incremental deletion semantics; a consented cloud snapshot has a different
privacy boundary and lifecycle.

### Validation and limits

Treat helper output as untrusted even though it arrived over an authenticated
SSH connection:

- cap stdout, stderr, record size, record count, and total duration;
- reject unknown required protocol versions;
- validate vendor names, identifiers, timestamps, numeric ranges, and nesting;
- reject duplicate or conflicting family records;
- publish completed families when a later family fails;
- commit the new cache snapshot atomically;
- preserve the last good family when its replacement is incomplete.

stderr is diagnostic-only and must remain bounded and redacted before it is
exposed through diagnostics.

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
emits tombstones for removed families, and reports unchanged families without
resending their facts. The helper can therefore remain stateless after
installation.

Fingerprint identity should include the relative path, size, and modification
time. The parser version is part of cache validity: changing parser behavior
forces affected families to be recollected even when files did not change.

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
- artifact digest verification before execution;
- bounded stdout and stderr;
- no raw transcript persistence on the Mac;
- no network access required by the helper;
- uninstall removes only known helper paths after resolving exact targets.

The threat model must cover a compromised remote account returning malicious
protocol data, a replaced helper binary, symlink attacks during installation,
unexpected executable formats, and an interrupted upgrade.

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
6. Cache family-level parsed facts and avoid rereading unchanged families.
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
- Define a transport-independent collection result and family cache model.
- Specify protocol records, compatibility rules, limits, and golden fixtures.
- Decide supported Linux architectures and the helper installation location.
- Define first-refresh and repeat-refresh performance targets from measurements.

Exit condition: the same fixtures can express complete, partial, skipped, stale,
and deleted families without depending on SFTP-specific errors.

### Phase 1: correct and incremental SFTP

- Implement the SFTP improvements listed above.
- Migrate the cache from composed-session-only storage to family-level facts.
- Verify that one oversized Codex file no longer removes other Codex or Claude
  sessions.

Exit condition: SFTP always provides deterministic best-effort results and
repeat refreshes do not transfer unchanged transcript families.

### Phase 2: helper prototype

- Add a Linux-only `coslash-helper collect` command around existing vendor
  parsers.
- Implement the request/response protocol and Mac-side subprocess transport.
- Initially install the helper manually on the benchmark host.
- Compare first and repeat refreshes with SFTP using the same window and data.
- Confirm that the largest Codex transcript returns normalized facts without
  transferring raw transcript bytes.

Exit condition: the prototype produces parity with local parser facts for the
fixture, bounded output, and materially better agent-box latency.

### Phase 3: managed installation and compatibility

- Build and publish signed or checksummed Linux artifacts for supported targets.
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

## Decisions to make before implementation

1. Is helper installation optional or required for remote Machines?
2. Which Linux architectures and libc variants are supported initially?
3. Where is the helper installed, and how are `noexec` home directories handled?
4. How are release artifacts authenticated: checksums, signatures, or both?
5. Which normalized fields are required for remote feature parity?
6. Are working directories or edited paths ever returned, and how are they
   represented without leaking unnecessary absolute paths?
7. What cache migration and parser-version support window is required?
8. What measured latency and response-size targets define success?

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
stateless, installed Linux parser as the target. Do not jump directly from the
current SFTP implementation to helper-only collection. First establish
transport-independent partial-result semantics and family-level caching, then
put the helper behind that boundary.

This sequencing prevents the helper from inheriting the current all-or-nothing
behavior, keeps an honest fallback for unsupported machines, and makes the
performance comparison meaningful. It also recognizes the fundamental limit:
complete facts for a 100+ MiB active transcript cannot be both timely and
bandwidth-light while parsing remains on the Mac.

## Related references

- `ssh_task/mix-plan/workload-context-for-debug.md`
- `collector/internal/remote/manager.go`
- `collector/internal/remote/source.go`
- `collector/internal/remote/cache.go`
- `collector/internal/vendors/codex/source.go`
- `collector/internal/vendors/codex/discovery.go`
- `collector/internal/vendors/claude/source.go`
- `docs/data-and-privacy.md`
