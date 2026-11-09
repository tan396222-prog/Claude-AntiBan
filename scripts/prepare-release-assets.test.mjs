import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { lstat, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { prepareReleaseAssets } from './prepare-release-assets.mjs';
import { checksumFor } from '../www/scripts/sync-release.mjs';

const version = (await readFile(new URL('../VERSION', import.meta.url), 'utf8')).trim();
const dependencies = JSON.parse(await readFile(new URL('../packaging/windows/dependencies.json', import.meta.url), 'utf8'));
const macCore = (await readFile(new URL('./fetch-proxy-core.sh', import.meta.url), 'utf8')).match(/^version=(v[\d.]+)$/m)[1];
const names = {
  mac: `Cage-${version}-macos-arm64.dmg`,
  windows: `Cage-${version}-windows-amd64-setup.exe`,
  macSource: `mihomo-${macCore}-source.tar.gz`,
  windowsSource: `mihomo-${dependencies.mihomo.version}-source.tar.gz`,
  qemu: `qemu-${dependencies.qemu.version}-windows-full-source.zip`,
};
const digest = data => createHash('sha256').update(data).digest('hex');

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'cage-release-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  return { root, mac: join(root, 'mac'), windows: join(root, 'windows'), output: join(root, 'publish') };
}

async function platform(directory, assets) {
  await mkdir(directory, { recursive: true });
  for (const [name, data] of Object.entries(assets)) await writeFile(join(directory, name), data);
  await writeFile(join(directory, 'SHA256SUMS'), Object.entries(assets).map(([name, data]) => `${digest(data)}  ${name}\n`).join(''));
}

const macAssets = () => ({ [names.mac]: 'mac package fixture', [names.macSource]: 'Mihomo source fixture' });
const windowsAssets = () => ({ [names.windows]: 'Windows package fixture', [names.windowsSource]: 'Mihomo source fixture', [names.qemu]: 'complete QEMU source fixture' });

test('includes Linux deb only with its verified corresponding source and merges common sources', async t => {
  const f = await fixture(t);
  const linux = join(f.root, 'linux');
  const dependencies = JSON.parse(await readFile(new URL('../packaging/linux/dependencies.json', import.meta.url), 'utf8'));
  const deb = `Cage-${version}-linux-amd64.deb`;
  await platform(linux, { [deb]: 'Linux package fixture' });
  await assert.rejects(prepareReleaseAssets({ inputs: [linux], output: f.output }), /Missing Linux Mihomo source/);
  await platform(linux, { [deb]: 'Linux package fixture', [dependencies.mihomoSource.file]: 'Mihomo source fixture' });
  await platform(f.mac, macAssets());
  const prepared = await prepareReleaseAssets({ inputs: [f.mac, linux], output: f.output });
  assert.ok(prepared.assets.includes(deb));
  assert.equal(await readFile(join(f.output, deb), 'utf8'), 'Linux package fixture');
  assert.equal(prepared.assets.filter(name => name === dependencies.mihomoSource.file).length, 1);
});

test('combines both platforms, deduplicates identical sources and keeps website checksum lookup valid', async t => {
  const f = await fixture(t);
  const extraSource = dependencies.qemuSource.file;
  await platform(f.mac, macAssets());
  await platform(f.windows, { ...windowsAssets(), [extraSource]: 'patched QEMU source fixture' });
  const result = await prepareReleaseAssets({ inputs: [f.mac, f.windows], output: f.output });
  const expected = new Map(Object.entries({ ...macAssets(), ...windowsAssets(), [extraSource]: 'patched QEMU source fixture' }));
  assert.deepEqual(new Set(await readdir(f.output)), new Set([...expected.keys(), 'SHA256SUMS']));
  assert.equal(result.version, version);
  const sums = await readFile(join(f.output, 'SHA256SUMS'), 'utf8');
  for (const [name, data] of expected) {
    assert.equal(await readFile(join(f.output, name), 'utf8'), data);
    assert.equal(checksumFor(sums, { name }), digest(data));
  }
  assert.equal(sums.trimEnd().split('\n').length, expected.size);
});

test('supports a single Windows or macOS platform with its corresponding sources', async t => {
  const f = await fixture(t);
  await platform(f.mac, macAssets());
  await platform(f.windows, windowsAssets());
  assert.equal((await prepareReleaseAssets({ inputs: [f.mac], output: f.output })).assets.length, 2);
  assert.equal((await prepareReleaseAssets({ inputs: [f.windows], output: join(f.root, 'windows-only') })).assets.length, 3);
});

test('rejects corrupted bytes before creating the output', async t => {
  const f = await fixture(t);
  await platform(f.mac, macAssets());
  await writeFile(join(f.mac, names.mac), 'changed after packaging');
  await assert.rejects(prepareReleaseAssets({ inputs: [f.mac], output: f.output }), /SHA-256 mismatch/);
  await assert.rejects(lstat(f.output), { code: 'ENOENT' });
});

test('rejects conflicting contents under the same asset name', async t => {
  const f = await fixture(t);
  const second = join(f.root, 'second-mac');
  await platform(f.mac, macAssets());
  await platform(second, { ...macAssets(), [names.macSource]: 'different source archive' });
  await assert.rejects(prepareReleaseAssets({ inputs: [f.mac, second], output: f.output }), /Conflicting same-name/);
});

test('checks duplicate input bytes even when their manifests claim identical digests', async t => {
  const f = await fixture(t);
  const second = join(f.root, 'second-mac');
  await platform(f.mac, macAssets());
  await platform(second, macAssets());
  await writeFile(join(second, names.macSource), 'corrupted duplicate');
  await assert.rejects(prepareReleaseAssets({ inputs: [f.mac, second], output: f.output }), /SHA-256 mismatch/);
});

test('rejects unsigned development installers including unlisted leftovers', async t => {
  const f = await fixture(t);
  const unsigned = names.windows.replace(/\.exe$/, '-unsigned.exe');
  const assets = windowsAssets();
  assets[unsigned] = assets[names.windows];
  delete assets[names.windows];
  await platform(f.windows, assets);
  await assert.rejects(prepareReleaseAssets({ inputs: [f.windows], output: f.output }), /Unsigned Windows development/);
  await platform(f.windows, windowsAssets());
  await assert.rejects(prepareReleaseAssets({ inputs: [f.windows], output: f.output }), /Unsigned Windows development/);
});

test('requires Windows complete QEMU source in the same verified manifest', async t => {
  const f = await fixture(t);
  const assets = windowsAssets();
  delete assets[names.qemu];
  await platform(f.windows, assets);
  await writeFile(join(f.windows, names.qemu), 'unlisted source is not verified');
  await assert.rejects(prepareReleaseAssets({ inputs: [f.windows], output: f.output }), /complete QEMU source/);
});

test('rejects wrong versions and manifest paths outside the release directory', async t => {
  const f = await fixture(t);
  await platform(f.mac, { ...macAssets(), 'Cage-99.0.0-macos-arm64.dmg': 'wrong version' });
  await assert.rejects(prepareReleaseAssets({ inputs: [f.mac], output: f.output }), /Unsupported asset or version/);
  await writeFile(join(f.mac, 'SHA256SUMS'), `${digest('outside')}  ../escape.dmg\n`);
  await assert.rejects(prepareReleaseAssets({ inputs: [f.mac], output: f.output }), /Invalid or duplicate/);
});

test('never overwrites an existing output, including an empty one', async t => {
  const f = await fixture(t);
  await platform(f.mac, macAssets());
  await mkdir(f.output);
  await assert.rejects(prepareReleaseAssets({ inputs: [f.mac], output: f.output }), /Output must be a new directory/);
  await writeFile(join(f.output, 'published-asset'), 'keep');
  await assert.rejects(prepareReleaseAssets({ inputs: [f.mac], output: f.output }), /Output must be a new directory/);
  assert.equal(await readFile(join(f.output, 'published-asset'), 'utf8'), 'keep');
});
