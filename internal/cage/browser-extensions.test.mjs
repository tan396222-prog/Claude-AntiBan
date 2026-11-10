import assert from 'node:assert/strict';
import test from 'node:test';
import { execFileSync } from 'node:child_process';
import { mkdtemp, access, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { Readable } from 'node:stream';
import { BrowserExtensions } from './browser-extensions.mjs';

for (const kind of ['traversal', 'symlink', 'legacy']) test(`extension ZIP refuses ${kind} before installation`, async () => {
  const root = await mkdtemp(join(tmpdir(), 'cage-extension-archive-'));
  try {
    const bytes = execFileSync('uv', ['run', '--no-project', 'python', '-c', `import io,json,stat,sys,zipfile
b=io.BytesIO()
with zipfile.ZipFile(b,'w') as z:
 z.writestr('manifest.json',json.dumps({'manifest_version':2 if sys.argv[1]=='legacy' else 3,'name':'Fixture','version':'1.0'}))
 if sys.argv[1]=='traversal':z.writestr('../outside','blocked')
 if sys.argv[1]=='symlink':
  link=zipfile.ZipInfo('link');link.create_system=3;link.external_attr=(stat.S_IFLNK|0o777)<<16;z.writestr(link,'/etc/passwd')
sys.stdout.buffer.write(b.getvalue())`, kind], { stdio: ['ignore', 'pipe', 'ignore'] });
    const manager = new BrowserExtensions({ directory: root, cdp: () => { throw new Error('must not install rejected ZIP'); }, control: null, protectedID: 'a'.repeat(32) });
    await assert.rejects(manager.upload(Readable.from([bytes])), /路径|符号链接|Manifest V3/);
    assert.equal(manager.pending.size, 0);
    await assert.rejects(access(join(root, 'extensions/staging/outside')));
  } finally { await rm(root, { recursive: true, force: true }); }
});
