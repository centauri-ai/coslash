#!/usr/bin/env bash
# Behavioral smoke for a packaged Linux coslash binary used as a remote collector.
# Runs as the invoking user; do not escalate to root.

set -euo pipefail

usage() {
  echo "usage: scripts/smoke-linux-remote.sh <binary>" >&2
}

if [[ $# -ne 1 ]]; then
  usage
  exit 2
fi

binary=$1
if [[ ! -x "$binary" ]]; then
  echo "error: binary is not executable: $binary" >&2
  exit 1
fi

if [[ "$(id -u)" -eq 0 ]]; then
  echo "error: refuse to run Linux remote smoke as root" >&2
  exit 1
fi

smoke_home=$(mktemp -d)
trap 'rm -rf -- "$smoke_home"' EXIT

export HOME="$smoke_home"
export COSLASH_HOME="$smoke_home/.coslash"

version=$("$binary" --version)
echo "version -> $version"
if [[ -z "$version" || "$version" == "dev" ]]; then
  echo "error: release binary has an invalid version: ${version:-<empty>}" >&2
  exit 1
fi

decode_frame_to() {
  local input=$1
  local output=$2
  python3 - "$input" "$output" <<'PY'
import json, pathlib, sys
data = pathlib.Path(sys.argv[1]).read_bytes()
magic = b"COSLASH-REMOTE/1 "
if not data.startswith(magic):
    raise SystemExit(f"missing frame magic: {data[:40]!r}")
newline = data.find(b"\n")
if newline < 0:
    raise SystemExit("missing frame header newline")
length = int(data[len(magic):newline])
payload = data[newline + 1 : newline + 1 + length]
if len(payload) != length:
    raise SystemExit("truncated frame payload")
pathlib.Path(sys.argv[2]).write_text(json.dumps(json.loads(payload)))
PY
}

probe_out="$smoke_home/probe.out"
probe_json="$smoke_home/probe.json"
"$binary" snapshot --probe >"$probe_out"
decode_frame_to "$probe_out" "$probe_json"
python3 - "$probe_json" <<'PY'
import json, pathlib, sys
probe = json.loads(pathlib.Path(sys.argv[1]).read_text())
caps = set(probe.get("capabilities") or [])
required = {"remote-session-view/v1", "remote-launch/v1"}
missing = required - caps
if missing:
    raise SystemExit(f"probe missing capabilities: {sorted(missing)}")
if probe.get("schemaVersion") != "remote-session-view/v1":
    raise SystemExit(f"unexpected schemaVersion: {probe.get('schemaVersion')!r}")
host = probe.get("host") or {}
if host.get("os") != "linux":
    raise SystemExit(f"expected linux host.os, got {host!r}")
for agent in probe.get("launchableAgents") or []:
    if agent not in ("claude", "codex"):
        raise SystemExit(f"unexpected launchable agent: {agent!r}")
    if "/" in agent or agent.startswith("."):
        raise SystemExit(f"launchable agent looks like a path: {agent!r}")
print("probe ok")
PY

now_ms=$(python3 -c 'import time; print(int(time.time() * 1000))')
snap_out="$smoke_home/snapshot.out"
snap_json="$smoke_home/snapshot.json"
"$binary" snapshot --since 0 --request-now "$now_ms" --agents claude,codex >"$snap_out"
decode_frame_to "$snap_out" "$snap_json"
python3 - "$snap_json" <<'PY'
import json, pathlib, sys
view = json.loads(pathlib.Path(sys.argv[1]).read_text())
if view.get("schemaVersion") != "remote-session-view/v1":
    raise SystemExit(f"unexpected schemaVersion: {view.get('schemaVersion')!r}")
if "sessions" not in view or not isinstance(view["sessions"], list):
    raise SystemExit("snapshot missing sessions list")
if view.get("requestedSinceMs") != 0:
    raise SystemExit(f"requestedSinceMs={view.get('requestedSinceMs')!r}")
print(f"snapshot ok ({len(view['sessions'])} sessions)")
PY

session="9c73be46-52af-4b1d-9ee7-123456789abc"
handoff_out="$smoke_home/handoff.out"
handoff_json="$smoke_home/handoff.json"
printf 'smoke handoff brief' | "$binary" handoff put --agent claude --session "$session" >"$handoff_out"
decode_frame_to "$handoff_out" "$handoff_json"
handoff_id=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$handoff_json")
if [[ ! "$handoff_id" =~ ^h_[0-9a-f]{32}$ ]]; then
  echo "error: unexpected handoff id: $handoff_id" >&2
  exit 1
fi

sys_prompts="$COSLASH_HOME/sys-prompts"
handoff_file="$sys_prompts/$handoff_id"
dir_mode=$(stat -c '%a' "$sys_prompts")
file_mode=$(stat -c '%a' "$handoff_file")
if [[ "$dir_mode" != "700" ]]; then
  echo "error: sys-prompts mode is $dir_mode, expected 700" >&2
  exit 1
fi
if [[ "$file_mode" != "600" ]]; then
  echo "error: handoff file mode is $file_mode, expected 600" >&2
  exit 1
fi
echo "handoff put ok (id=$handoff_id, modes 0700/0600)"

set +e
"$binary" launch --agent claude --session 'not-a-uuid' --mode resume >/dev/null 2>"$smoke_home/launch-bad.err"
bad_code=$?
"$binary" launch --agent claude --session "$session" --mode resume >/dev/null 2>"$smoke_home/launch-missing.err"
missing_code=$?
set -e
if [[ "$bad_code" -ne 2 ]]; then
  echo "error: invalid launch args exited $bad_code, expected 2" >&2
  cat "$smoke_home/launch-bad.err" >&2
  exit 1
fi
if [[ "$missing_code" -ne 1 ]]; then
  echo "error: missing-session launch exited $missing_code, expected 1" >&2
  cat "$smoke_home/launch-missing.err" >&2
  exit 1
fi
if ! grep -Fq 'session not found' "$smoke_home/launch-missing.err"; then
  echo "error: missing-session launch did not report session not found" >&2
  cat "$smoke_home/launch-missing.err" >&2
  exit 1
fi
echo "launch validation ok"

echo "linux remote smoke passed"
