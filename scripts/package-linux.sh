#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { echo 'Package on x86_64 Linux.' >&2; exit 1; }
case "${1:-}" in '') bash scripts/build-linux.sh ;; --skip-build) ;; *) echo 'Usage: scripts/package-linux.sh [--skip-build]' >&2; exit 2 ;; esac
version=$(tr -d '\r\n' < VERSION)
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || { echo 'Invalid VERSION.' >&2; exit 1; }
[[ "$(cat _output/linux-amd64/app/VERSION)" == "$version" ]] || { echo 'Build version does not match VERSION.' >&2; exit 1; }
deb_version="${version/-/\~}"
output="$PWD/_output/releases/$version/linux"
mkdir -p "$output"
stage=$(mktemp -d "$PWD/_output/linux-amd64/package.XXXXXX")
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage"/{DEBIAN,usr/bin,usr/lib/claude-cage,usr/share/applications,usr/share/icons/hicolor/512x512/apps,usr/share/doc/claude-cage}
cp -a _output/linux-amd64/app/. "$stage/usr/lib/claude-cage/"
# The distro already owns /usr/bin/cage (a Wayland compositor).
# Keep the backend adjacent to Mihomo, with a distinct public command.
ln -s ../lib/claude-cage/cage "$stage/usr/bin/claude-cage"
ln -s ../lib/claude-cage/cage-desktop "$stage/usr/bin/cage-desktop"
install -m 644 packaging/linux/io.github.lee_xeo.Cage.desktop "$stage/usr/share/applications/"
install -m 644 logo.png "$stage/usr/share/icons/hicolor/512x512/apps/io.github.lee_xeo.Cage.png"
install -m 644 docs/linux.md "$stage/usr/share/doc/claude-cage/README.md"
install -m 755 packaging/linux/preinst "$stage/DEBIAN/preinst"
install -m 755 packaging/linux/preinst "$stage/DEBIAN/prerm"
# dpkg-shlibdeps derives glibc/GTK dependencies from the build distribution.
# Use Debian 12 for releases; a newer build must not claim an older ABI.
mkdir -p "$stage/debian"
printf 'Source: claude-cage\nSection: devel\nPriority: optional\nMaintainer: Cage contributors <noreply@github.com>\n\nPackage: claude-cage\nArchitecture: amd64\nDescription: Cage desktop\n' > "$stage/debian/control"
dependencies=$(cd "$stage" && dpkg-shlibdeps -O usr/lib/claude-cage/cage-desktop | sed -n 's/^shlibs:Depends=//p')
[[ -n "$dependencies" ]] || { echo 'Cannot determine native dependencies.' >&2; exit 1; }
rm -rf "$stage/debian"
cat > "$stage/DEBIAN/control" <<EOF
Package: claude-cage
Version: $deb_version
Section: devel
Priority: optional
Architecture: amd64
Maintainer: Cage contributors <noreply@github.com>
Depends: $dependencies, libwebkit2gtk-4.1-0 (>= 2.40), libglib2.0-bin, ca-certificates
Suggests: qemu-system-x86, qemu-utils, ovmf, openssh-client, iproute2, git
Recommends: dbus-user-session, fonts-noto-cjk, libnotify-bin, zenity | kdialog
Homepage: https://github.com/Lee-xeo/Claude-Cage
Description: Isolated coding workspace with a persistent Linux VM
 Cage combines projects, terminals, a browser and an editor in a native
 desktop window. Select a local virtual machine or connect to a Cage server over SSH.
EOF
deb="$output/Cage-$version-linux-amd64.deb"
dpkg-deb --root-owner-group --build "$stage" "$deb"
cp _output/linux-deps/mihomo-v1.19.33-source.tar.gz "$output/"
(cd "$output" && sha256sum "$(basename "$deb")" mihomo-v1.19.33-source.tar.gz > SHA256SUMS)
printf 'Packaged: %s\n' "$deb"
