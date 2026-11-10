#!/bin/bash
# Extract the pinned vendor installer on the host. Nothing is installed here.
set -euo pipefail
[[ $# == 2 && $1 == /* && $2 == /* && ! -e $2 ]]
work=$(mktemp -d)
volume="$work/volume"
attached=0
cleanup() {
    if [[ $attached == 1 ]]; then hdiutil detach "$volume" >/dev/null || return; fi
    rm -rf "$work"
}
trap cleanup EXIT
mkdir "$volume"
[[ $(shasum -a 256 "$1" | cut -d' ' -f1) == 92c21ef2db4199bcf5bddc8e6efb57b23ecb9715f74ab38cba2c77ab226d3830 ]]
hdiutil attach -readonly -nobrowse -noautoopen -mountpoint "$volume" "$1" >/dev/null
attached=1
pkg="$volume/Install macFUSE.pkg"
[[ -n $pkg && $(shasum -a 256 "$pkg" | cut -d' ' -f1) == 5acf9e30bab227707de8d15eb28472a0cc9ed45709846b3e067d283294dc7f9e ]]
cp "$pkg" "$2"
