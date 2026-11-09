#!/usr/bin/env bash
set -euo pipefail
test "$#" -eq 1
test "${#1}" -le 8192
case "$1" in claude://*) ;; *) exit 2;; esac
systemctl is-active --quiet cage-claude-desktop.service || exit 1
export DISPLAY=:78 XAUTHORITY=/run/cage-desktop/Xauthority XDG_RUNTIME_DIR=/run/cage-desktop
export DBUS_SESSION_BUS_ADDRESS="$(cat /run/cage-desktop/bus-address)"
exec /usr/bin/claude-desktop "$1"
