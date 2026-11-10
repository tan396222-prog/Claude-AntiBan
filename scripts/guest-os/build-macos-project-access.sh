#!/bin/sh
# Build the scoped project-access application for the native arm64 guest.
set -eu
[ "$#" = 1 ] || { echo "usage: $0 /path/to/CageProjectAccess.app" >&2; exit 2; }
bundle=$1
[ ! -e "$bundle" ] || { echo "Choose a new output path." >&2; exit 1; }
mkdir -p "$bundle/Contents/MacOS"
cat > "$bundle/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.cage.guest.project-access</string>
<key>CFBundleDisplayName</key><string>Cage Project Access</string>
<key>CFBundleName</key><string>Cage Project Access</string>
<key>CFBundleExecutable</key><string>CageProjectAccess</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>CFBundleShortVersionString</key><string>1.0</string>
<key>LSUIElement</key><true/>
<key>NSNetworkVolumesUsageDescription</key><string>Open the mounted project you selected in Cage.</string>
</dict></plist>
PLIST
xcrun clang -arch arm64 -mmacosx-version-min=14.0 -fobjc-arc -O2 -framework AppKit \
  "$(dirname "$0")/macos-project-access.m" -o "$bundle/Contents/MacOS/CageProjectAccess"
codesign --force --sign "${CAGE_SIGNING_IDENTITY:--}" "$bundle"
