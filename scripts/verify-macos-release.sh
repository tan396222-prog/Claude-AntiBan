#!/usr/bin/env bash
# Validate the actual downloaded distribution without installing or launching it.
set -euo pipefail
directory="${1:?Usage: scripts/verify-macos-release.sh <release-directory>}"
# stapler contacts Apple's CloudKit even for a stapled artifact. Hosted runners
# can lose this connection; retry service failures without bypassing validation.
validate_ticket() {
  local attempt status
  for attempt in 1 2 3; do
    if xcrun stapler validate "$1"; then return 0; else status=$?; fi
    if [[ "$status" != 68 || "$attempt" == 3 ]]; then return "$status"; fi
    echo "Apple ticket service unavailable; retry $attempt/2." >&2
    sleep 5
  done
}
shopt -s nullglob
archives=("$directory"/Cage-*-macos-arm64.dmg)
if [[ ${#archives[@]} != 1 ]]; then echo 'Expected exactly one Cage DMG.' >&2; exit 1; fi
sources=("$directory"/mihomo-v*-source.tar.gz)
if [[ ${#sources[@]} != 1 ]]; then echo 'Expected exactly one Mihomo source archive.' >&2; exit 1; fi
archive="${archives[0]}"
version="${archive##*/Cage-}"
version="${version%-macos-arm64.dmg}"
guest_source="$directory/cage-macos-guest-$version-source.tar.gz"
[[ -f "$guest_source" ]] || { echo 'Expected the matching macOS guest corresponding source archive.' >&2; exit 1; }
# The release may also contain Windows installers and their corresponding
# sources. Validate this platform's downloaded files against the original list.
checksum_input="$(mktemp)"
trap 'rm -f "$checksum_input"' EXIT
awk -v archive="${archive##*/}" -v source="${sources[0]##*/}" -v guest="${guest_source##*/}" '
  NF == 2 && ($2 == archive || $2 == "*" archive) { archives++; print }
  NF == 2 && ($2 == source || $2 == "*" source) { sources++; print }
  NF == 2 && ($2 == guest || $2 == "*" guest) { guests++; print }
  END { if (archives != 1 || sources != 1 || guests != 1) exit 1 }
' "$directory/SHA256SUMS" > "$checksum_input"
(cd "$directory" && shasum -a 256 --check "$checksum_input")
rm -f "$checksum_input"
trap - EXIT
codesign --verify --strict --verbose=2 "$archive"
validate_ticket "$archive"
spctl --assess --type open --context context:primary-signature --verbose=2 "$archive"
mount="$(mktemp -d)"
cleanup() { hdiutil detach "$mount" >/dev/null 2>&1 || true; rmdir "$mount" 2>/dev/null || true; }
trap cleanup EXIT
hdiutil attach -readonly -nobrowse -mountpoint "$mount" "$archive" >/dev/null
bundle="$mount/Cage.app"
test "$(/usr/libexec/PlistBuddy -c 'Print :CageVersion' "$bundle/Contents/Info.plist")" = "$version"
codesign --verify --deep --strict --verbose=2 \
  -R='anchor apple generic and identifier "io.github.lee-xeo.claude-cage" and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists' "$bundle"
validate_ticket "$bundle"
spctl --assess --type execute --verbose=2 "$bundle"
cmp "$guest_source" "$bundle/Contents/Resources/guest-os/sources/${guest_source##*/}"
script_dir="$(cd "$(dirname "$0")" && pwd)"
node "$script_dir/guest-os/collect-macos-sshfs-sources.mjs" verify "$guest_source" \
  "$bundle/Contents/Resources/guest-os/project/cage-sshfs" "$version"
echo "Verified Developer ID, notarization, Gatekeeper, version and corresponding source: Cage $version"
