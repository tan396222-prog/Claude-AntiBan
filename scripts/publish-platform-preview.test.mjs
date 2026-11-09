import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { stagePreview, mergeReleaseChecksums } from './publish-platform-preview.mjs';
const version = '0.1.1-alpha';
const dependencies = { mihomoSource: { file: 'mihomo-v1.19.33-source.tar.gz' }, qemuSource: { file: 'qemu-' + 'a'.repeat(40) + '-source.tar' } };
const hash = text => createHash('sha256').update(text).digest('hex');
function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'cage-preview-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const windows = join(root, 'windows'), linux = join(root, 'linux'), output = join(root, 'public');
  for (const [directory, names] of [[windows, [`Cage-${version}-windows-amd64-setup-unsigned.exe`, dependencies.mihomoSource.file, dependencies.qemuSource.file + '.gz']], [linux, [`Cage-${version}-linux-amd64.deb`, dependencies.mihomoSource.file]]]) {
    mkdirSync(directory);
    for (const name of names) writeFileSync(join(directory, name), name);
    writeFileSync(join(directory, 'SHA256SUMS'), names.map(name => `${hash(name)}  ${name}\n`).join(''));
    writeFileSync(join(directory, 'private-report.json'), 'not a release file');
  }
  return { windows, linux, output };
}
test('preview publication stages verified installers and sources only, with one source copy', t => {
  const f = fixture(t), files = stagePreview(f.windows, f.linux, f.output, version, dependencies);
  assert.equal(files.size, 5);
  assert.deepEqual(readdirSync(f.output).sort(), [...files.keys()].sort());
  assert.ok(!files.has('private-report.json'));
  for (const [name, file] of files) assert.equal(hash(readFileSync(join(f.output, name))), file.digest);
});
test('corrupt, missing, duplicate and unexpected checksum entries never become release assets', async t => {
  for (const kind of ['corrupt', 'missing', 'duplicate', 'unexpected', 'conflicting-source']) await t.test(kind, t => {
    const f = fixture(t), manifest = join(f.windows, 'SHA256SUMS');
    const name = `Cage-${version}-windows-amd64-setup-unsigned.exe`;
    if (kind === 'corrupt') writeFileSync(join(f.windows, name), 'changed');
    if (kind === 'missing') rmSync(join(f.windows, name));
    if (kind === 'duplicate') writeFileSync(manifest, readFileSync(manifest, 'utf8') + `${hash(name)}  ${name}\n`);
    if (kind === 'unexpected') writeFileSync(manifest, readFileSync(manifest, 'utf8') + `${hash('other')}  other.exe\n`);
    if (kind === 'conflicting-source') {
      writeFileSync(join(f.linux, dependencies.mihomoSource.file), 'changed-source');
      const sums = join(f.linux, 'SHA256SUMS');
      writeFileSync(sums, readFileSync(sums, 'utf8').replace(hash(dependencies.mihomoSource.file), hash('changed-source')));
    }
    assert.throws(() => stagePreview(f.windows, f.linux, f.output, version, dependencies));
  });
});

test('consolidation preserves macOS checksums and rejects conflicting version assets', () => {
  const hash = 'a'.repeat(64), other = 'b'.repeat(64);
  const mac = 'Cage-0.1.1-alpha-macos-arm64.dmg', windows = 'Cage-0.1.1-alpha-windows-amd64-setup-unsigned.exe';
  const release = { tag_name: 'v0.1.1-alpha', draft: false, prerelease: true,
    assets: [{name: mac, state: 'uploaded', digest: `sha256:${hash}`}] };
  const files = new Map([[windows, {digest: other}], ['SHA256SUMS', {digest: other}]]);
  const original = `${hash}  ${mac}\n`;
  const merged = mergeReleaseChecksums(release, original, files, '0.1.1-alpha');
  assert.equal(merged, original + `${other}  ${windows}\n`);
  const partial = {...release, assets:[...release.assets, {name:windows, state:'uploaded', digest:`sha256:${other}`}]};
  assert.equal(mergeReleaseChecksums(partial, original, files, '0.1.1-alpha'), merged);
  assert.equal(mergeReleaseChecksums(partial, merged, files, '0.1.1-alpha'), merged);
  assert.throws(() => mergeReleaseChecksums(release, original + original, files, '0.1.1-alpha'));
  assert.throws(() => mergeReleaseChecksums(release, original, files, '0.1.0-alpha.1'));
  assert.throws(() => mergeReleaseChecksums(release, original, new Map([[mac,{digest:other}]]), '0.1.1-alpha'));
  assert.throws(() => mergeReleaseChecksums({...release,draft:true}, original, files, '0.1.1-alpha'));
  assert.throws(() => mergeReleaseChecksums({...release,prerelease:false}, original, files, '0.1.1-alpha'));
  assert.throws(() => mergeReleaseChecksums(partial, original, new Map(), '0.1.1-alpha'));
});
