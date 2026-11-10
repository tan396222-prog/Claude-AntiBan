import test from "node:test";
import assert from "node:assert/strict";
import { copyGuestText, shareClipboard } from "./shared-clipboard.mjs";

const settle = () => new Promise(resolve => setImmediate(resolve));

test("web copy reads a fresh GUI revision only after its keys are acknowledged", async () => {
  const events = [];
  const value = await copyGuestText({
    request: async request => {
      events.push(request.phase);
      if (request.phase === "prepare") return { revision: 4, session: "gui-a" };
      assert.equal(request.revision, 4);
      return { revision: 5, session: "gui-a", text: "newly copied text" };
    },
    sendCopy: async () => { events.push("keys-ack"); },
    valid: () => true,
  });
  assert.deepEqual(events, ["prepare", "keys-ack", "read"]);
  assert.deepEqual(value, { text: "newly copied text" });
});

test("web copy rejects an unchanged clipboard and a switched GUI", async () => {
  for (const result of [{ revision: 4, session: "gui-a", text: "old" }, { revision: 5, session: "gui-b", text: "other session" }]) {
    await assert.rejects(copyGuestText({
      request: async request => request.phase === "prepare" ? { revision: 4, session: "gui-a" } : result,
      sendCopy: async () => {}, valid: () => true,
    }));
  }
});

test("web copy stops before key injection after its target changes", async () => {
  let sent = false;
  await assert.rejects(copyGuestText({
    request: async () => ({ revision: 4, session: "gui-a" }),
    sendCopy: async () => { sent = true; }, valid: () => false,
  }));
  assert.equal(sent, false);
});
function fixture({ available = true } = {}) {
  const sent = [], writes = [], active = [], notices = [], released = [];
  let revision = 1, content = { text: "host initial" }, pauseRead, pauseWrite, nativeGeneration = 0, delayAcks = false;
  const pendingAcks = [];
  const socket = { readyState: 1, send: text => {
    const value = JSON.parse(text);
    sent.push(Object.fromEntries(Object.entries(value).filter(([key]) => key !== "serial")));
    if (delayAcks) pendingAcks.push(value.serial);
    else queueMicrotask(() => socket.onmessage({ data: JSON.stringify({ ack: value.serial }) }));
  }, close() { this.readyState = 3; this.onclose?.(); } };
  const controller = shareClipboard({
    connect: () => socket, interval: 60000, onStatus: text => notices.push(text), setActive: value => active.push(value),
    native: async (action, value) => {
      if (action === "clipboardRevision") return revision;
      if (action === "clipboardSyncRead") {
        const snapshot = { ...content, revision };
        if (pauseRead) await pauseRead;
        return snapshot;
      }
      if (action === "clipboardSyncCancel") { nativeGeneration++; return; }
      if (action === "clipboardSyncRelease") { released.push(value.files); return; }
      if (action === "clipboardSyncWrite") {
        const generation = nativeGeneration;
        if (pauseWrite) await pauseWrite;
        if (generation !== nativeGeneration || value.revision !== revision) return { superseded: true, revision };
        content = { ...value }; delete content.revision;
        writes.push(content); return { revision: ++revision };
      }
    },
  });
  socket.onopen();
  socket.onmessage({ data: JSON.stringify({ hello: { version: 1, os: "windows", session: "fixture-session", formats: ["text", "png", "files"], available } }) });
  return { socket, controller, sent, writes, active, notices, released,
    host: value => { content = value; revision++; },
    guest: value => socket.onmessage({ data: JSON.stringify(value) }),
    readDelay: value => { pauseRead = value; }, writeDelay: value => { pauseWrite = value; },
    delayAcks: () => { delayAcks = true; },
    acknowledge: () => {
      assert.ok(pendingAcks.length, "a guest acknowledgement is pending");
      socket.onmessage({ data: JSON.stringify({ ack: pendingAcks.shift() }) });
    },
  };
}

test("explicit sync rereads a superseded host snapshot and waits for the latest guest acknowledgement", async () => {
  const f = fixture();
  try {
    await settle();
    let release;
    f.host({ files: "slow host files" });
    f.readDelay(new Promise(resolve => { release = resolve; }));
    const background = f.controller.poll();
    await settle();
    f.host({ text: "latest host copy" });
    f.delayAcks();
    let finished = false;
    const sync = f.controller.sync().then(() => { finished = true; });
    release();
    await background; await settle();
    assert.deepEqual(f.sent.at(-1), { text: "latest host copy" });
    assert.deepEqual(f.released, ["slow host files"]);
    assert.equal(finished, false);
    f.acknowledge();
    await sync;
    assert.equal(finished, true);
  } finally { f.controller.dispose(); }
});

test("guest file references stay alive until the last queued consumer finishes", async () => {
  const f = fixture();
  try {
    await settle();
    let release;
    f.writeDelay(new Promise(resolve => { release = resolve; }));
    f.guest({ files: "shared-selection" });
    await settle();
    f.guest({ files: "shared-selection" });
    await settle();
    assert.deepEqual(f.released, []);
    release();
    await settle(); await settle();
    assert.deepEqual(f.released, ["shared-selection"]);
    assert.deepEqual(f.writes, [{ files: "shared-selection" }]);
  } finally { f.controller.dispose(); }
});

test("explicit sync follows a new host revision that arrives while its acknowledgement is pending", async () => {
  const f = fixture();
  try {
    await settle();
    f.delayAcks();
    f.host({ text: "first copy" });
    let finished = false;
    const sync = f.controller.sync().then(() => { finished = true; });
    await settle();
    f.host({ text: "second copy" });
    f.acknowledge();
    await settle();
    assert.deepEqual(f.sent.at(-1), { text: "second copy" });
    assert.equal(finished, false);
    f.acknowledge();
    await sync;
  } finally { f.controller.dispose(); }
});

for (const transition of ["lock", "switch", "disconnect"]) {
  test(`explicit sync fails when the guest ${transition} occurs before acknowledgement`, async () => {
    const f = fixture();
    try {
      await settle();
      f.delayAcks();
      f.host({ text: "unconfirmed host copy" });
      const rejected = assert.rejects(f.controller.sync());
      await settle();
      if (transition === "disconnect") f.socket.close();
      else f.guest({ hello: { version: 1, os: "windows", session: transition === "lock" ? "fixture-session" : "next-session",
        formats: ["text", "png", "files"], available: transition !== "lock" } });
      f.acknowledge();
      await rejected;
    } finally { f.controller.dispose(); }
  });
}

test("disconnect rejects explicit sync before a slow native read finishes", async () => {
  const f = fixture();
  let release;
  try {
    await settle();
    f.host({ files: "slow host files" });
    f.readDelay(new Promise(resolve => { release = resolve; }));
    const rejected = assert.rejects(f.controller.sync());
    await settle();
    f.socket.close();
    await Promise.race([rejected, new Promise((_, reject) => setTimeout(() => reject(new Error("sync waited for an invalidated native read")), 100))]);
  } finally { release?.(); f.controller.dispose(); }
});

test("explicit sync rejects an unavailable guest", async () => {
  const f = fixture({ available: false });
  try { await assert.rejects(f.controller.sync()); }
  finally { f.controller.dispose(); }
});

test("a guest write error rejects its pending sync", async () => {
  const f = fixture();
  try {
    await settle();
    f.delayAcks();
    f.host({ text: "unconfirmed copy" });
    const rejected = assert.rejects(f.controller.sync());
    await settle();
    f.guest({ error: "clipboard_write_failed" });
    await rejected;
    assert.equal(f.active.at(-1), false);
  } finally { f.controller.dispose(); }
});

test("a guest without an unlocked GUI cannot activate clipboard sharing", async () => {
  const f = fixture({ available: false });
  try {
    await settle(); await f.controller.poll();
    assert.deepEqual(f.sent, []);
    assert.equal(f.active.at(-1), false);
    f.guest({ hello: { version: 1, os: "windows", session: "unlocked-session", formats: ["text", "png", "files"], available: true } });
    await settle();
    assert.deepEqual(f.sent, [{ text: "host initial" }]);
    assert.equal(f.active.at(-1), true);
  } finally { f.controller.dispose(); }
});

test("locking a guest invalidates an in-flight host clipboard snapshot", async () => {
  const f = fixture();
  try {
    await settle();
    let release;
    f.host({ text: "copy made before guest lock" });
    f.readDelay(new Promise(resolve => { release = resolve; }));
    const pending = f.controller.poll(); await settle();
    f.guest({ hello: { version: 1, os: "windows", session: "locked", formats: ["text", "png", "files"], available: false } });
    release(); await pending; await settle();
    assert.equal(f.sent.some(value => value.text === "copy made before guest lock"), false);
    assert.equal(f.active.at(-1), false);
  } finally { f.controller.dispose(); }
});

test("a restarted GUI session seeds the current host selection again", async () => {
  const f=fixture();
  try {
    await settle();
    f.guest({hello:{version:1,os:"windows",session:"restarted-gui",formats:["text","png","files"],available:true}});
    await settle();
    assert.deepEqual(f.sent,[{text:"host initial"},{text:"host initial"}]);
    assert.equal(f.active.at(-1),true);
  }finally{f.controller.dispose();}
});

for (const transition of ["lock", "switch", "disconnect"]) {
  test(`a pending native file write cannot update the host after guest ${transition}`, async () => {
    const f = fixture();
    try {
      await settle();
      let release;
      f.writeDelay(new Promise(resolve => { release = resolve; }));
      f.guest({ files: "old-session-selection" });
      await settle();
      if (transition === "disconnect") f.socket.close();
      else f.guest({ hello: { version: 1, os: "windows", session: transition === "lock" ? "fixture-session" : "next-session",
        formats: ["text", "png", "files"], available: transition !== "lock" } });
      release();
      await settle();
      assert.deepEqual(f.writes, []);
      assert.deepEqual(f.notices, []);
    } finally { f.controller.dispose(); }
  });
}

test("text, images and file selections synchronize both ways and do not echo", async () => {
  const f = fixture();
  try {
    await settle();
    assert.deepEqual(f.sent, [{ text: "host initial" }]);
    for (const content of [{ text: "中文🙂\nsecond line" }, { png: "fixture-png" }, { files: "fixture-file-selection" }]) {
      f.guest(content); await settle();
      assert.deepEqual(f.writes.at(-1), content);
      const sent = f.sent.length;
      await f.controller.poll(); assert.equal(f.sent.length, sent, "a native write is not sent back");
      const writes = f.writes.length;
      f.guest(content); await settle(); assert.equal(f.writes.length, writes, "a duplicate guest event is ignored");
      f.host({ ...content, ...(content.text ? { text: "new host text" } : content.png ? { png: "new host PNG" } : { files: "new host files" }) });
      await f.controller.poll();
      assert.equal(f.sent.length, sent + 1);
    }
  } finally { f.controller.dispose(); }
});

test("a local copy made before a delayed guest write wins", async () => {
  const f = fixture();
  try {
    await settle();
    let release;
    f.writeDelay(new Promise(resolve => { release = resolve; }));
    f.guest({ text: "old guest copy" }); await settle();
    f.host({ text: "new local copy" }); release(); await settle(); await settle();
    assert.deepEqual(f.writes, []);
    assert.deepEqual(f.sent.at(-1), { text: "new local copy" });
  } finally { f.controller.dispose(); }
});

test("a guest copy invalidates a host snapshot that was already being read", async () => {
  const f = fixture();
  try {
    await settle();
    f.host({ files: "slow host upload" });
    let release;
    f.readDelay(new Promise(resolve => { release = resolve; }));
    const poll = f.controller.poll(); await settle();
    f.guest({ text: "new guest copy" }); await settle(); release(); await poll; await settle();
    assert.equal(f.sent.some(value => value.files === "slow host upload"), false);
  } finally { f.controller.dispose(); }
});

test("disposed connections cannot send snapshots or receive delayed writes", async () => {
  const f = fixture(); await settle();
  f.controller.dispose();
  f.guest({ text: "late guest update" }); await f.controller.poll(); await settle();
  assert.deepEqual(f.writes, []);
  assert.deepEqual(f.active.at(-1), false);
});
