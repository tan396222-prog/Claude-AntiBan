#!/bin/sh
# The Debian cloud image uses separate lists for its archive and security
# updates. Replace only the stock archive list, preserving customized sources.
if [ -f /etc/apt/mirrors/debian.list ] && [ "$(cat /etc/apt/mirrors/debian.list)" = "https://deb.debian.org/debian" ]; then
  sudo sh -eu <<'CAGE_DEBIAN_MIRRORS'
list=/etc/apt/mirrors/debian.list
stage=$(mktemp "$list.XXXXXX")
trap 'rm -f "$stage"' EXIT
printf '%s\tpriority:%s\n' \
  https://mirrors.nju.edu.cn/debian 1 \
  https://mirrors.ustc.edu.cn/debian 2 \
  https://deb.debian.org/debian 3 > "$stage"
chmod 0644 "$stage"
mv -f "$stage" "$list"
CAGE_DEBIAN_MIRRORS
fi
