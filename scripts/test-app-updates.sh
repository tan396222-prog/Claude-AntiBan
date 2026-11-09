#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
swiftc -O -parse-as-library -framework Cocoa -framework Security desktop/Updates.swift desktop/UpdatesTests.swift -o "$temporary/updates-tests"
"$temporary/updates-tests" "$@"
go test ./internal/appupdate
