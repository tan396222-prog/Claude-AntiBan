#!/bin/bash
# Build the FSKit-compatible SSHFS against the SDK from the signed macFUSE
# installer. Pass its extracted usr/local directory; no host driver is installed.
set -euo pipefail
[[ ( $# == 2 || $# == 3 ) && $(uname -sm) == 'Darwin arm64' ]] || { echo 'Usage: build-macos-sshfs.sh /absolute/macfuse-sdk/usr/local /absolute/cage-sshfs [/absolute/source-materials]' >&2; exit 2; }
sdk=$1
output=$2
script_dir=$(cd "$(dirname "$0")" && pwd)
[[ $sdk == /* && $output == /* && -f $sdk/include/fuse3/fuse.h && ! -e $output ]]
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
materials=${3:-$work/source}
node "$script_dir/collect-macos-sshfs-sources.mjs" prepare "$sdk" "$materials"
bash "$script_dir/rebuild-macos-sshfs.sh" "$materials" "$sdk" "$work/cage-sshfs"
codesign -s - --force "$work/cage-sshfs"
install -m 755 "$work/cage-sshfs" "$output"
