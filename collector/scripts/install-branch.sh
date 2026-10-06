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
trap 'rm -rf "$work_dir"' EXIT

printf 'Building coSlash Local from branch %s…\n' "$branch"
git clone --depth 1 --single-branch --branch "$branch" "$repo" "$work_dir/source" >/dev/null
commit="$(git -C "$work_dir/source" rev-parse --short HEAD)"
make -C "$work_dir/source/collector" release VERSION=0.0.0

binary="$work_dir/source/collector/bin/coslash"
"$binary" connect --help >/dev/null 2>&1 || fail "branch $branch does not include the Hub connect command"

mkdir -p "$install_dir"
install -m 0755 "$binary" "$install_dir/coslash"
printf 'Installed coSlash Local from %s (%s) to %s/coslash\n' "$branch" "$commit" "$install_dir"

case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *) printf 'Add it to PATH with: export PATH=%q:$PATH\n' "$install_dir" ;;
esac

if [[ -n "$connect_code" ]]; then
  "$install_dir/coslash" connect "$connect_code" --hub "$hub"
fi
