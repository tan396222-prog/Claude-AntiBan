#!/usr/bin/env bash
# Publish a Linux release of this fork so installed apps offer it as a
# one-click in-app update.
#
#   scripts/release-antiban.sh 0.1.7-rc.1.antiban.4 notes.md
#
# Needs: x86_64 Debian 13 / Ubuntu 24.04+, Go, Node, gh (logged in, repo scope),
# pkg-config libwebkit2gtk-4.1-dev libgtk-3-dev libjson-glib-dev dpkg-dev.
# The version must be higher than the previous release (semver), e.g. bump the
# last number: ...antiban.3 -> ...antiban.4, or move to 0.1.8 / 0.1.8-antiban.1.
# SKIP_BUILD=1 reuses an existing _output/releases/VERSION/linux build.
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:?usage: scripts/release-antiban.sh VERSION NOTES.md}
notes=${2:?usage: scripts/release-antiban.sh VERSION NOTES.md}
repo=tan396222-prog/Claude-AntiBan
[[ -f "$notes" ]] || { echo "Missing release notes: $notes" >&2; exit 1; }
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]] || { echo 'Invalid version (no "-" inside the pre-release part).' >&2; exit 1; }
printf '%s\n' "$version" > VERSION
for d in ui www; do
  node -e 'const fs=require("fs");const [d,v]=process.argv.slice(1);for(const f of ["package.json","package-lock.json"]){const p=d+"/"+f;const j=JSON.parse(fs.readFileSync(p));j.version=v;if(j.packages&&j.packages[""])j.packages[""].version=v;fs.writeFileSync(p,JSON.stringify(j,null,2)+"\n");}' "$d" "$version"
done
node scripts/check-version.mjs
[[ "${SKIP_BUILD:-}" == 1 ]] || bash scripts/package-linux.sh
out="_output/releases/$version/linux"
deb="$out/Cage-$version-linux-amd64.deb"
[[ -f "$deb" && -f "$out/SHA256SUMS" ]] || { echo "Missing build output in $out" >&2; exit 1; }
git add VERSION ui/package.json ui/package-lock.json www/package.json www/package-lock.json
git diff --cached --quiet || git commit -m "Release $version"
git tag -a "v$version" -m "Claude 防封 v$version"
git push origin HEAD
git push origin "v$version"
# Must be a published (not draft) release with the .deb and SHA256SUMS: the
# in-app updater looks for Cage-VERSION-linux-amd64.deb on tag vVERSION.
gh release create "v$version" -R "$repo" --title "Claude 防封 v$version" --notes-file "$notes" --latest --verify-tag \
  "$deb" "$out/SHA256SUMS" "$out"/mihomo-*-source.tar.gz
