// Real CLI, controller HTTP and compiled UI; only the host OS bridge is a stub.
// No VM is created. The initialization record is an isolated existing-user
// fixture so the stopped workbench, including its sidebar, can be inspected.
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { isAbsolute, join } from 'node:path';
import { request as httpRequest } from 'node:http';
import { setTimeout } from 'node:timers/promises';

const binary = process.argv[2];
assert.ok(binary && isAbsolute(binary), 'Pass an absolute newly built Cage CLI binary path.');
const { chromium } = createRequire(import.meta.url)(process.env.CAGE_PLAYWRIGHT_MODULE || 'playwright');
const profile = await mkdtemp(join(tmpdir(), 'cage-agent-native-browser-'));
const config = join(profile, 'profile', 'config.json');
let pid, browser;
const errors = [];
const operations = [];
const claims = [];
const refreshTickets = new Set();
let registrations = 0;
let prooflessClaims = 0;

async function cli(args, input) {
  const child = execFile(binary, ['agent', ...args, '--config', config], { timeout: 30000, encoding: 'utf8', maxBuffer: 4 << 20 });
  if (input !== undefined) child.stdin.end(JSON.stringify(input));
  const result = await new Promise((resolve, reject) => {
    let stdout = '', stderr = '';
    child.stdout.on('data', chunk => { stdout += chunk; });
    child.stderr.on('data', chunk => { stderr += chunk; });
    child.on('error', reject);
    child.on('close', code => {
      if (code !== 0) reject(new Error(`Agent ${args[0]} failed (${code}): ${stdout} ${stderr}`));
      else resolve(JSON.parse(stdout));
    });
  });
  assert.equal(result.protocol, 1);
  return result;
}

async function entry() {
  const record = JSON.parse(await readFile(join(profile, 'profile', 'control.json'), 'utf8'));
  return new Promise((resolve, reject) => {
    const body = JSON.stringify({ token: record.token });
    const request = httpRequest({ hostname: 'localhost', port: record.port, path: '/api/launch', method: 'POST', headers: { Origin: `http://localhost:${record.port}`, 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) } }, response => {
      let data = '';
      response.setEncoding('utf8');
      response.on('data', chunk => { data += chunk; });
      response.on('end', () => {
        if (response.statusCode !== 200) { reject(new Error('Could not authenticate the isolated launch entry.')); return; }
        resolve(JSON.parse(data).url);
      });
    });
    request.on('error', reject);
    request.setTimeout(10000, () => request.destroy(new Error('Isolated controller launch timed out.')));
    request.end(body);
  });
}

async function submit(request) {
  const reply = await cli(['request', 'POST', '/api/agent/native', '--input', '-'], { request, connection_id: 'local' });
  assert.equal(reply.status, 'accepted');
  assert.equal(reply.data.http_status, 202);
  assert.equal(reply.data.body.status, 'pending');
  assert.ok(reply.data.body.id);
  return reply.data.body.id;
}

async function completed(id) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    const reply = await cli(['request', 'GET', `/api/agent/native/${id}`]);
    const item = reply.data.body;
    if (item.status === 'completed' || item.status === 'failed') return item;
    assert.ok(['pending', 'running'].includes(item.status), 'Unknown native request status.');
    await setTimeout(150);
  }
  throw new Error('The compiled desktop consumer did not complete its original request.');
}

try {
  await cli(['init']);
  await writeFile(join(profile, 'profile', 'initialization.json'), JSON.stringify({ version: 1, completed: true }), { mode: 0o600 });
  await cli(['service.start']);
  pid = JSON.parse(await readFile(join(profile, 'profile', 'control.json'), 'utf8')).pid;
  await cli(['call', 'account.create', '--input', '-'], { username: 'Agent browser acceptance', password: 'Ephemeral-Browser-Password-123!', confirm_password: 'Ephemeral-Browser-Password-123!' });
  browser = await chromium.launch({ headless: true, executablePath: process.env.CAGE_TEST_BROWSER_PATH });
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', error => errors.push(error.message));
  page.on('response', async response => {
    if (new URL(response.url()).pathname === '/api/agent/native/window' && response.status() === 200) registrations++;
    if (new URL(response.url()).pathname === '/api/agent/native' && response.request().method() === 'GET') {
      if (!response.request().headers()['x-cage-agent-window']) prooflessClaims++;
      try {
        const value = await response.json(); if (value.id) claims.push(value.id);
      } catch { /* A reload can end the observation response. */ }
    }
  });
  await page.exposeFunction('__cageNativeTest', async request => {
    operations.push({ action: request.action, operation: request.operation, connection_id: request.connection_id });
    if (request.action === 'accountDevice') {
      if (request.operation === 'status') return { supported: true, biometric_available: true, device_status: { automatic: 'disabled', biometric: 'disabled' } };
      throw new Error('请完成 Touch ID 用户验证（操作系统桥验收替身）。');
    }
    if (request.action === 'refreshSession') {
      const url = await entry();
      refreshTickets.add(new URL(url).hash.slice(1));
      return { url };
    }
    if (request.action === 'appUpdate') return { phase: 'manual', currentVersion: 'acceptance-fixture', message: 'OS updater fixture; no app files are modified.' };
    if (request.action === 'appearance' || request.action === 'workbenchReady' || request.action === 'clipboardSyncCancel') return true;
    throw new Error(`Unexpected OS bridge action in this smoke test: ${request.action}`);
  });
  await page.addInitScript(() => {
    window.cageNativeBridge = { postMessage: request => window.__cageNativeTest(request) };
    // Count real document boots across the consumer's requested reload.
    sessionStorage.setItem('agent-native-browser-boots', String(Number(sessionStorage.getItem('agent-native-browser-boots') || 0) + 1));
  });
  await page.goto(await entry());
  await page.locator('.app').waitFor({ timeout: 20000 });
  await page.getByRole('complementary', { name: '项目与终端' }).waitFor();
  const boot = await page.evaluate(() => Number(sessionStorage.getItem('agent-native-browser-boots')));
  const origin = new URL(page.url()).origin;
  const ordinaryClaim = await context.request.get(`${origin}/api/agent/native?consumer=window`, { headers: { 'X-Cage-Connection': 'local' } });
  assert.equal(ordinaryClaim.status(), 403, 'A browser session without native window proof could claim an OS action.');
  const initial = await cli(['call', 'status']);
  assert.equal(initial.data.vm_running, false);
  assert.equal(initial.data.ready, false);

  const preference = await submit({ action: 'clientPreferences', operation: 'set', values: { 'cage-sidebar-collapsed': 'true', 'cage-browser-tab-layout': '"vertical"', 'cage.audio': '{"muted":true,"volume":0.25}' } });
  const saved = await completed(preference);
  assert.equal(saved.status, 'completed');
  assert.equal(saved.result.reload_required, true);
  await page.waitForFunction(previous => Number(sessionStorage.getItem('agent-native-browser-boots')) > previous, boot);
  await page.locator('.app.sidebar-collapsed').waitFor();
  assert.equal(await page.getByRole('complementary', { name: '项目与终端' }).count(), 0, 'The real workbench did not apply sidebar state after reload.');
  assert.deepEqual(await page.evaluate(() => ({ sidebar: localStorage.getItem('cage-sidebar-collapsed'), layout: localStorage.getItem('cage-browser-tab-layout'), audio: localStorage.getItem('cage.audio') })), { sidebar: 'true', layout: '"vertical"', audio: '{"muted":true,"volume":0.25}' });

  const read = await completed(await submit({ action: 'clientPreferences', operation: 'get' }));
  assert.equal(read.status, 'completed');
  assert.equal(read.result.values['cage-sidebar-collapsed'], 'true');
  assert.equal(read.result.values['cage-browser-tab-layout'], '"vertical"');

  const biometric = await completed(await submit({ action: 'accountDevice', operation: 'enable', kind: 'biometric' }));
  assert.equal(biometric.status, 'failed');
  assert.match(biometric.error, /请完成 Touch ID 用户验证/);
  assert.equal(operations.filter(item => item.action === 'accountDevice' && item.operation === 'enable').length, 1, 'The failed biometric action was repeated.');

  const refreshed = await completed(await submit({ action: 'refreshSession' }));
  assert.equal(refreshed.status, 'completed');
  assert.deepEqual(refreshed.result, { refreshed: true });
  assert.ok(refreshTickets.size > 0);
  for (const ticket of refreshTickets) assert.ok(!JSON.stringify(refreshed).includes(ticket), 'The native refresh ticket reached Agent output.');
  const forgedCompletion = await context.request.post(`${origin}/api/agent/native/${refreshed.id}/complete`, { headers: { Origin: origin, 'X-Cage-Connection': 'local' }, data: { result: { forged: true } } });
  assert.equal(forgedCompletion.status(), 403, 'A browser session without window proof could forge an OS completion.');

  const restored = await completed(await submit({ action: 'clientPreferences', operation: 'set', values: { 'cage-sidebar-collapsed': 'false' } }));
  assert.equal(restored.status, 'completed');
  await page.getByRole('complementary', { name: '项目与终端' }).waitFor();
  assert.equal(await page.locator('.app.sidebar-collapsed').count(), 0);
  // The reverse direction uses the real user control, then the Agent reads
  // the same stored state rather than a separate Agent-only preference.
  await page.getByRole('button', { name: '收起侧栏', exact: true }).click();
  await page.locator('.app.sidebar-collapsed').waitFor();
  const userChanged = await completed(await submit({ action: 'clientPreferences', operation: 'get' }));
  assert.equal(userChanged.status, 'completed');
  assert.equal(userChanged.result.values['cage-sidebar-collapsed'], 'true');
  assert.ok(registrations >= 2, 'Document reload did not establish an independent native window proof.');
  assert.equal(prooflessClaims, 0, 'The real consumer omitted its window proof.');
  const persistedClientState = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  for (const ticket of refreshTickets) assert.ok(!persistedClientState.includes(ticket), 'A native registration ticket was persisted by the UI.');
  assert.equal(claims.filter(id => id === preference).length, 1, 'A native request was claimed more than once.');
  assert.deepEqual(errors, []);
  console.log('PASS: real Cage CLI + HTTP + compiled UI native queue, private window registration, ordinary-session claim/completion rejection, single claim, bidirectional sidebar preference, persistence/reload, OS verification failure, private refresh result and no VM startup. OS bridge was a fixture; no physical Touch ID or app installation was tested.');
} finally {
  await browser?.close();
  if (pid) {
    try { process.kill(pid, 'SIGTERM'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
    let retired = false;
    for (let attempt = 0; attempt < 100; attempt++) {
      try { await stat(join(profile, 'profile', 'control.json')); } catch (error) { if (error.code === 'ENOENT') { retired = true; break; } throw error; }
      await setTimeout(100);
    }
    assert.ok(retired, 'Test controller did not retire; preserve its isolated directory.');
  }
  await rm(profile, { recursive: true, force: true });
}
