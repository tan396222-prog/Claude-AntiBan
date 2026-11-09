#!/usr/bin/env bash
set -euo pipefail
if systemctl is-active --quiet cage-claude-desktop.service; then
  test -f /run/cage-desktop/keyring-ready || { echo '请先保存并关闭现有 Claude Desktop，再使用 Cage 账户重新打开。' >&2; exit 1; }
  exit 0
fi
if systemctl is-failed --quiet cage-claude-desktop.service; then
  sudo systemctl reset-failed cage-claude-desktop.service
fi
sudo systemctl start --no-block cage-claude-desktop.service
for i in $(seq 1 150); do
  if [ -p /run/cage-desktop/unlock ]; then break; fi
  if systemctl is-failed --quiet cage-claude-desktop.service; then
    echo '图形会话未能启动。' >&2; exit 1
  fi
  sleep .1
done
test -p /run/cage-desktop/unlock || { echo '等待凭据解锁通道超时。' >&2; exit 1; }
# The internal random key comes exclusively from SSH stdin, never argv or disk.
timeout 10 sh -c 'cat > /run/cage-desktop/unlock'
for i in $(seq 1 300); do
  if systemctl is-active --quiet cage-claude-desktop.service; then exit 0; fi
  if systemctl is-failed --quiet cage-claude-desktop.service; then
    echo 'Claude Desktop 未能启动或密钥环未能解锁；已有登录数据保持不变。' >&2; exit 1
  fi
  sleep .1
done
echo 'Claude Desktop 启动超时。' >&2
exit 1
