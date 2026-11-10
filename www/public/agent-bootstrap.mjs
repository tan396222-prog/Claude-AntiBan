#!/usr/bin/env node
// Node >=20, standard library only. No installation or user data mutation.
import { createHash } from 'node:crypto';
import { createWriteStream } from 'node:fs';
import { chmod, link, mkdir, mkdtemp, rm } from 'node:fs/promises';
import { isAbsolute, join, resolve } from 'node:path';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { pathToFileURL } from 'node:url';

const repo = 'Lee-xeo/Claude-Cage';
const protocol = 1;
export class BootstrapError extends Error {
  constructor(code, message, nextAction) { super(message); this.code = code; this.next_action = nextAction; }
}
const fail = (code, message, next = 'plan') => { throw new BootstrapError(code, message, next); };

export function parseArgs(args) {
  const options = { action: 'plan', platform: `${process.platform}-${process.arch === 'x64' ? 'amd64' : process.arch}`, version: '', destination: '', allowUnsigned: false };
  if (args[0] && !args[0].startsWith('--')) options.action = args.shift();
  for (let i = 0; i < args.length; i++) {
    const flag = args[i];
    if (flag === '--allow-unsigned') { options.allowUnsigned = true; continue; }
    if (!['--platform', '--version', '--destination'].includes(flag) || !args[i + 1] || args[i + 1].startsWith('--')) fail('invalid_input', 'Usage: agent-bootstrap.mjs plan|download [--version TAG] [--platform darwin-arm64|win32-amd64|linux-amd64] [--destination ABSOLUTE_DIRECTORY] [--allow-unsigned]');
    options[flag.slice(2)] = args[++i];
  }
  if (!['plan', 'download'].includes(options.action)) fail('invalid_input', 'Action must be plan or download.');
  if (!['darwin-arm64', 'win32-amd64', 'linux-amd64'].includes(options.platform)) fail('unsupported_platform', 'No desktop package is selected for this platform. Read the platform guide.');
  if (options.version && !/^v?\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$/.test(options.version)) fail('invalid_input', 'Use an explicit release tag such as v0.1.6-alpha-preview.');
  if (options.version && !options.version.startsWith('v')) options.version = `v${options.version}`;
  if (options.action === 'download' && !isAbsolute(options.destination)) fail('invalid_input', 'download requires --destination with an absolute directory path.');
  return options;
}

// Validate HTTPS at every redirect; do not use browser credentials or tokens.
export async function httpsResponse(url, fetchImpl = fetch) {
  for (let redirects = 0; redirects <= 5; redirects++) {
    const parsed = new URL(url);
    if (parsed.protocol !== 'https:' || parsed.username || parsed.password) fail('unsafe_download', 'Only public HTTPS downloads without embedded credentials are supported.');
    let response;
    try {
      response = await fetchImpl(parsed, { redirect: 'manual', signal: AbortSignal.timeout(120000), headers: { 'User-Agent': 'Cage-Agent-Bootstrap/1', Accept: 'application/json, application/octet-stream' } });
    } catch {
      fail('download_connection_failed', 'Could not connect to the public release service. Check host connectivity and user-configured proxy settings; Node 24 can use --use-env-proxy.', 'check_host_network');
    }
    if ([301, 302, 303, 307, 308].includes(response.status)) {
      const location = response.headers.get('location');
      await response.body?.cancel();
      if (!location) fail('download_failed', 'Download redirect has no location.');
      url = new URL(location, parsed).href;
      continue;
    }
    if (!response.ok) { await response.body?.cancel(); fail(response.status === 403 || response.status === 429 ? 'rate_limited' : 'download_failed', `Public download returned HTTP ${response.status}. Retry later or choose another published release.`); }
    return response;
  }
  fail('download_failed', 'Too many redirects.');
}

async function boundedText(url, fetchImpl) {
  const response = await httpsResponse(url, fetchImpl);
  const chunks = []; let total = 0;
  for await (const chunk of response.body) { total += chunk.length; if (total > 4 * 1024 * 1024) fail('invalid_release', 'Release metadata is too large.'); chunks.push(chunk); }
  return Buffer.concat(chunks).toString('utf8').replace(/^\uFEFF/, '');
}

export function selectAsset(release, options) {
  const tag = release.tag_name;
  if (!/^v\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$/.test(tag) || release.draft || (options.version && tag !== options.version)) fail('invalid_release', 'Release tag does not match the requested published release.');
  const version = tag.slice(1);
  const suffix = { 'darwin-arm64': 'macos-arm64.dmg', 'linux-amd64': 'linux-amd64.deb', 'win32-amd64': 'windows-amd64-setup.exe' }[options.platform];
  let asset = release.assets?.find(a => a.name === `Cage-${version}-${suffix}`);
  let unsigned = false;
  if (!asset && options.platform === 'win32-amd64') {
    asset = release.assets?.find(a => a.name === `Cage-${version}-windows-amd64-setup-unsigned.exe`);
    if (asset && !options.allowUnsigned) fail('unsigned_package_requires_consent', 'Only an unsigned Windows preview is available. Ask the user before selecting it with --allow-unsigned.', 'user_decision');
    unsigned = !!asset;
  }
  const sums = release.assets?.find(a => a.name === 'SHA256SUMS');
  if (!asset || !sums) fail('asset_unavailable', 'This release has no matching package and checksum manifest. Choose a published release containing both.');
  if (!Number.isSafeInteger(asset.size) || asset.size <= 0) fail('invalid_release', 'The package has no valid published size.');
  for (const item of [asset, sums]) {
    if (item.browser_download_url !== `https://github.com/${repo}/releases/download/${tag}/${item.name}`) fail('invalid_release', 'Unexpected release asset origin.');
  }
  return { asset, sums, unsigned, tag, version };
}

export function checksumFor(manifest, name) {
  const matches = manifest.split(/\r?\n/).map(line => line.match(/^([a-fA-F0-9]{64}) [ *](\S+)\s*$/)).filter(match => match && match[2] === name);
  if (matches.length !== 1) fail('invalid_checksum_manifest', 'Expected exactly one SHA-256 entry for the selected package.');
  return matches[0][1].toLowerCase();
}

export async function makePlan(options, fetchImpl = fetch) {
  const releaseURL = `https://api.github.com/repos/${repo}/releases/${options.version ? `tags/${options.version}` : 'latest'}`;
  let release;
  try { release = JSON.parse(await boundedText(releaseURL, fetchImpl)); } catch (error) { if (error instanceof BootstrapError) throw error; fail('invalid_release', 'Could not read public release metadata.'); }
  const selected = selectAsset(release, options);
  const sha256 = checksumFor(await boundedText(selected.sums.browser_download_url, fetchImpl), selected.asset.name);
  const cli = { 'darwin-arm64': '/Applications/Cage.app/Contents/Resources/cage', 'linux-amd64': '/usr/bin/claude-cage', 'win32-amd64': '%LOCALAPPDATA%\\Programs\\Cage\\resources\\cage.exe' }[options.platform];
  const installation = {
    'darwin-arm64': { guide: 'https://lee-xeo.github.io/Claude-Cage/docs/installation/', steps: ['Check codesign and spctl on the mounted Cage.app. Preserve macOS Gatekeeper checks.', 'For an update, save work and stop Cage through its existing UI before replacing app files.', 'Mount the verified DMG read-only with hdiutil attach -readonly -nobrowse. Copy Cage.app to /Applications using the existing installation guide; eject the mounted volume.', 'Run the installed CLI agent capabilities. If agent is unknown, this release predates Agent support; use its documented UI or an authorized newer build.'] },
    'linux-amd64': { guide: 'https://lee-xeo.github.io/Claude-Cage/docs/installation/', argv: ['sudo', 'apt-get', 'install', '--', '<downloaded_absolute_path>'], steps: ['Save work and stop Cage before an update. Run the package manager in the user-approved privilege context.', 'Run /usr/bin/claude-cage agent capabilities. The name cage belongs to a different Linux program.'] },
    'win32-amd64': { guide: 'https://lee-xeo.github.io/Claude-Cage/docs/installation/', argv: ['<downloaded_absolute_path>', '/S'], steps: ['Save work and stop Cage before an update. Inspect Get-AuthenticodeSignature on signed packages.', 'Run the installer in the user-approved context; system security prompts require the user.', 'Default per-user CLI: $env:LOCALAPPDATA\\Programs\\Cage\\resources\\cage.exe. Honor any custom install directory; then run agent capabilities.'] },
  }[options.platform];
  return { protocol, status: 'planned', release: selected.tag, release_url: `https://github.com/${repo}/releases/tag/${selected.tag}`, platform: options.platform, asset: selected.asset.name, url: selected.asset.browser_download_url, size: selected.asset.size, sha256, unsigned: selected.unsigned, cli, installation, data_policy: 'Preserve ~/.claude-cage, user projects and persistent VM disks. Downloads and application files are separate from profile data.' };
}

export async function downloadPlan(plan, destination, fetchImpl = fetch) {
  if (!isAbsolute(destination)) fail('invalid_input', 'Destination must be an absolute directory.');
  await mkdir(destination, { recursive: true });
  const stage = await mkdtemp(join(destination, '.cage-download-'));
  try {
    const temp = join(stage, plan.asset);
    const response = await httpsResponse(plan.url, fetchImpl);
    const hash = createHash('sha256'); let size = 0;
    const verify = new Transform({ transform(chunk, encoding, callback) {
      size += chunk.length;
      if (size > plan.size) { callback(new BootstrapError('size_mismatch', 'Package exceeds the release size.', 'plan')); return; }
      hash.update(chunk); callback(null, chunk);
    } });
    await pipeline(Readable.fromWeb(response.body), verify, createWriteStream(temp, { flags: 'wx', mode: 0o600 }));
    if (size !== plan.size || hash.digest('hex') !== plan.sha256) fail('checksum_mismatch', 'Package size or SHA-256 does not match the selected release. The staged download was discarded.');
    await chmod(temp, 0o600);
    const path = join(destination, plan.asset);
    // link is exclusive: never truncate a prior download, application or data.
    await link(temp, path);
    const installation = { ...plan.installation };
    if (installation.argv) installation.argv = installation.argv.map(arg => arg === '<downloaded_absolute_path>' ? path : arg);
    return { ...plan, status: 'downloaded', path, installation };
  } finally { await rm(stage, { recursive: true, force: true }); }
}

export async function main(args = process.argv.slice(2)) {
  try {
    const options = parseArgs([...args]);
    let result = await makePlan(options);
    if (options.action === 'download') result = await downloadPlan(result, options.destination);
    process.stdout.write(`${JSON.stringify(result)}\n`);
  } catch (error) {
    // Network errors can contain URLs; use bounded static messages on unknown errors.
    const known = error instanceof BootstrapError;
    process.stdout.write(`${JSON.stringify({ protocol, status: 'error', error: { code: known ? error.code : error.code === 'EEXIST' ? 'destination_exists' : 'bootstrap_failed', message: known ? error.message : 'Could not prepare the package. Preserve existing files and inspect connectivity, destination permissions and available space.', next_action: known ? error.next_action : 'plan' } })}\n`);
    process.exitCode = known && error.code === 'invalid_input' ? 2 : 1;
  }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) await main();
