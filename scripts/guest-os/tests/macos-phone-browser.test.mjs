import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { once } from "node:events";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import net from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { PassThrough } from "node:stream";
import { createInterface } from "node:readline";
import { promisify } from "node:util";
import { fileURLToPath, pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";

test("packaged Mac phone controller uses isolated profile and public-web policy", { skip: !process.env.CAGE_TEST_CHROMIUM }, async t => {
  const temporary = await mkdtemp(path.join(tmpdir(), "cage-mac-phone-controller-"));
  t.after(() => rm(temporary, { recursive: true, force: true }));
  const bundle = path.join(temporary, "controller");
  const script = new URL("../package-macos-browser.sh", import.meta.url);
  await promisify(execFile)("bash", [fileURLToPath(script), bundle]);
  const { runMacOSPhoneBrowser } = await import(pathToFileURL(path.join(bundle, "macos-phone-browser.mjs")));
  const site = http.createServer((_, response) => {
    response.setHeader("Content-Type", "text/html");
    response.end('<meta name="viewport" content="width=device-width,initial-scale=1"><title>native phone fixture</title><input aria-label="message">');
  });
  site.listen(0, "127.0.0.1");
  await once(site, "listening");
  t.after(() => site.close());
  const input = new PassThrough();
  const output = new PassThrough();
  const replies = createInterface({ input: output })[Symbol.asyncIterator]();
  const controller = runMacOSPhoneBrowser(input, output, {
    testing: true, binary: process.env.CAGE_TEST_CHROMIUM, directory: path.join(temporary, "phone-profile"),
    connect: async (name, port) => {
      assert.equal(name, "phone.example");
      assert.equal(port, site.address().port);
      const socket = net.connect({ host: "127.0.0.1", port });
      await once(socket, "connect");
      return socket;
    },
  });
  t.after(async () => { input.end(); await controller; output.end(); });
  const id = randomUUID();
  const request = async (operation, values = {}) => {
    const reply = replies.next();
    input.write(JSON.stringify({ method: "POST", path: "/phone-browser/" + operation, input: { id, ...values } }) + "\n");
    return JSON.parse((await reply).value);
  };
  const profile = "a".repeat(64);
  const rejected = await request("open", { profile, url: "http://127.0.0.1/private", width: 390, height: 640 });
  assert.equal(rejected.status, 403);
  assert.equal((await request("open", { profile, url: `http://phone.example:${site.address().port}/`, width: 390, height: 640 })).id, id);
  let frame;
  for (let attempt = 0; attempt < 90; attempt++) {
    frame = await request("frame");
    if (frame.title === "native phone fixture" && frame.image) break;
    await delay(frame.status === 425 ? 1000 : 50);
  }
  assert.equal(frame.title, "native phone fixture");
  assert.equal(frame.width, 390);
  assert.equal(frame.height, 640);
  assert.ok(frame.document && frame.image);
  assert.equal((await request("action", { type: "navigate", document: frame.document, url: "file:///etc/passwd" })).status, 403);
  assert.equal((await request("close")).id, id);
  assert.equal((await request("frame")).status, 410);
});

test("a cold phone launch is shared across polls and stopped on close, expiry and transport EOF", { skip: process.platform === "win32" }, async t => {
  const directory = await mkdtemp(path.join(tmpdir(), "cage-phone-start-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const bundle = path.join(directory, "controller");
  await promisify(execFile)("bash", [fileURLToPath(new URL("../package-macos-browser.sh", import.meta.url)), bundle]);
  const { runMacOSPhoneBrowser } = await import(pathToFileURL(path.join(bundle, "macos-phone-browser.mjs")));
  const marker = path.join(directory, "starts");
  const binary = path.join(directory, "slow-browser");
  await writeFile(binary, `#!/usr/bin/env node\nrequire("node:fs").appendFileSync(${JSON.stringify(marker)}, process.pid + "\\n"); setInterval(() => {}, 1000);\n`);
  await chmod(binary, 0o700);
  const input = new PassThrough(), output = new PassThrough();
  const replies = createInterface({ input: output })[Symbol.asyncIterator]();
  const controller = runMacOSPhoneBrowser(input, output, { testing: true, binary, directory });
  t.after(async () => { input.end(); await controller; output.end(); });
  async function request(id, operation) {
    const reply = replies.next();
    const fields = operation === "open" ? { width: 390, height: 640, profile: "a".repeat(64) } : {};
    input.write(JSON.stringify({ method: "POST", path: "/phone-browser/" + operation, input: { id, ...fields } }) + "\n");
    return JSON.parse((await reply).value);
  }
  async function starts(count) {
    let list = [];
    for (let i = 0; i < 60; i++) {
      try { list = (await readFile(marker, "utf8")).trim().split("\n").map(Number); }
      catch (error) { if (error.code !== "ENOENT") throw error; }
      if (list.length >= count) break;
      await delay(50);
    }
    assert.equal(list.length, count);
    return list;
  }
  const dead = pid => assert.throws(() => process.kill(pid, 0), { code: "ESRCH" });
  const first = randomUUID();
  assert.equal((await request(first, "open")).id, first);
  const polls = await Promise.all([request(first, "frame"), request(first, "frame"), request(first, "open")]);
  assert.deepEqual(polls.map(reply => reply.status ?? reply.id), [425, 425, first]);
  const [firstPid] = await starts(1);
  assert.equal((await request(first, "close")).id, first);
  dead(firstPid);

  const realTimeout = globalThis.setTimeout;
  const shortDeadline = t.mock.method(globalThis, "setTimeout", (fn, ms, ...args) => realTimeout(fn, ms === 75000 ? 600 : ms, ...args));
  const second = randomUUID();
  assert.equal((await request(second, "open")).id, second);
  let final;
  for (let i = 0; i < 50; i++) {
    final = await request(second, "frame");
    if (final.status !== 425) break;
    await delay(100);
  }
  assert.equal(final.status, 503);
  dead((await starts(2))[1]);
  shortDeadline.mock.restore();

  assert.equal((await request(first, "open")).id, first);
  const launched = await starts(3);
  input.end();
  await controller;
  dead(launched[2]);
});
