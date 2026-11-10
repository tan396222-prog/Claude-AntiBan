#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

skip_ui=false
case "${1:-}" in
  '') ;;
  --skip-ui) skip_ui=true ;;
  *) echo 'Usage: scripts/build-client.sh [--skip-ui]' >&2; exit 2 ;;
esac
case "$(uname -s)" in Darwin) platform=macos ;; Linux) platform=linux ;; *) echo 'Use build-client-windows.ps1 for Windows.' >&2; exit 1 ;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64) arch=amd64 ;; *) echo 'Unsupported client build architecture.' >&2; exit 1 ;; esac
version="$(tr -d '\r\n' < VERSION)"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || { echo 'Invalid VERSION.' >&2; exit 1; }
short_version="${version%%-*}"
output_root="$PWD/_output/client-$platform-$arch"
mkdir -p "$output_root"
stage="$(mktemp -d "$output_root/.stage.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
suffix="${stage##*.stage.}"
destination="$output_root/build-$(date +%Y%m%d-%H%M%S)-$suffix"
[[ ! -e "$destination" ]] || { echo 'Client output already exists; no files were replaced.' >&2; exit 1; }

if [[ "$skip_ui" == false ]]; then
  [[ -d ui/node_modules ]] || npm --prefix ui ci --no-audit --no-fund
  npm --prefix ui run build
else
  node ui/scripts/verify-build.mjs
fi
[[ -f internal/workbench/web/index.html ]] || { echo 'Build the UI before using --skip-ui.' >&2; exit 1; }
go list -deps ./cmd/cage-client > "$stage/dependencies.txt"
if grep -Eq 'internal/limaembed|github.com/lima-vm/lima/v2|github.com/Code-Hex/vz' "$stage/dependencies.txt"; then
  echo 'The client unexpectedly depends on a virtual machine runtime; build stopped.' >&2; exit 1
fi
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/sylvinus/agent-vm/internal/version.Version=$version" -o "$stage/cage" ./cmd/cage-client
"$stage/cage" verify-web-assets
mkdir -p "$stage/result"

if [[ "$platform" == macos ]]; then
  bundle="$stage/result/Cage Client.app"
  mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources"
  swiftc -O -target "$(uname -m)-apple-macosx14.0" -parse-as-library \
    -framework Cocoa -framework WebKit -framework Security -framework LocalAuthentication \
    desktop/Cage.swift desktop/MacKeyboard.swift desktop/NativeInput.swift desktop/Updates.swift desktop/AccountDevice.swift desktop/MacGuestLogin.swift \
    -o "$bundle/Contents/MacOS/Cage"
  node scripts/bundle-cloud-kit.mjs "$bundle/Contents/Resources"
  cp "$stage/cage" "$bundle/Contents/Resources/cage"
  node scripts/collect-app-licenses.mjs "$bundle/Contents/Resources/licenses"
  swift desktop/Icon.swift logo.png "$stage/Cage.iconset"
  iconutil -c icns "$stage/Cage.iconset" -o "$bundle/Contents/Resources/Cage.icns"
  cat > "$bundle/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Cage Client</string>
<key>CFBundleDisplayName</key><string>Cage Client</string>
<key>CFBundleIdentifier</key><string>io.github.lee-xeo.claude-cage-client</string>
<key>CFBundleVersion</key><string>1</string>
<key>CFBundleShortVersionString</key><string>$short_version</string>
<key>CageVersion</key><string>$version</string>
<key>CageClientOnly</key><true/>
<key>CFBundleExecutable</key><string>Cage</string>
<key>CFBundleIconFile</key><string>Cage</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict></plist>
PLIST
  # An ad-hoc development signature makes the local build runnable. No release
  # certificate, notarization, installer, or installed app is touched.
  codesign --force --sign - "$bundle/Contents/Resources/cage"
  codesign --force --sign - "$bundle/Contents/MacOS/Cage"
  codesign --force --sign - "$bundle"
  codesign --verify --deep --strict "$bundle"
  ditto -c -k --keepParent "$bundle" "$stage/result/Cage-Client-macos-$arch.zip"
else
  pkg-config --atleast-version=2.40 webkit2gtk-4.1
  pkg-config --exists gtk+-3.0 json-glib-1.0
  app="$stage/result/app"
  mkdir -p "$app"
  cc -std=c11 -O2 -Wall -Wextra -Werror -Wno-deprecated-declarations \
    "-DCAGE_VERSION=\"$version\"" -DCAGE_CLIENT_ONLY=1 desktop/linux/cage-desktop.c \
    $(pkg-config --cflags --libs gtk+-3.0 webkit2gtk-4.1 json-glib-1.0) -o "$app/cage-desktop"
  install -m 755 "$stage/cage" "$app/cage"
  node scripts/bundle-cloud-kit.mjs "$app"
  touch "$app/client-only"
  node scripts/collect-app-licenses.mjs "$app/licenses"
  printf '%s\n' 'libwebkit2gtk-4.1-0 (>= 2.40), libgtk-3-0, libjson-glib-1.0-0, libglib2.0-bin, ca-certificates' > "$app/dependencies.txt"
  cp /etc/os-release "$app/BUILD-PLATFORM.txt"
  pkg-config --modversion gtk+-3.0 webkit2gtk-4.1 json-glib-1.0 >> "$app/BUILD-PLATFORM.txt"
  cat > "$app/README.txt" <<'LINUX_README'
Cage 轻量远程客户端（Linux 开发构建）
在兼容 BUILD-PLATFORM.txt 所列发行版的图形桌面中，以普通用户运行 cage-desktop。
系统依赖见 dependencies.txt。项目、应用与网络在云端运行；本机连接配置默认位于 ~/.claude-cage/client。
构建不安装或覆盖现有应用，不包含虚拟机、Mihomo 或 OpenSSH 程序。验收范围以项目测试报告为准。
LINUX_README
  "$app/cage-desktop" --self-test
  tar -C "$stage/result" -czf "$stage/result/Cage-Client-linux-$arch.tar.gz" app
fi
cp LICENSE "$stage/result/LICENSE"
printf '%s\n' "$version" > "$stage/result/VERSION"
cat > "$stage/result/README.txt" <<'README'
Cage 轻量远程客户端（开发构建）

本机只提供窗口、SSH 连接、输入和文件传递；项目、浏览器、终端与网络在云端 Cage 运行。
默认连接配置目录为 ~/.claude-cage/client；显式设置 CLAUDE_CAGE_CONFIG 时沿用指定路径。
此目录为独立构建产物，不会安装或替换已安装的 Cage。当前构建使用手动更新。
macOS 运行 Cage Client.app。Linux 安装 app/dependencies.txt 中的系统依赖后运行 app/cage-desktop。
本构建未公证或发布；原生运行验收范围以项目测试报告为准。
README
mv "$stage/result" "$destination"
printf 'Built standalone client: %s\n' "$destination"
