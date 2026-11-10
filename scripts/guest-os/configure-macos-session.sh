#!/bin/bash
# Root configures only the installer-bound ordinary account inside a Cage VM.
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
[[ $# == 2 && $EUID == 0 && $(uname -sm) == 'Darwin arm64' && $(sysctl -n hw.model) =~ ^VirtualMac[0-9]+,[0-9]+$ ]]
user=$1
installation=$2
[[ $user =~ ^[a-z][a-z0-9_]{0,30}$ && $user != root && $user != cageprovision && $user != cagesetup ]]
[[ -f /var/db/cage-guest-os && ! -L /var/db/cage-guest-os && $(stat -f %u /var/db/cage-guest-os) == 0 && $(cat /var/db/cage-guest-os) == macos ]]
receipt=/var/db/cage-initial-setup/account.json
[[ -f $receipt && ! -L $receipt && $(stat -f %u "$receipt") == 0 && $(plutil -extract installation raw "$receipt") == "$installation" ]]
bound_user=$(plutil -extract user raw "$receipt" 2>/dev/null || echo cage)
[[ $bound_user == "$user" ]]
phase=$(plutil -extract state raw "$receipt")
[[ $phase == ready ]]
[[ $(dscl . -read /Users/$user NFSHomeDirectory) == "NFSHomeDirectory: /Users/$user" && " $(id -Gn "$user") " != *' admin '* ]]
IFS= read -r password
[[ -n $password ]]
printf '%s\n' "$password" | dscl /Local/Default -authonly "$user" >/dev/null
# FileVault prevents system automatic login; retain its protection and report it.
if fdesetup status | grep -q 'FileVault is On'; then
    echo 'FileVault 已启用，请使用客机密码登录。' >&2
    exit 1
fi
existing=$(defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser 2>/dev/null || true)
[[ -z $existing || $existing == "$user" ]]
umask 077
tmp=$(mktemp /etc/cage-autologin.XXXXXXXX)
trap 'rm -f "$tmp"' EXIT
# macOS's system auto-login record is reversible and root-readable. The host
# copy is separately encrypted with the Cage account key. No password argument.
printf '%s\n' "$password" | /usr/bin/perl -e '
    use strict; use warnings;
    my $password = <STDIN>; chomp $password;
    my @key = (0x7d,0x89,0x52,0x23,0xd2,0xbc,0xdd,0xea,0xa3,0xb9,0x1f);
    my $plain = $password . "\0";
    $plain .= "\0" while length($plain) % 12;
    print pack("C*", map { ord(substr($plain,$_,1)) ^ $key[$_ % @key] } 0..length($plain)-1);
' > "$tmp"
unset password
[[ ! -L /etc/kcpassword ]]
chmod 600 "$tmp"
mv -f "$tmp" /etc/kcpassword
defaults write /Library/Preferences/com.apple.loginwindow autoLoginUser -string "$user"
defaults write /Library/Preferences/com.apple.loginwindow autoLoginUserUID -int "$(id -u "$user")"
# Match Lima's first-login suppression for the ordinary account. Its generated
# plist contains the current OS build/version stamps and no credentials.
provision=cageprovision
source=/Users/$provision/Library/Preferences/com.apple.SetupAssistant.plist
if [[ -f $source && ! -L $source ]]; then
    cat "$source" | sudo -Hu "$user" /bin/bash -ec '
        umask 077
        mkdir -p "$HOME/Library/Preferences"
        target="$HOME/Library/Preferences/com.apple.SetupAssistant.plist"
        [[ ! -L $target ]]
        cat > "$target"
        plutil -lint "$target" >/dev/null
    '
fi
# Write the current OS stamps directly through the account's preference service.
sudo -Hu "$user" /bin/bash -ec '
    domain=com.apple.SetupAssistant
    version=$(sw_vers -productVersion)
    build=$(sw_vers -buildVersion)
    for key in DidSeeAccessibility DidSeeActivationLock DidSeeAppStore DidSeeAppearanceSetup DidSeeApplePaySetup DidSeeCloudSetup DidSeeLockdownMode DidSeePrivacy DidSeeScreenTime DidSeeSetupSequence DidSeeSiriSetup DidSeeSyncSetup DidSeeSyncSetup2 DidSeeTermsOfAddress DidSeeTouchIDSetup DidSeeiCloudLoginForStorageServices SkipExpressSettingsUpdating SkipFirstLoginOptimization; do
        defaults write "$domain" "$key" -bool true
    done
    for key in LastPreLoginTasksPerformedBuild LastSeenBuddyBuildVersion; do defaults write "$domain" "$key" -string "$build"; done
    for key in LastPreLoginTasksPerformedVersion LastSeenAgeRangeSelectionProductVersion LastSeenCloudProductVersion LastSeenDiagnosticsProductVersion; do defaults write "$domain" "$key" -string "$version"; done
    defaults write "$domain" MiniBuddyLaunchReason -int 0
    defaults write "$domain" MiniBuddyShouldLaunchToResumeSetup -bool false
    defaults -currentHost write com.apple.screensaver idleTime -int 0
'
# Cage owns guest lifetime; the hidden desktop should not sleep while the
# workbench is running. These settings affect only this virtual Mac.
pmset -a sleep 0 displaysleep 0
echo '客机自动登录与首次设置已准备。'
