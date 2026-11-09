import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import test from 'node:test';
import {
  assembleRelease, expectedFiles, prepareRelease, publishRelease, releaseFiles,
  verifyCandidate, versionPaths, workflowContext,
} from './release-control.mjs';

const repository = 'Lee-xeo/Claude-Cage';
const base = `/repos/${repository}`;
const currentVersion = (await readFile(new URL('../VERSION', import.meta.url), 'utf8')).trim();
const windowsDependencies = JSON.parse(await readFile(new URL('../packaging/windows/dependencies.json', import.meta.url), 'utf8'));
const linuxDependencies = JSON.parse(await readFile(new URL('../packaging/linux/dependencies.json', import.meta.url), 'utf8'));
const macCoreVersion = (await readFile(new URL('./fetch-proxy-core.sh', import.meta.url), 'utf8')).match(/^version=(v[\d.]+)$/m)[1];
const digest = value => createHash('sha256').update(value).digest('hex');
const copy = value => structuredClone(value);

async function temporary(t) {
  const root = await mkdtemp(join(tmpdir(), 'cage-release-control-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

// Git trees and commits are real. Only the GitHub transport and remote refs
// are simulated, so changes to version files can be checked against Git data.
async function repositoryFixture(t) {
  const root = await temporary(t);
  const environment = {
    ...process.env, GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: join(root, 'no-global-config'),
    GIT_AUTHOR_NAME: 'Release Test', GIT_AUTHOR_EMAIL: 'test@example.invalid',
    GIT_COMMITTER_NAME: 'Release Test', GIT_COMMITTER_EMAIL: 'test@example.invalid',
  };
  const git = (args, options = {}) => execFileSync('git', args, {
    cwd: root, encoding: 'utf8', env: environment, stdio: ['pipe', 'pipe', 'pipe'], ...options,
  }).trimEnd();
  git(['init', '--quiet', '--initial-branch=main']);
  await writeFile(join(root, 'VERSION'), currentVersion + '\n');
  for (const directory of ['ui', 'www']) {
    await mkdir(join(root, directory));
    const manifest = { name: directory, version: currentVersion, dependencies: { fixture: '1.0.0' } };
    const lock = { ...manifest, lockfileVersion: 3, packages: { '': manifest, 'node_modules/fixture': { version: '1.0.0', integrity: 'keep-this-integrity' } } };
    await writeFile(join(root, directory, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');
    await writeFile(join(root, directory, 'package-lock.json'), JSON.stringify(lock, null, 2) + '\n');
  }
  git(['add', '--', ...versionPaths]);
  git(['commit', '--quiet', '-m', 'Fixture source']);
  const sourceSha = git(['rev-parse', 'HEAD']);
  const context = { repository, sourceSha, runId: '1042', defaultBranch: 'main', bump: 'prerelease' };
  const state = { main: sourceSha, refs: new Map(), release: null, otherReleases: [], nextReleaseId: 42, assets: [], calls: [], uploads: [], promotions: [] };
  async function tree(baseTree, entries) {
    const index = join(root, '.git', `test-index-${randomUUID()}`);
    const env = { ...environment, GIT_INDEX_FILE: index };
    try {
      git(['read-tree', baseTree], { env });
      for (const entry of entries) {
        const blob = git(['hash-object', '-w', '--stdin'], { input: entry.content, env });
        git(['update-index', '--add', '--cacheinfo', entry.mode || '100644', blob, entry.path], { env });
      }
      return git(['write-tree'], { env });
    } finally { await rm(index, { force: true }); }
  }
  const notFound = () => { throw Object.assign(new Error('Not found'), { status: 404 }); };
  const api = async (method, fullPath, body) => {
    state.calls.push({ method, path: fullPath, body: copy(body) });
    assert.ok(fullPath.startsWith(base + '/'), 'API must stay in the configured repository');
    const path = fullPath.slice(base.length);
    if (method === 'GET' && path.startsWith('/git/ref/')) {
      const ref = path.slice('/git/ref/'.length);
      const sha = ref === 'heads/main' ? state.main : state.refs.get(ref);
      return sha ? { object: { sha } } : notFound();
    }
    if (method === 'GET' && path.startsWith('/git/commits/')) {
      const sha = path.slice('/git/commits/'.length);
      return { sha, message: git(['show', '-s', '--format=%B', sha]), tree: { sha: git(['rev-parse', `${sha}^{tree}`]) },
        parents: git(['show', '-s', '--format=%P', sha]).split(' ').filter(Boolean).map(parent => ({ sha: parent })) };
    }
    if (method === 'GET' && path.startsWith('/compare/')) {
      const [from, to] = path.slice('/compare/'.length).split('...');
      const files = git(['diff', '--name-status', from, to]).split('\n').filter(Boolean).map(line => {
        const [status, filename] = line.split('\t');
        return { status: status === 'M' ? 'modified' : 'added', filename };
      });
      return { status: 'ahead', ahead_by: Number(git(['rev-list', '--count', `${from}..${to}`])), files };
    }
    if (method === 'GET' && path.startsWith('/contents/')) {
      const [filename, sha] = path.slice('/contents/'.length).split('?ref=');
      const content = execFileSync('git', ['show', `${sha}:${filename}`], { cwd: root, env: environment });
      return { type: 'file', encoding: 'base64', content: content.toString('base64') };
    }
    if (method === 'GET' && path === '/git/matching-refs/tags/v') return [];
    if (method === 'GET' && path.startsWith('/releases?')) {
      const query = new URL(path, 'https://api.example.invalid').searchParams;
      const page = Number(query.get('page') || 1);
      const size = Number(query.get('per_page') || 30);
      const releases = [...state.otherReleases, ...(state.release ? [state.release] : [])];
      return copy(releases.slice((page - 1) * size, page * size));
    }
    if (method === 'POST' && path === '/git/trees') return { sha: await tree(body.base_tree, body.tree) };
    if (method === 'POST' && path === '/git/commits') {
      return { sha: git(['commit-tree', body.tree, '-p', body.parents[0]], { input: body.message }) };
    }
    if (method === 'POST' && path === '/git/refs') {
      state.refs.set(body.ref.replace('refs/', ''), body.sha);
      return { object: { sha: body.sha } };
    }
    // GitHub's tag lookup exposes published Releases only, even when this
    // token can list private drafts. Resume logic must use the list endpoint.
    if (method === 'GET' && path.startsWith('/releases/tags/')) {
      const tag = decodeURIComponent(path.slice('/releases/tags/'.length));
      const found = [...state.otherReleases, ...(state.release ? [state.release] : [])]
        .find(release => !release.draft && release.tag_name === tag);
      return found ? copy(found) : notFound();
    }
    if (method === 'POST' && path === '/releases/generate-notes') return { body: 'Fixture release notes' };
    if (method === 'POST' && path === '/releases') {
      if (state.release) state.otherReleases.push(copy(state.release));
      const id = state.nextReleaseId++;
      state.release = { ...body, id, html_url: `https://example.invalid/release/${id}` };
      state.assets = [];
      return copy(state.release);
    }
    if (method === 'GET' && path === `/releases/${state.release?.id}/assets?per_page=100`) return copy(state.assets);
    if (method === 'GET' && path === `/releases/${state.release?.id}`) return copy(state.release);
    if (method === 'DELETE' && path.startsWith('/releases/assets/')) {
      state.assets = state.assets.filter(asset => asset.id !== Number(path.split('/').at(-1)));
      return null;
    }
    if (method === 'PATCH' && path === `/releases/${state.release?.id}`) {
      Object.assign(state.release, body);
      return copy(state.release);
    }
    throw new Error(`Unexpected fake API request: ${method} ${path}`);
  };
  const promote = async candidate => {
    state.promotions.push(copy(candidate));
    state.main = candidate.commitSha;
    state.refs.set(`tags/v${candidate.version}`, candidate.commitSha);
  };
  const upload = async (_repository, _id, file) => {
    state.uploads.push(file.name);
    state.assets.push({ id: state.assets.length + 1, name: file.name, state: 'uploaded', size: file.size, digest: file.digest });
  };
  const candidate = () => prepareRelease({ api, root, ...context });
  return { root, git, tree, api, context, state, promote, upload, candidate };
}

async function uploadFiles(root, version) {
  const directory = join(root, 'publish');
  await mkdir(directory);
  const names = [`Cage-${version}-macos-arm64.dmg`, `Cage-${version}-windows-amd64-setup.exe`, `Cage-${version}-linux-amd64.deb`];
  for (const name of names) await writeFile(join(directory, name), `fixture ${name}`);
  await writeFile(join(directory, 'SHA256SUMS'), names.map(name => `${digest(`fixture ${name}`)}  ${name}\n`).join(''));
  return releaseFiles(directory);
}

function publish(fixture, candidate, files, overrides = {}) {
  const { api, root, promote, upload } = fixture;
  return publishRelease({ api, root, promote, upload, ...candidate, files, ...overrides });
}

function published(fixture, candidate, files, runId = candidate.runId) {
  fixture.state.main = candidate.commitSha;
  fixture.state.refs.set(`tags/v${candidate.version}`, candidate.commitSha);
  fixture.state.release = { id: 42, tag_name: `v${candidate.version}`, draft: false,
    body: `<!-- cage-release-run:${runId}; commit:${candidate.commitSha} -->` };
  fixture.state.assets = files.map((file, index) => ({ id: index + 1, name: file.name, state: 'uploaded', size: file.size, digest: file.digest }));
}

test('release context rejects ordinary pushes, forks and non-main dispatches', () => {
  const env = { GITHUB_ACTIONS: 'true', GITHUB_EVENT_NAME: 'workflow_dispatch', GITHUB_REF: 'refs/heads/main',
    GITHUB_REPOSITORY: repository, GITHUB_SHA: 'a'.repeat(40), GITHUB_RUN_ID: '1042' };
  const event = { repository: { default_branch: 'main' }, inputs: { bump: 'prerelease' } };
  assert.equal(workflowContext(env, event).sourceSha, env.GITHUB_SHA);
  for (const change of [{ GITHUB_EVENT_NAME: 'push' }, { GITHUB_REPOSITORY: 'someone/fork' }, { GITHUB_REF: 'refs/heads/feature' }, { GITHUB_ACTIONS: 'false' }, { GITHUB_SHA: 'main' }]) {
    assert.throws(() => workflowContext({ ...env, ...change }, event));
  }
});

test('prepare leaves the working version unchanged and reuses the same run candidate', async t => {
  const f = await repositoryFixture(t);
  const before = await readFile(join(f.root, 'VERSION'), 'utf8');
  const first = await f.candidate();
  assert.notEqual(first.version, currentVersion);
  assert.equal(await readFile(join(f.root, 'VERSION'), 'utf8'), before);
  assert.equal(f.git(['status', '--porcelain']), '');
  assert.equal(f.state.main, f.context.sourceSha);
  const second = await f.candidate();
  assert.equal(second.commitSha, first.commitSha);
  assert.equal(second.version, first.version);
  assert.equal(f.state.calls.filter(call => call.method === 'POST' && call.path.endsWith('/git/commits')).length, 1);
});

test('main movement prevents candidate creation and later publication', async t => {
  const f = await repositoryFixture(t);
  f.state.main = 'b'.repeat(40);
  await assert.rejects(f.candidate(), /Main advanced/);
  assert.equal(f.state.calls.some(call => call.method !== 'GET'), false);
  f.state.main = f.context.sourceSha;
  const candidate = await f.candidate();
  const files = await uploadFiles(f.root, candidate.version);
  f.state.main = 'b'.repeat(40);
  await assert.rejects(publish(f, candidate, files), /Main changed/);
  assert.equal(f.state.promotions.length, 0);
  assert.equal(f.state.uploads.length, 0);
  assert.equal(f.state.release, null);
});

test('candidate verification rejects extra files and dependency edits hidden in version files', async t => {
  for (const kind of ['extra file', 'dependency change']) await t.test(kind, async t => {
    const f = await repositoryFixture(t);
    const candidate = await f.candidate();
    const expected = await expectedFiles(f.root, candidate.sourceSha, candidate.version);
    const changed = new Map(expected);
    if (kind === 'extra file') changed.set('unexpected-script.sh', 'echo unauthorized\n');
    else changed.set('ui/package.json', changed.get('ui/package.json').replace('"fixture": "1.0.0"', '"fixture": "9.9.9"'));
    const treeSha = await f.tree(f.git(['rev-parse', `${candidate.sourceSha}^{tree}`]), [...changed].map(([path, content]) => ({ path, content })));
    const message = f.git(['show', '-s', '--format=%B', candidate.commitSha]);
    const commitSha = f.git(['commit-tree', treeSha, '-p', candidate.sourceSha], { input: message });
    await assert.rejects(verifyCandidate(f.api, { ...candidate, commitSha }, expected), /only the five|unexpected change/);
  });
});

async function artifactFixture(t) {
  const root = await temporary(t);
  const commitSha = 'a'.repeat(40);
  const packages = {
    macos: { [`Cage-${currentVersion}-macos-arm64.dmg`]: 'mac installer', [`mihomo-${macCoreVersion}-source.tar.gz`]: 'same core source' },
    windows: { [`Cage-${currentVersion}-windows-amd64-setup.exe`]: 'windows installer', [`mihomo-${windowsDependencies.mihomo.version}-source.tar.gz`]: 'same core source', [`qemu-${windowsDependencies.qemu.version}-windows-full-source.zip`]: 'complete qemu source' },
    linux: { [`Cage-${currentVersion}-linux-amd64.deb`]: 'linux installer', [linuxDependencies.mihomoSource.file]: 'same core source' },
  };
  for (const [platform, entries] of Object.entries(packages)) {
    const directory = join(root, platform);
    await mkdir(directory);
    for (const [name, content] of Object.entries(entries)) await writeFile(join(directory, name), content);
    await writeFile(join(directory, 'SHA256SUMS'), Object.entries(entries).map(([name, content]) => `${digest(content)}  ${name}\n`).join(''));
    await writeFile(join(directory, 'build-metadata.json'), JSON.stringify({ schema: 1, platform, version: currentVersion, commitSha }));
  }
  return { directory: root, output: join(root, 'assembled'), version: currentVersion, commitSha };
}

test('all three platform identities are required before any assets are assembled', async t => {
  for (const kind of ['missing metadata', 'different commit', 'different version']) await t.test(kind, async t => {
    const f = await artifactFixture(t);
    const path = join(f.directory, 'linux', 'build-metadata.json');
    if (kind === 'missing metadata') await rm(path);
    else {
      const metadata = JSON.parse(await readFile(path, 'utf8'));
      if (kind === 'different commit') metadata.commitSha = 'b'.repeat(40);
      else metadata.version = '99.0.0';
      await writeFile(path, JSON.stringify(metadata));
    }
    let assembled = false;
    await assert.rejects(assembleRelease({ ...f, assemble: async () => { assembled = true; throw new Error('must not assemble'); } }));
    assert.equal(assembled, false);
  });
});

test('assembly accepts matching platforms but excludes metadata from public assets', async t => {
  const f = await artifactFixture(t);
  const result = await assembleRelease(f);
  assert.ok(result.assets.includes(`Cage-${currentVersion}-linux-amd64.deb`));
  assert.ok(!result.assets.some(name => name.includes('metadata')));
  assert.ok((await releaseFiles(f.output)).length > 3);
});

test('an installer for an unsupported version cannot enter the public release', async t => {
  const f = await artifactFixture(t);
  const wrong = 'Cage-99999.0.0-linux-amd64.deb';
  const path = join(f.directory, 'linux');
  await writeFile(join(path, wrong), 'wrong version');
  const manifest = await readFile(join(path, 'SHA256SUMS'), 'utf8');
  await writeFile(join(path, 'SHA256SUMS'), manifest + `${digest('wrong version')}  ${wrong}\n`);
  await assert.rejects(assembleRelease(f), /Unsupported asset or version/);
});

test('a public release owned by another run is never overwritten', async t => {
  const f = await repositoryFixture(t);
  const candidate = await f.candidate();
  const files = await uploadFiles(f.root, candidate.version);
  published(f, candidate, files, '9999');
  const before = copy(f.state.release);
  await assert.rejects(publish(f, candidate, files), /another operation/);
  assert.deepEqual(f.state.release, before);
  assert.equal(f.state.promotions.length, 0);
  assert.equal(f.state.uploads.length, 0);
});

test('an upload failure retains a private draft and never publishes partial assets', async t => {
  const f = await repositoryFixture(t);
  const candidate = await f.candidate();
  const files = await uploadFiles(f.root, candidate.version);
  let attempts = 0;
  await assert.rejects(publish(f, candidate, files, { upload: async (...args) => {
    if (++attempts === 2) throw new Error('fixture upload failure');
    await f.upload(...args);
  } }), /fixture upload failure/);
  assert.equal(f.state.release.draft, true);
  assert.equal(f.state.assets.length, 1);
  assert.equal(f.state.calls.some(call => call.method === 'PATCH'), false);
});

test('retry after an interrupted upload resumes the same private draft, including on the second page', async t => {
  for (const precedingReleases of [0, 100]) await t.test(precedingReleases ? 'draft on page two' : 'draft on page one', async t => {
    const f = await repositoryFixture(t);
    const candidate = await f.candidate();
    const files = await uploadFiles(f.root, candidate.version);
    let attempts = 0;
    await assert.rejects(publish(f, candidate, files, { upload: async (...args) => {
      if (++attempts === 2) throw new Error('fixture interrupted upload');
      await f.upload(...args);
    } }), /fixture interrupted upload/);
    const draftId = f.state.release.id;
    assert.equal(f.state.release.draft, true);
    assert.equal(f.state.assets.length, 1);
    const completedAsset = f.state.assets[0].name;
    await assert.rejects(f.api('GET', `${base}/releases/tags/v${candidate.version}`), { status: 404 });
    f.state.otherReleases = Array.from({ length: precedingReleases }, (_, i) => ({
      id: 1000 + i, tag_name: `v0.0.0-fixture.${i}`, draft: false, body: 'Unrelated older release',
    }));
    const resumed = await publish(f, candidate, files);
    assert.equal(resumed.id, draftId);
    assert.equal(resumed.draft, false);
    assert.equal(f.state.calls.filter(call => call.method === 'POST' && call.path === `${base}/releases`).length, 1);
    assert.equal(f.state.uploads.filter(name => name === completedAsset).length, 1);
    assert.deepEqual(new Set(f.state.assets.map(asset => asset.name)), new Set(files.map(file => file.name)));
  });
});

test('multiple drafts reserving the same tag are left untouched', async t => {
  const f = await repositoryFixture(t);
  const candidate = await f.candidate();
  const files = await uploadFiles(f.root, candidate.version);
  published(f, candidate, files);
  f.state.release.draft = true;
  f.state.otherReleases.push({ ...copy(f.state.release), id: 43 });
  const before = copy([f.state.release, ...f.state.otherReleases]);
  f.state.calls.length = 0;
  await assert.rejects(publish(f, candidate, files), /Multiple Releases reserve/);
  assert.deepEqual([f.state.release, ...f.state.otherReleases], before);
  assert.equal(f.state.calls.some(call => call.method !== 'GET'), false);
  assert.equal(f.state.promotions.length, 0);
  assert.equal(f.state.uploads.length, 0);
});

test('missing or incorrect server digests prevent publication even after successful upload requests', async t => {
  for (const receivedDigest of [undefined, `sha256:${'0'.repeat(64)}`]) await t.test(receivedDigest ? 'wrong digest' : 'missing digest', async t => {
    const f = await repositoryFixture(t);
    const candidate = await f.candidate();
    const files = await uploadFiles(f.root, candidate.version);
    await assert.rejects(publish(f, candidate, files, { upload: async (...args) => {
      await f.upload(...args);
      f.state.assets.at(-1).digest = receivedDigest;
    } }), /uploaded assets do not match/);
    assert.equal(f.state.release.draft, true);
    assert.equal(f.state.calls.some(call => call.method === 'PATCH'), false);
  });
});

test('retrying this run after publication checks identical assets without uploading or changing refs', async t => {
  const f = await repositoryFixture(t);
  const candidate = await f.candidate();
  const files = await uploadFiles(f.root, candidate.version);
  const first = await publish(f, candidate, files);
  assert.equal(first.draft, false);
  const uploaded = [...f.state.uploads];
  const promotions = f.state.promotions.length;
  const mutations = f.state.calls.filter(call => call.method !== 'GET').length;
  const again = await publish(f, candidate, files);
  assert.equal(again.id, first.id);
  assert.deepEqual(f.state.uploads, uploaded);
  assert.equal(f.state.promotions.length, promotions);
  assert.equal(f.state.calls.filter(call => call.method !== 'GET').length, mutations);
});
