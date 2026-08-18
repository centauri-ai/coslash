# `snapshot-preview/v1` (T2-I2)

This adapter is a view over the accepted `session-snapshot/v1` payload. It
does not define or assemble snapshot fields. `sessionexport.Marshal` creates
the canonical bytes once; `FromCanonical` decodes those exact bytes for the
preview, and `UploadBytes` returns the unchanged bytes after asserting that
they still match the previewed semantic object.

The canonical fixture pin is `fixturegen/1` at public head `e7035bc`. All
fixtures under `snapshot/v1/testdata/fixtures` are the provider-side consumer
test kit. The response schema is [`schema.json`](schema.json).

| State | Approval | Meaning | Action |
| --- | --- | --- | --- |
| `ready` | allowed | Canonical v1 bytes decoded and parity-checked | Review the exact payload and privacy notices |
| `invalid` | blocked | Serialization, hash, canonical form, redaction, or field-budget validation failed | Update coSlash or repair the source |
| `unsupported_version` | blocked | The payload names a schema other than v1 | Update coSlash |
| `stale_source` | blocked | The selected source revision changed before preview | Refresh and review the new revision |
| `oversized` | blocked | The mandatory canonical payload cannot fit the 256 KiB cap | Reduce evidence and retry |

Successful snapshots can still contain `redactions` and `truncation` records.
Those records describe the exact bounded content being reviewed; they are
shown prominently but do not create an alternate payload. A canonical
validation failure in either mechanism is blocked as `invalid`.
