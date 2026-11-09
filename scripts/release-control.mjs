import { createHash } from 'node:crypto';
import { execFileSync, spawnSync } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { appendFile, mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { Readable } from 'node:stream';
import { compareVersions, nextVersion, parseVersion, versionFiles } from './release-version.mjs';
import { prepareReleaseAssets } from './prepare-release-assets.mjs';

export const versionPaths = ['VERSION', 'ui/package.json', 'ui/package-lock.json', 'www/package.json', 'www/package-lock.json'];
const repoRoot = fileURLToPath(new URL('../', import.meta.url));
const shaPattern = /^[0-9a-f]{40}$/;
const platforms = ['macos', 'windows', 'linux'];

function checkContext(context) {
  if (context.repository !== 'Lee-xeo/Claude-Cage' || !shaPattern.test(context.sourceSha) || !/^\d+$/.test(context.runId) || context.defaultBranch !== 'main') {
    throw new Error('Invalid release repository, source commit, run ID or default branch.');
  }
}
const marker = context => `<!-- cage-release-run:${context.runId}; commit:${context.commitSha} -->`;
const message = (version, runId) => `chore(release): v${version}\n\nCage-Release-Run: ${runId}`;
const branch = runId => `codex/release/${runId}`;
const apiRoot = context => `/repos/${context.repository}`;
async function optional(api, path) {
  try { return await api('GET', path); }
  catch (error) { if (error.status === 404) return null; throw error; }
}

// The tag endpoint only returns published Releases. Authenticated pagination
// also includes drafts, which must be found before retrying an interrupted run.
async function findRelease(api, base, tag) {
  let found = null;
  for (let page = 1; ; page++) {
    const releases = await api('GET', `${base}/releases?per_page=100&page=${page}`);
    for (const release of releases) {
      if (release.tag_name !== tag) continue;
      if (found) throw new Error(`Multiple Releases reserve ${tag}; review them before retrying.`);
      found = release;
    }
    if (releases.length < 100) return found;
  }
}

// Build expected version files from the immutable source commit, even when
// publish is checking out the candidate. This catches unrelated file edits.
export async function expectedFiles(root, sourceSha, version) {
  if (!shaPattern.test(sourceSha)) throw new Error('Invalid source commit.');
  const fixture = await mkdtemp(join(tmpdir(), 'cage-release-version-'));
  try {
    for (const path of versionPaths) {
      const content = execFileSync('git', ['show', `${sourceSha}:${path}`], { cwd: root, maxBuffer: 20 << 20 });
      await mkdir(dirname(join(fixture, path)), { recursive: true });
      await writeFile(join(fixture, path), content);
    }
    return new Map([...versionFiles(fixture, version)].map(([path, text]) => [relative(fixture, path).replaceAll('\\', '/'), text]));
  } finally { await rm(fixture, { recursive: true, force: true }); }
}

export async function verifyCandidate(api, context, expected) {
  checkContext(context);
  parseVersion(context.version);
  if (!shaPattern.test(context.commitSha)) throw new Error('Invalid candidate commit.');
  const base = apiRoot(context);
  const commit = await api('GET', `${base}/git/commits/${context.commitSha}`);
  if (commit.message !== message(context.version, context.runId) || commit.parents?.length !== 1 || commit.parents[0].sha !== context.sourceSha) {
    throw new Error('Candidate does not belong to this release run and source commit.');
  }
  const diff = await api('GET', `${base}/compare/${context.sourceSha}...${context.commitSha}`);
  if (diff.status !== 'ahead' || diff.ahead_by !== 1 || diff.files?.length !== versionPaths.length ||
      diff.files.some(file => file.status !== 'modified' || !versionPaths.includes(file.filename))) {
    throw new Error('Release candidate must change only the five application version files.');
  }
  for (const [path, content] of expected) {
    const file = await api('GET', `${base}/contents/${path}?ref=${context.commitSha}`);
    if (file.type !== 'file' || file.encoding !== 'base64' || Buffer.from(file.content, 'base64').toString('utf8') !== content) {
      throw new Error(`Candidate contains an unexpected change: ${path}`);
    }
  }
}

export async function prepareRelease({ api, root, ...context }) {
  checkContext(context);
  const base = apiRoot(context);
  const main = await api('GET', `${base}/git/ref/heads/${context.defaultBranch}`);
  const existing = await optional(api, `${base}/git/ref/heads/${branch(context.runId)}`);
  if (existing) {
    const commit = await api('GET', `${base}/git/commits/${existing.object.sha}`);
    const version = /^chore\(release\): v([^\n]+)\n\nCage-Release-Run: \d+$/.exec(commit.message)?.[1];
    parseVersion(version);
    if (main.object.sha !== context.sourceSha && main.object.sha !== existing.object.sha) throw new Error('Main advanced; start a new explicitly requested release.');
    const result = { ...context, version, commitSha: existing.object.sha };
    await verifyCandidate(api, result, await expectedFiles(root, context.sourceSha, version));
    return result;
  }
  if (main.object.sha !== context.sourceSha) throw new Error('Main advanced after dispatch; no version was changed. Start a new release.');
  const current = execFileSync('git', ['show', `${context.sourceSha}:VERSION`], { cwd: root, encoding: 'utf8' }).trim();
  const refs = await api('GET', `${base}/git/matching-refs/tags/v`);
  const reserved = refs.map(ref => ref.ref.replace('refs/tags/', ''));
  for (let page = 1; ; page++) {
    const releases = await api('GET', `${base}/releases?per_page=100&page=${page}`);
    reserved.push(...releases.map(release => release.tag_name));
    if (releases.length < 100) break;
  }
  const version = nextVersion(current, context.bump, reserved);
  if (compareVersions(version, current) <= 0) throw new Error('Release version must advance.');
  // Tags and draft Releases reserve their version. Never reuse someone else's draft.
  if (await findRelease(api, base, `v${version}`)) throw new Error(`Release v${version} is already reserved.`);
  const files = await expectedFiles(root, context.sourceSha, version);
  const source = await api('GET', `${base}/git/commits/${context.sourceSha}`);
  const tree = await api('POST', `${base}/git/trees`, {
    base_tree: source.tree.sha,
    tree: [...files].map(([path, content]) => ({ path, mode: '100644', type: 'blob', content })),
  });
  const candidate = await api('POST', `${base}/git/commits`, {
    message: message(version, context.runId), tree: tree.sha, parents: [context.sourceSha],
  });
  await api('POST', `${base}/git/refs`, { ref: `refs/heads/${branch(context.runId)}`, sha: candidate.sha });
  const result = { ...context, version, commitSha: candidate.sha };
  await verifyCandidate(api, result, files);
  return result;
}

export async function assembleRelease({ directory, output, version, commitSha, assemble = prepareReleaseAssets }) {
  parseVersion(version);
  if (!shaPattern.test(commitSha)) throw new Error('Invalid artifact commit.');
  for (const platform of platforms) {
    const metadata = JSON.parse(await readFile(join(directory, platform, 'build-metadata.json'), 'utf8'));
    if (metadata.schema !== 1 || metadata.platform !== platform || metadata.version !== version || metadata.commitSha !== commitSha) {
      throw new Error(`${platform} artifact was not built from the selected version and commit.`);
    }
  }
  const result = await assemble({ inputs: platforms.map(platform => join(directory, platform)), output });
  const required = [`Cage-${version}-macos-arm64.dmg`, `Cage-${version}-windows-amd64-setup.exe`, `Cage-${version}-linux-amd64.deb`];
  if (result.version !== version || required.some(name => !result.assets.includes(name))) throw new Error('All three verified platform installers are required.');
  return result;
}

async function sha256(path) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest('hex');
}
export async function releaseFiles(directory) {
  const manifest = await readFile(join(directory, 'SHA256SUMS'), 'utf8');
  const files = new Map();
  for (const line of manifest.trim().split(/\r?\n/)) {
    const match = /^([a-f0-9]{64})  ([A-Za-z0-9][A-Za-z0-9._-]*)$/.exec(line);
    if (!match || files.has(match[2])) throw new Error('Invalid release checksum manifest.');
    files.set(match[2], match[1]);
  }
  files.set('SHA256SUMS', await sha256(join(directory, 'SHA256SUMS')));
  const actual = await readdir(directory);
  if (actual.length !== files.size || actual.some(name => !files.has(name))) throw new Error('Release output contains unlisted files.');
  const result = [];
  for (const [name, digest] of files) {
    const path = join(directory, name);
    const info = await stat(path);
    if (!info.isFile() || !info.size || await sha256(path) !== digest) throw new Error(`Invalid release file: ${name}`);
    result.push({ name, path, digest: `sha256:${digest}`, size: info.size });
  }
  return result;
}

function assertOwned(release, context) {
  if (!release.body?.includes(marker(context)) || release.tag_name !== `v${context.version}`) throw new Error('Existing Release belongs to another operation; refusing to change it.');
}
function correctAsset(asset, file) { return asset?.state === 'uploaded' && asset.size === file.size && asset.digest === file.digest; }
function assertAssets(assets, files) {
  if (assets.length !== files.length || files.some(file => !correctAsset(assets.find(asset => asset.name === file.name), file))) {
    throw new Error('GitHub uploaded assets do not match the complete verified release.');
  }
}

export async function publishRelease({ api, upload, promote, root, files, ...context }) {
  checkContext(context);
  await verifyCandidate(api, context, await expectedFiles(root, context.sourceSha, context.version));
  const base = apiRoot(context);
  const main = await api('GET', `${base}/git/ref/heads/${context.defaultBranch}`);
  if (![context.sourceSha, context.commitSha].includes(main.object.sha)) throw new Error('Main changed during the build; no Release was published.');
  const tag = await optional(api, `${base}/git/ref/tags/v${context.version}`);
  if (tag && tag.object.sha !== context.commitSha) throw new Error('Version tag already points to another commit.');
  let release = await findRelease(api, base, `v${context.version}`);
  if (release) {
    assertOwned(release, context);
    if (!release.draft) {
      if (!tag || main.object.sha !== context.commitSha) throw new Error('Published Release has unexpected Git references.');
      assertAssets(await api('GET', `${base}/releases/${release.id}/assets?per_page=100`), files);
      return release; // A failed website deployment may safely retry this job.
    }
  }
  await promote(context); // Atomic, fast-forward-only main + tag; never force-push.
  if (!release) {
    const notes = await api('POST', `${base}/releases/generate-notes`, { tag_name: `v${context.version}`, target_commitish: context.commitSha });
    release = await api('POST', `${base}/releases`, {
      tag_name: `v${context.version}`, target_commitish: context.commitSha,
      name: `Cage ${context.version}`, draft: true, prerelease: context.version.includes('-'),
      body: `${marker(context)}\n\n${notes.body || ''}\n\nThree platform builds, package verification and automated checks completed for ${context.commitSha}. Hardware virtualization and real Claude account acceptance are separate checks; see the platform guides.`,
    });
  }
  const assets = await api('GET', `${base}/releases/${release.id}/assets?per_page=100`);
  if (assets.some(asset => !files.some(file => file.name === asset.name))) throw new Error('Draft contains unexpected assets; review them before retrying.');
  for (const file of files) {
    const prior = assets.find(asset => asset.name === file.name);
    if (correctAsset(prior, file)) continue;
    // Only this run's still-private draft may replace a partial or rebuilt asset.
    const current = await api('GET', `${base}/releases/${release.id}`);
    assertOwned(current, context);
    if (!current.draft) throw new Error('Published assets cannot be replaced.');
    if (prior) await api('DELETE', `${base}/releases/assets/${prior.id}`);
    await upload(context.repository, release.id, file);
  }
  assertAssets(await api('GET', `${base}/releases/${release.id}/assets?per_page=100`), files);
  return api('PATCH', `${base}/releases/${release.id}`, { draft: false, make_latest: context.version.includes('-') ? 'false' : 'true' });
}

export function workflowContext(env, event) {
  const context = {
    repository: env.GITHUB_REPOSITORY, sourceSha: env.RELEASE_SOURCE_SHA || env.GITHUB_SHA,
    runId: env.GITHUB_RUN_ID, defaultBranch: event.repository?.default_branch, bump: event.inputs?.bump,
    version: env.RELEASE_VERSION, commitSha: env.RELEASE_COMMIT_SHA,
  };
  checkContext(context);
  if (env.GITHUB_ACTIONS !== 'true' || env.GITHUB_EVENT_NAME !== 'workflow_dispatch' || env.GITHUB_REF !== 'refs/heads/main') {
    throw new Error('Release mutations require an explicit workflow dispatch on main.');
  }
  return context;
}

async function main() {
  const context = workflowContext(process.env, JSON.parse(await readFile(process.env.GITHUB_EVENT_PATH, 'utf8')));
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN;
  if (!token) throw new Error('Missing release token.');
  const headers = { authorization: `Bearer ${token}`, accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' };
  const api = async (method, path, body) => {
    const response = await fetch(`https://api.github.com${path}`, {
      method, headers: { ...headers, 'content-type': 'application/json' }, redirect: 'error',
      body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(60000),
    });
    if (!response.ok) {
      const error = new Error(`GitHub ${method} ${path.split('?')[0]} failed (${response.status}).`);
      error.status = response.status; throw error;
    }
    return response.status === 204 ? null : response.json();
  };
  const output = async values => {
    if (!process.env.GITHUB_OUTPUT) throw new Error('Missing workflow output file.');
    await appendFile(process.env.GITHUB_OUTPUT, Object.entries(values).map(([key, value]) => `${key}=${value}\n`).join(''));
  };
  if (process.argv[2] === 'prepare') {
    const prepared = await prepareRelease({ api, root: repoRoot, ...context });
    await output({ version: prepared.version, commit_sha: prepared.commitSha, source_sha: prepared.sourceSha });
    console.log(`Prepared v${prepared.version} from ${prepared.sourceSha}; candidate ${prepared.commitSha}.`);
  } else if (process.argv[2] === 'publish') {
    const directory = resolve('_output/release-artifacts');
    const destination = resolve('_output/publish');
    await assembleRelease({ directory, output: destination, version: context.version, commitSha: context.commitSha });
    const files = await releaseFiles(destination);
    const release = await publishRelease({ api, root: repoRoot, files, ...context,
      promote: async candidate => {
        const result = spawnSync('git', ['-c', 'credential.helper=!gh auth git-credential', 'push', '--atomic',
          `https://github.com/${candidate.repository}.git`, `${candidate.commitSha}:refs/heads/main`, `${candidate.commitSha}:refs/tags/v${candidate.version}`],
        { cwd: repoRoot, env: { ...process.env, GH_TOKEN: token }, encoding: 'utf8' });
        if (result.status !== 0) throw new Error('Atomic main/tag promotion failed; no public Release was created. Check branch rules and newer commits.');
      },
      upload: async (repository, id, file) => {
        const response = await fetch(`https://uploads.github.com/repos/${repository}/releases/${id}/assets?name=${encodeURIComponent(file.name)}`, {
          method: 'POST', headers: { ...headers, 'content-type': 'application/octet-stream', 'content-length': String(file.size) },
          redirect: 'error', body: Readable.toWeb(createReadStream(file.path)), duplex: 'half', signal: AbortSignal.timeout(20 * 60 * 1000),
        });
        if (!response.ok) throw new Error(`Uploading ${file.name} failed (${response.status}); the draft remains private.`);
        if (!correctAsset(await response.json(), file)) throw new Error(`GitHub did not confirm the checksum of ${file.name}; the draft remains private.`);
      },
    });
    await output({ tag: `v${context.version}`, url: release.html_url });
    console.log(`Published ${release.html_url}`);
  } else throw new Error('Usage: release-control.mjs prepare|publish (explicit release workflow only).');
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
