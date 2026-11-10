#!/usr/bin/env bash
# Build a guest-only payload without adding Tailscale's dependency graph to Cage.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
[[ $# == 3 && $3 == /* ]] || { echo 'Usage: build-tailnet.sh OS ARCH /absolute/output' >&2; exit 2; }
os=$1 arch=$2 destination=$3
case "$os/$arch" in linux/amd64|linux/arm64|darwin/arm64|windows/amd64) ;; *) exit 2 ;; esac
mkdir -p "$(dirname "$destination")"
cd "$root/tools/tailnet"
GOOS=$os GOARCH=$arch CGO_ENABLED=0 GOFLAGS= go build -trimpath -ldflags '-s -w' -o "$destination" .
if [[ $os == darwin ]]; then codesign -f -s "${CAGE_SIGNING_IDENTITY:--}" "$destination"; fi
