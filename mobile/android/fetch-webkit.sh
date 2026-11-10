#!/bin/bash
# Pinned AndroidX artifacts from Google's Maven repository.
set -euo pipefail
build="$1"
fetch_aar() {
  local name="$1" url="$2" digest="$3"
  if [[ ! -s "$build/$name.aar" ]]; then curl -fLsS --retry 2 "$url" -o "$build/$name.aar"; fi
  printf '%s  %s\n' "$digest" "$build/$name.aar" | shasum -a 256 --check --status
  unzip -p "$build/$name.aar" classes.jar > "$build/$name.jar"
}
fetch_aar webkit-1.14.0 https://dl.google.com/dl/android/maven2/androidx/webkit/webkit/1.14.0/webkit-1.14.0.aar 265604786ea1c9679e3f860f7f13072375c141f46db9092ed8ef5ecd599a516d
fetch_aar core-1.1.0 https://dl.google.com/dl/android/maven2/androidx/core/core/1.1.0/core-1.1.0.aar 76c7cfbe596fe3c09a6983bf1c89e889299c08ac9a3b52ce5182a088d056647e
