#!/bin/bash
# Sign only the guest kit's Mach-O code, inside out. Source and foreign payloads
# are data; every native executable is refreshed even if a cache was ad-hoc signed.
set -euo pipefail
[[ $# == 2 && -d $1 ]] || { echo 'Usage: sign-macos-guest.sh /guest-kit signing-identity' >&2; exit 2; }
kit=$1 identity=$2
src=$(cd "$(dirname "$0")" && pwd)
sign=(/usr/bin/codesign --force --sign "$identity")
if [[ $identity != - ]]; then
  [[ $identity == 'Developer ID Application:'* || $identity =~ ^[[:xdigit:]]{40}$ ]] || { echo 'Guest release code requires a Developer ID Application name or certificate fingerprint.' >&2; exit 2; }
  sign+=(--timestamp --options runtime)
fi
count=0
while IFS= read -r -d '' executable; do
  kind=$(/usr/bin/file -b "$executable")
  [[ $kind == *Mach-O* ]] || continue
  if [[ $executable == "$kit/project/cage-sshfs" ]]; then
    # macFUSE is installed in the guest by its own publisher, outside our team.
    "${sign[@]}" --entitlements "$src/sshfs.entitlements" "$executable"
  else
    "${sign[@]}" "$executable"
  fi
  /usr/bin/codesign --verify --strict "$executable"
  if [[ $identity != - ]]; then
    metadata=$(/usr/bin/codesign -d --verbose=4 "$executable" 2>&1)
    grep -q '^Authority=Developer ID Application:' <<< "$metadata"
    grep -q 'flags=.*runtime' <<< "$metadata"
    grep -q '^Timestamp=' <<< "$metadata"
  fi
  count=$((count+1))
done < <(/usr/bin/find "$kit" -type f -print0)
[[ $count -gt 0 ]] || { echo 'Guest kit contains no native executable.' >&2; exit 1; }
while IFS= read -r -d '' app; do
  "${sign[@]}" "$app"
  /usr/bin/codesign --verify --deep --strict "$app"
done < <(/usr/bin/find "$kit" -depth -type d -name '*.app' -print0)
printf 'Signed and verified %s guest Mach-O files.\n' "$count"
