import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { createPrivacyController } from './browser-privacy-fixture.mjs';

const fixture = await createPrivacyController();
const { api, cdp, evaluate, waitFor, origin, directory } = fixture;
const list = (id, extra = {}) => api('/cookies', { id, mode: 'list', scope: 'all', ...extra });
const checks = [];
try {
  const normal = (await api('/tabs')).active;
  const ready = id => waitFor(() => evaluate(id, 'document.readyState === "complete"'), 'page loaded');
  await ready(normal);
  await cdp(normal, 'Network.setCookie', { name: 'normal', value: 'normal-value', url: origin, expires: Date.now() / 1000 + 3600 });
  assert.equal((await list(normal)).cookies[0].name, 'normal');
  const restored = await readFile(join(directory, 'workbench-state.json'), 'utf8');
  const first = await api('/new', { url: origin + '/private', incognito: true });
  await ready(first.id);
  assert.equal((await api('/tabs')).tabs.find(tab => tab.id === first.id).incognito, true);
  assert.equal((await list(first.id)).total, 0);
  assert.equal(await evaluate(first.id, 'document.cookie'), '');
  await evaluate(first.id, 'localStorage.setItem("private-only", "temporary"); document.cookie = "private=private-value;path=/"');
  assert.equal(await readFile(join(directory, 'workbench-state.json'), 'utf8'), restored);
  const second = await api('/new', { url: origin + '/private-second', incognito: true });
  await ready(second.id);
  assert.equal(await evaluate(second.id, 'localStorage.getItem("private-only")'), 'temporary');
  assert.match(await evaluate(second.id, 'document.cookie'), /private=private-value/);
  checks.push('private cookies and localStorage isolated from normal tabs, shared by private tabs; restore URL unchanged');

  const privateCookie = (await list(first.id)).cookies.find(cookie => cookie.name === 'private');
  assert.equal('value' in privateCookie, false);
  assert.equal((await api('/cookies', { id: first.id, mode: 'details', scope: 'all', cookieKey: privateCookie.key })).value, 'private-value');
  await api('/cookies', { id: second.id, mode: 'clear', scope: 'all' });
  assert.equal((await list(first.id)).total, 0);
  assert.equal((await list(normal)).total, 1);
  checks.push('values are returned only on demand; clearing private cookies preserves normal login');

  await api('/close', { id: first.id });
  assert.equal(await evaluate(second.id, 'localStorage.getItem("private-only")'), 'temporary');
  const popup = await cdp(second.id, 'Target.createTarget', {
    url: origin + '/popup', browserContextId: (await cdp(second.id, 'Target.getTargetInfo')).targetInfo.browserContextId,
  });
  await waitFor(async () => (await api('/tabs')).tabs.find(tab => tab.id === popup.targetId)?.incognito, 'private popup classification');
  await api('/close', { id: second.id });
  await ready(popup.targetId);
  assert.equal(await evaluate(popup.targetId, 'localStorage.getItem("private-only")'), 'temporary');
  await api('/close', { id: popup.targetId });
  const third = await api('/new', { url: origin + '/private-fresh', incognito: true });
  await ready(third.id);
  assert.equal(await evaluate(third.id, 'localStorage.getItem("private-only")'), null);
  assert.equal((await list(third.id)).total, 0);
  const vault = await api('/vault', { action: 'status', tab: third.id, lease: 'f'.repeat(64) });
  assert.equal(vault.origin, ''); assert.deepEqual(vault.candidates, []);
  checks.push('private popups stay private; closing final private tab clears storage; private pages do not enter vault');

  // Same name and domain, different paths and partition keys must be distinct.
  await cdp(normal, 'Network.setCookies', { cookies: [
    { name: 'same', value: 'root', domain: 'localhost', path: '/' },
    { name: 'same', value: 'nested', domain: 'localhost', path: '/nested' },
    { name: 'unrelated', value: 'preserve', domain: 'other.test', path: '/' },
    { name: 'partition', value: 'plain', domain: 'localhost', path: '/', secure: true },
    { name: 'partition', value: 'partitioned', domain: 'localhost', path: '/', secure: true, partitionKey: { topLevelSite: 'https://example.test', hasCrossSiteAncestor: true } },
  ] });
  let cookies = await list(normal, { scope: 'site' });
  assert.ok(cookies.cookies.every(cookie => cookie.domain === 'localhost'));
  const nested = cookies.cookies.find(cookie => cookie.name === 'same' && cookie.path === '/nested');
  await api('/cookies', { id: normal, mode: 'delete', scope: 'site', cookieKey: nested.key });
  cookies = await list(normal);
  assert.equal(cookies.cookies.filter(cookie => cookie.name === 'same').length, 1);
  const partitioned = cookies.cookies.find(cookie => cookie.partitionKey);
  assert.ok(partitioned, 'real partitioned fixture stored');
  await api('/cookies', { id: normal, mode: 'delete', scope: 'all', cookieKey: partitioned.key });
  assert.equal((await list(normal)).cookies.filter(cookie => cookie.name === 'partition').length, 1);
  checks.push('deletion respects path and partition key; site filtering excludes unrelated hosts');

  await cdp(normal, 'Network.setCookies', { cookies: Array.from({ length: 57 }, (_, i) => ({ name: 'paged-' + i, value: String(i), domain: 'localhost', path: '/' })) });
  cookies = await list(normal, { query: 'paged-', page: 1 });
  assert.equal(cookies.total, 57); assert.equal(cookies.cookies.length, 7);
  await api('/cookies', { id: normal, mode: 'clear', scope: 'site', query: 'paged-' });
  cookies = await list(normal);
  assert.equal(cookies.total, 1); assert.equal(cookies.cookies[0].name, 'unrelated');
  checks.push('search and pagination; clearing a site ignores search and preserves other hosts');

  await cdp(normal, 'Network.setCookie', { name: 'persistent', value: 'retained', url: origin, expires: Date.now() / 1000 + 3600 });
  await assert.rejects(api('/cookies', { id: normal, url: origin + '/stale-page', mode: 'clear', scope: 'all' }), /网页已切换/);
  assert.ok((await list(normal)).cookies.some(cookie => cookie.name === 'persistent'));
  await api('/activate', { id: normal });
  await api('/activate', { id: third.id });
  await fixture.restart();
  const afterRestart = await api('/tabs');
  assert.ok(afterRestart.tabs.every(tab => !tab.incognito && !tab.url.includes('/private')));
  assert.ok((await list(afterRestart.active)).cookies.some(cookie => cookie.name === 'persistent'));
  checks.push('controller restart restores normal state and persistent cookies, never private pages');
  const lastPrivate = await api('/new', { url: origin + '/private-last', incognito: true });
  for (const tab of (await api('/tabs')).tabs.filter(tab => !tab.incognito)) await api('/close', { id: tab.id });
  await api('/close', { id: lastPrivate.id });
  const last = await api('/tabs');
  assert.equal(last.tabs.length, 0); assert.equal(last.active, '');
  checks.push('closing the only remaining private page leaves the browser empty');
  console.log(JSON.stringify({ passed: true, checks }, null, 2));
} finally { await fixture.close(); }
