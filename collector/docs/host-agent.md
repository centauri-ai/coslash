# Direct Hub sync from a Linux host

The Linux `coslash` binary can run as a paired, headless device. It reads agent
sessions on that host and sends complete bundles to Hub over outbound HTTPS.
The Mac, SSH relay, SFTP transport and `coslash-helper` are not in this upload
path. SSH can be used to install or maintain the binary.

## Set up

Install the release `coslash` Linux binary under a dedicated, non-root service
user that owns the agent session files. The account needs outbound HTTPS to
Hub. Set `COSLASH_HUB_URL` to the Hub HTTPS origin. A loopback HTTP URL is
accepted for development only. The host needs systemd v256 or later,
`/usr/bin/systemd-creds`, and a usable TPM2 accessible to the service user.
Pairing fails if TPM2 encryption is unavailable.

As the service user, run:

```sh
COSLASH_HUB_URL=https://hub.example.com coslash host pair
```

The command prints a short user code and Hub verification URL. Confirm the code
in the owner's browser. The private device code and returned credential are
never printed or passed on a command line. Pairing asks `systemd-creds` to
encrypt the device credential for this user and this host's TPM2, then stores
only ciphertext in `~/.coslash/host-credentials/` under a separate name for
each Hub URL. The directory is mode `0700`, the file is mode `0600`, and
exposed or symlinked files are rejected. The host owns its credential and
installation identity; do not copy either from a Mac or another host.

Run one cycle after the Hub's v4 upload flag is enabled:

```sh
COSLASH_HUB_URL=https://hub.example.com COSLASH_V4_SYNC_ENABLED=1 coslash host once
```

For continuous sync, run `coslash host run` with the same environment. A user
service can use this unit after replacing the Hub URL:

```ini
[Unit]
Description=coSlash host sync
After=network-online.target
Wants=network-online.target

[Service]
Environment=COSLASH_HUB_URL=https://hub.example.com
Environment=COSLASH_V4_SYNC_ENABLED=1
ExecStart=%h/.local/bin/coslash host run
Restart=on-failure
RestartSec=15s

[Install]
WantedBy=default.target
```

Only one coSlash process may use the service user's state directory. The host
mode shares the runtime lock with the desktop process and refuses a second
writer. `host run` retries on a five-minute cadence after transient failures;
the durable queue resumes missing chunks after restart. Disable new writes by
removing `COSLASH_V4_SYNC_ENABLED=1` and restarting the service. Existing Hub
revisions remain readable. Revoke the host from Hub Devices to reject future
credential use.

## Current capability

The host scheduler currently offers complete Codex capture. Claude Code,
OpenCode and Cursor stay unsupported until their local complete exporters and
v4 queue adapters integrate. A headless host cannot open the user's Mac
terminal; an incoming resume or SSH-install command is acknowledged as
unavailable. Old relayed sessions keep their prior device/session identities;
pairing a direct host creates a distinct device and installation identity.
The final device UI and live-host qualification remain separate activation
gates.
