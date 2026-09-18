# Full-session v2 client envelope

This package is the client-side canonical serializer for S01's additive full
session upload. It wraps one validated `full-session-record/v1` without
changing its bytes or identity.

The envelope order is `schemaVersion`, `mediaType`, `recordByteCount`,
`recordSha256`, `repository`, then `record`. `recordSha256` is SHA-256 over the
canonical nested record bytes. The whole envelope is sent once as gzip with
media type `application/vnd.coslash.session-revision.v2+json`; multipart and
resumable upload are not supported.

The pinned C01/S01 fixture has a 2,318-byte record, a 2,620-byte envelope, and
record hash
`sha256:10660c3b6a01cde4838b8dddcaff29ce1d38bd67adfca87447e0b45743d04fb2`.
`export_test.go` verifies these values against the public C01 record fixture.
