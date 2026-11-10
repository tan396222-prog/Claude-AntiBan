#!/bin/bash
# Build the per-user clipboard agent and its bounded file-transfer helper.
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
app=${1:?Specify a new output .app path}
[[ $app == /*.app && ! -e $app ]] || { echo 'Choose a new absolute .app path.' >&2; exit 2; }
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.cage.GuestClipboard</string>
<key>CFBundleName</key><string>Cage Guest Clipboard</string>
<key>CFBundleExecutable</key><string>CageGuestClipboard</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>1.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSUIElement</key><true/>
</dict></plist>
PLIST
xcrun swiftc -parse-as-library -O -target arm64-apple-macos14.0 "$src/guest-clipboard-macos.swift" "$src/macos-diagnostics.swift" -o "$app/Contents/MacOS/CageGuestClipboard"
(cd "$src/../.." && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '-s -w' -o "$app/Contents/Resources/cage-guest-files" ./cmd/cage-guest-files)
identity=${CAGE_GUEST_SIGN_IDENTITY:-${CAGE_SIGNING_IDENTITY:--}}
codesign --force --sign "$identity" "$app/Contents/Resources/cage-guest-files"
codesign --force --sign "$identity" --identifier dev.cage.GuestClipboard "$app"
codesign --verify --deep --strict "$app"
