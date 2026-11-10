#!/bin/bash
set -eu
export DISPLAY=:78 XAUTHORITY=/run/cage-desktop/Xauthority
export XDG_RUNTIME_DIR=/run/cage-phone-desktop
exec /usr/bin/python3 /usr/local/lib/cage-phone-desktop/view.py "$@"
