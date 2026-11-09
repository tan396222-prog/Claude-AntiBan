#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
binary="$temporary/account-device-tests"
case "${1:-}" in
  '') ;;
  --biometric)
    : "${CAGE_SIGNING_IDENTITY:?Biometric tests require a Developer ID identity}"
    : "${CAGE_PROVISIONING_PROFILE:?Biometric tests require a provisioning profile}"
    bundle="$temporary/Cage.app"
    mkdir -p "$bundle/Contents/MacOS"
    cp "$CAGE_PROVISIONING_PROFILE" "$bundle/Contents/embedded.provisionprofile"
    cat > "$bundle/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>io.github.lee-xeo.claude-cage</string><key>CFBundleExecutable</key><string>Cage</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>
PLIST
    binary="$bundle/Contents/MacOS/Cage"
    ;;
  *) echo 'Usage: scripts/test-account-device.sh [--biometric]' >&2; exit 2 ;;
esac
swiftc -O -parse-as-library -framework Security -framework LocalAuthentication \
  desktop/AccountDevice.swift desktop/AccountDeviceTests.swift -o "$binary"
if [[ "${1:-}" == --biometric ]]; then
  codesign --force --sign "$CAGE_SIGNING_IDENTITY" --timestamp=none "$binary"
  signing_team="$(codesign -dv --verbose=4 "$binary" 2>&1 | sed -n 's/^TeamIdentifier=//p')"
  bash scripts/prepare-account-signing.sh "$CAGE_PROVISIONING_PROFILE" "$signing_team" "$temporary/account.entitlements"
  codesign --force --sign "$CAGE_SIGNING_IDENTITY" --options runtime --timestamp=none \
    --entitlements "$temporary/account.entitlements" "$bundle"
fi
"$binary" "$@"
