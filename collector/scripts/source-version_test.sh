#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

expect_version() {
	local want="$1"
	local got
	got="$(sh "$script_dir/source-version.sh" "$2" "$3")"
	if [[ "$got" != "$want" ]]; then
		printf 'source version for %q = %q, want %q\n' "$2" "$got" "$want" >&2
		exit 1
	fi
}

expect_version '0.2.0-122-g06c5ddc3-dirty' '0.2.0-122-g06c5ddc3-dirty' 'deadbeef'
expect_version '1.2.3-rc.1+build.9' 'v1.2.3-rc.1+build.9' 'deadbeef'
expect_version '0.0.0-dev.deadbeef' 'deadbeef' 'deadbeef'
expect_version '0.0.0-dev.deadbeef.dirty' 'deadbeef-dirty' 'deadbeef'
expect_version '0.0.0-dev.deadbeef' '0.1.0-01' 'deadbeef'
