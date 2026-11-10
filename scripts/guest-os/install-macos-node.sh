#!/bin/bash
# The runtime used by ordinary terminal and debugger sessions in the macOS guest.
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
[[ $# == 1 && $EUID == 0 && $(sysctl -n hw.model) =~ ^VirtualMac[0-9]+,[0-9]+$ ]]
[[ -f /var/db/cage-guest-os && ! -L /var/db/cage-guest-os && $(stat -f %u /var/db/cage-guest-os) == 0 && $(cat /var/db/cage-guest-os) == macos ]]
base=/usr/local/libexec
target="$base/cage-node-v24.22.2"
mkdir -p "$base" /usr/local/bin
stage=$(mktemp -d "$base/.cage-node.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT
if [[ ! -e $target ]]; then
  cp "$1" "$stage/node.tgz"
  [[ $(shasum -a 256 "$stage/node.tgz" | cut -d' ' -f1) == aa9549349e47dffaa42dcf95d6e06ade8c4bdafc980979883b88767670df385a ]]
  tar -xzf "$stage/node.tgz" -C "$stage" --no-same-owner
  [[ $("$stage/node-v24.22.2-darwin-arm64/bin/node" -p 'process.platform + "/" + process.arch + "/" + process.version') == darwin/arm64/v24.22.2 ]]
  mv "$stage/node-v24.22.2-darwin-arm64" "$target"
fi
for tool in node npm npx; do
  if [[ ! -e /usr/local/bin/$tool && ! -L /usr/local/bin/$tool ]]; then
    ln -s "$target/bin/$tool" "/usr/local/bin/$tool"
  fi
done
