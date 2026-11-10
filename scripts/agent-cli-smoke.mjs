// Exercise the shipped entry point against a disposable profile, without a VM.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { isAbsolute, join } from 'node:path';
import { setTimeout } from 'node:timers/promises';

const binary = process.argv[2];
assert.ok(binary && isAbsolute(binary), 'Pass an absolute Cage CLI binary path.');
const profile = await mkdtemp(join(tmpdir(), 'cage-agent-cli-'));
const config = join(profile, 'profile', 'config.json');
let controllerPID;
function call(args, input, expectedCode = 0) {
  const result = spawnSync(binary, ['agent', ...args, '--config', config], { input: input === undefined ? undefined : JSON.stringify(input), encoding: 'utf8', timeout: 30000 });
  assert.ifError(result.error);
  assert.equal(result.status, expectedCode, `Unexpected exit from ${args[0]}: ${result.stdout} ${result.stderr}`);
  const reply = JSON.parse(result.stdout);
  assert.equal(reply.protocol, 1);
  assert.equal(reply.config_path, config);
  return reply;
}
try {
  const capabilities = call(['capabilities']);
  assert.ok(capabilities.data.operations.some(operation => operation.name === 'settings.patch'));
  assert.equal(call(['doctor']).data.checks[0].code, 'config_missing');
  assert.equal(call(['call', 'settings.get'], undefined, 1).error.code, 'service_unavailable');
  await assert.rejects(stat(join(profile, 'profile')), { code: 'ENOENT' });
  call(['init']);
  const originalConfig = await readFile(config, 'utf8');
  call(['init']); assert.equal(await readFile(config, 'utf8'), originalConfig);
  call(['service.start']);
  controllerPID = JSON.parse(await readFile(join(profile, 'profile', 'control.json'), 'utf8')).pid;
  call(['service.start']);
  assert.equal(JSON.parse(await readFile(join(profile, 'profile', 'control.json'), 'utf8')).pid, controllerPID);
  assert.equal(call(['call', 'settings.get'], undefined, 3).error.code, 'account_locked');
  call(['call', 'account.create', '--input', '-'], { username: 'agent_acceptance', password: 'Ephemeral-Test-Password-123!', confirm_password: 'Ephemeral-Test-Password-123!' });
  const before = call(['call', 'settings.get']).data;
  const patch = { balanced_display: !before.balanced_display };
  call(['call', 'settings.patch', '--input', '-'], { patch, dry_run: true });
  assert.deepEqual(call(['call', 'settings.get']).data, before);
  const applied = call(['call', 'settings.patch', '--input', '-', '--wait', '10s'], { patch });
  assert.equal(applied.status, 'completed'); assert.equal(applied.data.running, false);
  assert.equal(call(['job', applied.data.id]).data.id, applied.data.id);
  assert.equal(call(['call', 'settings.get']).data.balanced_display, patch.balanced_display);
  call(['call','browser.preferences.set','--input','-'],{browser_home_url:'https://example.org/'});
  assert.equal(call(['call','settings.get']).data.browser_home_url,'https://example.org/');
  const rejected = call(['call', 'settings.patch', '--input', '-'], { patch: { proxy_url: 'socks5://127.0.0.1:1' } }, 1);
  assert.equal(rejected.error.code, 'request_rejected');
  const proxy = call(['call', 'proxy.status']);
  assert.equal(proxy.data.sources.length, 0);
  assert.ok(!JSON.stringify(proxy).includes('proxy_url'));
  const bundle = join(profile, 'diagnostics.zip');
  call(['diagnostics', '--output', bundle]);
  assert.ok((await stat(bundle)).size > 0);
  assert.equal(call(['diagnostics', '--output', bundle], undefined, 2).error.code, 'invalid_input');
  console.log('PASS: real CLI discovery, offline doctor, no implicit writes, idempotent init/start, account locking, private input, dry run, settings patch, tracked completion, proxy status and diagnostic export.');
} finally {
  if (controllerPID) {
    try { process.kill(controllerPID, 'SIGTERM'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
    // Wait for this test's controller to release its record before cleanup.
    let retired = false;
    for (let attempt = 0; attempt < 100; attempt++) {
      try { await stat(join(profile, 'profile', 'control.json')); } catch (error) { if (error.code === 'ENOENT') { retired = true; break; } throw error; }
      await setTimeout(100);
    }
    assert.ok(retired, 'Controller did not retire; preserve the isolated profile for inspection.');
  }
  await rm(profile, { recursive: true, force: true });
}
