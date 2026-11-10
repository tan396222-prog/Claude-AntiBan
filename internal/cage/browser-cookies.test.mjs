import assert from 'node:assert/strict';
import test from 'node:test';
import { browserCookies } from './browser-cookies.mjs';
import { BrowserVault } from './browser-vault.mjs';

const tab = { targetId: 'tab', url: 'https://sub.example.test/page', browserContextId: 'private-context', incognito: true };
const cookie = (domain, extra = {}) => ({ name: 'session', value: 'fixture-secret', domain, path: '/', expires: -1, session: true, ...extra });

test('site scope includes parent domain cookies and excludes host-only parent and lookalikes', async () => {
  const calls = [];
  const cdp = async (method, params) => {
    calls.push({ method, params });
    return { cookies: [cookie('.example.test'), cookie('example.test'), cookie('sub.example.test'), cookie('.notexample.test'), cookie('other.example.test')] };
  };
  const result = await browserCookies(cdp, async () => 'sid', tab, { mode: 'list', scope: 'site' });
  assert.deepEqual(result.cookies.map(cookie => cookie.domain), ['.example.test', 'sub.example.test']);
  assert.equal(result.incognito, true);
  assert.ok(result.cookies.every(cookie => !('value' in cookie)));
  assert.deepEqual(calls, [{ method: 'Storage.getCookies', params: { browserContextId: 'private-context' } }]);
});

test('opaque partitions refuse site-wide and individual deletion before any mutation', async () => {
  const calls = [];
  const cdp = async method => { calls.push(method); return { cookies: [cookie('sub.example.test'), cookie('sub.example.test', { partitionKeyOpaque: true })] }; };
  const data = await browserCookies(cdp, async () => 'sid', tab, { mode: 'list', scope: 'site' });
  await assert.rejects(browserCookies(cdp, async () => 'sid', tab, { mode: 'delete', scope: 'site', cookieKey: data.cookies.find(cookie => cookie.partitionKeyOpaque).key }), /分区/);
  await assert.rejects(browserCookies(cdp, async () => 'sid', tab, { mode: 'clear', scope: 'site' }), /分区/);
  assert.ok(calls.every(method => method === 'Storage.getCookies'));
});

test('stale page or authorization tabs cannot reach cookie storage', async () => {
  const cdp = () => { throw new Error('must not contact cookie storage'); };
  await assert.rejects(browserCookies(cdp, null, tab, { mode: 'clear', scope: 'all', url: 'https://previous.test/' }), /网页已切换/);
  await assert.rejects(browserCookies(cdp, null, { ...tab, login: true }, { mode: 'list', scope: 'all' }), /不可管理/);
});

test('vault never attaches credential observers to private or authorization tabs', async () => {
  for (const mode of ['incognito', 'login']) {
    const vault = new BrowserVault({ cdp: () => { throw new Error('must not install credential observer'); }, active: () => 'tab', tab: () => ({ [mode]: true }) });
    await vault.attach('tab', 'sid');
    assert.equal(vault.sessions.size, 0);
    await vault.request({ action: 'status', lease: 'f'.repeat(64), tab: 'tab' });
    await assert.rejects(vault.request({ action: 'fill', lease: 'f'.repeat(64), tab: 'tab' }), /网页已切换/);
  }
});
