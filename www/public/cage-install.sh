#!/bin/sh
# Cage download/installation plan. No application or profile is changed.
# sh install.sh plan [--version TAG] [--platform darwin-arm64|win32-amd64|linux-amd64]
# sh install.sh download --destination /absolute/path [--version TAG]
set -eu
if ! command -v node >/dev/null 2>&1 || ! command -v curl >/dev/null 2>&1; then
  printf '%s\n' '{"protocol":1,"status":"error","error":{"code":"dependency_missing","message":"Cage bootstrap requires Node.js 20+ and curl. Read https://lee-xeo.github.io/Claude-Cage/agent.md","next_action":"install_dependencies"}}'
  exit 1
fi
cage_bootstrap_tmp=$(mktemp -d)
trap 'rm -rf "$cage_bootstrap_tmp"' EXIT
trap 'exit 1' HUP INT TERM
if ! curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --connect-timeout 15 --max-time 120 \
  https://lee-xeo.github.io/Claude-Cage/agent-bootstrap.mjs -o "$cage_bootstrap_tmp/bootstrap.mjs"; then
  printf '%s\n' '{"protocol":1,"status":"error","error":{"code":"download_failed","message":"Could not download the public Cage bootstrap. No application or profile was changed.","next_action":"check_connectivity"}}'
  exit 1
fi
node "$cage_bootstrap_tmp/bootstrap.mjs" "$@"
