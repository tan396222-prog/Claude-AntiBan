# A small compressed swap area absorbs short allocation bursts while the host
# adjusts the balloon. It consumes RAM only as used and is bounded at 2 GiB.
# Containers share the host kernel. Their memory and swap limits belong to
# the container cgroup; never load host modules or create swap devices here.
if ! systemd-detect-virt --container --quiet; then
sudo tee /usr/local/bin/cage-memory-start >/dev/null <<'SCRIPT'
#!/bin/sh
set -eu
if [ -f /run/cage-zram-device ]; then
  device=$(cat /run/cage-zram-device)
  if grep -q "^$device " /proc/swaps; then exit 0; fi
fi
modprobe zram
number=$(cat /sys/class/zram-control/hot_add)
device=/dev/zram$number
cleanup() { echo "$number" > /sys/class/zram-control/hot_remove; }
trap cleanup EXIT
size=$(awk '/^MemTotal:/ { n=int($2*1024/4); if(n>2147483648)n=2147483648; printf "%.0f",n }' /proc/meminfo)
echo "$size" > "/sys/block/zram$number/disksize"
mkswap "$device" >/dev/null
swapon -p 100 "$device"
printf '%s\n' "$device" > /run/cage-zram-device
trap - EXIT
SCRIPT
sudo chmod 0755 /usr/local/bin/cage-memory-start
sudo tee /etc/systemd/system/cage-memory.service >/dev/null <<'UNIT'
[Unit]
Description=Cage compressed memory reserve
After=systemd-modules-load.service
Before=cage-browser.service cage-editor.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/cage-memory-start
[Install]
WantedBy=multi-user.target
UNIT
sudo systemctl daemon-reload
sudo systemctl enable cage-memory.service >/dev/null
if ! sudo systemctl start cage-memory.service; then
  printf 'Compressed memory reserve unavailable; automatic host memory management remains enabled.\n' >&2
fi
fi
