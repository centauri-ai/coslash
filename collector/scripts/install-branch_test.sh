#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fake_bin="$tmp/fake-bin"
install_dir="$tmp/custom install path"
mkdir -p "$fake_bin" "$tmp/home"

cat > "$fake_bin/fake" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
case "${0##*/}" in
  git)
    case "$1" in
      check-ref-format) exit 0 ;;
      clone) mkdir -p "${@: -1}/collector/bin"; exit 0 ;;
      -C) [[ "${3:-}" == rev-parse ]] && { printf 'testcommit\n'; exit 0; } ;;
    esac
    exit 2
    ;;
  make)
    args=" $* "
    [[ "$args" == *" INSTALL_CHANNEL=script "* && "$args" == *" BRANCH_BUILD=true "* && "$args" == *" release "* && "$args" == *" VERSION=0.0.0 "* ]] || exit 3
    collector_dir=""
    while (($#)); do
      if [[ "$1" == -C ]]; then collector_dir="$2"; shift 2; else shift; fi
    done
    [[ -n "$collector_dir" ]] || exit 2
    mkdir -p "$collector_dir/bin"
    printf '#!/usr/bin/env bash\nexit 0\n' > "$collector_dir/bin/coslash"
    chmod +x "$collector_dir/bin/coslash"
    ;;
  uname) printf 'Darwin\n' ;;
  go) printf 'go version go1.26.1 darwin/arm64\n' ;;
  node) printf 'v24.1.0\n' ;;
  npm|lsof) exit 0 ;;
  pgrep) exit 1 ;;
  *) exit 2 ;;
esac
FAKE
chmod +x "$fake_bin/fake"
for command_name in git make uname go node npm pgrep lsof; do
  ln -s fake "$fake_bin/$command_name"
done

HOME="$tmp/home" TMPDIR="$tmp" COSLASH_INSTALL_DIR="$install_dir" PATH="$fake_bin:$PATH" \
  bash "$repo_root/collector/scripts/install-branch.sh"
test -x "$install_dir/coslash"
