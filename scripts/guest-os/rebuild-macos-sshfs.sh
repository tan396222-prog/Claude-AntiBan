#!/bin/bash
# Relink SSHFS from the frozen source distribution. An optional fourth argument
# selects an already extracted/patched source tree for local modifications.
set -euo pipefail
[[ $# == 3 || $# == 4 ]] || { echo 'Usage: rebuild-macos-sshfs.sh /source-bundle /macfuse-sdk/usr/local /output [patched-source-tree]' >&2; exit 2; }
materials=$1
sdk=$2
output=$3
[[ $materials == /* && $sdk == /* && $output == /* && ! -e $output && -f $sdk/include/fuse3/fuse.h ]]
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
if [[ $# == 4 ]]; then
  cd "$4"
else
  tar -xf "$materials/upstream/sshfs.tar.gz" -C "$work" --strip-components 1
  cd "$work"
  patch -p1 < "$materials/cage/sshfs-openwith.patch"
fi
cat > config.h <<'HEADER'
#define PACKAGE_VERSION "3.7.6"
#define IDMAP_DEFAULT "user"
HEADER
export MACOSX_DEPLOYMENT_TARGET=${MACOSX_DEPLOYMENT_TARGET:-14.0}
glib=$materials/dependencies/glib
intl=$materials/dependencies/gettext
pcre=$materials/dependencies/pcre2
clang -arch arm64 -O2 -D_REENTRANT -DHAVE_CONFIG_H -DFUSE_DARWIN_ENABLE_EXTENSIONS=0 -DFUSE_USE_VERSION=31 \
  -I. -Icompat -I"$sdk/include/fuse3" -I"$glib/include/glib-2.0" -I"$glib/lib/glib-2.0/include" -I"$intl/include" \
  sshfs.c cache.c compat/darwin_compat.c "$glib/lib/libgthread-2.0.a" "$glib/lib/libglib-2.0.a" "$intl/lib/libintl.a" "$pcre/lib/libpcre2-8.a" \
  -L"$sdk/lib" -lfuse3 -liconv -framework Foundation -framework CoreFoundation -framework AppKit -framework Carbon -o "$output"
