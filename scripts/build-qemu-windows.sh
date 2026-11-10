#!/usr/bin/env bash
set -euo pipefail

source_dir=$(cygpath -u "$1")
build_dir=$(cygpath -u "$2")
output_dir=$(cygpath -u "$3")
patch_dir=$(cygpath -u "$4")

for patch_file in "$patch_dir"/*.patch; do
    patch -d "$source_dir" -p1 < "$patch_file"
done
mkdir -p "$build_dir" "$output_dir"
cd "$build_dir"
"$source_dir/configure" --target-list=x86_64-softmmu --without-default-features \
    --prefix=C:/CageQemu --bindir=. --datadir=share --with-suffix= \
    --extra-cflags=-D_WIN32_WINNT=0x0A00 \
    --enable-whpx --enable-tcg --enable-pixman --enable-slirp --enable-tools \
    --disable-docs --disable-guest-agent --disable-werror \
    --disable-debug-info
ninja -j 6 qemu-system-x86_64.exe qemu-img.exe
cp qemu-system-x86_64.exe qemu-img.exe "$output_dir/"
strip "$output_dir"/*.exe

# Copy the DLL dependency closure from this toolchain, using the same versions
# that were linked into the executables. Windows system DLLs remain system-owned.
pending=("$output_dir"/*.exe)
packages=()
while ((${#pending[@]})); do
    binary=${pending[0]}
    pending=("${pending[@]:1}")
    while read -r dll; do
        if [[ -f "$MINGW_PREFIX/bin/$dll" && ! -f "$output_dir/$dll" ]]; then
            cp "$MINGW_PREFIX/bin/$dll" "$output_dir/$dll"
            packages+=("$(pacman -Qqo "$MINGW_PREFIX/bin/$dll")")
            pending+=("$output_dir/$dll")
        fi
    done < <(objdump -p "$binary" | awk '/DLL Name:/ {print $3}')
done
pacman -Q > "$output_dir/build-packages.txt"
# Keep the exact source package identity with cached binaries. A later runner
# must not collect the source of whichever library version happens to be installed.
for package in "${packages[@]}"; do
    read -r name version < <(pacman -Q "$package")
    base=$(awk '/^%BASE%$/ {getline; print; exit}' "/var/lib/pacman/local/$name-$version/desc")
    [[ -n "$base" ]] || { echo "Source package identity missing for $package" >&2; exit 1; }
    printf '%s %s\n' "$base" "$version"
done | sort -u > "$output_dir/build-source-packages.txt"
