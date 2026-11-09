#!/usr/bin/env bash
set -euo pipefail
# Existing environments can still open Desktop before their next maintenance
# update installs IBus. Their established committed-text path remains usable.
if ! command -v ibus-daemon >/dev/null || ! /usr/bin/python3 -c 'import gi; gi.require_version("IBus", "1.0")' 2>/dev/null; then
  exec "$@"
fi
umask 077
input_root="$HOME/.cache/cage-input"
mkdir -p "$input_root"
chmod 700 "$input_root"
export IBUS_ADDRESS="unix:path=$input_root/bus"
export GTK_IM_MODULE=ibus QT_IM_MODULE=ibus XMODIFIERS=@im=ibus
bus_pid='' bridge_pid='' app_pid='' session_pid=''
cleanup() {
  if [ -n "$app_pid" ]; then
    kill -TERM "$app_pid" 2>/dev/null || true
    # Let Node close Chromium and flush its profile before closing the buses.
    wait "$app_pid" 2>/dev/null || true
  fi
  for pid in "$bridge_pid" "$bus_pid" "$session_pid"; do
    if [ -n "$pid" ]; then kill -TERM "$pid" 2>/dev/null || true; fi
  done
  rm -f "$input_root/ready" "$input_root/input.sock"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
# GTK's IBus portal needs a shared session bus. For the browser, allow only
# that service to activate, preserving its existing absence of a desktop
# keyring/portal session. Desktop keeps its already-owned keyring bus.
if [ -z "${DBUS_SESSION_BUS_ADDRESS:-}" ]; then
  mkdir -p "$input_root/services"
  cp /usr/share/dbus-1/services/org.freedesktop.portal.IBus.service "$input_root/services/"
  cat > "$input_root/session.conf" <<CONFIG
<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN" "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type><listen>unix:path=$input_root/session-bus</listen><auth>EXTERNAL</auth>
  <servicedir>$input_root/services</servicedir>
  <policy context="default"><allow send_destination="*"/><allow receive_sender="*"/><allow own="*"/></policy>
</busconfig>
CONFIG
  rm -f "$input_root/session-bus"
  dbus-daemon --nofork --config-file="$input_root/session.conf" >/dev/null 2>&1 &
  session_pid=$!
  for i in $(seq 1 100); do
    kill -0 "$session_pid"
    if [ -S "$input_root/session-bus" ]; then break; fi
    sleep .05
  done
  test -S "$input_root/session-bus"
  export DBUS_SESSION_BUS_ADDRESS="unix:path=$input_root/session-bus"
fi
rm -f "$input_root/ready" "$input_root/bus" "$input_root/input.sock"
ibus-daemon --single --panel=disable --config=disable --emoji-extension=disable --cache=none --address="$IBUS_ADDRESS" &
bus_pid=$!
for i in $(seq 1 100); do
  kill -0 "$bus_pid"
  if [ -S "$input_root/bus" ]; then break; fi
  sleep .05
done
/usr/local/bin/cage-native-input &
bridge_pid=$!
for i in $(seq 1 100); do
  kill -0 "$bridge_pid"
  if [ -f "$input_root/ready" ]; then break; fi
  sleep .05
done
test -f "$input_root/ready"
"$@" &
app_pid=$!
wait "$app_pid"
