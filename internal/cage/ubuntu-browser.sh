# shellcheck shell=bash
# Ubuntu's Chromium package is a Snap transition. Use the upstream portable
# build with extension loading and inherited CDP pipes, without snapd.
cage_is_ubuntu() (
  . /etc/os-release
  [ "$ID" = ubuntu ]
)

cage_install_ubuntu_browser() (
  set -euo pipefail
  local version="$CAGE_UBUNTU_BROWSER_VERSION" checksum="$CAGE_UBUNTU_BROWSER_SHA256" platform=linux64
  case "$(dpkg --print-architecture)" in
    amd64) ;;
    arm64) platform=linux-arm64; checksum="$CAGE_UBUNTU_BROWSER_ARM64_SHA256" ;;
    *) echo 'Ubuntu guest browsers require amd64 or arm64.' >&2; exit 1 ;;
  esac
  local directory=chrome-$platform
  local cache="$HOME/.cache/cage/downloads/$checksum" target="/opt/cage-browser/$version" stage
  mkdir -p "$cache"
  exec 8>"$cache/lock"
  flock -w 60 8
  # Package names differ after Ubuntu's time_t transition.
  local asound=libasound2 atk=libatk1.0-0 bridge=libatk-bridge2.0-0 cups=libcups2
  if . /etc/os-release; [ "$VERSION_ID" = 24.04 ]; then
    asound=libasound2t64 atk=libatk1.0-0t64 bridge=libatk-bridge2.0-0t64 cups=libcups2t64
  fi
  sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get install -y --no-install-recommends \
    "$asound" "$atk" "$bridge" "$cups" libnss3 libnspr4 libdrm2 libgbm1 libgtk-3-0 \
    libx11-6 libxcb1 libxcomposite1 libxdamage1 libxext6 libxfixes3 libxrandr2 libxkbcommon0 \
    fonts-liberation fonts-dejavu-core xvfb xauth unzip </dev/null
  if [ ! -x "$target/chrome" ]; then
    cage_download_tool "https://storage.googleapis.com/chrome-for-testing-public/$version/$platform/chrome-$platform.zip" "$checksum" "$cache/chrome.zip"
    sudo install -d -m 0755 /opt/cage-browser
    stage=$(sudo mktemp -d /opt/cage-browser/.install-XXXXXX)
    trap 'sudo rm -rf "$stage"' EXIT
    sudo unzip -q "$cache/chrome.zip" -d "$stage"
    sudo chown -R root:root "$stage/$directory"
    sudo chmod 4755 "$stage/$directory/chrome_sandbox"
    sudo mv "$stage/$directory" "$target"
  fi
  sudo ln -sfn "$target/chrome" /usr/bin/chromium
)

cage_install_chromium() {
  if cage_is_ubuntu; then
    cage_install_ubuntu_browser
  else
    cage_cache_chromium --no-install-recommends chromium chromium-l10n
    sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get install -y --no-install-recommends chromium chromium-l10n </dev/null
  fi
}

# Jammy ships noVNC 1.0. The shared input/clipboard patch needs 1.6.
# Keep the system package untouched; websockify serves the Cage-owned copy.
cage_install_ubuntu_novnc() (
  set -euo pipefail
  local cache="$HOME/.cache/cage/downloads/$CAGE_UBUNTU_NOVNC_SHA256" stage
  if [ -f /usr/local/share/cage-novnc/VERSION ] && [ "$(cat /usr/local/share/cage-novnc/VERSION)" = "$CAGE_UBUNTU_NOVNC_VERSION" ]; then
    sudo chmod 0755 /usr/local/share/cage-novnc
    return
  fi
  mkdir -p "$cache"
  exec 8>"$cache/lock"
  flock -w 60 8
  cage_download_tool "https://github.com/novnc/noVNC/archive/refs/tags/v$CAGE_UBUNTU_NOVNC_VERSION.tar.gz" "$CAGE_UBUNTU_NOVNC_SHA256" "$cache/novnc.tar.gz"
  sudo install -d -m 0755 /usr/local/share
  stage=$(sudo mktemp -d /usr/local/share/.cage-novnc-XXXXXX)
  trap 'sudo rm -rf "$stage"' EXIT
  sudo tar -xzf "$cache/novnc.tar.gz" --strip-components=1 -C "$stage"
  sudo chmod 0755 "$stage"
  printf '%s\n' "$CAGE_UBUNTU_NOVNC_VERSION" | sudo tee "$stage/VERSION" >/dev/null
  # Called only after cage-webvnc has stopped.
  sudo rm -rf /usr/local/share/cage-novnc
  sudo mv "$stage" /usr/local/share/cage-novnc
)
