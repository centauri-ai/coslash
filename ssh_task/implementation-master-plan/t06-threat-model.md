# T06 threat model and residual risks

Reviewed: 2026-09-01

## Trust boundaries

The Mac application owns settings, release trust, cache writes, and UI
composition. The SSH alias, remote account, helper stdout/stderr, SFTP replies,
remote filesystem, and transcript data are untrusted inputs. The helper runs as
the SSH user only; it has no listener, network dependency, elevated privilege,
or request-selected command/path.

The protocol accepts only bounded strict NDJSON with an exact request identity,
sequence, requested vendor lifecycle, and final `request_complete`. Family
facts and cache records are schema-validated. Skip/stale state uses a fixed
content-free code set, so an error string cannot become durable cache content.
Incomplete vendor enumeration cannot authorize tombstones.

Helper execution accepts only a locally chosen, exact versioned install path.
Lifecycle selection verifies signed metadata, platform, digest, regular-file
type, owner, mode, and capability ranges. Installation uses an exclusive
temporary file, sync, verification, and atomic activation; removal accepts only
the exact validated helper path. Unsupported, revoked, tampered, blocked, or
incompatible helpers fall back to SFTP without execution.

The Linux helper confines reads with a root directory handle plus no-follow
opens. SFTP validates the fixed allowlist, rejects symlinks and malformed
directory entry names, revalidates immediately before opening, and applies
entry/depth/file/total-byte/deadline limits. SSH collection and control-master
operations use bounded stdout/stderr and cancellation; helper children use a
dedicated process group for termination.

Only approved, bounded display facts are cached for rendering. No transcript
rows, prompts, raw commands/tool output, edited paths, worktrees, credentials,
absolute paths, raw protocol bytes, or raw stderr enter remote diagnostics or
metrics. Session names are approved UI/cache display facts, but never appear in
remote observability.

## Tested mitigations

| Threat | Mitigation and evidence |
| --- | --- |
| Replay, conflict, truncated, oversized, nested, or trailing protocol output | Strict decoder/accumulator limits and lifecycle checks; bounded decoder fuzz targets and negative tests. |
| Hostile helper error text persisted as cache data | Fixed stale-reason codes at helper, protocol, fact, and cache boundaries; negative cache/protocol tests. |
| Shell, argv, request-path, and malformed SFTP directory-entry injection | Closed helper subcommands, quoted fixed helper location, alias/path validation, fixed allowlist, and directory-entry validation tests. |
| Output flood, helper hang, cancellation, or orphan process | Capped pipes, context cancellation, process-group kill, grace reaping, bounded control-master output; race, cancellation, and spawned-child cleanup tests. |
| Changing, disappearing, oversized, or unreadable transcripts | Fresh fingerprints, per-family skips, byte/entry limits, partial cache merge, and tombstone authority tests. |
| Corrupt/interrupted cache and lifecycle metadata state | Size/schema validation, private temp-write/sync/rename commits, cache corruption tests, monotonic signed metadata sequence storage, and interrupted install/uninstall lifecycle-manager fault tests. |
| Symlink/unsafe-mode/replaced lifecycle files | Exact install layout, lstat, owner/mode/type checks, exclusive upload, digest verification, and lifecycle symlink tests. |

## Accepted residual risks

| Risk | Owner | Rationale and disposition |
| --- | --- | --- |
| SFTP v3 cannot provide an `openat`/`O_NOFOLLOW` descriptor-relative primitive for a final remote pathname. A malicious remote account can race the final `Lstat`/`RealPath` and `Open` or lifecycle request. | SSH transport maintainer; revalidate in T07 | The helper path is still digest-verified before execution, and SFTP revalidates immediately before reads, but full race resistance requires server support not exposed by the SFTP protocol. Treat a failed verification as SFTP fallback; validate supported-server behavior in T07. |
| A compromised SSH account can supply false but schema-valid display facts. | Product security owner; accepted source-trust boundary | SSH authenticates the account, not transcript correctness. Bounds and allowlists prevent path/raw-content persistence; correctness remains subject to normal source-data trust. |
| Production signing keys, revocation data, metadata endpoint, and artifact source are not yet approved. | Release owner; T07 gate | The production provider is feature-gated and fails closed. No managed helper installation or execution is enabled until T07 supplies and validates these inputs. |
