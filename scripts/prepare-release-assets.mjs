// Assemble local assets produced by the platform release packagers. Those
// packagers and the native updater own signature verification; this script
// checks the release names, complete source materials and exact file digests.
import { createHash } from 'node:crypto';
import { constants, createWriteStream } from 'node:fs';
import { copyFile, lstat, mkdir, mkdtemp, open, readFile, readdir, rm, rmdir, unlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { Transform, Writable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';

const maxChecksums = 64 * 1024;
const maxInstaller = 512 * 1024 * 1024;

async function releaseNames() {
  const version = (await readFile(new URL('../VERSION', import.meta.url), 'utf8')).trim();
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) throw new Error('Invalid repository VERSION');
  const dependencies = JSON.parse(await readFile(new URL('../packaging/windows/dependencies.json', import.meta.url), 'utf8'));
  const linuxDependencies = JSON.parse(await readFile(new URL('../packaging/linux/dependencies.json', import.meta.url), 'utf8'));
  const macCoreScript = await readFile(new URL('./fetch-proxy-core.sh', import.meta.url), 'utf8');
  const macCore = macCoreScript.match(/^version=(v[\d.]+)$/m)?.[1];
  if (!macCore || !/^v[\d.]+$/.test(dependencies.mihomo.version) || !/^[\d.]+$/.test(dependencies.qemu.version)) {
    throw new Error('Cannot identify the pinned release dependencies');
  }
  return {
    version,
    mac: `Cage-${version}-macos-arm64.dmg`,
    windows: `Cage-${version}-windows-amd64-setup.exe`,
    linux: `Cage-${version}-linux-amd64.deb`,
    linuxSource: linuxDependencies.mihomoSource.file,
    macSource: `mihomo-${macCore}-source.tar.gz`,
    windowsSource: `mihomo-${dependencies.mihomo.version}-source.tar.gz`,
    qemuPrefix: `qemu-${dependencies.qemu.version}-windows-full-source.`,
    qemuSource: dependencies.qemuSource?.file,
  };
}

async function regularFile(path) {
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink()) throw new Error(`Expected a regular file: ${path}`);
  return info;
}

async function readManifest(directory) {
  const path = join(directory, 'SHA256SUMS');
  const info = await regularFile(path);
  if (!info.size || info.size > maxChecksums) throw new Error(`Invalid SHA256SUMS size: ${directory}`);
  const text = (await readFile(path, 'utf8')).replace(/^\uFEFF/, '');
  const entries = new Map();
  for (const line of text.split(/\r?\n/)) {
    if (!line) continue;
    const match = /^([a-f0-9]{64}) [ *]([A-Za-z0-9][A-Za-z0-9._-]*)$/i.exec(line);
    if (!match || entries.has(match[2])) throw new Error(`Invalid or duplicate SHA256SUMS entry: ${directory}`);
    entries.set(match[2], match[1].toLowerCase());
  }
  if (!entries.size) throw new Error(`Empty SHA256SUMS: ${directory}`);
  return entries;
}

function isQemuArchive(name, names) {
  return name.startsWith(names.qemuPrefix) && /^(?:tar\.)?(?:gz|xz|bz2|zst|zip|7z|tar)$/.test(name.slice(names.qemuPrefix.length));
}

async function verifyAndStage(source, digest, destination, installer) {
  const before = await regularFile(source);
  if (!before.size || (installer && before.size > maxInstaller)) throw new Error(`Invalid release asset size: ${source}`);
  const file = await open(source, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
  try {
    const opened = await file.stat();
    if (!opened.isFile() || before.ino !== opened.ino || before.dev !== opened.dev) throw new Error(`Release asset changed while opening: ${source}`);
    const hash = createHash('sha256');
    let size = 0;
    const sink = destination ? createWriteStream(destination, { flags: 'wx', mode: 0o600 }) : new Writable({ write(_chunk, _encoding, callback) { callback(); } });
    await pipeline(file.createReadStream({ autoClose: false }), new Transform({
      transform(chunk, _encoding, callback) {
        size += chunk.length;
        if (size > opened.size) return callback(new Error(`Release asset changed while reading: ${source}`));
        hash.update(chunk);
        callback(null, chunk);
      },
    }), sink);
    if (size !== opened.size || hash.digest('hex') !== digest) throw new Error(`SHA-256 mismatch: ${source}`);
  } finally {
    await file.close();
  }
}

export async function prepareReleaseAssets({ inputs, output }) {
  if (!Array.isArray(inputs) || !inputs.length || !output) throw new Error('Provide --output and at least one platform release directory');
  const target = resolve(output);
  try {
    await lstat(target);
    throw new Error(`Output must be a new directory; existing release assets are never overwritten: ${target}`);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  const names = await releaseNames();
  const all = new Map();
  const inputsToVerify = [];
  for (const input of inputs) {
    const directory = resolve(input);
    const info = await lstat(directory);
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error(`Expected a platform release directory: ${directory}`);
    const files = await readdir(directory);
    if (files.some(name => /^Cage-.*-windows-.*-unsigned\.exe$/i.test(name) || name === 'DEVELOPMENT-BUILD.txt')) {
      throw new Error(`Unsigned Windows development build cannot be prepared for release: ${directory}`);
    }
    const entries = await readManifest(directory);
    const mac = entries.has(names.mac);
    const windows = entries.has(names.windows);
    const linux = entries.has(names.linux);
    if (!mac && !windows && !linux) throw new Error(`No supported package name for VERSION ${names.version}: ${directory}`);
    if (linux && !entries.has(names.linuxSource)) throw new Error(`Missing Linux Mihomo source in SHA256SUMS: ${directory}`);
    if (mac && !entries.has(names.macSource)) throw new Error(`Missing macOS Mihomo source in SHA256SUMS: ${directory}`);
    if (windows && (!entries.has(names.windowsSource) || ![...entries.keys()].some(name => isQemuArchive(name, names)))) {
      throw new Error(`Windows release requires its Mihomo and complete QEMU source archives in the same SHA256SUMS: ${directory}`);
    }
    for (const [name, digest] of entries) {
      const installer = name === names.mac || name === names.windows || name === names.linux;
      if (!installer && name !== names.macSource && name !== names.windowsSource && name !== names.linuxSource && name !== names.qemuSource && !isQemuArchive(name, names)) {
        throw new Error(`Unsupported asset or version in SHA256SUMS: ${name}`);
      }
      const existing = all.get(name);
      if (existing && existing !== digest) throw new Error(`Conflicting same-name release asset: ${name}`);
      inputsToVerify.push({ source: join(directory, name), digest, name, installer, first: !existing });
      all.set(name, digest);
    }
  }

  const staging = await mkdtemp(join(tmpdir(), 'cage-release-assets-'));
  const created = [];
  let ownsTarget = false;
  try {
    // Validate every input, including identical-name duplicates. The output
    // gets these verified snapshots, never a second read of mutable inputs.
    for (const asset of inputsToVerify) {
      await verifyAndStage(asset.source, asset.digest, asset.first ? join(staging, asset.name) : undefined, asset.installer);
    }
    const sorted = [...all].sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0);
    const checksums = sorted.map(([name, digest]) => `${digest}  ${name}\n`).join('');
    if (Buffer.byteLength(checksums) > maxChecksums) throw new Error('Merged SHA256SUMS exceeds the updater limit');
    await mkdir(dirname(target), { recursive: true });
    await mkdir(target); // Exclusive creation also closes the preflight race.
    ownsTarget = true;
    for (const [name] of sorted) {
      await copyFile(join(staging, name), join(target, name), constants.COPYFILE_EXCL);
      created.push(name);
    }
    await writeFile(join(target, 'SHA256SUMS'), checksums, { flag: 'wx', mode: 0o644 });
    created.push('SHA256SUMS');
    return { version: names.version, directory: target, assets: sorted.map(([name]) => name) };
  } catch (error) {
    if (ownsTarget) {
      // Delete only our files. Preserve any unexpected concurrently added file.
      for (const name of created) await unlink(join(target, name)).catch(() => {});
      await rmdir(target).catch(() => {});
    }
    throw error;
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}

async function main() {
  const { values, positionals } = parseArgs({ allowPositionals: true, options: {
    output: { type: 'string' }, help: { type: 'boolean', short: 'h' },
  } });
  if (values.help) {
    console.log('Usage: node scripts/prepare-release-assets.mjs --output NEW_DIRECTORY PLATFORM_DIRECTORY [...]\nAccepts macOS, Windows and Linux release outputs with SHA256SUMS. Checks current VERSION, source archives and hashes; macOS/Windows signature verification belongs to their packagers. Linux packages use manual system installation. Never uploads or overwrites an existing output directory.');
    return;
  }
  const result = await prepareReleaseAssets({ inputs: positionals, output: values.output });
  console.log(`Prepared Cage ${result.version}: ${result.assets.length} verified assets and one SHA256SUMS in ${result.directory}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
