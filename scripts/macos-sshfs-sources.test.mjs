import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { extractLicenses, finish, inspectDependency, verifyArchive } from './guest-os/collect-macos-sshfs-sources.mjs';

test('macOS guest source is bound to the final signed binary and release version', t => {
  const root = mkdtempSync(join(tmpdir(), 'cage-sshfs-source-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const source = join(root, 'source');
  mkdirSync(join(source, 'licenses'), { recursive: true });
  writeFileSync(join(source, 'build.json'), JSON.stringify({ cageVersion: '0.1.7-rc.1' }));
  writeFileSync(join(source, 'NOTICE'), 'copyright and license notices');
  writeFileSync(join(source, 'licenses/LICENSE'), 'upstream license');
  writeFileSync(join(source, 'dependency.a'), 'the actual linked static library');
  const binary = join(root, 'cage-sshfs');
  writeFileSync(binary, 'final Developer ID signed executable');
  const archive = join(root, 'source.tar.gz');
  finish(source, binary, archive, join(root, 'app-licenses'));
  assert.equal(readFileSync(join(root, 'app-licenses/LICENSE'), 'utf8'), 'upstream license');
  assert.doesNotThrow(() => verifyArchive(archive, binary, '0.1.7-rc.1'));
  assert.throws(() => verifyArchive(archive, binary, '0.1.7-rc.2'), /does not match/);
  writeFileSync(binary, 'earlier ad-hoc signed executable');
  assert.throws(() => verifyArchive(archive, binary, '0.1.7-rc.1'), /does not match/);
  writeFileSync(binary, 'final Developer ID signed executable');
  writeFileSync(join(source, 'dependency.a'), 'different static library');
  execFileSync('tar', ['-czf', archive, '-C', source, '.']);
  assert.throws(() => verifyArchive(archive, binary, '0.1.7-rc.1'), /integrity check failed: dependency.a/);
});

test('same-version Homebrew formula changes cannot silently substitute another build', t => {
  const root = mkdtempSync(join(tmpdir(), 'cage-sshfs-formula-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, '.brew'));
  writeFileSync(join(root, 'INSTALL_RECEIPT.json'), JSON.stringify({ source: { spec: 'stable', versions: { stable: '2.90.1' } } }));
  writeFileSync(join(root, '.brew/glib.rb'), 'same version, unreviewed upstream patch');
  assert.throws(() => inspectDependency('glib', root), /installed formula\/version differs/);
});

test('upstream license symlinks and PCRE2 LICENCE.md are distributed as readable texts', t => {
  const root = mkdtempSync(join(tmpdir(), 'cage-sshfs-license-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const upstream = join(root, 'upstream');
  mkdirSync(join(upstream, 'glib/LICENSES'), { recursive: true });
  writeFileSync(join(upstream, 'glib/LICENSES/LGPL-2.1-or-later.txt'), 'full LGPL text');
  symlinkSync('LICENSES/LGPL-2.1-or-later.txt', join(upstream, 'glib/COPYING'));
  mkdirSync(join(upstream, 'pcre2/deps/sljit'), { recursive: true });
  writeFileSync(join(upstream, 'pcre2/COPYING'), 'See LICENCE.md');
  writeFileSync(join(upstream, 'pcre2/LICENCE.md'), 'BSD-3-Clause WITH PCRE2-exception');
  writeFileSync(join(upstream, 'pcre2/deps/sljit/LICENSE'), 'SLJIT license');
  for (const component of ['glib', 'pcre2']) {
    const archive = join(root, `${component}.tar.gz`);
    execFileSync('tar', ['-czf', archive, '-C', upstream, component]);
    extractLicenses(archive, join(root, 'licenses', component), component);
  }
  assert.equal(readFileSync(join(root, 'licenses/glib/COPYING'), 'utf8'), 'full LGPL text');
  assert.equal(readFileSync(join(root, 'licenses/pcre2/LICENCE.md'), 'utf8'), 'BSD-3-Clause WITH PCRE2-exception');
  assert.equal(readFileSync(join(root, 'licenses/pcre2/deps/sljit/LICENSE'), 'utf8'), 'SLJIT license');
});
