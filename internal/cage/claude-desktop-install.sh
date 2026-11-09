#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$HOME/.config/claude-cage"
exec 9>"$HOME/.config/claude-cage/desktop-install.lock"
flock -n 9 || { echo 'Claude Desktop installation is already running.' >&2; exit 1; }
if systemctl is-active --quiet cage-claude-desktop.service; then
  echo '请先保存工作并关闭 Claude Desktop，再更新或修复安装。' >&2
  exit 1
fi
case "$(dpkg --print-architecture)" in arm64|amd64) ;; *) echo 'Unsupported desktop architecture.' >&2; exit 1;; esac
printf '正在准备安装依赖…\n'
sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get -o DPkg::Lock::Timeout=60 update </dev/null
# Satisfy the vendor's kde-cli-tools | ... | libglib2.0-bin dependency before
# resolving Claude, so apt does not pull in a KDE/Qt desktop toolchain.
sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get -o DPkg::Lock::Timeout=60 install -y --no-install-recommends ca-certificates curl gnupg dbus-x11 xauth x11-utils x11-xserver-utils tigervnc-standalone-server tigervnc-scraping-server tigervnc-tools openbox gnome-keyring gcr bubblewrap socat openssh-client libglib2.0-bin ibus ibus-gtk3 gir1.2-ibus-1.0 python3-gi </dev/null
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
printf '正在验证 Anthropic 安装源…\n'
cage_curl_download "$temporary/key.asc" --proto '=https' --proto-redir '=https' -fSL --connect-timeout 15 https://downloads.claude.ai/claude-desktop/key.asc -o "$temporary/key.asc"
fingerprint=$(gpg --batch --show-keys --with-colons "$temporary/key.asc" | awk -F: '$1 == "fpr" {print $10; exit}')
test "$fingerprint" = 31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE || { echo 'Anthropic signing key did not match; installation stopped.' >&2; exit 1; }
sudo install -m 0644 "$temporary/key.asc" /usr/share/keyrings/claude-desktop-archive-keyring.asc
printf '%s\n' 'deb [arch=amd64,arm64 signed-by=/usr/share/keyrings/claude-desktop-archive-keyring.asc] https://downloads.claude.ai/claude-desktop/apt/stable stable main' | sudo tee /etc/apt/sources.list.d/claude-desktop.list >/dev/null
sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get -o DPkg::Lock::Timeout=60 update </dev/null
printf '正在下载并安装 Claude Desktop…\n'
# Cowork's nested VM packages are intentionally optional; Chat and Code share
# the official, unmodified desktop binary. Apt verifies repository signatures.
sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get -o DPkg::Lock::Timeout=60 install -y --no-install-recommends claude-desktop </dev/null
test "$(dpkg-query -W -f='${Status}' claude-desktop)" = 'install ok installed'
printf 'Claude Desktop 已安装。\n'
