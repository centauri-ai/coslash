# Documentation

Start with the root [README](../README.md) for installation, the product tour,
and the command reference.

## Use and operate coSlash

- [Data and privacy](data-and-privacy.md) is the canonical user-facing reference
  for local access, optional remote collection, outbound data, and removal.
- [Troubleshooting](troubleshooting.md) covers diagnostics, missing sessions,
  startup and build failures, synthesis, and terminal launches.
- [Security policy](../SECURITY.md) describes the threat model and private
  vulnerability reporting.

## Develop coSlash

- [Contributing](../CONTRIBUTING.md) contains the development loop and required
  checks.
- [Product baseline from 2026-09-11](archive/product-baseline-2026-09-11.md) is
  a historical implementation inventory, including architecture, API routes,
  privacy contracts, and deferred work.

Stable technical contracts remain next to the code that implements them:

- [`session-snapshot/v1`](../collector/snapshot/v1/README.md)
- [`snapshot-preview/v1`](../collector/internal/sessionpreview/README.md)
- [SSH collection protocol v1](../collector/internal/remoteprotocol/README.md)
- [Remote family facts v2](../collector/internal/remotefacts/README.md)
- [Linux collection helper](../collector/internal/remotehelper/README.md)
