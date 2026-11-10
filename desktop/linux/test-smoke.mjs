#!/usr/bin/env node
// Run under a real Linux display or xvfb-run + dbus-run-session.
import { mkdtemp, mkdir, readFile, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { randomBytes } from 'node:crypto';
import { spawn } from 'node:child_process';

const [nativeArgument, outputArgument] = process.argv.slice(2);
if (!nativeArgument || !outputArgument || process.argv.length !== 4) {
  console.error('Usage: test-smoke.mjs /path/to/cage-desktop /absolute/report.json');
  process.exit(2);
}
const native = resolve(nativeArgument);
const backend = join(dirname(native), 'cage');
const output = resolve(outputArgument);
const root = await mkdtemp(join(tmpdir(), 'cage-linux-smoke-'));
// Cage places the default workspace beside the profile; both stay in this
// fixture directory so cleanup also removes the temporary workspace.
// Keep the selected gateway alive for the two native storage phases. The
// native windows register their own owners in addition to this test process.
const env = { ...process.env, CLAUDE_CAGE_CONFIG: join(root, 'profile', 'config.json'), CAGE_DESKTOP_OWNER_PID: String(process.pid) };
let clean = false;
let result = 1;

function execute(executable, args, timeout = 190000) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(executable, args, { env, detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '', overflow = false;
    const stopGroup = () => {
      try { process.kill(-child.pid, 'SIGKILL'); } catch { /* Already exited. */ }
    };
    const timer = setTimeout(stopGroup, timeout);
    child.stdout.on('data', data => {
      stdout += data;
      if (stdout.length > 65536) { overflow = true; stopGroup(); }
    });
    child.stderr.on('data', data => {
      stderr = (stderr + data).slice(-8192);
      if (executable === native && args[0] === '--smoke-test') process.stderr.write(data);
    });
    child.on('error', error => { clearTimeout(timer); reject(error); });
    // A failed native process can leave WebKit children holding its pipes open.
    // The controller deliberately detaches into its own group and is cleaned
    // through prepare-update below, never by this process-group signal.
    child.on('exit', (code, signal) => { if (code !== 0 || signal) stopGroup(); });
    child.on('close', (code, signal) => {
      clearTimeout(timer);
      if (overflow) reject(new Error('Backend response exceeded the output limit.'));
      else resolvePromise({ code, signal, stdout, stderr });
    });
  });
}
function parseBackend(run) {
  // Do not include stdout in errors: desktop's URL carries a session token.
  let data;
  try { data = JSON.parse(run.stdout); } catch { throw new Error('Backend returned invalid JSON.'); }
  if (run.code !== 0) throw new Error('Backend operation failed; inspect the isolated control.log.');
  return data;
}

try {
  if (!isAbsolute(output)) throw new Error('Report path must be absolute.');
  await mkdir(join(root, 'profile'), { mode: 0o700 });
  await mkdir(dirname(output), { recursive: true });
  await rm(output, { force: true });
  await rm(output + '.png', { force: true });
  const policy = await execute(native, ['--self-test'], 30000);
  if (policy.code !== 0) throw new Error('Native origin policy tests failed.');
  const entry = parseBackend(await execute(backend, ['desktop', '--json']));
  if (entry.status !== 'ready') throw new Error('Isolated backend was not ready.');
  const url = new URL(entry.url);
  if (url.protocol !== 'http:' || url.hostname !== 'localhost' || !url.hash || url.username || url.password)
    throw new Error('Backend returned an invalid loopback entry.');
  const authenticate = await fetch(url.origin + '/api/session', {
    method: 'POST', redirect: 'error',
    headers: { 'Origin': url.origin, 'Content-Type': 'application/json' },
    body: JSON.stringify({ token: url.hash.slice(1) }),
  });
  if (!authenticate.ok) throw new Error('Session authentication failed.');
  const cookie = authenticate.headers.get('set-cookie')?.split(';')[0];
  if (!cookie) throw new Error('Authenticated response did not create a session cookie.');
  async function api(path, body, generation) {
    const headers = { Origin: url.origin, Cookie: cookie };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (generation) headers['X-Cage-Connection'] = generation;
    const response = await fetch(url.origin + '/api/' + path, {
      method: body === undefined ? 'GET' : 'POST', redirect: 'error', headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) throw new Error(`Isolated smoke API ${path} failed with ${response.status}.`);
    return response.json();
  }
  let current = await api('client/context');
  if (!current.desktop_gateway || current.mode !== 'none') throw new Error('A fresh full Cage must start at the environment picker.');
  const idle = await api('client/local-status');
  if (!idle.known || idle.service_running || idle.running) throw new Error('The desktop picker started a local workload.');
  const original = current.connection_id;
  const pending = await api('client/candidate', { connection_id: original, mode: 'local' });
  const deadline = Date.now() + 30000;
  while (true) {
    current = await api('client/context');
    if (current.connection_id !== original || current.candidate?.id !== pending.id) throw new Error('The local candidate lost its original desktop view.');
    if (current.candidate.connection.state === 'connected') break;
    if (current.candidate.connection.state === 'failed' || Date.now() > deadline) throw new Error('Could not open the local test workbench.');
    await new Promise((r) => setTimeout(r, 100));
  }
  current = await api('client/commit', { connection_id: original, candidate_id: pending.id, local_action: 'auto' });
  if (current.mode !== 'local' || current.connection_id === original) throw new Error('Local workbench generation was not selected.');
  const password = randomBytes(24).toString('base64url');
  const account = await api('account/create', { username: 'Linux Smoke', password, confirm_password: password }, current.connection_id);
  if (!account.unlocked) throw new Error('Could not prepare the isolated unlocked test account.');
  env.CAGE_SMOKE_STORAGE_MARKER = randomBytes(24).toString('hex');
  for (const phase of ['seed', 'verify']) {
    env.CAGE_SMOKE_STORAGE_PHASE = phase;
    await rm(output, { force: true });
    const nativeRun = await execute(native, ['--smoke-test', output], 150000);
    if (nativeRun.code !== 0) throw new Error(`Native WebKit workbench ${phase} test failed.`);
    const pass = JSON.parse(await readFile(output, 'utf8'));
    if (pass.success !== true || pass.storageMarkerMatched !== true) throw new Error(`Native storage ${phase} evidence is incomplete.`);
  }
  const evidence = JSON.parse(await readFile(output, 'utf8'));
  for (const key of ['success', 'nativeBridgeReady', 'authenticated', 'accountUnlocked', 'workbenchRendered', 'workbenchReadyAcknowledged', 'statusReceived', 'settingsReceived', 'unauthenticatedBridgeDenied', 'localStoragePersisted']) {
    if (evidence[key] !== true) throw new Error(`Native smoke report lacks ${key}.`);
  }
  if ((await stat(output + '.png')).size < 1024) throw new Error('Native screenshot is missing or empty.');
  for (const directory of ['webkit', 'webkit/data', 'webkit/cache']) {
    if ((await stat(join(root, 'profile', directory))).mode & 0o077) throw new Error('WebKit profile storage is not private.');
  }
  result = 0;
  console.log(JSON.stringify({ success: true, report: output, screenshot: output + '.png', version: evidence.version }));
} catch (error) {
  console.error(error.message);
} finally {
  try {
    const cleanupRun = await execute(backend, ['prepare-update', '--json']);
    if (cleanupRun.code !== 0) {
      // prepare-update returns only status/message. Never log desktop's
      // entry JSON or arbitrary stdout, which can contain a session URL.
      let message = '';
      try {
        const response = JSON.parse(cleanupRun.stdout);
        if (typeof response.message === 'string') message = response.message.slice(0, 500);
      } catch { /* Keep malformed or non-JSON output private. */ }
      console.error(`Cleanup prepare-update failed (exit ${cleanupRun.code}, signal ${cleanupRun.signal || 'none'}): ${message}`);
    }
    const stopped = parseBackend(cleanupRun);
    clean = stopped.status === 'prepared';
    if (!clean) console.error(`Cleanup prepare-update returned ${stopped.status || 'unknown status'}.`);
  } catch (error) {
    console.error(`Cleanup could not confirm controller exit: ${error.message}`);
    // Preserve the fixture until a remaining controller can be stopped.
  }
  if (clean) await rm(root, { recursive: true, force: true });
  else { console.error(`Cleanup incomplete; isolated profile retained at ${root}`); result = 1; }
}
process.exitCode = result;
