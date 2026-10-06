#!/usr/bin/env bash
set -euo pipefail

repo="https://github.com/centauri-ai/coslash.git"
branch="${COSLASH_SOURCE_BRANCH:-hlu/hub-support}"
connect_code="${COSLASH_CONNECT:-}"
hub="${COSLASH_HUB:-}"
install_dir="${COSLASH_INSTALL_DIR:-}"

fail() {
  printf 'coSlash branch installer: %s\n' "$*" >&2
  exit 1
}

find_existing_coslash_servers() {
  command -v pgrep >/dev/null 2>&1 || fail "pgrep is required to find a running coSlash Local server"
  command -v lsof >/dev/null 2>&1 || fail "lsof is required to identify running coSlash Local servers"

  local uid pid executable is_coslash coslash_pid
  local -a verified_coslash_pids=()
  selected_coslash_pids=()
  uid="$(id -u)"

  while IFS= read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    if ! lsof -nP -a -p "$pid" -iTCP -sTCP:LISTEN >/dev/null 2>&1; then
      continue
    fi
    executable="$(lsof -nP -a -p "$pid" -d txt -Fn 2>/dev/null | sed -n 's/^n//p' | head -n 1)"
    executable="${executable% (deleted)}"
    [[ "$(basename "$executable")" == "coslash" ]] || fail "could not verify the listener process (PID $pid); no processes were stopped"
    verified_coslash_pids+=("$pid")
    if [[ "$executable" == "$target_binary" ]]; then
      selected_coslash_pids+=("$pid")
    fi
  done < <(pgrep -u "$uid" -x coslash || true)

  while IFS= read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    is_coslash=0
    for coslash_pid in "${verified_coslash_pids[@]}"; do
      if [[ "$pid" == "$coslash_pid" ]]; then
        is_coslash=1
        break
      fi
    done
    ((is_coslash)) || fail "port 8787 is in use by PID $pid, which is not a verified coSlash Local server; no processes were stopped"
  done < <(lsof -nP -t -iTCP:8787 -sTCP:LISTEN 2>/dev/null | sort -u || true)

}

stop_existing_coslash_servers() {
  local pid attempt
  local -a remaining_pids=()

  ((${#selected_coslash_pids[@]})) || return 0
  printf 'Stopping %d running coSlash Local server(s)…\n' "${#selected_coslash_pids[@]}"
  for pid in "${selected_coslash_pids[@]}"; do
    if ! kill -TERM "$pid" 2>/dev/null; then
      kill -0 "$pid" 2>/dev/null && fail "the new binary was installed, but selected coSlash Local PID $pid could not be stopped"
    fi
  done

  for attempt in {1..950}; do
    remaining_pids=()
    for pid in "${selected_coslash_pids[@]}"; do
      if kill -0 "$pid" 2>/dev/null; then
        remaining_pids+=("$pid")
      fi
    done
    ((${#remaining_pids[@]} == 0)) && return 0
    sleep 0.2
  done
  fail "coSlash Local did not stop after SIGTERM; the new binary was installed"
}

while (($#)); do
  case "$1" in
    --branch)
      (($# >= 2)) || fail "--branch requires a branch name"
      branch="$2"
      shift 2
      ;;
    --connect)
      (($# >= 2)) || fail "--connect requires a code"
      connect_code="$2"
      shift 2
      ;;
    --hub)
      (($# >= 2)) || fail "--hub requires an origin"
      hub="$2"
      shift 2
      ;;
    --help|-h)
      cat <<'USAGE'
Usage: install-branch.sh [--branch NAME] [--connect CODE --hub ORIGIN]

Builds coSlash Local from a GitHub source branch. Requires Git, Go 1.26+,
Node 24+, npm, and make. The default branch is hlu/hub-support.
Replaces the binary, then stops the selected branch's running coSlash Local server.
USAGE
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

for command_name in git go node npm make; do
  command -v "$command_name" >/dev/null 2>&1 || fail "$command_name is required"
done

[[ "$(uname -s)" == Darwin ]] || fail "this installer builds the macOS client only"
git check-ref-format --branch "$branch" >/dev/null 2>&1 || fail "invalid branch name"

go_version="$(go version | sed -n 's/^go version go\([0-9][0-9.]*\).*/\1/p')"
node_version="$(node --version | sed -n 's/^v//p')"
version_at_least() {
  awk -v actual="$1" -v required="$2" 'BEGIN {
    actual_count = split(actual, a, ".")
    required_count = split(required, r, ".")
    count = actual_count > required_count ? actual_count : required_count
    for (i = 1; i <= count; i++) {
      av = i <= actual_count ? a[i] + 0 : 0
      rv = i <= required_count ? r[i] + 0 : 0
      if (av > rv) exit 0
      if (av < rv) exit 1
    }
    exit 0
  }'
}
[[ -n "$go_version" ]] && version_at_least "$go_version" "1.26" || fail "Go 1.26+ is required"
[[ -n "$node_version" ]] && version_at_least "$node_version" "24" || fail "Node 24+ is required"

if [[ -n "$connect_code" && -z "$hub" || -z "$connect_code" && -n "$hub" ]]; then
  fail "--connect and --hub must be provided together"
fi

branch_slug="${branch//\//-}"
if [[ -z "$install_dir" ]]; then
  install_dir="${HOME}/.local/bin/coslash-dev/${branch_slug}"
fi
if [[ -z "${COSLASH_HOME:-}" ]]; then
  export COSLASH_HOME="${HOME}/.coslash-dev/${branch_slug}"
fi

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/coslash-branch-install.XXXXXX")"
staged_binary=""
trap '[[ -z "$staged_binary" ]] || rm -f "$staged_binary"; rm -rf "$work_dir"' EXIT

printf 'Building coSlash Local from branch %s…\n' "$branch"
git clone --depth 1 --single-branch --branch "$branch" "$repo" "$work_dir/source" >/dev/null
commit="$(git -C "$work_dir/source" rev-parse --short HEAD)"
make -C "$work_dir/source/collector" release VERSION=0.0.0

binary="$work_dir/source/collector/bin/coslash"
"$binary" connect --help >/dev/null 2>&1 || fail "branch $branch does not include the Hub connect command"

mkdir -p "$install_dir"
install_dir="$(cd -P -- "$install_dir" && pwd)"
target_binary="$install_dir/coslash"
staged_binary="$(mktemp "$install_dir/.coslash.XXXXXX")"
install -m 0755 "$binary" "$staged_binary"

find_existing_coslash_servers
mv -f -- "$staged_binary" "$target_binary" || fail "could not publish the new binary; existing servers were left running"
staged_binary=""
stop_existing_coslash_servers
printf 'Installed coSlash Local from %s (%s) to %s/coslash\n' "$branch" "$commit" "$install_dir"

case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *) printf 'Add it to PATH with: export PATH=%q:$PATH\n' "$install_dir" ;;
esac

if [[ -n "$connect_code" ]]; then
  "$install_dir/coslash" connect "$connect_code" --hub "$hub"
fi
