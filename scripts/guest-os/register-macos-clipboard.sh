#!/bin/bash
# Register only the intended guest user's private socket-activated GUI agent.
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
[[ $(sysctl -n hw.model) =~ ^VirtualMac[0-9]+,[0-9]+$ && $(cat /var/db/cage-guest-os) == macos ]] || exit 2
[[ $# == 0 || ( $# == 1 && $1 == --prepare ) ]] || exit 2
uid=$(id -u)
[[ $uid -ge 500 && ( ${1:-} == --prepare || $(stat -f %u /dev/console) == "$uid" ) ]] || { echo 'Log into the guest as the intended desktop user.' >&2; exit 4; }
app='/Applications/Cage Guest Clipboard.app'
codesign --verify --deep --strict -R '=identifier "dev.cage.GuestClipboard"' "$app"
label=dev.cage.GuestClipboard
cache="$HOME/Library/Caches/$label"
agent="$HOME/Library/LaunchAgents/$label.plist"
umask 077
mkdir -p "$HOME/Library/LaunchAgents" "$cache"
[[ ! -L $cache && $(stat -f %u "$cache") == "$uid" ]] || exit 2
chmod 700 "$cache"
temp=$(mktemp "$cache/plist.XXXXXXXX")
trap 'rm -f "$temp"' EXIT
plutil -create xml1 "$temp"
plutil -insert Label -string "$label" "$temp"
plutil -insert ProgramArguments -xml '<array/>' "$temp"
plutil -insert ProgramArguments.0 -string "$app/Contents/MacOS/CageGuestClipboard" "$temp"
plutil -insert LimitLoadToSessionType -string Aqua "$temp"
plutil -insert Sockets -xml '<dict><key>Clipboard</key><dict/></dict>' "$temp"
plutil -insert Sockets.Clipboard.SockPathName -string "$cache/clipboard.sock" "$temp"
plutil -insert Sockets.Clipboard.SockPathMode -integer 384 "$temp"
if [[ -f $agent ]] && ! cmp -s "$agent" "$temp"; then
    echo 'An existing clipboard LaunchAgent has different settings.' >&2
    exit 2
fi
mv -f "$temp" "$agent"
[[ ${1:-} == --prepare ]] && exit 0
if ! launchctl print "gui/$uid/$label" >/dev/null 2>&1; then
    launchctl bootstrap "gui/$uid" "$agent"
fi
