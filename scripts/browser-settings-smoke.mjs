import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { createPrivacyController } from './browser-privacy-fixture.mjs';
const fixture = await createPrivacyController();
const { api, cdp, evaluate, waitFor, origin, directory } = fixture;
const checks = [];
try {
  const one = await api('/settings', { mode: 'general' });
  assert.equal(one.kind, 'browser-settings'); assert.equal(one.url, 'cage://settings#general');
  const two = await api('/settings', { mode: 'extensions' });
  assert.equal(two.id, one.id);
  assert.equal((await api('/tabs')).tabs.find(tab => tab.id === one.id).kind, 'browser-settings');
  assert.equal((await api('/cookies', { id: one.id, url: two.url, mode: 'list', scope: 'all' })).incognito, false);
  checks.push('menu settings action reuses one real browser target; public internal URL and ordinary-session cookie management work');
  const bytes = execFileSync('uv', ['run', '--no-project', 'python', '-c', `import io,zipfile,json,sys
b=io.BytesIO()
with zipfile.ZipFile(b,'w') as z:
 z.writestr('manifest.json',json.dumps({'manifest_version':3,'name':'Cage test extension','version':'1.0','permissions':['storage'],'content_scripts':[{'matches':['http://localhost/*'],'js':['content.js']}]}))
 z.writestr('content.js','document.documentElement.dataset.cageExtension="active"; chrome.storage.local.set({persisted:"retained"});')
sys.stdout.buffer.write(b.getvalue())`], { maxBuffer: 1 << 20, stdio: ['ignore', 'pipe', 'ignore'] });
  const selected = await fixture.uploadExtension(bytes);
  assert.equal(selected.name, 'Cage test extension'); assert.deepEqual(selected.permissions, ['storage']);
  assert.ok(selected.hostPermissions.includes('http://localhost/*'));
  assert.equal((await api('/extensions', { mode: 'list' })).items.length, 0);
  const source = 'a'.repeat(32) + ':' + 'b'.repeat(32);
  const installed = await api('/extensions', { mode: 'install', session: selected.session, target: source });
  const extension = installed.items.find(item => item.name === selected.name);
  assert.ok(extension?.enabled); assert.ok(extension.managed);
  assert.equal(extension.source, source);
  const tab = await api('/new', { url: origin + '/extension-test' });
  await waitFor(() => evaluate(tab.id, 'document.documentElement.dataset.cageExtension === "active"'), 'real extension content script');
  checks.push('ZIP review does not install until confirmed; declared and content-script permissions shown; installed extension executes in a real page');
  await fixture.restart();
  let list = await api('/extensions', { mode: 'list' });
  assert.equal(list.items.find(item => item.id === extension.id).enabled, true);
  assert.equal(list.items.find(item => item.id === extension.id).source, source);
  let page = await api('/new', { url: origin + '/after-restart' });
  await waitFor(() => evaluate(page.id, 'document.documentElement.dataset.cageExtension === "active"'), 'extension restored after restart');
  await api('/extensions', { mode: 'disable', id: extension.id });
  page = await api('/new', { url: origin + '/disabled' });
  await waitFor(() => evaluate(page.id, 'document.readyState === "complete"'), 'disabled fixture ready');
  assert.equal(await evaluate(page.id, 'document.documentElement.dataset.cageExtension || ""'), '');
  await fixture.restart();
  list = await api('/extensions', { mode: 'list' });
  assert.equal(list.items.find(item => item.id === extension.id).enabled, false);
  await api('/extensions', { mode: 'enable', id: extension.id });
  page = await api('/new', { url: origin + '/enabled-again' });
  await waitFor(() => evaluate(page.id, 'document.documentElement.dataset.cageExtension === "active"'), 'enabled fixture');
  const storage = await cdp(page.id, 'Extensions.getStorageItems', { id: extension.id, storageArea: 'local' });
  assert.equal(storage.data.persisted, 'retained');
  checks.push('enabled extensions restore after restart; disable survives restart; re-enable preserves extension storage');
  await api('/extensions', { mode: 'remove', id: extension.id });
  assert.equal((await api('/extensions', { mode: 'list' })).items.length, 0);
  await fixture.restart();
  assert.equal((await api('/extensions', { mode: 'list' })).items.length, 0);
  const registry = JSON.parse(await readFile(join(directory, 'extensions/registry.json'), 'utf8'));
  assert.deepEqual(registry, []);
  checks.push('uninstall removes the extension and persistent installation record');
  const helper = (await cdp(null, 'Extensions.getExtensions')).extensions.find(item => item.name === 'Cage browser panes');
  const control = (await cdp(null, 'Target.getTargets')).targetInfos.find(item => item.url.endsWith('/control.html'));
  const { sessionId } = await cdp(null, 'Target.attachToTarget', { targetId: control.targetId, flatten: true });
  await api('/__privacy-test', { sessionId, method: 'Runtime.evaluate', params: { expression: `chrome.management.setEnabled(${JSON.stringify(helper.id)}, false)`, userGesture: true } }).catch(() => {});
  await waitFor(async () => { try { return Array.isArray((await api('/extensions', { mode: 'list' })).items); } catch { return false; } }, 'built-in browser controller recovers after native extension disable');
  checks.push('built-in browser component is protected by controller recovery');
  await assert.rejects(api('/internal-page', { mode: 'arbitrary' }), /内部页面无效/);
  await assert.rejects(fixture.uploadExtension(Buffer.from('invalid zip')));
  console.log(JSON.stringify({ passed: true, checks }, null, 2));
} finally { await fixture.close(); }
