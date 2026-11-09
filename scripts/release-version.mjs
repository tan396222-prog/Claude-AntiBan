import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const numeric = /^(?:0|[1-9][0-9]*)$/;
const syntax = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

export function parseVersion(version) {
  if (typeof version !== 'string' || version !== version.trim()) throw new Error('Expected a canonical SemVer without build metadata');
  const match = syntax.exec(version);
  if (!match) throw new Error(`Invalid version: ${version}`);
  const prerelease = match[4] ? match[4].split('.') : [];
  if (prerelease.some(part => /^[0-9]+$/.test(part) && !numeric.test(part))) throw new Error(`Invalid numeric prerelease identifier: ${version}`);
  return { major: BigInt(match[1]), minor: BigInt(match[2]), patch: BigInt(match[3]), prerelease };
}

function compare(a, b) {
  return a === b ? 0 : a < b ? -1 : 1;
}

export function compareVersions(a, b) {
  const left = parseVersion(a);
  const right = parseVersion(b);
  for (const part of ['major', 'minor', 'patch']) {
    const order = compare(left[part], right[part]);
    if (order) return order;
  }
  if (!left.prerelease.length || !right.prerelease.length) {
    return compare(Number(!left.prerelease.length), Number(!right.prerelease.length));
  }
  for (let index = 0; index < Math.max(left.prerelease.length, right.prerelease.length); index++) {
    const x = left.prerelease[index];
    const y = right.prerelease[index];
    if (x === undefined || y === undefined) return compare(left.prerelease.length, right.prerelease.length);
    const xNumeric = numeric.test(x);
    const yNumeric = numeric.test(y);
    const order = xNumeric && yNumeric ? compare(BigInt(x), BigInt(y))
      : xNumeric !== yNumeric ? (xNumeric ? -1 : 1) : compare(x, y);
    if (order) return order;
  }
  return 0;
}

export function nextVersion(current, bump, existingTags = []) {
  parseVersion(current);
  if (!['prerelease', 'patch', 'minor', 'major'].includes(bump)) throw new Error(`Unsupported version bump: ${bump}`);
  if (!Array.isArray(existingTags)) throw new TypeError('existingTags must be an array');
  let highest = current;
  for (const tag of existingTags) {
    if (typeof tag !== 'string' || !tag.startsWith('v')) continue;
    const version = tag.slice(1);
    try { parseVersion(version); } catch { continue; }
    if (compareVersions(version, highest) > 0) highest = version;
  }
  let { major, minor, patch, prerelease } = parseVersion(highest);
  if (bump === 'major') {
    major++;
    minor = patch = 0n;
    prerelease = [];
  } else if (bump === 'minor') {
    minor++;
    patch = 0n;
    prerelease = [];
  } else if (bump === 'patch') {
    if (!prerelease.length) patch++;
    prerelease = [];
  } else if (!prerelease.length) {
    patch++;
    prerelease = ['alpha', '1'];
  } else {
    const last = prerelease.length - 1;
    if (numeric.test(prerelease[last])) prerelease[last] = String(BigInt(prerelease[last]) + 1n);
    else prerelease.push('1');
  }
  return `${major}.${minor}.${patch}${prerelease.length ? `-${prerelease.join('.')}` : ''}`;
}

// JSON.parse validates the document first. Locate only the requested string
// values so dependency data, escaped strings and all formatting stay intact.
function replaceVersions(text, paths, version) {
  const tokens = [...text.matchAll(/"(?:\\.|[^"\\])*"|[{}\[\]:,]|-?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?|true|false|null/g)];
  const changes = [];
  const counts = paths.map(() => 0);
  let index = 0;
  function value(path) {
    const token = tokens[index++];
    const target = paths.findIndex(candidate => candidate.length === path.length && candidate.every((part, i) => part === path[i]));
    if (target !== -1) {
      if (!token[0].startsWith('"')) throw new Error('Package version fields must be strings');
      counts[target]++;
      changes.push({ start: token.index, end: token.index + token[0].length });
    }
    if (token[0] === '{') {
      if (tokens[index][0] === '}') { index++; return; }
      do {
        const key = JSON.parse(tokens[index++][0]);
        index++; // Colon.
        value([...path, key]);
      } while (tokens[index++][0] !== '}');
    } else if (token[0] === '[') {
      if (tokens[index][0] === ']') { index++; return; }
      let item = 0;
      do { value([...path, item++]); } while (tokens[index++][0] !== ']');
    }
  }
  value([]);
  if (counts.some(count => count !== 1)) throw new Error('Expected exactly one value for each package version field');
  let result = text;
  for (const { start, end } of changes.sort((a, b) => b.start - a.start)) {
    result = result.slice(0, start) + JSON.stringify(version) + result.slice(end);
  }
  return result;
}

export function versionFiles(root, version) {
  parseVersion(version);
  const versionPath = resolve(root, 'VERSION');
  const original = readFileSync(versionPath, 'utf8');
  const current = original.trim();
  parseVersion(current);
  const result = new Map([[versionPath, original.replace(current, version)]]);
  for (const directory of ['ui', 'www']) {
    for (const filename of ['package.json', 'package-lock.json']) {
      const path = resolve(root, directory, filename);
      const text = readFileSync(path, 'utf8');
      const document = JSON.parse(text);
      const lock = filename === 'package-lock.json';
      if (document?.version !== current || (lock && document?.packages?.['']?.version !== current)) {
        throw new Error(`${directory}/${filename}: version fields must match VERSION (${current})`);
      }
      const paths = lock ? [['version'], ['packages', '', 'version']] : [['version']];
      result.set(path, replaceVersions(text, paths, version));
    }
  }
  return result;
}

function main(args) {
  const [command, platform, output] = args;
  if (args.length !== 3 || command !== 'metadata' || !['macos', 'windows', 'linux'].includes(platform)) {
    throw new Error('Usage: node scripts/release-version.mjs metadata <macos|windows|linux> <output>');
  }
  const root = fileURLToPath(new URL('../', import.meta.url));
  const version = readFileSync(resolve(root, 'VERSION'), 'utf8').trim();
  parseVersion(version);
  const commitSha = execFileSync('git', ['-C', root, 'rev-parse', 'HEAD'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  if (!/^[0-9a-f]{40}$/.test(commitSha)) throw new Error('Release metadata requires a 40-character Git commit SHA');
  const destination = resolve(output);
  mkdirSync(dirname(destination), { recursive: true });
  writeFileSync(destination, `${JSON.stringify({ schema: 1, platform, version, commitSha }, null, 2)}\n`);
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try { main(process.argv.slice(2)); } catch (error) { console.error(error.message); process.exitCode = 1; }
}
