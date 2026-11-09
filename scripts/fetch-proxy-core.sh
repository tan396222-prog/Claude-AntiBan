#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version=v1.19.33
case "${CAGE_CORE_ARCH:-$(uname -m)}" in
  arm64) asset="mihomo-darwin-arm64-$version.gz"; checksum=060b64b0f2c9a310831db0fe4adb2878de67bfa606c311813c1f13966aa504b6 ;;
  x86_64|amd64) asset="mihomo-darwin-amd64-compatible-$version.gz"; checksum=eefe967b74312f70b64efa0dfa54929d00c7af8e94416e1b12df09863a40f35b ;;
  *) echo 'Unsupported Cage proxy-core architecture' >&2; exit 1 ;;
esac
directory=_output/proxy-core
mkdir -p "$directory"
archive="$directory/$asset"
if [[ ! -f "$archive" ]]; then
  curl --fail --location --retry 2 --connect-timeout 15 --max-time 180 "https://github.com/MetaCubeX/mihomo/releases/download/$version/$asset" -o "$archive.part"
  mv "$archive.part" "$archive"
fi
actual="$(shasum -a 256 "$archive" | cut -d ' ' -f 1)"
if [[ "$actual" != "$checksum" ]]; then echo 'Cage proxy-core archive checksum mismatch' >&2; exit 1; fi
gzip -dc "$archive" > "$directory/mihomo.part"
chmod 755 "$directory/mihomo.part"
mv "$directory/mihomo.part" "$directory/mihomo"
if [[ ! -f "$directory/LICENSE" ]]; then
  curl --fail --location --retry 2 --connect-timeout 15 --max-time 60 "https://raw.githubusercontent.com/MetaCubeX/mihomo/$version/LICENSE" -o "$directory/LICENSE.part"
  mv "$directory/LICENSE.part" "$directory/LICENSE"
fi
cat > "$directory/NOTICE" <<EOF
Mihomo $version, distributed as a separate executable by Cage.
Upstream: https://github.com/MetaCubeX/mihomo
Source for this unmodified binary: https://github.com/MetaCubeX/mihomo/archive/refs/tags/$version.tar.gz
License: GPL-3.0. See LICENSE.
Archive: $asset
SHA-256: $checksum
Release digest: https://api.github.com/repos/MetaCubeX/mihomo/releases/tags/$version
EOF
"$directory/mihomo" -v
