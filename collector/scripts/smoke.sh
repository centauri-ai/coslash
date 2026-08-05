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

base_url=
for port in 18787 18788 18789; do
  : >"$server_log"
  env HOME="$smoke_home" "$binary" --no-open --port "$port" >"$server_log" 2>&1 &
  server_pid=$!

  ready=false
  for ((attempt = 1; attempt <= 40; attempt++)); do
    if ! kill -0 "$server_pid" 2>/dev/null; then
      break
    fi
    if grep -Fq "listening on http://127.0.0.1:$port" "$server_log" &&
      curl -fs -o /dev/null "http://127.0.0.1:$port$readiness_path" &&
      kill -0 "$server_pid" 2>/dev/null; then
      ready=true
      break
    fi
    sleep 0.25
  done

  if [[ "$ready" == "true" ]]; then
    base_url="http://127.0.0.1:$port"
    echo "server ready on $base_url ($mode mode)"
    break
  fi

  if kill -0 "$server_pid" 2>/dev/null; then
    echo "error: server did not become ready on port $port" >&2
    cat "$server_log" >&2
    exit 1
  fi

  wait "$server_pid" 2>/dev/null || true
  server_pid=
done

if [[ -z "$base_url" ]]; then
  echo "error: server failed to start on candidate ports 18787, 18788, and 18789" >&2
  cat "$server_log" >&2
  exit 1
fi

expect_status() {
  local path=$1
  local expected=$2
  local actual

  echo "GET $path -> expecting $expected"
  if ! actual=$(curl -sS -o "$response_body" -w '%{http_code}' "$base_url$path"); then
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
