// Used only by disposable package-install test users. Never point seed at a
// real profile: exclusive creation refuses to overwrite existing user files.
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const entries = [
  '.claude-cage/config.json',
  '.claude-cage/environment.json',
  '.claude-cage/account.json',
  '.claude-cage/password-vault.json',
  '.claude-cage/favorites.json',
  '.claude-cage/projects.json',
  '.claude-cage/workbench.json',
  '.claude-cage/appearance.json',
  '.claude-cage/webview2/Default/Local Storage/leveldb/000001.log',
  '.claude-cage/webkit/data/WebsiteData/LocalStorage/storage.sqlite3',
  '.claude-cage/state/lima/persistent/disk',
  '.claude-cage-workspace/project.txt',
];
const digest = (data) => createHash('sha256').update(data).digest('hex');

export async function seed(home, manifest) {
  const records = {};
  for (const name of entries) {
    const path = join(home, name);
    await mkdir(dirname(path), { recursive: true, mode: 0o700 });
    const bytes = Buffer.from(`Cage package test: preserve ${name}\n历史、收藏与登录数据\n\0\xff`, 'utf8');
    await writeFile(path, bytes, { flag: 'wx', mode: 0o600 });
    records[name] = digest(bytes);
  }
  await writeFile(manifest, JSON.stringify(records), { flag: 'wx', mode: 0o600 });
}

export async function verify(home, manifest) {
  const records = JSON.parse(await readFile(manifest, 'utf8'));
  for (const [name, expected] of Object.entries(records)) {
    if (digest(await readFile(join(home, name))) !== expected) {
      throw new Error(`Installation changed personal data: ${name}`);
    }
  }
  return Object.keys(records).length;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [operation, home, manifest] = process.argv.slice(2);
  if (!home || !manifest || !['seed', 'verify'].includes(operation)) {
    throw new Error('Usage: personal-data-fixture.mjs seed|verify TEST_HOME MANIFEST');
  }
  if (operation === 'seed') await seed(home, manifest);
  else console.log(`PASS: ${await verify(home, manifest)} personal data files preserved byte for byte.`);
}
