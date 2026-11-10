#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || {
  echo 'Build the guest image on x86_64 Linux.' >&2; exit 1;
}
cage_binary="${CAGE_SERVER_BINARY:-$PWD/_output/bin/cage}"
cage_engine="${CAGE_CONTAINER_ENGINE:-$HOME/.claude-cage/container-engine/tools/docker}"
cage_socket="${DOCKER_HOST:-unix:///run/user/$(id -u)/cage-container-engine/docker.sock}"
cage_tag="${CAGE_GUEST_IMAGE_TAG:-cage-guest:development}"
[[ "$cage_binary" == /* && -x "$cage_binary" && "$cage_engine" == /* && -x "$cage_engine" ]] || {
  echo 'CAGE_SERVER_BINARY and CAGE_CONTAINER_ENGINE must name executable absolute paths.' >&2; exit 1;
}
[[ "$cage_socket" == unix:///* && "$cage_socket" != *$'\n'* ]] || {
  echo 'Use a local rootless Docker Unix socket.' >&2; exit 1;
}
[[ "$cage_tag" =~ ^[a-z0-9][a-z0-9./:_-]*$ ]] || { echo 'Invalid local image tag.' >&2; exit 1; }

cage_evidence=${CAGE_IMAGE_BUILD_DIR:-"$PWD/_output/cloud-images"}
mkdir -p "$cage_evidence"
cage_stage=$(mktemp -d "$cage_evidence/build.XXXXXX")
chmod 0700 "$cage_stage"
mkdir -m 700 "$cage_stage/client"
engine() {
  env -u DOCKER_CONTEXT -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH \
    "$cage_engine" --host "$cage_socket" --config "$cage_stage/client" "$@"
}
[[ "$(engine info --format '{{json .SecurityOptions}}')" == *'"name=rootless"'* ]] || {
  echo 'The selected Docker daemon is not rootless.' >&2; exit 1;
}
[[ "$(engine info --format '{{.CgroupDriver}} {{.CgroupVersion}}')" == 'systemd 2' ]] || {
  echo 'Rootless Docker must use systemd and cgroup v2.' >&2; exit 1;
}

cage_build_id="$(date +%Y%m%d%H%M%S)$$"
cage_slice="cageimage${cage_build_id}.slice"
cage_anchor="cageimage${cage_build_id}.service"
cage_network="cage-image-$cage_build_id"
cage_builder="cage-image-$cage_build_id"
cage_base="cage-base:build-$cage_build_id"
cage_base_args=()
if [[ -n "${CAGE_GUEST_BASE_IMAGE:-}" ]]; then
  [[ "$CAGE_GUEST_BASE_IMAGE" =~ ^(sha256:|[a-zA-Z0-9][a-zA-Z0-9./:_-]*@sha256:)[a-f0-9]{64}$ ]] || {
    echo 'CAGE_GUEST_BASE_IMAGE must use a fixed image ID or registry digest.' >&2; exit 1;
  }
  cage_base_args+=(--build-arg "BASE_IMAGE=$CAGE_GUEST_BASE_IMAGE")
fi
if [[ -n "${CAGE_DEBIAN_MIRROR:-}" ]]; then
  [[ "$CAGE_DEBIAN_MIRROR" =~ ^https?://[a-zA-Z0-9.-]+(:[0-9]+)?$ ]] || {
    echo 'CAGE_DEBIAN_MIRROR must be an HTTP(S) mirror origin.' >&2; exit 1;
  }
  cage_base_args+=(--build-arg "DEBIAN_MIRROR=$CAGE_DEBIAN_MIRROR")
fi
cage_container_id=''
cage_network_id=''
cage_completed=false
finish() {
  local result=$?
  if [[ "$cage_completed" == true || -n "${CAGE_IMAGE_ID_OUTPUT:-}" ]]; then
    # These IDs were created by this invocation and contain build data only.
    [[ -z "$cage_container_id" ]] || engine rm -f "$cage_container_id" >/dev/null || true
    [[ -z "$cage_network_id" ]] || engine network rm "$cage_network_id" >/dev/null || true
    systemctl --user stop "$cage_anchor" || true
  else
    printf 'Build incomplete. Retained its container %s and evidence in %s.\n' "$cage_container_id" "$cage_stage" >&2
  fi
  exit "$result"
}
trap finish EXIT

systemd-run --user --unit "$cage_anchor" --slice "$cage_slice" /usr/bin/sleep infinity
systemctl --user set-property --runtime "$cage_slice" MemoryMax=4G CPUQuota=200% TasksMax=512
cage_parent=$(systemctl --user show "$cage_slice" -p ControlGroup --value)
[[ "$(cat "/sys/fs/cgroup$cage_parent/memory.max")" == 4294967296 &&
   "$(cat "/sys/fs/cgroup$cage_parent/pids.max")" == 512 ]] || {
  echo 'The build parent cgroup limits were not applied.' >&2; exit 1;
}
read -r cage_cpu_quota cage_cpu_period < "/sys/fs/cgroup$cage_parent/cpu.max"
[[ "$cage_cpu_quota" =~ ^[0-9]+$ && "$cage_cpu_period" =~ ^[0-9]+$ &&
   "$cage_cpu_quota" -eq $((2 * cage_cpu_period)) ]] || {
  echo 'The build parent CPU limit was not applied.' >&2; exit 1;
}
cage_network_id=$(engine network create --label "cn.cage.image-build=$cage_build_id" "$cage_network")
mkdir "$cage_stage/context"
cp deploy/cloud/Dockerfile.guest deploy/cloud/restore-apt-sources.sh internal/cage/apt-bootstrap.sh "$cage_stage/context/"
DOCKER_BUILDKIT=0 engine build --cgroup-parent "$cage_slice" --network "$cage_network" \
  "${cage_base_args[@]}" --file "$cage_stage/context/Dockerfile.guest" --tag "$cage_base" "$cage_stage/context" > "$cage_stage/base-build.log" 2>&1
cage_container_id=$(engine create --name "$cage_builder" --label "cn.cage.image-build=$cage_build_id" \
  --hostname cage --add-host cage:127.0.1.1 \
  --network "$cage_network" --cgroupns private --cgroup-parent "$cage_slice" --ipc private \
  --cpus 2 --memory 4g --memory-swap 4g --pids-limit 512 \
  --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --device /dev/net/tun \
  --tmpfs /run --tmpfs /tmp --stop-signal SIGRTMIN+3 --stop-timeout 30 \
  --entrypoint /bin/sh "$cage_base" \
  -ec 'umount /sys/fs/cgroup; mount -t cgroup2 none /sys/fs/cgroup; exec /sbin/init')
engine start "$cage_container_id" >/dev/null
for ((attempt=0; attempt<50; attempt++)); do
  if engine exec "$cage_container_id" sh -c 'systemctl is-active --quiet ssh && systemctl is-active --quiet dbus' >/dev/null 2>&1; then break; fi
  sleep .2
done
engine exec "$cage_container_id" sh -c 'systemctl is-active --quiet ssh && systemctl is-active --quiet dbus'
if [[ -n "${CAGE_DEBIAN_MIRROR:-}" ]]; then
  engine exec "$cage_container_id" python3 -c \
    'from pathlib import Path; import sys; p=Path("/etc/apt/sources.list.d/debian.sources"); p.write_text(p.read_text().replace("http://deb.debian.org", sys.argv[1]))' \
    "$CAGE_DEBIAN_MIRROR"
fi
CLAUDE_CAGE_CONFIG="$cage_stage/profile/config.json" "$cage_binary" container-install-script tools > "$cage_stage/install-tools.sh"
CLAUDE_CAGE_CONFIG="$cage_stage/profile/config.json" "$cage_binary" container-install-script services > "$cage_stage/install-services.sh"
engine exec --interactive --user cage --workdir /home/cage "$cage_container_id" bash -l \
  < "$cage_stage/install-tools.sh" > "$cage_stage/tools.log" 2>&1
engine exec --interactive --user cage --workdir /home/cage "$cage_container_id" zsh -l \
  < "$cage_stage/install-services.sh" > "$cage_stage/services.log" 2>&1
engine cp "$cage_binary" "$cage_container_id:/usr/local/lib/cage-runtime"

# No StartScript or account login is run while constructing this image.
engine exec --interactive "$cage_container_id" sh -s > "$cage_stage/identity-cleanup.log" 2>&1 <<'GUEST'
set -eu
test ! -e /etc/claude-cage/browser-control.json
test ! -e /home/cage/.config/claude-cage/browser-password
test ! -e /home/cage/.claude/.credentials.json
test ! -e /home/cage/.claude.json
test ! -e /home/cage/.claude-cage/account.json
sh /usr/local/lib/cage-image-restore-sources
systemctl stop ssh cage-audio
systemctl stop systemd-journald@cage.service systemd-journald@cage.socket systemd-journald-dev-log@cage.socket 2>/dev/null || true
systemctl stop systemd-journald.service systemd-journald.socket systemd-journald-dev-log.socket systemd-journald-audit.socket 2>/dev/null || true
if systemctl is-active --quiet ssh cage-audio systemd-journald@cage.service; then exit 1; fi
rm -f /etc/ssh/ssh_host_* /etc/ssh/cage_authorized_keys
rm -rf /home/cage/.cache/cage/downloads /home/cage/.cache/mise /home/cage/.npm /home/cage/.config/code-server /home/cage/.config/pulse
rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/* /var/log/journal/* /run/log/journal/*
find /var/log -type f -exec truncate -s 0 {} +
: > /etc/machine-id
chmod 0755 /usr/local/lib/cage-runtime
GUEST
engine stop "$cage_container_id" >/dev/null
engine commit --change 'ENTRYPOINT []' --change 'CMD ["/sbin/init"]' --change 'STOPSIGNAL SIGRTMIN+3' \
  "$cage_container_id" "$cage_tag" > "$cage_stage/image-id"
cage_image_id=$(cat "$cage_stage/image-id")
[[ "$cage_image_id" =~ ^sha256:[a-f0-9]{64}$ ]] || { echo 'Invalid committed image identity.' >&2; exit 1; }
engine run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges \
  --cgroup-parent "$cage_slice" --cpus 1 --memory 256m --pids-limit 64 --entrypoint /bin/sh "$cage_image_id" -ec \
  'test ! -s /etc/machine-id; test -z "$(find /etc/ssh -maxdepth 1 -name "ssh_host_*" -print -quit)"; test ! -e /etc/ssh/cage_authorized_keys; test ! -e /etc/claude-cage/browser-control.json; test ! -e /home/cage/.config/claude-cage/browser-password; test ! -e /home/cage/.claude/.credentials.json; test ! -e /home/cage/.claude-cage; test ! -e /home/cage/.config/code-server/config.yaml; test ! -e /home/cage/.config/pulse/cookie; test ! -e /etc/systemd/system/cage-memory.service; test -x /usr/local/lib/cage-runtime; test -s /usr/local/lib/cage-browser-agent.mjs; grep -qx "URIs: http://deb.debian.org/debian" /etc/apt/sources.list.d/debian.sources; grep -qx "URIs: http://deb.debian.org/debian-security" /etc/apt/sources.list.d/debian.sources; test "$(grep -c "^URIs:" /etc/apt/sources.list.d/debian.sources)" = 2; printf "image identity and software source checks passed\n"' \
  > "$cage_stage/image-check.log"
if [[ -n "${CAGE_IMAGE_ID_OUTPUT:-}" ]]; then
  cp "$cage_stage/image-id" "$CAGE_IMAGE_ID_OUTPUT"
else
  engine save --output "$cage_stage/cage-guest.tar" "$cage_image_id"
  (cd "$cage_stage" && sha256sum cage-guest.tar > SHA256SUMS)
fi
cage_completed=true
printf 'Built %s\nImage ID: %s\nArtifacts: %s\n' "$cage_tag" "$(cat "$cage_stage/image-id")" "$cage_stage"
