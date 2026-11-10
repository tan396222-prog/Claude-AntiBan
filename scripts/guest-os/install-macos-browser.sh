#!/bin/bash
# Run as the administrator inside the prepared virtual Mac. All media are local.
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
[[ ( $# == 1 || ( $# == 2 && $2 == --update-tools ) ) && $EUID == 0 && $(sysctl -n hw.model) =~ ^VirtualMac[0-9]+,[0-9]+$ ]]
[[ -f /var/db/cage-guest-os && ! -L /var/db/cage-guest-os && $(stat -f %u /var/db/cage-guest-os) == 0 && $(cat /var/db/cage-guest-os) == macos ]]
media=$(cd "$1" && pwd)
base=/usr/local/libexec
tools="$base/cage-workbench-browser-4.145.0-pw1.65.0"
mkdir -p "$base"
stage=$(mktemp -d "$base/.cage-browser.XXXXXXXX")
volume="$stage/volume"
trap 'if mount | grep -Fq " on $volume ("; then hdiutil detach "$volume" >/dev/null; fi; rm -rf "$stage"' EXIT
umask 022
verify() {
    [[ $(shasum -a 256 "$1" | cut -d' ' -f1) == "$2" ]] || { echo "Unexpected package digest: $1" >&2; exit 1; }
}
if [[ ! -x $tools/editor/lib/node || ! -f $tools/playwright/package.json ]]; then
    mkdir -p "$stage/tools/editor" "$stage/tools/playwright"
    cp "$media/editor.tgz" "$media/playwright.tgz" "$stage/"
    verify "$stage/editor.tgz" a79774d8d34eed785c1f831bc0125f7534bc21a0039bef8e8a2aa32463f9421a
    verify "$stage/playwright.tgz" 315247c55a793ca760b2f05bdcee0e6a53fd65b1f22d1d3de8323c8c9395add3
    tar -xzf "$stage/editor.tgz" -C "$stage/tools/editor" --no-same-owner --strip-components 1
    tar -xzf "$stage/playwright.tgz" -C "$stage/tools/playwright" --no-same-owner --strip-components 1
    "$stage/tools/editor/lib/node" --version
    mkdir -p "$tools"
    for component in editor playwright; do
        [[ ! -L $tools/$component ]]
        if [[ -e $tools/$component ]]; then mv "$tools/$component" "$stage/previous-$component"; fi
        if ! mv "$stage/tools/$component" "$tools/$component"; then
            if [[ -d $stage/previous-$component ]]; then mv "$stage/previous-$component" "$tools/$component"; fi
            exit 1
        fi
    done
fi
app='/Applications/Google Chrome.app'
requirement='anchor apple generic and identifier "com.google.Chrome" and certificate leaf[subject.OU] = "EQHXZ8M8AV"'
if [[ ! -e $app || ${2:-} == --update-tools ]]; then
    cp "$media/chrome.dmg" "$stage/chrome.dmg"
    mkdir "$volume"
    hdiutil attach -readonly -nobrowse -noautoopen -mountpoint "$volume" "$stage/chrome.dmg" >/dev/null
    spctl --assess --type execute "$volume/Google Chrome.app"
    ditto --norsrc "$volume/Google Chrome.app" "$stage/Google Chrome.app"
    codesign --verify --strict --deep -R="$requirement" "$stage/Google Chrome.app"
    spctl --assess --type execute "$stage/Google Chrome.app"
    install_chrome=yes
    if [[ -e $app ]]; then
        [[ -d $app && ! -L $app ]]
        codesign --verify --strict -R="$requirement" "$app"
        current=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist")
        incoming=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$stage/Google Chrome.app/Contents/Info.plist")
        [[ $current =~ ^[0-9]+(\.[0-9]+)*$ && $incoming =~ ^[0-9]+(\.[0-9]+)*$ ]]
        if ! /usr/bin/perl -e '@a=split(/\./,$ARGV[0]);@b=split(/\./,$ARGV[1]);for($i=0;$i<@a||$i<@b;$i++){exit(($a[$i]//0)>($b[$i]//0)?0:1) if ($a[$i]//0)!=($b[$i]//0)}exit 1' "$incoming" "$current"; then install_chrome=no; fi
    fi
    if [[ $install_chrome == yes ]]; then
        if [[ -e $app ]]; then mv "$app" "$stage/previous-chrome.app"; fi
        if ! mv "$stage/Google Chrome.app" "$app"; then
            if [[ -d $stage/previous-chrome.app ]]; then mv "$stage/previous-chrome.app" "$app"; fi
            exit 1
        fi
    fi
fi
codesign --verify --strict -R="$requirement" "$app"
sources=$(cd "$(dirname "$0")" && pwd)
install -m 644 "$sources/macos-browser.mjs" "$base/cage-macos-browser.mjs"
install -m 755 "$sources/cage-macos-editor" "$base/cage-macos-editor"
if [[ -f "$media/native-browser/macos-native-browser.mjs" ]]; then
    cp -R "$media/native-browser" "$stage/controller"
else
    "$sources/package-macos-browser.sh" "$stage/controller"
fi
install -d "$tools/controller" "$tools/controller/modules" "$tools/controller/split-extension"
install -m 644 "$stage/controller/"*.mjs "$tools/controller/"
install -m 755 "$stage/controller/cage-tailnet" "$tools/controller/cage-tailnet"
install -m 644 "$stage/controller/modules/"*.mjs "$tools/controller/modules/"
install -m 644 "$stage/controller/split-extension/"* "$tools/controller/split-extension/"
