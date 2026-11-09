#!/usr/bin/env bash
# Inputs: trusted Linux Cage build, the verified guest build directory, Docker archives.
set -euo pipefail
cd "$(dirname "$0")/../.."
: "${CAGE_SERVER_BINARY:?Specify the Linux amd64 Cage binary}"
: "${CAGE_GUEST_BUILD:?Specify the build-guest.sh output directory}"
: "${CAGE_DOCKER_DOWNLOADS:?Specify the directory containing the two pinned Docker archives}"
target="${CAGE_KIT_OUTPUT:-$PWD/_output/cloud-kit}"
mkdir -p "$target"
stage=$(mktemp -d "$target/kit.XXXXXX")
trap 'rm -rf "$stage"' EXIT
(cd "$CAGE_GUEST_BUILD"; shasum -a 256 -c SHA256SUMS)
grep -Exq 'sha256:[a-f0-9]{64}' "$CAGE_GUEST_BUILD/image-id"
cp "$CAGE_GUEST_BUILD/image-id" "$stage/"
zstd -q -T2 -4 "$CAGE_GUEST_BUILD/cage-guest.tar" -o "$stage/guest.tar.zst"
cp "$CAGE_SERVER_BINARY" "$stage/cage"
bash scripts/fetch-linux-proxy-core.sh
cp _output/linux-deps/mihomo "$stage/"
cp deploy/cloud/*.service "$stage/"
while read -r digest archive; do
  cp "$CAGE_DOCKER_DOWNLOADS/$archive" "$stage/"
  (cd "$stage"; printf '%s  %s\n' "$digest" "$archive" | shasum -a 256 -c -)
done < <(node -e 'for(const a of require("./deploy/cloud/runtime-artifacts.json").artifacts)console.log(a.sha256,a.file)')
name=cloud-linux-amd64.tar
COPYFILE_DISABLE=1 tar -cf "$stage/$name" -C "$stage" cage mihomo cage-container-engine.service cage-server.service image-id guest.tar.zst docker-29.9.1.tgz docker-rootless-extras-29.9.1.tgz
shasum -a 256 "$stage/$name" | cut -d' ' -f1 > "$stage/$name.sha256"
mv "$stage/$name" "$stage/$name.sha256" "$target/"
