import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const lock = JSON.parse(readFileSync(join(here, 'macos-sshfs-dependencies.json')));
const sha256 = file => createHash('sha256').update(readFileSync(file)).digest('hex');
const run = (command, args) => execFileSync(command, args, { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
const saveJSON = (file, value) => writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
const copy = (source, dest) => { mkdirSync(dirname(dest), { recursive: true }); cpSync(source, dest, { recursive: true, dereference: true }); };

function files(root, relative = '') {
  return readdirSync(join(root, relative)).sort().flatMap(name => {
    const path = relative ? `${relative}/${name}` : name;
    return statSync(join(root, path)).isDirectory() ? files(root, path) : [path];
  });
}

export function inspectDependency(name, prefix) {
  const spec = lock[name];
  const formula = join(prefix, '.brew', `${name}.rb`);
  const receipt = JSON.parse(readFileSync(join(prefix, 'INSTALL_RECEIPT.json')));
  const formulaDigest = sha256(formula);
  if (![spec.formulaSha256, ...(spec.additionalFormulaSha256 ?? [])].includes(formulaDigest) || receipt.source?.spec !== 'stable' || receipt.source?.versions?.stable !== spec.version) {
    throw new Error(`${name}: installed formula/version differs from the reviewed SSHFS dependency lock; actual version=${receipt.source?.versions?.stable}, formulaSHA256=${formulaDigest}. Update the source lock before building.`);
  }
  // The raw receipt can contain personal paths. Keep only build identity fields.
  const selected = Object.fromEntries(['homebrew_version', 'compiler', 'arch', 'built_as_bottle', 'poured_from_bottle', 'built_on', 'used_options', 'runtime_dependencies'].map(key => [key, receipt[key]]));
  return { spec, formula, receipt: { ...selected, source: { spec: 'stable', version: spec.version, tap: receipt.source.tap, tap_git_head: receipt.source.tap_git_head } } };
}

function download(url, expected, dest) {
  mkdirSync(dirname(dest), { recursive: true });
  run('curl', ['--fail', '--location', '--silent', '--show-error', '--proto', '=https', '--proto-redir', '=https', '--retry', '2', '--connect-timeout', '20', '--max-time', '240', url, '-o', dest]);
  if (sha256(dest) !== expected) throw new Error(`Source checksum mismatch: ${basename(dest)}`);
}

export function extractLicenses(archive, output, component) {
  const selectors = {
    sshfs: [/^(COPYING|AUTHORS|LICENSE[^/]*)$/],
    glib: [/^COPYING$/, /^LICENSES\//],
    gettext: [/^COPYING$/, /^gettext-runtime\/(COPYING|intl\/(COPYING.LIB|AUTHORS))$/],
    pcre2: [/^COPYING$/, /^LICENCE(?:\.md)?$/, /^deps\/sljit\/LICENSE$/],
  }[component];
  const names = run('tar', ['-tf', archive]).trim().split('\n');
  const selected = names.filter(name => !name.endsWith('/') && selectors.some(match => match.test(name.replace(/^[^/]+\//, ''))));
  if (!selected.length) throw new Error(`No license material in ${basename(archive)}`);
  const temp = mkdtempSync(join(tmpdir(), 'cage-sshfs-licenses-'));
  try {
    // GLib's COPYING is a symlink to LICENSES/. Extract together and read its
    // target instead of publishing the empty output of tar -xOf on a symlink.
    run('tar', ['-xf', archive, '-C', temp, ...selected]);
    for (const name of selected) {
      const relative = name.replace(/^[^/]+\//, '');
      const dest = join(output, relative);
      mkdirSync(dirname(dest), { recursive: true });
      writeFileSync(dest, readFileSync(join(temp, name)));
    }
  } finally { rmSync(temp, { recursive: true, force: true }); }
}

export function prepare(sdk, output) {
  if (existsSync(output)) throw new Error('Source materials destination already exists.');
  // Resolve and verify all installed recipes before downloading or compiling.
  const installed = Object.fromEntries(['glib', 'gettext', 'pcre2'].map(name => {
    const prefix = realpathSync(run('brew', ['--prefix', name]).trim());
    return [name, { prefix, ...inspectDependency(name, prefix) }];
  }));
  mkdirSync(output, { recursive: true });
  copy(join(here, 'rebuild-macos-sshfs.sh'), join(output, 'rebuild-macos-sshfs.sh'));
  copy(join(here, '../../LICENSE'), join(output, 'cage/LICENSE'));
  for (const file of ['sshfs-openwith.patch', 'macos-fuse-media.sh', 'macos-sshfs-dependencies.json']) copy(join(here, file), join(output, 'cage', file));
  copy(join(here, 'macos-sshfs-source-README'), join(output, 'README'));
  download(lock.sshfs.url, lock.sshfs.sha256, join(output, 'upstream/sshfs.tar.gz'));
  extractLicenses(join(output, 'upstream/sshfs.tar.gz'), join(output, 'licenses/sshfs'), 'sshfs');
  const archives = { glib: 'glib.tar.xz', gettext: 'gettext.tar.gz', pcre2: 'pcre2.tar.bz2' };
  const inputs = { glib: ['lib/libgthread-2.0.a', 'lib/libglib-2.0.a', 'include/glib-2.0', 'lib/glib-2.0/include'], gettext: ['lib/libintl.a', 'include/libintl.h'], pcre2: ['lib/libpcre2-8.a', 'include/pcre2.h', 'include/pcre2posix.h'] };
  for (const [name, { prefix, spec, formula, receipt }] of Object.entries(installed)) {
    copy(formula, join(output, 'homebrew', `${name}.rb`));
    saveJSON(join(output, 'homebrew', `${name}-receipt.json`), receipt);
    const archive = join(output, 'upstream', archives[name]);
    download(spec.url, spec.sha256, archive);
    extractLicenses(archive, join(output, 'licenses', name), name);
    for (const resource of spec.resources ?? []) download(resource.url, resource.sha256, join(output, 'upstream', `${resource.name}.tar.xz`));
    for (const patch of spec.patches ?? []) download(patch.url, patch.sha256, join(output, 'homebrew', patch.file));
    for (const file of inputs[name]) {
      const target = join(output, 'dependencies', name, file);
      copy(join(prefix, file), target);
      // Detect an in-place package update during the snapshot. Compilation uses
      // the frozen copy, never /opt/homebrew/opt symlinks that can change later.
      for (const path of statSync(target).isDirectory() ? files(target) : ['']) {
        if (sha256(join(target, path)) !== sha256(join(prefix, file, path))) throw new Error(`${name}: dependency changed while collecting sources.`);
      }
    }
  }
  download('https://raw.githubusercontent.com/Homebrew/homebrew-core/68093a0680c21eb19fb034af0d8c5104848315e7/LICENSE.txt', 'f80329e58613ad669c0e73cb132d8060b9b2c55e339c73848068e4d1567f4627', join(output, 'licenses/homebrew/LICENSE.txt'));
  const sdkFiles = [...files(join(sdk, 'include')).map(path => `include/${path}`), 'lib/libfuse3.dylib'];
  saveJSON(join(output, 'build.json'), {
    cageVersion: readFileSync(join(here, '../../VERSION'), 'utf8').trim(),
    dependencies: lock,
    toolchain: { clang: run('clang', ['--version']).split('\n')[0], sdk: run('xcrun', ['--show-sdk-version']).trim(), deploymentTarget: process.env.MACOSX_DEPLOYMENT_TARGET ?? '14.0' },
    macfuse: { version: '5.5.0', url: 'https://github.com/macfuse/macfuse/releases/download/macfuse-5.5.0/macfuse-5.5.0.dmg', sha256: '92c21ef2db4199bcf5bddc8e6efb57b23ecb9715f74ab38cba2c77ab226d3830', files: sdkFiles.map(path => ({ path, sha256: sha256(join(sdk, path)) })) },
  });
  writeFileSync(join(output, 'NOTICE'), `Cage macOS SSHFS corresponding source and relink materials\n\nSSHFS ${lock.sshfs.version} (${lock.sshfs.commit}): GNU GPL version 2 (see licenses/sshfs/COPYING).\nCage changes: cage/sshfs-openwith.patch (guest-local file-opening metadata).\nStatically linked GLib ${lock.glib.version} and GNU gettext libintl ${lock.gettext.version}: LGPL-2.1-or-later.\nPCRE2 ${lock.pcre2.version}: BSD-3-Clause WITH PCRE2-exception; bundled SLJIT: BSD-2-Clause.\nUpstream copyright and license notices are retained in licenses/ and the complete upstream archives.\nHomebrew recipes are under the BSD 2-Clause license in licenses/homebrew/LICENSE.txt.\nThe source archive includes the exact static libraries and headers used by the build.\nThe external macFUSE runtime is installed separately; its pinned SDK acquisition instructions are in README.\nThe final signed executable hash and every distributed source/relink input hash are in manifest.json.\n`);
}

export function finish(materials, binary, archive, licenses) {
  const build = JSON.parse(readFileSync(join(materials, 'build.json')));
  const manifest = { format: 1, cageVersion: build.cageVersion, executable: { path: 'project/cage-sshfs', sha256: sha256(binary) }, files: files(materials).filter(path => path !== 'manifest.json').map(path => ({ path, sha256: sha256(join(materials, path)) })) };
  saveJSON(join(materials, 'manifest.json'), manifest);
  copy(join(materials, 'licenses'), licenses);
  copy(join(materials, 'NOTICE'), join(licenses, 'NOTICE'));
  copy(join(materials, 'build.json'), join(licenses, 'build.json'));
  mkdirSync(dirname(archive), { recursive: true });
  run('tar', ['-czf', archive, '-C', materials, '.']);
  verifyArchive(archive, binary, build.cageVersion);
}

export function verifyArchive(archive, binary, version) {
  const temp = mkdtempSync(join(tmpdir(), 'cage-sshfs-source-verify-'));
  try {
    run('tar', ['-xzf', archive, '-C', temp]);
    const manifest = JSON.parse(readFileSync(join(temp, 'manifest.json')));
    if (manifest.format !== 1 || manifest.cageVersion !== version || manifest.executable?.sha256 !== sha256(binary)) throw new Error('macOS guest source does not match this signed executable/version.');
    for (const item of manifest.files) {
      if (!item.path.split('/').every(part => part && part !== '.' && part !== '..') || item.path.startsWith('/') || sha256(join(temp, item.path)) !== item.sha256) throw new Error(`macOS guest source integrity check failed: ${item.path}`);
    }
  } finally { rmSync(temp, { recursive: true, force: true }); }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [action, ...args] = process.argv.slice(2);
  if (action === 'prepare' && args.length === 2) prepare(...args);
  else if (action === 'finish' && args.length === 4) finish(...args);
  else if (action === 'verify' && args.length === 3) verifyArchive(...args);
  else throw new Error('Usage: collect-macos-sshfs-sources.mjs prepare SDK DIRECTORY | finish DIRECTORY BINARY ARCHIVE LICENSES | verify ARCHIVE BINARY VERSION');
}
