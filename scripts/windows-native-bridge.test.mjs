import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import vm from 'node:vm';

const source = await readFile(new URL('../desktop/windows/MainWindow.cs', import.meta.url), 'utf8');
const script = source.match(/private const string BridgeScript = """\s*([\s\S]*?)\s*""";/)?.[1];
assert.ok(script);

function bridge() {
  let reply, sent;
  const timers = new Map();
  const window = {};
  window.top = window;
  vm.runInNewContext(script, {
    window, location: { protocol: 'http:', hostname: 'localhost' }, crypto: { randomUUID },
    chrome: { webview: {
      addEventListener: (_, callback) => { reply = callback; },
      postMessage: value => { sent = value; },
    } },
    setTimeout: (callback, delay) => { const id = randomUUID(); timers.set(id, { callback, delay }); return id; },
    clearTimeout: id => timers.delete(id),
  });
  return {
    call: request => window.webkit.messageHandlers.cage.postMessage(request),
    reply: value => reply({ data: { id: sent.id, ...value } }),
    timers,
  };
}

test('native file replies remain pending beyond four minutes and through the backend deadline', async () => {
  for (const action of ['uploadHostFiles', 'chooseUploadFiles', 'clipboardSyncRead', 'clipboardSyncWrite',
    'saveBrowserDownload', 'browserDownloadAction', 'desktopFileAction', 'exportDiagnostics', 'importTheme', 'exportTheme',
    'shareLocalProject', 'chooseFolder', 'chooseGuestMedia']) {
    const fixture = bridge();
    const pending = fixture.call({ action });
    const timer = [...fixture.timers.values()][0];
    assert.ok(timer.delay > 30 * 60 * 1000, action);
    fixture.reply({ result: { saved: true } });
    assert.equal((await pending).saved, true, action);
    assert.equal(fixture.timers.size, 0);
  }
});

test('file failure reaches the caller and the following request can succeed', async () => {
  const fixture = bridge();
  const first = fixture.call({ action: 'saveBrowserDownload' });
  fixture.reply({ error: '磁盘空间不足' });
  await assert.rejects(first, /磁盘空间不足/);
  const second = fixture.call({ action: 'clipboardSyncRead' });
  fixture.reply({ result: { text: 'next copy' } });
  assert.equal((await second).text, 'next copy');
  assert.equal(fixture.timers.size, 0);
});

test('ordinary native commands retain their bounded timeout', async () => {
  const fixture = bridge();
  const pending = fixture.call({ action: 'accountDevice' });
  const timer = [...fixture.timers.values()][0];
  assert.equal(timer.delay, 240_000);
  timer.callback();
  await assert.rejects(pending, /原生操作超时/);
  fixture.reply({ result: true });
});
