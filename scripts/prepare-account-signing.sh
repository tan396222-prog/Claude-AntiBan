#!/usr/bin/env bash
# Optional Developer ID provisioning for macOS biometric Keychain access.
set -euo pipefail
profile="$1"
team="$2"
output="$3"
bundle_id=io.github.lee-xeo.claude-cage
[[ "$team" =~ ^[A-Z0-9]{10}$ ]] || { echo 'A valid Developer ID team is required.' >&2; exit 1; }
security cms -D -i "$profile" -o "$output.profile.plist"
profile_team=$(/usr/libexec/PlistBuddy -c 'Print :TeamIdentifier:0' "$output.profile.plist")
profile_app=$(/usr/libexec/PlistBuddy -c 'Print :Entitlements:com.apple.application-identifier' "$output.profile.plist")
[[ "$profile_team" == "$team" && "$profile_app" == "$team.$bundle_id" ]] || { echo 'Provisioning profile does not match the signing team and Cage bundle.' >&2; exit 1; }
cat > "$output" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>com.apple.application-identifier</key><string>$team.$bundle_id</string>
<key>com.apple.developer.team-identifier</key><string>$team</string>
<key>keychain-access-groups</key><array><string>$team.$bundle_id</string></array>
</dict></plist>
PLIST
