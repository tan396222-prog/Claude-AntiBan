#!/usr/bin/env bash
set -euo pipefail
sudo ln -sf "/usr/share/zoneinfo/$CAGE_TZ" /etc/localtime
printf '%s\n' "$CAGE_TZ" | sudo tee /etc/timezone >/dev/null
if ! locale -a | tr '[:upper:]' '[:lower:]' | grep -qx "${CAGE_LOCALE,,}" && ! locale -a | grep -q "${CAGE_LOCALE%%.*}.utf8"; then
  printf '%s UTF-8\n' "$CAGE_LOCALE" | sudo tee -a /etc/locale.gen >/dev/null
  sudo locale-gen >/dev/null
fi
printf 'TZ=%s\nLANG=%s\nLC_ALL=%s\nCAGE_LANGUAGE=%s\nBROWSER=/usr/local/bin/cage-open\nPULSE_SERVER=unix:/run/cage-audio/native\n' "$CAGE_TZ" "$CAGE_LOCALE" "$CAGE_LOCALE" "$CAGE_LANGUAGE" | sudo tee /etc/claude-cage/environment >/dev/null
sudo update-locale "LANG=$CAGE_LOCALE" "LC_ALL=$CAGE_LOCALE"
set -a
. /etc/claude-cage/environment
set +a
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY FTP_PROXY NO_PROXY http_proxy https_proxy all_proxy ftp_proxy no_proxy

# Only the scoped installer receives this variable; services load their fixed environment.
if [ -n "${CAGE_INSTALL_PROXY:-}" ]; then
  export http_proxy="$CAGE_INSTALL_PROXY" https_proxy="$CAGE_INSTALL_PROXY" HTTP_PROXY="$CAGE_INSTALL_PROXY" HTTPS_PROXY="$CAGE_INSTALL_PROXY"
fi
