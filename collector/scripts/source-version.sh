#!/bin/sh
set -eu

raw=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
hash=${2:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}
version=${raw#v}

if printf '%s\n' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
	printf '%s' "$version"
	exit 0
fi

dirty=''
case "$raw" in
	*-dirty) dirty='.dirty' ;;
esac
printf '0.0.0-dev.%s%s' "$hash" "$dirty"
