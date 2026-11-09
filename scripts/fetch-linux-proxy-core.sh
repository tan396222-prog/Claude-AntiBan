#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
directory="$PWD/_output/linux-deps"
mkdir -p "$directory"
for component in mihomo mihomoSource; do
  metadata=$(node -e 'const d=JSON.parse(require("fs").readFileSync("packaging/linux/dependencies.json"))[process.argv[1]]; console.log([d.file,d.url,d.sha256].join("\t"))' "$component")
  IFS=$'\t' read -r file url checksum <<< "$metadata"
  archive="$directory/$file"
  if [[ ! -f "$archive" ]]; then
    curl --fail --location --retry 2 --connect-timeout 15 --max-time 300 "$url" -o "$archive.part"
    mv "$archive.part" "$archive"
  fi
  actual=$(shasum -a 256 "$archive" | cut -d ' ' -f 1)
  [[ "$actual" == "$checksum" ]] || { echo "Checksum mismatch: $file" >&2; exit 1; }
done
gzip -dc "$directory/mihomo-linux-amd64-compatible-v1.19.33.gz" > "$directory/mihomo.part"
chmod 755 "$directory/mihomo.part"
mv "$directory/mihomo.part" "$directory/mihomo"
tar -xOf "$directory/mihomo-v1.19.33-source.tar.gz" mihomo-1.19.33/LICENSE > "$directory/LICENSE"
cat > "$directory/NOTICE" <<'EOF'
Mihomo v1.19.33, unmodified executable distributed under GPL-3.0.
Source: https://github.com/MetaCubeX/mihomo/tree/v1.19.33
Corresponding source accompanies the Cage Linux release as mihomo-v1.19.33-source.tar.gz.
Binary checksum and download URL: packaging/linux/dependencies.json in Cage sources.
EOF
