import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

test('fresh-cache Mac core build downloads VZ source before copying and builds its patched binding', () => {
  const root = mkdtempSync(join(tmpdir(), 'cage-vz-fresh-cache-'));
  try {
    const bin = join(root, 'bin'), module = join(root, 'module');
    mkdirSync(bin); mkdirSync(module);
    writeFileSync(join(module, 'go.mod'), 'module github.com/Code-Hex/vz/v3\n');
    writeFileSync(join(bin, 'go'), `#!/bin/sh
set -eu
if test "$1 $2" = 'mod download'; then touch "$CAGE_VZ_FIXTURE/downloaded"; exit; fi
if test "$1 $2" = 'mod edit'; then exit; fi
case "$*" in
  *Version*) printf v3.7.1 ;;
  *Dir*) if test -f "$CAGE_VZ_FIXTURE/downloaded"; then printf '%s/module' "$CAGE_VZ_FIXTURE"; fi ;;
  *) exit 2 ;;
esac
`, { mode: 0o755 });
    writeFileSync(join(bin, 'make'), `#!/bin/sh
set -eu
trial=$(printf '%s' "$GOFLAGS" | sed 's/.*-modfile=//')
test -f "$(dirname "$trial")/vz/cage_view_object_darwin.go"
grep -q CageViewObject "$(dirname "$trial")/vz/cage_view_object_darwin.go"
test "$1" = cage
printf patched-binding-built
`, { mode: 0o755 });
    const result = spawnSync('/bin/bash', [resolve('scripts/guest-os/build-macos-guest-core.sh'), '0.1.7-rc.1'], {
      encoding: 'utf8', env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, CAGE_VZ_FIXTURE: root },
    });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /patched-binding-built/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
