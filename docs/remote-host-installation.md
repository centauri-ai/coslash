# Install the Linux remote collector

coSlash on macOS can monitor Claude Code and Codex sessions on one Linux host over SSH. The Mac app never downloads or upgrades the remote binary; install it manually once on the Linux host.

The Mac looks for the collector at the fixed path `~/.local/bin/coslash`. Settings and probe output never supply an executable path.

## Choose the release asset

Published asset names match the packaging matrix exactly:

| Platform | Archive |
| --- | --- |
| macOS Apple Silicon | `coslash_<VERSION>_darwin_arm64.tar.gz` |
| macOS Intel | `coslash_<VERSION>_darwin_amd64.tar.gz` |
| Linux x86_64 | `coslash_<VERSION>_linux_amd64.tar.gz` |
| Linux ARM64 | `coslash_<VERSION>_linux_arm64.tar.gz` |
| Checksums | `checksums.txt` |

`<VERSION>` is the GitHub release tag, for example `v0.0.2` or `v0.0.2-rc.1`.

On the Linux host:

```sh
uname -m
```

Use `amd64` when the output is `x86_64`, and `arm64` when the output is `aarch64` or `arm64`.

## Download, verify, and install

Run these commands on the Linux host as your normal user. Do not use root.

```sh
VERSION="v0.0.2" # or the desired release tag
ARCH="arm64"     # or amd64
ASSET="coslash_${VERSION}_linux_${ARCH}.tar.gz"
BASE_URL="https://github.com/centauri-ai/coslash/releases/download/${VERSION}"

curl -fLO "${BASE_URL}/${ASSET}"
curl -fLO "${BASE_URL}/checksums.txt"
grep -F "  ${ASSET}" checksums.txt | sha256sum -c -

mkdir -p ~/.local/bin
tar -xzf "${ASSET}"
install -m 0755 "${ASSET%.tar.gz}/coslash" ~/.local/bin/coslash
```

Confirm the install:

```sh
~/.local/bin/coslash --version
~/.local/bin/coslash snapshot --probe
```

`snapshot --probe` prints one `COSLASH-REMOTE/1` framed JSON document on stdout. It must advertise `remote-session-view/v1`. When launch support is present it also advertises `remote-launch/v1` and lists currently available agents in `launchableAgents` without executable paths.

The Linux archive embeds the same UI binary used on macOS. On a remote host, invoke only the `snapshot`, `handoff`, and `launch` subcommands. There is no supported Linux desktop UI in this feature.

## SSH prerequisites

On the Mac, configure an SSH alias for the Linux host in `~/.ssh/config`, then run interactive SSH once so host keys and authentication work before coSlash uses `BatchMode=yes`:

```sh
ssh <alias>
```

Background probe, snapshot, and handoff staging use non-interactive SSH. Interactive Resume and Start Fresh open a normal SSH session that may prompt.

Ensure the remote login environment can find `claude` and/or `codex` on `PATH` for launches. Non-login shells often miss CLI installs that only appear in interactive shell startup files.

## After installation

In coSlash on the Mac, open Settings → Machines, enter the SSH alias, enable the host, and use Test connection. When probe reports setup required or upgrade required, return to this guide, install or replace `~/.local/bin/coslash`, then test again.

coSlash does not chmod, upload, download, or upgrade the remote collector for you.
