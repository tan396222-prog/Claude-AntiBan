#!/bin/bash
# Offline installation. Only Cage-owned guest tools are published.
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
[[ ( $# == 1 || ( $# == 2 && $2 == --components ) ) && $1 == /* && $EUID == 0 && $(sysctl -n hw.model) =~ ^VirtualMac[0-9]+,[0-9]+$ ]]
components=${2:-}
marker=/var/db/cage-guest-os
[[ ! -L $marker && -f $marker && $(stat -f %u "$marker") == 0 && $(cat "$marker") == macos ]]
mode=$(stat -f %OLp "$marker")
(( (8#$mode & 022) == 0 ))
kit=$(cd -P "$1" && pwd)
# Local helper sources are supplied by the trusted host, not by the GUI guest user.
[[ -z $(find "$kit" \( -type l -o ! -user root -o -perm -0020 -o -perm -0002 \) -print -quit) ]]
parent=$kit
while [[ $parent != / ]]; do
    mode=$(stat -f %Op "$parent")
    [[ $(stat -f %u "$parent") == 0 ]] || exit 1
    (( (8#$mode & 022) == 0 || (8#$mode & 01000) != 0 )) || exit 1
    parent=$(dirname "$parent")
done
umask 022
base=/usr/local/libexec
mkdir -p "$base" /usr/local/bin
work=$(mktemp -d "$base/.cage-cli-install.XXXXXXXX")
trap 'rm -rf "$work"' EXIT
cd "$work"
cp "$kit"/{macos-cli.py,cli_channel.py,cli_channel.resume.md,claude_account_resume.md,claude-account.py} .
runtime="$base/cage-cli-runtime-2.1.311-py3.13.18-git2.56.0"
if [[ $components == --components ]]; then
    [[ -x $runtime/bin/python3 && -x $runtime/bin/claude && -x $runtime/bin/git ]] || { echo '请先更新环境工具，再同步组件。' >&2; exit 1; }
else
cp "$kit"/{claude,uv.tar.gz,python.tar.gz,git.tar.gz} .
check() { [[ $(shasum -a 256 "$2" | cut -d' ' -f1) == "$1" ]]; }
check 85d8c08707fcfd1e78ffa0204eefbc6ec9e4fe22f375fb2767ef85d957ed3161 claude
check 81b811b57e4624df7ed3aac148e0359100d9962f3d9dce00aec533dc2e086731 uv.tar.gz
check 8392b6ea51d94391befb2c31ebe75463f045d0b69dfcf5e1bb3234e1f5fa3405 python.tar.gz
check 1110de343de11cc6f469b782b60f36217b9199b5e06875dd65baa26dd60563c7 git.tar.gz
chmod 755 claude
codesign --verify --strict --architecture arm64 -R '=anchor apple generic and identifier "com.anthropic.claude-code" and certificate leaf[subject.OU] = "Q6L2SF6YDW"' claude
fi
# Use rename(2), which never nests the staged directory inside a
# concurrently completed installation.
publish_dir() {
    /usr/bin/perl -e 'rename($ARGV[0], $ARGV[1]) || -d $ARGV[1] || die "publish: $!"' "$1" "$2"
}
if [[ ! -d $runtime ]]; then
    tar -xzf uv.tar.gz --no-same-owner
    mkdir -p tools/bin
    # uv recognizes the distribution format from the archive filename.
    mv python.tar.gz cpython-3.13.18-aarch64-apple-darwin.tar.gz
    cat > python.json <<JSON
{"cpython-3.13.18-darwin-aarch64-none":{"name":"cpython","arch":{"family":"aarch64","variant":null},"os":"darwin","libc":"none","major":3,"minor":13,"patch":18,"prerelease":"","url":"file://$work/cpython-3.13.18-aarch64-apple-darwin.tar.gz","sha256":"8392b6ea51d94391befb2c31ebe75463f045d0b69dfcf5e1bb3234e1f5fa3405","variant":null,"build":"20261026"}}
JSON
    UV_CACHE_DIR="$work/cache" ./uv-aarch64-apple-darwin/uv python install 3.13.18 --install-dir "$work/tools/python" --no-bin --no-config --offline --python-downloads-json-url "$work/python.json"
    ln -s ../python/cpython-3.13.18-macos-aarch64-none/bin/python3.13 tools/bin/python3
    tools/bin/python3 -I -c 'import sys, ssl; assert sys.version_info[:3] == (3,13,18)'
    mv claude tools/bin/
    mkdir tools/git
    tar -xzf git.tar.gz -C tools/git --no-same-owner
    [[ $(tools/git/bin/git --version) == 'git version 2.56.0' ]]
    # This GitHub Desktop distribution is relocatable. Its remote helpers
    # and templates must be resolved from the same pinned distribution.
    cat > tools/bin/git <<GIT
#!/bin/sh
export GIT_EXEC_PATH="\${GIT_EXEC_PATH:-$runtime/git/libexec/git-core}"
export GIT_TEMPLATE_DIR="\${GIT_TEMPLATE_DIR:-$runtime/git/share/git-core/templates}"
export GIT_CONFIG_SYSTEM="\${GIT_CONFIG_SYSTEM:-$runtime/git/etc/gitconfig}"
exec "$runtime/git/bin/git" "\$@"
GIT
    chmod 755 tools/bin/git
    chmod -R a+rX,go-w tools
    publish_dir tools "$runtime"
fi
revision=$(shasum -a 256 macos-cli.py cli_channel.py cli_channel.resume.md claude_account_resume.md claude-account.py | shasum -a 256 | cut -c1-16)
target="$base/cage-cli-2.1.311-git2.56.0-$revision"
if [[ -d $target ]]; then
    intact=yes
    for name in macos-cli.py cli_channel.py cli_channel.resume.md; do
        if ! cmp -s "$name" "$target/$name"; then intact=no; fi
    done
    if ! cmp -s claude_account_resume.md "$target/cli_channel.account-resume.md" ||
        ! cmp -s <(sed "1s@.*@#!$runtime/bin/python3 -I@" claude-account.py) "$target/bin/cage-claude-account"; then intact=no; fi
    for name in python3 claude git; do
        if [[ ! -L $target/bin/$name || $(readlink "$target/bin/$name") != "$runtime/bin/$name" ]]; then intact=no; fi
    done
    if [[ $intact == no ]]; then
        # Keep the damaged directory available for rollback while publishing a
        # fresh helper set for the same application revision.
        target="$target-repair-${work##*.}"
    fi
fi
if [[ ! -d $target ]]; then
    mkdir -p helpers/bin
    for name in python3 claude git; do ln -s "$runtime/bin/$name" "helpers/bin/$name"; done
    install -m 644 macos-cli.py cli_channel.py cli_channel.resume.md helpers/
    install -m 644 claude_account_resume.md helpers/cli_channel.account-resume.md
    sed "1s@.*@#!$runtime/bin/python3 -I@" claude-account.py > helpers/bin/cage-claude-account
    chmod 755 helpers/bin/cage-claude-account
    ln -s cage-claude-account helpers/bin/cage-open
    publish_dir helpers "$target"
fi
current="$base/cage-cli"
if [[ -e $current || -L $current ]]; then
    [[ -L $current && $(readlink "$current") == "$base"/cage-cli-2.* ]]
fi
ln -s "$target" "$work/current"
mv -fh "$work/current" "$current"
account=/usr/local/bin/cage-claude-account
if [[ ! -e $account && ! -L $account ]]; then ln -s "$current/bin/cage-claude-account" "$account"; fi
[[ -L $account && $(readlink "$account") == "$current/bin/cage-claude-account" ]]
for name in claude git; do
    if [[ ! -e /usr/local/bin/$name && ! -L /usr/local/bin/$name ]]; then ln -s "$current/bin/$name" "/usr/local/bin/$name"; fi
done
"$current/bin/claude" --version
