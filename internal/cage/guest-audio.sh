# A virtual output shared by Cage applications. No hardware or microphone
# modules are loaded, and the PulseAudio protocol is Unix-socket only.
if ! command -v pulseaudio >/dev/null || ! command -v parec >/dev/null; then
  sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get -o DPkg::Lock::Timeout=60 update </dev/null
  sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get -o DPkg::Lock::Timeout=60 install -y --no-install-recommends pulseaudio pulseaudio-utils </dev/null
fi
getent group cage-audio >/dev/null || sudo groupadd --system cage-audio
sudo usermod -a -G cage-audio cage
sudo usermod -a -G cage-audio cage-browser
sudo install -d -m 0755 /etc/claude-cage /etc/pulse/client.conf.d
sudo tee /etc/pulse/client.conf.d/99-cage.conf >/dev/null <<'CLIENT'
default-server = unix:/run/cage-audio/native
autospawn = no
CLIENT
sudo tee /etc/claude-cage/audio.pa >/dev/null <<'PULSE'
load-module module-null-sink sink_name=cage_output rate=48000 channels=2 format=s16le sink_properties=device.description=Cage
load-module module-native-protocol-unix socket=/run/cage-audio/native auth-anonymous=0 auth-cookie-enabled=0 auth-group-enable=1 auth-group=cage-audio
set-default-sink cage_output
set-default-source cage_output.monitor
PULSE
sudo tee /etc/systemd/system/cage-audio.service >/dev/null <<'UNIT'
[Unit]
Description=Cage virtual audio output
[Service]
User=cage
Group=cage-audio
Environment=HOME=/home/cage
Environment=PULSE_RUNTIME_PATH=/run/cage-audio/runtime
Environment=PULSE_STATE_PATH=/run/cage-audio/state
RuntimeDirectory=cage-audio
RuntimeDirectoryMode=0750
ExecStart=/usr/local/bin/cage-log-run /usr/bin/pulseaudio -n --daemonize=no --exit-idle-time=-1 --disallow-exit --disallow-module-loading --disable-shm=true --realtime=no --high-priority=no --use-pid-file=no --file=/etc/claude-cage/audio.pa
LogNamespace=cage
Restart=on-failure
RestartSec=1
TimeoutStopSec=5
NoNewPrivileges=true
UNIT
sudo chmod 0644 /etc/claude-cage/audio.pa /etc/pulse/client.conf.d/99-cage.conf /etc/systemd/system/cage-audio.service
sudo systemctl daemon-reload
sudo systemctl reset-failed cage-audio 2>/dev/null || true
sudo systemctl restart cage-audio
for i in $(seq 1 50); do
  if pactl --server=unix:/run/cage-audio/native info >/dev/null 2>&1; then break; fi
  sleep .1
done
pactl --server=unix:/run/cage-audio/native info >/dev/null
