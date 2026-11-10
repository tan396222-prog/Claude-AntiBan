import assert from "node:assert/strict";
import test from "node:test";
import net from "node:net";
import { once } from "node:events";
import { mkdtemp, rm, chmod, writeFile, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";
import { serveMacOSBrowser, relayMacOSBrowser } from "../macos-browser-service.mjs";
const unixService = process.platform === "win32" ? test.skip : test;

async function fixture(t, dispatch) {
  const directory = await mkdtemp(path.join(tmpdir(), "cage-bs-"));
  let stopped = 0;
  const service = await serveMacOSBrowser({ directory, dispatch, close: async () => { stopped++; } });
  t.after(async () => { await service.close(); await rm(directory, { recursive: true, force: true }); });
  const clients = [];
  t.after(() => { for (const client of clients) client.destroy(); });
  const connect = async () => {
    const socket = net.createConnection(path.join(directory, "browser.sock"));
    await once(socket, "connect");
    clients.push(socket);
    const lines = createInterface({ input: socket });
    return { socket, call: async value => {
      const reply = once(lines, "line");
      socket.write(JSON.stringify(value) + "\n");
      return JSON.parse((await reply)[0]);
    } };
  };
  return { directory, connect, service, stopped: () => stopped };
}

unixService("Mac browser connections retain owned state and publish service ownership", async t => {
  let value = "";
  const f = await fixture(t, async request => {
    if (request.text !== undefined) value = request.text;
    return { text: value };
  });
  const first = await f.connect();
  await first.call({ text: "unsaved editor buffer" });
  first.socket.destroy();
  const second = await f.connect();
  const result = await second.call({});
  assert.equal(result.text, "unsaved editor buffer");
  assert.deepEqual(result.service, { version: 1, persistent: true });
  assert.equal(f.stopped(), 0);
  assert.equal((await stat(path.join(f.directory, "browser.sock"))).mode & 0o777, 0o600);
  await assert.rejects(serveMacOSBrowser({ directory: f.directory, dispatch: async () => ({}), close: async () => {} }), /already running/);
});

unixService("Mac browser service drops a disconnected caller's queued mutation", async t => {
  let release, began;
  const started = new Promise(resolve => { began = resolve; });
  const wait = new Promise(resolve => { release = resolve; });
  const applied = [];
  const f = await fixture(t, async request => {
    if (request.hold) { began(); await wait; }
    applied.push(request.id);
    return { ok: true };
  });
  const first = await f.connect(), second = await f.connect();
  const held = first.call({ id: "first", hold: true });
  await started;
  second.socket.write('{"id":"canceled"}\n');
  second.socket.destroy();
  await new Promise(resolve => setTimeout(resolve, 30));
  release();
  await held;
  await first.call({ id: "last" });
  assert.deepEqual(applied, ["first", "last"]);
});

unixService("Mac browser service refuses insecure directories and unrelated socket paths", async t => {
  const directory = await mkdtemp(path.join(tmpdir(), "cage-bs-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await chmod(directory, 0o755);
  await assert.rejects(serveMacOSBrowser({ directory, dispatch: async () => ({}), close: async () => {} }), /directory/);
  await chmod(directory, 0o700);
  await writeFile(path.join(directory, "browser.sock"), "retained file", { mode: 0o600 });
  await assert.rejects(relayMacOSBrowser(directory), /socket/);
  await assert.rejects(serveMacOSBrowser({ directory, dispatch: async () => ({}), close: async () => {} }), /socket/);
  assert.equal((await stat(path.join(directory, "browser.sock"))).isFile(), true);
});

unixService("Mac browser service releases its endpoint when owned browser shutdown fails", async t => {
  const directory = await mkdtemp(path.join(tmpdir(), "cage-bs-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const service = await serveMacOSBrowser({ directory, dispatch: async () => ({}), close: async () => { throw new Error("fixture shutdown failure"); } });
  await assert.rejects(service.close(), /fixture shutdown failure/);
  await assert.rejects(stat(path.join(directory, "browser.sock")), { code: "ENOENT" });
  const replacement = await serveMacOSBrowser({ directory, dispatch: async () => ({}), close: async () => {} });
  await replacement.close();
});
