#!/bin/sh
# Run as root before any dependency transaction. Keep the bootstrap independent
# of python3/curl and of archive mirrors that may have triggered APT's retry bug.
set -eu
. /etc/os-release
if [ "$ID" != debian ] || [ "${VERSION_ID:-}" != 13 ]; then
  exit 0
fi
version=$(dpkg-query -W -f='${Version}' apt)
if dpkg --compare-versions "$version" ge 3.0.4; then
  exit 0
fi
printf 'Updating Debian APT %s to a version with the delayed-item memory fix...\n' "$version"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
chmod 0755 "$stage"
mkdir -m 0755 "$stage/lists"
# HTTP also works in minimal images without a CA bundle. APT still requires the
# Debian archive signature and package hashes; no insecure options are enabled.
printf '%s\n' 'deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian-security trixie-security main' > "$stage/security.list"
apt_bootstrap_get() {
  env DEBIAN_FRONTEND=noninteractive apt-get \
    -o Acquire::Retries=0 -o Acquire::http::Timeout=15 -o Acquire::https::Timeout=15 \
    -o DPkg::Lock::Timeout=60 -o APT::Update::Error-Mode=any \
    -o Dir::Etc::sourcelist="$stage/security.list" -o Dir::Etc::sourceparts=- \
    -o Dir::State::lists="$stage/lists" "$@" </dev/null
}
apt_bootstrap_get update
packages='apt libapt-pkg7.0'
if [ "$(dpkg-query -W -f='${Status}' apt-utils 2>/dev/null || true)" = 'install ok installed' ]; then
  packages="$packages apt-utils"
fi
# The package names above are fixed, and --only-upgrade preserves optional tools.
apt_bootstrap_get install -y --only-upgrade --no-install-recommends $packages
version=$(dpkg-query -W -f='${Version}' apt)
if ! dpkg --compare-versions "$version" ge 3.0.4; then
  printf 'APT is still %s; dependency installation stopped. Check the Debian security source and retry.\n' "$version" >&2
  exit 1
fi
printf 'Debian APT %s is ready.\n' "$version"
