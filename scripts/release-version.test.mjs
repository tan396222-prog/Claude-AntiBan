import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { compareVersions, nextVersion, parseVersion, versionFiles } from './release-version.mjs';

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'cage-release-version-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(join(root, 'VERSION'), '1.2.3-alpha.9\r\n');
  for (const directory of ['ui', 'www']) {
    mkdirSync(join(root, directory));
    writeFileSync(join(root, directory, 'package.json'), '{\r\n\t"name" : "fixture",\r\n\t"version": "1.2.3-alpha.9",\r\n\t"dependencies": {"same-version": "1.2.3-alpha.9"},\r\n\t"number": 900719925474099312345\r\n}\r\n');
    writeFileSync(join(root, directory, 'package-lock.json'), JSON.stringify({
      name: 'fixture', version: '1.2.3-alpha.9', lockfileVersion: 3,
      packages: {
        '': { name: 'fixture', version: '1.2.3-alpha.9', dependencies: { library: '^2.0.0' } },
        'node_modules/library': { version: '1.2.3-alpha.9', resolved: 'https://example.invalid/library.tgz', integrity: 'sha512-fixture', engines: { node: '>=24' } },
      },
      metadata: [{ version: '1.2.3-alpha.9' }, true, null, 1.2e-3],
    }, null, 4) + '\n');
  }
  return root;
}

test('parses canonical SemVer and rejects ambiguous or unsupported forms', () => {
  assert.deepEqual(parseVersion('1.2.3-alpha.10'), { major: 1n, minor: 2n, patch: 3n, prerelease: ['alpha', '10'] });
  assert.deepEqual(parseVersion('0.0.0-0'), { major: 0n, minor: 0n, patch: 0n, prerelease: ['0'] });
  assert.deepEqual(parseVersion('1.0.0-01a'), { major: 1n, minor: 0n, patch: 0n, prerelease: ['01a'] });
  for (const invalid of [null, 1, '', 'v1.2.3', '1.2', '01.2.3', '1.02.3', '1.2.03', '1.2.3-01', '1.2.3-a.00', '1.2.3-', '1.2.3-a..b', '1.2.3+build', '1.2.3-a+b', ' 1.2.3', '1.2.3\n', '1.2.3-中文']) {
    assert.throws(() => parseVersion(invalid), /version|SemVer|prerelease/i, String(invalid));
  }
});

test('orders prereleases numerically and compares large identifiers without rounding', () => {
  const ordered = ['1.0.0-alpha', '1.0.0-alpha.1', '1.0.0-alpha.beta', '1.0.0-beta', '1.0.0-beta.2', '1.0.0-beta.11', '1.0.0-rc.1', '1.0.0', '1.0.1', '1.1.0', '2.0.0'];
  for (let i = 0; i < ordered.length; i++) {
    assert.equal(compareVersions(ordered[i], ordered[i]), 0);
    for (let j = i + 1; j < ordered.length; j++) {
      assert.equal(compareVersions(ordered[i], ordered[j]), -1);
      assert.equal(compareVersions(ordered[j], ordered[i]), 1);
    }
  }
  assert.equal(compareVersions('1.0.0-999999999999999999', '1.0.0-1000000000000000000'), -1);
  assert.equal(compareVersions('9007199254740992.0.0', '9007199254740993.0.0'), -1);
  assert.equal(compareVersions('1.0.0-1', '1.0.0-A'), -1);
});

test('applies the four bump policies to stable and prerelease versions', () => {
  const cases = [
    ['1.2.3', 'prerelease', '1.2.4-alpha.1'],
    ['1.2.3-alpha.9', 'prerelease', '1.2.3-alpha.10'],
    ['1.2.3-rc', 'prerelease', '1.2.3-rc.1'],
    ['1.2.3-alpha.beta', 'prerelease', '1.2.3-alpha.beta.1'],
    ['1.2.3-alpha.999999999999999999', 'prerelease', '1.2.3-alpha.1000000000000000000'],
    ['1.2.3-alpha.9', 'patch', '1.2.3'],
    ['1.2.3', 'patch', '1.2.4'],
    ['1.2.3-alpha.9', 'minor', '1.3.0'],
    ['1.2.3', 'minor', '1.3.0'],
    ['1.2.3-alpha.9', 'major', '2.0.0'],
    ['1.2.3', 'major', '2.0.0'],
  ];
  for (const [current, bump, expected] of cases) assert.equal(nextVersion(current, bump), expected);
  assert.throws(() => nextVersion('1.2.3', 'build'), /Unsupported/);
});

test('reserves existing tags including future draft tags and ignores noncanonical tags', () => {
  const tags = ['windows-preview-20261102', 'v0.2.0-alpha.1', 'v0.2.0-alpha.10', 'v0.2.0-alpha.2', 'v999.0', 'v01.0.0', 'v900.0.0+build', '2.0.0'];
  assert.equal(nextVersion('0.1.0-alpha.1', 'prerelease', tags), '0.2.0-alpha.11');
  assert.equal(nextVersion('0.1.0-alpha.1', 'patch', tags), '0.2.0');
  assert.equal(nextVersion('0.1.0-alpha.1', 'prerelease', [...tags, 'v0.2.0']), '0.2.1-alpha.1');
  assert.equal(nextVersion('0.3.0-alpha.2', 'prerelease', tags), '0.3.0-alpha.3');
  assert.equal(nextVersion('0.1.0', 'minor', ['v2.4.9-rc.3']), '2.5.0');
  assert.equal(nextVersion('0.1.0', 'major', ['v2.4.9-rc.3']), '3.0.0');
});

test('returns five edits without writing files or changing dependency data and formatting', t => {
  const root = fixture(t);
  const paths = ['VERSION', 'ui/package.json', 'ui/package-lock.json', 'www/package.json', 'www/package-lock.json'];
  const before = new Map(paths.map(path => [join(root, path), readFileSync(join(root, path), 'utf8')]));
  const edits = versionFiles(root, '1.2.3');
  assert.equal(edits.size, 5);
  assert.deepEqual([...edits.keys()], [...before.keys()]);
  assert.equal(edits.get(join(root, 'VERSION')), '1.2.3\r\n');
  for (const [path, original] of before) {
    assert.equal(readFileSync(path, 'utf8'), original, 'The function must not write its inputs');
    if (path.endsWith('package.json')) {
      assert.equal(edits.get(path), original.replace('"version": "1.2.3-alpha.9"', '"version": "1.2.3"'));
    } else if (path.endsWith('package-lock.json')) {
      assert.equal(edits.get(path), original.replace('"version": "1.2.3-alpha.9"', '"version": "1.2.3"').replace('"version": "1.2.3-alpha.9"', '"version": "1.2.3"'));
      const lock = JSON.parse(edits.get(path));
      const old = JSON.parse(original);
      assert.deepEqual(lock.packages['node_modules/library'], old.packages['node_modules/library']);
      assert.deepEqual(lock.metadata, old.metadata);
    }
  }
});

test('rejects inconsistent versions in any package or lockfile root without writing', t => {
  for (const path of ['ui/package.json', 'ui/package-lock.json', 'www/package.json', 'www/package-lock.json']) {
    const root = fixture(t);
    const file = join(root, path);
    const original = readFileSync(file, 'utf8');
    const changed = original.replace('"version": "1.2.3-alpha.9"', '"version": "9.0.0"');
    writeFileSync(file, changed);
    assert.throws(() => versionFiles(root, '2.0.0'), /must match VERSION/);
    assert.equal(readFileSync(file, 'utf8'), changed);
    assert.equal(readFileSync(join(root, 'VERSION'), 'utf8'), '1.2.3-alpha.9\r\n');
  }
  const root = fixture(t);
  const path = join(root, 'ui/package-lock.json');
  const lock = JSON.parse(readFileSync(path, 'utf8'));
  lock.packages[''].version = '1.2.3';
  writeFileSync(path, JSON.stringify(lock));
  assert.throws(() => versionFiles(root, '2.0.0'), /must match VERSION/);
});

test('handles escaped JSON keys and rejects duplicate target version fields', t => {
  const root = fixture(t);
  const path = join(root, 'ui/package.json');
  writeFileSync(path, '{"ver\\u0073ion":"1.2.3-alpha.9","data":"escaped \\\"version\\\": text"}\n');
  assert.equal(versionFiles(root, '1.2.3').get(path), '{"ver\\u0073ion":"1.2.3","data":"escaped \\\"version\\\": text"}\n');
  writeFileSync(path, '{"version":"1.2.3-alpha.9","version":"1.2.3-alpha.9"}');
  assert.throws(() => versionFiles(root, '1.2.3'), /exactly one/);
  writeFileSync(path, '{"version":{},"version":"1.2.3-alpha.9"}');
  assert.throws(() => versionFiles(root, '1.2.3'), /must be strings/);
});

test('metadata CLI reads the repository root even from a different working directory', t => {
  const destination = fixture(t);
  const modulePath = fileURLToPath(new URL('./release-version.mjs', import.meta.url));
  const root = fileURLToPath(new URL('../', import.meta.url));
  const version = readFileSync(join(root, 'VERSION'), 'utf8').trim();
  const output = join(destination, 'artifacts', 'release-build.json');
  const result = spawnSync(process.execPath, [modulePath, 'metadata', 'linux', output], { cwd: destination, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(readFileSync(output, 'utf8')), {
    schema: 1, platform: 'linux', version,
    commitSha: execFileSync('git', ['-C', root, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
  });
  const invalidOutput = join(destination, 'invalid.json');
  const invalid = spawnSync(process.execPath, [modulePath, 'metadata', 'unknown', invalidOutput], { cwd: destination, encoding: 'utf8' });
  assert.notEqual(invalid.status, 0);
  assert.equal(existsSync(invalidOutput), false);
});
