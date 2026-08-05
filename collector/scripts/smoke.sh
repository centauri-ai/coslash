#!/usr/bin/env bash

set -euo pipefail

usage() {
  echo "usage: scripts/smoke.sh <binary> <embedded|bare>" >&2
}

if [[ $# -ne 2 ]]; then
  usage
  exit 2
fi

binary=$1
mode=$2
if [[ ! -x "$binary" ]]; then
  echo "error: binary is not executable: $binary" >&2
  exit 1
fi
if [[ "$mode" != "embedded" && "$mode" != "bare" ]]; then
  usage
  exit 2
fi

smoke_home=$(mktemp -d)
server_pid=

cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf -- "$smoke_home"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

response_body="$smoke_home/response-body"
server_log="$smoke_home/server.log"
readiness_path=/
if [[ "$mode" == "bare" ]]; then
  readiness_path=/api/sessions
fi

# --port 0 lets the kernel pick a free port, so this cannot collide with
# whatever else is listening. The server logs the port it actually bound.
env HOME="$smoke_home" "$binary" --no-open --port 0 >"$server_log" 2>&1 &
server_pid=$!

base_url=
token=
for ((attempt = 1; attempt <= 40; attempt++)); do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "error: server exited during startup" >&2
    cat "$server_log" >&2
    exit 1
  fi
  base_url=$(sed -n 's|.*listening on \(http://127\.0\.0\.1:[0-9]\{1,\}\).*|\1|p' "$server_log" | head -1)
  if [[ -f "$smoke_home/.coslash/token" ]]; then
    IFS= read -r token <"$smoke_home/.coslash/token"
  fi
  if [[ -n "$base_url" && -n "$token" ]] &&
    printf 'header = "X-Coslash-Token: %s"\n' "$token" |
      curl --config - -fs -o /dev/null "$base_url$readiness_path"; then
    break
  fi
  base_url=
  sleep 0.25
done

if [[ -z "$base_url" ]]; then
  echo "error: server did not become ready" >&2
  cat "$server_log" >&2
  exit 1
fi
echo "server ready on $base_url ($mode mode)"

expect_status() {
  local path=$1
  local expected=$2
  local actual

  echo "GET $path -> expecting $expected"
  if ! actual=$(
    printf 'header = "X-Coslash-Token: %s"\n' "$token" |
      curl --config - -sS -o "$response_body" -w '%{http_code}' "$base_url$path"
  ); then
    echo "error: GET $path failed" >&2
    exit 1
  fi
  if [[ "$actual" != "$expected" ]]; then
    echo "error: GET $path: expected $expected, got $actual" >&2
    cat "$response_body" >&2
    exit 1
  fi
}

assert_contains() {
  local value=$1
  local description=$2
  if ! grep -Fq "$value" "$response_body"; then
    echo "error: response did not contain $description" >&2
    cat "$response_body" >&2
    exit 1
  fi
}

if [[ "$mode" == "embedded" ]]; then
  expect_status / 200
  assert_contains '<div id="root"' 'the application root'
  assert_contains '<title>coSlash' 'the application title'

  asset=$(grep -Eo '/assets/[^\"]*\.js' "$response_body" | sed -n '1p' || true)
  if [[ -z "$asset" ]]; then
    echo "error: application document did not reference a JavaScript asset" >&2
    cat "$response_body" >&2
    exit 1
  fi
  expect_status "$asset" 200

  expect_status /coslash 200
  assert_contains '<div id="root"' 'the application root'

  expect_status /assets/does-not-exist.js 404

  expect_status /api/nope 404
  if grep -Fq '<div id="root"' "$response_body"; then
    echo "error: unrouted API path returned the application document" >&2
    cat "$response_body" >&2
    exit 1
  fi

  # The failure this guards against is /api/sessions falling through to the
  # SPA document, so the opening bracket is the whole question.
  expect_status /api/sessions 200
  if ! grep -Eq '^[[:space:]]*[\[{]' "$response_body"; then
    echo "error: /api/sessions did not return JSON" >&2
    cat "$response_body" >&2
    exit 1
  fi

  version=$("$binary" --version)
  echo "version -> $version"
  if [[ -z "$version" || "$version" == "dev" ]]; then
    echo "error: release binary has an invalid version: ${version:-<empty>}" >&2
    exit 1
  fi
else
  expect_status / 503
  expect_status /api/sessions 200
fi

echo "smoke test passed ($mode mode)"
