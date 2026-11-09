#!/usr/bin/env bash
set -euo pipefail
# Keep dbus-daemon and other children from announcing readiness for the app.
if [ "${CAGE_DESKTOP_SESSION:-}" != 1 ]; then
  export CAGE_DESKTOP_SESSION=1 CAGE_NOTIFY_SOCKET="$NOTIFY_SOCKET"
  unset NOTIFY_SOCKET
  exec dbus-run-session -- "$0"
fi
export DISPLAY=:78 XAUTHORITY=/run/cage-desktop/Xauthority
export XDG_RUNTIME_DIR=/run/cage-desktop XDG_CURRENT_DESKTOP=Openbox
export PATH="$HOME/.local/bin:$PATH" BROWSER=/usr/local/bin/cage-open
dbus-update-activation-environment DISPLAY XAUTHORITY XDG_RUNTIME_DIR XDG_CURRENT_DESKTOP
umask 077
touch "$XAUTHORITY"
xauth -f "$XAUTHORITY" add "$DISPLAY" . "$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
printf '%s' "$DBUS_SESSION_BUS_ADDRESS" > "$XDG_RUNTIME_DIR/bus-address"
password="$HOME/.config/claude-cage/desktop-password"
if [ ! -s "$password" ]; then
  mkdir -p "$(dirname "$password")"
  od -An -N4 -tx1 /dev/urandom | tr -d ' \n' > "$password"
fi
tigervncpasswd -f < "$password" > "$XDG_RUNTIME_DIR/vnc-password"
Xtigervnc "$DISPLAY" -geometry 1600x1000 -depth 24 -rfbport -1 -rfbunixpath "$XDG_RUNTIME_DIR/display.sock" -rfbunixmode 0600 -nolisten tcp -auth "$XAUTHORITY" -SecurityTypes VncAuth -PasswordFile "$XDG_RUNTIME_DIR/vnc-password" &
display_pid=$!
trap 'kill "$display_pid" 2>/dev/null || true' EXIT
for i in $(seq 1 100); do
  if xdpyinfo -display "$DISPLAY" >/dev/null 2>&1; then break; fi
  kill -0 "$display_pid"
  sleep .1
done
xdpyinfo -display "$DISPLAY" >/dev/null
openbox --config-file /etc/claude-cage/desktop-openbox.xml &
X0tigervnc -display "$DISPLAY" -rfbport -1 -rfbunixpath "$XDG_RUNTIME_DIR/vnc.sock" -rfbunixmode 0600 -SecurityTypes VncAuth -PasswordFile "$XDG_RUNTIME_DIR/vnc-password" -AlwaysShared &
mkfifo -m 0600 "$XDG_RUNTIME_DIR/unlock"
gnome-keyring-daemon --unlock --components=secrets < "$XDG_RUNTIME_DIR/unlock" >/dev/null
rm "$XDG_RUNTIME_DIR/unlock"
# Do not replace or erase an existing keyring when the supplied key is wrong.
keyring_unlocked() {
  gdbus call --session --dest org.freedesktop.secrets --object-path /org/freedesktop/secrets/aliases/login --method org.freedesktop.DBus.Properties.Get org.freedesktop.Secret.Collection Locked 2>/dev/null | grep -q false &&
  gdbus call --session --dest org.freedesktop.secrets --object-path /org/freedesktop/secrets/aliases/default --method org.freedesktop.DBus.Properties.Get org.freedesktop.Secret.Collection Locked 2>/dev/null | grep -q false
}
for i in $(seq 1 50); do
  if keyring_unlocked; then break; fi
  sleep .1
done
keyring_unlocked || { echo '无法解锁已有密钥环，登录数据未修改。' >&2; exit 1; }
touch "$XDG_RUNTIME_DIR/keyring-ready"
# RemoteView displays two framebuffer pixels per CSS pixel, as for Chromium.
# Scale the application's layout too so controls retain their intended size.
/usr/local/bin/cage-desktop-files &
files_pid=$!
for i in $(seq 1 100); do
  kill -0 "$files_pid"
  if [ -S "$XDG_RUNTIME_DIR/files.sock" ]; then break; fi
  sleep .1
done
test -S "$XDG_RUNTIME_DIR/files.sock"
export GTK_USE_PORTAL=1
/usr/local/bin/cage-input-session claude-desktop --force-device-scale-factor=2 --xdg-portal-required-version=3 &
app_pid=$!
# The service becomes ready only after the application has opened a window.
for i in $(seq 1 200); do
  kill -0 "$app_pid"
  if xprop -root _NET_CLIENT_LIST 2>/dev/null | grep -q '0x[1-9a-f]'; then
    NOTIFY_SOCKET="$CAGE_NOTIFY_SOCKET" systemd-notify --ready
    break
  fi
  sleep .1
done
wait "$app_pid"
