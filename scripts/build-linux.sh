#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { echo 'Build on x86_64 Linux (Ubuntu 22.04 baseline).' >&2; exit 1; }
case "${1:-}" in ''|--prebuilt-backend) ;; *) echo 'Usage: scripts/build-linux.sh [--prebuilt-backend]' >&2; exit 2 ;; esac
version=$(tr -d '\r\n' < VERSION)
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || { echo 'Invalid VERSION.' >&2; exit 1; }
pkg-config --atleast-version=2.40 webkit2gtk-4.1
pkg-config --exists gtk+-3.0 json-glib-1.0
bash scripts/fetch-linux-proxy-core.sh
if [[ "${1:-}" != --prebuilt-backend ]]; then
  make cage GOOS=linux GOARCH=amd64 CGO_ENABLED=0 VERSION="$version"
else
  # For constrained remote test hosts. The caller supplies the backend built
  # from this exact source tree, and npm licenses from a locked npm ci.
  [[ -x _output/bin/cage ]] || { echo 'Missing prebuilt Linux backend.' >&2; exit 1; }
  _output/bin/cage help >/dev/null
fi
_output/bin/cage verify-web-assets
mkdir -p _output/linux-amd64
stage=$(mktemp -d "$PWD/_output/linux-amd64/build.XXXXXX")
trap 'rm -rf "$stage"' EXIT
app="$stage/app"
mkdir -p "$app/licenses/mihomo"
cc -std=c11 -O2 -Wall -Wextra -Werror -Wno-deprecated-declarations \
  "-DCAGE_VERSION=\"$version\"" desktop/linux/cage-desktop.c \
  $(pkg-config --cflags --libs gtk+-3.0 webkit2gtk-4.1 json-glib-1.0) \
  -o "$app/cage-desktop"
install -m 755 _output/bin/cage "$app/cage"
node scripts/bundle-cloud-kit.mjs "$app"
install -m 755 _output/linux-deps/mihomo "$app/mihomo"
cp _output/linux-deps/{LICENSE,NOTICE} "$app/licenses/mihomo/"
if [[ "${1:-}" == --prebuilt-backend && -d _output/linux-amd64/prebuilt-licenses ]]; then
  cp -a _output/linux-amd64/prebuilt-licenses/. "$app/licenses/"
else
  node scripts/collect-app-licenses.mjs "$app/licenses"
fi
"$app/cage" help >/dev/null
"$app/cage-desktop" --version
"$app/cage-desktop" --self-test
"$app/mihomo" -v
printf '%s\n' "$version" > "$app/VERSION"
# Build output only; installed files are changed exclusively by dpkg/apt.
rm -rf _output/linux-amd64/app
mv "$app" _output/linux-amd64/app
printf 'Built: %s/_output/linux-amd64/app\n' "$PWD"
