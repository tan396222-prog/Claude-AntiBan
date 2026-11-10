#!/bin/bash
# Register the ordinary guest user's persistent browser service.
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
[[ $(sysctl -n hw.model) =~ ^VirtualMac[0-9]+,[0-9]+$ && $(cat /var/db/cage-guest-os) == macos ]] || exit 2
[[ $# == 0 || ( $# == 1 && $1 == --prepare ) ]] || exit 2
uid=$(id -u)
[[ $uid -ge 500 ]] || exit 2
base=/usr/local/libexec/cage-workbench-browser-4.145.0-pw1.65.0
[[ -x $base/editor/lib/node && -f $base/controller/macos-native-browser.mjs && -f $base/controller/macos-browser-service.mjs ]]
label=dev.cage.GuestBrowser
cache="$HOME/Library/Caches/$label"
agents="$HOME/Library/LaunchAgents"
agent="$agents/$label.plist"
umask 077
mkdir -p "$cache" "$agents"
[[ ! -L $cache && ! -L $agents && $(stat -f %u "$cache") == "$uid" && $(stat -f %u "$agents") == "$uid" ]]
chmod 700 "$cache"
temp=$(mktemp "$cache/plist.XXXXXXXX")
trap 'rm -f "$temp"' EXIT
plutil -create xml1 "$temp"
plutil -insert Label -string "$label" "$temp"
plutil -insert ProgramArguments -xml '<array/>' "$temp"
plutil -insert ProgramArguments.0 -string "$base/editor/lib/node" "$temp"
plutil -insert ProgramArguments.1 -string "$base/controller/macos-native-browser.mjs" "$temp"
plutil -insert ProgramArguments.2 -string '--service' "$temp"
plutil -insert LimitLoadToSessionType -string Aqua "$temp"
plutil -insert RunAtLoad -bool true "$temp"
plutil -insert KeepAlive -xml '<dict><key>SuccessfulExit</key><false/></dict>' "$temp"
[[ ! -L $agent && ( ! -e $agent || ( -f $agent && $(stat -f %u "$agent") == "$uid" ) ) ]]
mv -f "$temp" "$agent"
[[ ${1:-} == --prepare ]] && exit 0
if ! launchctl print "gui/$uid/$label" >/dev/null 2>&1; then
    launchctl bootstrap "gui/$uid" "$agent"
fi
