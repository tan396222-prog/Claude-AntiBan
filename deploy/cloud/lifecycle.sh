# Invoked over a host-key verified SSH connection. Stdout is protocol data.
set -eu
umask 077
action=$1
exe=$2
config=${3:-"$HOME/.claude-cage/config.json"}
export CLAUDE_CAGE_CONFIG="$config"
profile=$(dirname -- "$config")
root="$profile/cloud-host"
key=$(printf %s "$config" | sha256sum | cut -c1-16)
unit=cage-cloud-$key
engine=$unit-engine
units="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
owned() { [[ -f "$root/owner" && $(cat "$root/owner") == "$config" ]]; }
exists() { [[ -e "$1" || -L "$1" ]]; }
installed() { owned && [[ -e "$root/ready" ]]; }
manual() {
  if [[ "$exe" != cage ]]; then [[ -x "$exe" ]] && printf '%s' "$exe"
  elif command -v cage >/dev/null; then command -v cage
  elif [[ -x "$HOME/.local/lib/cage/cage" ]]; then printf '%s' "$HOME/.local/lib/cage/cage"
  else return 1
  fi
}
preflight() {
  [[ $(uname -s) == Linux && $(uname -m) == x86_64 && $(id -u) != 0 ]] || exit 61
  [[ -c /dev/fuse && -c /dev/net/tun && -f /sys/fs/cgroup/cgroup.controllers ]] || exit 62
  for tool in newuidmap newgidmap iptables ip tar sha256sum systemctl flock; do command -v "$tool" >/dev/null || exit 62; done
  systemctl --user show-environment >/dev/null || exit 63
  for map in /etc/subuid /etc/subgid; do
    awk -F: -v user="$(id -un)" -v uid="$(id -u)" '($1==user || $1==uid) && $3>=65536 {ok=1} END {exit !ok}' "$map" || exit 62
  done
  # Unit templates and JSON below deliberately accept portable absolute paths.
  [[ "$config" =~ ^/[a-zA-Z0-9._/-]+$ && "$config" != *'/../'* && "$profile" != / ]] || exit 64
}
vacant() {
  if exists "$root"; then owned || exit 65
  elif exists "$profile" && [[ -n $(ls -A -- "$profile") ]]; then exit 65
  fi
}
case "$action" in
probe)
  if [[ "$exe" == cage ]] && installed; then printf managed
  elif binary=$(manual); then
    # Match the service's exact profile before starting a manual installation.
    if [[ " $(systemctl --user show cage-server.service -p Environment --value) " == *" CLAUDE_CAGE_CONFIG=$config "* ]] &&
       [[ "$(systemctl --user show cage-server.service -p ExecStart --value)" == *"path=$binary ;"* ]]; then printf manual
    else
      exec "$binary" server-entry --json
    fi
  elif [[ "$exe" != cage ]]; then exit 66
  else preflight; vacant; printf missing
  fi ;;
entry)
  if [[ "$exe" == cage ]] && installed; then binary="$root/bin/cage"; else binary=$(manual); fi
  exec "$binary" server-entry --json ;;
start)
  if [[ "$exe" == cage ]] && installed; then systemctl --user start "$unit.service"
  else systemctl --user start cage-server.service; fi ;;
install)
  preflight; vacant
  [[ "$exe" == cage ]] || exit 66
  digest=$4
  [[ "$digest" =~ ^[0-9a-f]{64}$ ]]
  mkdir -p -- "$profile"
  if mkdir -- "$root" 2>/dev/null; then printf '%s' "$config" > "$root/owner"; fi
  owned || exit 65
  exec 9>"$root/install.lock"
  flock -n 9 || exit 67
  installed && { cat >/dev/null; exit 0; }
  [[ $(df -Pk "$root" | awk 'END {print $4}') -gt 7340032 ]] || exit 68
  if exists "$root/kit-id"; then [[ $(cat "$root/kit-id") == "$digest" ]] || exit 65; fi
  stage=$(mktemp -d "$root/upload.XXXXXX")
  trap 'rm -rf -- "$stage"' EXIT
  cat > "$stage/payload.tar"
  (cd "$stage"; printf '%s  payload.tar\n' "$digest" | sha256sum -c --status -) || exit 69
  printf '%s' "$digest" > "$root/kit-id"
  tar -xf "$stage/payload.tar" -C "$stage"
  rm "$stage/payload.tar"
  mkdir -p "$root/bin" "$root/engine/tools" "$root/engine/client" "$units"
  if [[ -f "$stage/engine-downloads" ]]; then
    for name in cage mihomo; do gzip -dc "$stage/$name.gz" > "$stage/$name"; done
    mkdir -p "$root/downloads"
    while read -r checksum name url; do
      cached="$root/downloads/$name"
      if ! (cd "$root/downloads"; printf '%s  %s\n' "$checksum" "$name" | sha256sum -c --status -) 2>/dev/null; then
        # Fixed-size ranges also work through mirrors which truncate a large response.
        if [[ -f "$cached.part" ]] && printf '%s  %s\n' "$checksum" "$cached.part" | sha256sum -c --status -; then
          mv "$cached.part" "$cached"
          ln "$cached" "$stage/$name"
          continue
        fi
        length=0
        while [[ $length -eq 0 || $offset -lt $length ]]; do
          offset=$(stat -c %s "$cached.part" 2>/dev/null || echo 0)
          end=$((offset + 4194303))
          ok=false
          for source in "$url" "https://mirrors.ustc.edu.cn/docker-ce/linux/static/stable/x86_64/$name" "https://mirrors.tuna.tsinghua.edu.cn/docker-ce/linux/static/stable/x86_64/$name"; do
            if curl -fsSL --connect-timeout 8 --max-time 90 --range "$offset-$end" -D "$stage/headers" -o "$stage/chunk" "$source"; then
              range=$(tr -d '\r' < "$stage/headers" | awk 'tolower($1)=="content-range:" {print $3}' | tail -1)
              if [[ "$range" =~ ^([0-9]+)-([0-9]+)/([0-9]+)$ ]]; then
                first=${BASH_REMATCH[1]} last=${BASH_REMATCH[2]} total=${BASH_REMATCH[3]}
                if (( first == offset && last <= end && last < total && total < 268435456 && last-first+1 == $(stat -c %s "$stage/chunk") )); then
                  cat "$stage/chunk" >> "$cached.part"; length=$total; offset=$((last+1)); ok=true; break
                fi
              elif printf '%s  %s\n' "$checksum" "$stage/chunk" | sha256sum -c --status -; then
                mv "$stage/chunk" "$cached.part"; length=1; offset=1; ok=true; break
              fi
            fi
          done
          [[ "$ok" == true ]] || exit 74
        done
        printf '%s  %s\n' "$checksum" "$cached.part" | sha256sum -c --status - || { rm -f "$cached.part"; exit 69; }
        mv "$cached.part" "$cached"
      fi
      ln "$cached" "$stage/$name"
    done < "$stage/engine-downloads"
  fi
  for name in cage mihomo; do
    if ! cmp -s "$stage/$name" "$root/bin/$name"; then
      install -m 700 "$stage/$name" "$root/bin/$name.new"
      mv "$root/bin/$name.new" "$root/bin/$name"
    fi
  done
  eng="$root/engine"
  if [[ ! -f "$eng/tools/.complete" ]]; then
    for tgz in "$stage"/docker-*.tgz; do tar -xzf "$tgz" --strip-components=1 -C "$eng/tools"; done
    touch "$eng/tools/.complete"
  fi
  [[ -f "$eng/daemon.json" ]] || printf '{}\n' > "$eng/daemon.json"
  # Install only our units. An existing independent service must never be replaced.
  write_unit() {
    local name=$1 src=$2
    sed -e "s|%h/.claude-cage/container-engine|$eng|g" \
        -e "s|%h/.claude-cage/config.json|$config|g" -e "s|%h/.local/lib/cage|$root/bin|g" \
        -e "s|cage-container-engine|$engine|g" "$src" > "$stage/$name"
    if exists "$units/$name"; then cmp -s "$stage/$name" "$units/$name" || exit 65
    else ln "$stage/$name" "$units/$name" 2>/dev/null || ( set -C; cat "$stage/$name" > "$units/$name" ); fi
  }
  write_unit "$engine.service" "$stage/cage-container-engine.service"
  write_unit "$unit.service" "$stage/cage-server.service"
  systemctl --user daemon-reload
  systemctl --user start "$engine.service" || exit 70
  export DOCKER_HOST="unix:///run/user/$(id -u)/$engine/docker.sock" DOCKER_CONFIG="$eng/client"
  unset DOCKER_CONTEXT
  docker="$eng/tools/docker"
  for i in {1..120}; do "$docker" info >/dev/null 2>&1 && break; sleep 1; done
  [[ $("$docker" info --format '{{json .SecurityOptions}} {{.CgroupDriver}} {{.CgroupVersion}}') == *'name=rootless'*' systemd 2' ]] || exit 70
  if [[ -f "$stage/engine-downloads" ]]; then
    if [[ ! -f "$root/image-id" ]]; then
      base=$(sed -n 's/^ARG BASE_IMAGE=//p' "$stage/deploy/cloud/Dockerfile.guest")
      ref=${base#*@}
      [[ "$ref" =~ ^sha256:[0-9a-f]{64}$ ]]
      for repo in docker.io/library/debian docker.m.daocloud.io/library/debian docker.1ms.run/library/debian public.ecr.aws/docker/library/debian; do
        if timeout 120 "$docker" pull "$repo@$ref" >> "$root/build.log" 2>&1; then
          base_id=$("$docker" image inspect --format '{{.Id}}' "$repo@$ref")
          break
        fi
      done
      [[ -n "${base_id:-}" ]] || exit 75
      mirror=http://deb.debian.org
      best=0
      for site in http://deb.debian.org http://mirrors.ustc.edu.cn http://mirrors.nju.edu.cn; do
        rate=$(curl -fsSL --range 0-131071 --max-time 4 -o /dev/null -w '%{speed_download}' "$site/debian/dists/trixie/main/binary-amd64/Packages.xz") || continue
        rate=${rate%%.*}
        if [[ "$rate" =~ ^[0-9]+$ ]] && ((rate > best)); then mirror=$site; best=$rate; fi
      done
      CAGE_DEBIAN_MIRROR="$mirror" CAGE_CONTAINER_ENGINE="$docker" CAGE_SERVER_BINARY="$root/bin/cage" CAGE_GUEST_BASE_IMAGE="$base_id" \
        CAGE_IMAGE_BUILD_DIR="$root/build" CAGE_IMAGE_ID_OUTPUT="$root/image-id.new" \
        bash "$stage/deploy/cloud/build-guest.sh" >> "$root/build.log" 2>&1 || exit 76
      mv "$root/image-id.new" "$root/image-id"
    fi
    image=$(cat "$root/image-id")
  else
    image=$(cat "$stage/image-id")
    "$docker" image inspect "$image" >/dev/null 2>&1 || "$docker" load -i "$stage/guest.tar.zst" >/dev/null || exit 71
  fi
  [[ "$image" =~ ^sha256:[0-9a-f]{64}$ ]]
  "$docker" image inspect "$image" >/dev/null || exit 71
  cage="$root/bin/cage"
  [[ -e "$config" ]] || "$cage" init >/dev/null
  printf '{"engine":"docker","executable":"%s","socket":"%s","image":"%s","cpus":2,"memory_bytes":4294967296}\n' "$docker" "$DOCKER_HOST" "$image" | "$cage" container-configure --json >/dev/null
  "$cage" setup >"$root/setup.log" 2>&1 || exit 72
  systemctl --user enable --now "$unit.service" || exit 73
  touch "$root/ready" ;;
*) exit 2 ;;
esac
