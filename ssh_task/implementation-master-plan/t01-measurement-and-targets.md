# T01 measurement procedure and proposed gates

Run the narrow, content-free fixture measurement from `collector/`:

```sh
go run ./internal/remotemetrics/cmd/measure \
  -manifest ./internal/remotemetrics/testdata/fixture-manifest.json
```

For a real host, instrument the collector/helper to write the same numeric
manifest fields. Do not write file/session IDs, paths, names, transcript text,
commands, prompts, or tool output. Record candidate/selected families and files;
metadata/header/body bytes and operations separately; parser/total/first-result
duration; request/normalized-response sizes; build; hardware; filesystem; SSH
RTT; requested window; and active limits.

The checked-in manifest is a deterministic synthetic regression fixture, not an
agent-box benchmark. The available 2026-08-31 workload evidence establishes the
scale only: Claude 131 files/92.57 MiB; Codex 296 files/1,127.3 MiB; maximum file
135.73 MiB. Access to the private benchmark corpus and its hardware/filesystem is
still required to publish observed percentiles.

Until that measurement is approved, T07's proposed gates remain:

| Metric | Proposed gate |
| --- | ---: |
| Helper cold refresh p95 | <= 60 s |
| Unchanged warm refresh p95 | <= 5 s |
| One changed active family p95 | <= 15 s |
| First publishable family p95 | <= 5 s |
| Request | <= 256 KiB and 1,024 known families |
| Response | <= 32 MiB; cold <= 2% selected transcript bytes |
| Repeat SSH receive | <= 1 MiB |
| Helper CPU / peak RSS | report from real benchmark; gate pending evidence |
| Raw transcript bytes crossing SSH | 0 |
| Cached families lost after incomplete scan | 0 |

Warm means no transcript body or unchanged Codex header is read. Changed-family
means exactly one selected family fingerprint changes. Cold means no usable
baseline. Each real run must include the manifest environment so results are
reproducible and comparable.
