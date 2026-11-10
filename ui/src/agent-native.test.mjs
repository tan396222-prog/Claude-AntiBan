import assert from "node:assert/strict";
import test from "node:test";
import { agentClientPreferences, clientPreferences, createAgentNativeConsumer, executeAgentNative } from "./agent-native.js";

const context = { mode: "remote", connection_id: "generation-a", instance_id: "server-one" };
function storage() {
  const values = new Map();
  return { values, getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) };
}
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
function clock() {
  const scheduled = [];
  return { scheduled, schedule: callback => { scheduled.push(callback); return callback; }, cancel: callback => { const index = scheduled.indexOf(callback); if (index >= 0) scheduled.splice(index, 1); }, tick: async () => { scheduled.shift()?.(); await flush(); } };
}
function consumerForTest(dependencies) {
  return createAgentNativeConsumer({
    ...dependencies,
    getOrigin: () => "http://localhost:17777",
    native: async (action, extra) => action === "refreshSession" ? { url: "http://localhost:17777/#fresh-native-ticket" } : dependencies.native(action, extra),
    api: async (path, body, method, headers) => {
      if (path === "/agent/native/window") {
        assert.deepEqual(body, { ticket: "fresh-native-ticket" });
        return { window_proof: "private-window-proof" };
      }
      assert.equal(headers?.["X-Cage-Agent-Window"], "private-window-proof");
      return dependencies.api(path, body, method, headers);
    },
  });
}

test("client preference writes use the window's remote instance and preserve original string encodings", () => {
  const state = storage();
  state.setItem("unrelated-token", "keep-private");
  state.setItem("cage-sidebar-collapsed:remote:server-two", "false");
  const values = {
    "cage-sidebar-collapsed": "true",
    "cage-browser-tab-layout": '"vertical"',
    "cage-keyboard": "native",
    "cage.audio": '{"muted":false,"volume":0.25}',
    "cage-desktop-zoom": "1.25",
    "cage-tab-layout": '{"/workspace":{"order":["browser-1"],"sessionOrder":{"/project":["session-1"]},"browser-1":{"title":"Saved title","pinned":true}}}',
  };
  const result = clientPreferences({ operation: "set", values }, context, state);
  assert.equal(result.reload_required, true);
  assert.equal(state.getItem("cage-sidebar-collapsed:remote:server-one"), "true");
  assert.equal(state.getItem("cage-sidebar-collapsed:remote:server-two"), "false");
  assert.equal(state.getItem("cage-browser-tab-layout:remote:server-one"), '"vertical"');
  assert.equal(state.getItem("cage.audio"), values["cage.audio"]);
  assert.deepEqual(result.values["cage-keyboard"], "native");
  assert.ok(!JSON.stringify(result).includes("keep-private"));
  assert.ok(!JSON.stringify(result).includes("unrelated-token"));
  assert.equal(result.storage_keys["cage-keyboard"], "cage-keyboard:remote:server-one");
  assert.ok(agentClientPreferences.length >= 8);
});

test("invalid or server-owned preference patches do not change any earlier field", () => {
  const state = storage();
  for (const values of [
    { "cage-sidebar-collapsed": "true", "unknown": "secret" },
    { "cage-sidebar-collapsed": "true", "cage.audio": '{"volume":5,"muted":false}' },
    { "cage-appearance": "{}" },
    { "cage-tab-layout": '{"project":{"order":{}}}' },
    { "cage-browser-tab-layout": "vertical" },
  ]) assert.throws(() => clientPreferences({ operation: "set", values }, context, state));
  assert.equal(state.values.size, 0);
  assert.throws(() => clientPreferences({ operation: "set", values: { "cage-keyboard": "mac" } }, { mode: "local" }, state), /settings.keyboard/);
});

test("partial storage failure restores entries already changed in the patch", () => {
  const state = storage();
  state.setItem("cage.audio", '{"volume":1,"muted":false}');
  const original = state.setItem;
  state.setItem = (key, value) => { if (key === "cage-desktop-zoom") throw new Error("full"); original(key, value); };
  assert.throws(() => clientPreferences({ operation: "set", values: { "cage.audio": '{"volume":0,"muted":true}', "cage-desktop-zoom": "1.5" } }, context, state), /full/);
  assert.equal(state.getItem("cage.audio"), '{"volume":1,"muted":false}');
});

test("a late native result is rejected and a request for an old generation never invokes the bridge", async () => {
  let current = context, release, calls = 0;
  const dependencies = { storage: storage(), getContext: () => current, native: () => { calls++; return new Promise(resolve => { release = resolve; }); } };
  await assert.rejects(executeAgentNative({ connection_id: "old", request: { action: "chooseFolder" } }, dependencies), { code: "connection_changed" });
  assert.equal(calls, 0);
  const pending = executeAgentNative({ connection_id: context.connection_id, request: { action: "accountDevice", operation: "unlock", kind: "biometric", connection_id: "malicious-override" } }, dependencies);
  current = { ...context, connection_id: "generation-b" };
  release({ unlocked: true });
  await assert.rejects(pending, { code: "connection_changed" });
  assert.equal(calls, 1);
});

test("native operations retain their production checks and never return the refresh ticket", async () => {
  let received;
  const deps = { storage: storage(), getContext: () => context, native: async (action, input) => { received = { action, input }; return { url: "http://localhost:17777/#private-ticket" }; } };
  const result = await executeAgentNative({ connection_id: context.connection_id, request: { action: "refreshSession", connection_id: "override" } }, deps);
  assert.deepEqual(result, { refreshed: true });
  assert.deepEqual(received, { action: "refreshSession", input: {} });
  deps.native = async () => { throw new Error("macOS 要求 Touch ID"); };
  await assert.rejects(executeAgentNative({ connection_id: context.connection_id, request: { action: "accountDevice", operation: "unlock" } }, deps), /Touch ID/);
});

test("lost completion response retries the receipt without repeating a native action", async () => {
  const timers = clock(); let actions = 0, claims = 0, completes = 0;
  const consumer = consumerForTest({
    ...timers, getContext: () => context, storage: storage(), reload: () => {}, nativeAvailable: () => true,
    native: async () => { actions++; return { ok: true }; },
    api: async path => {
      if (path.includes("consumer=window")) { claims++; return claims === 1 ? { id: "request-1", connection_id: context.connection_id, request: { action: "clipboardRead" } } : {}; }
      completes++; if (completes === 1) throw new Error("lost acknowledgement"); return { ok: true };
    },
  });
  consumer.start(); await flush();
  assert.equal(actions, 1); assert.equal(completes, 1);
  await timers.tick();
  assert.equal(actions, 1); assert.equal(completes, 2); assert.equal(claims, 1);
  consumer.stop(); assert.equal(timers.scheduled.length, 0);
});

test("preference reload follows a successful receipt and never a lost one", async () => {
  const timers = clock(); let completes = 0, reloads = 0;
  const consumer = consumerForTest({
    ...timers, getContext: () => context, storage: storage(), reload: () => { reloads++; }, nativeAvailable: () => true,
    native: async () => { throw new Error("must use client preference handler"); },
    api: async path => {
      if (path.includes("consumer=window")) return { id: "request-1", connection_id: context.connection_id, request: { action: "clientPreferences", operation: "set", values: { "cage-sidebar-collapsed": "true" } } };
      completes++; if (completes === 1) throw new Error("lost"); return {};
    },
  });
  consumer.start(); await flush(); assert.equal(reloads, 0);
  await timers.tick(); assert.equal(reloads, 1); assert.equal(timers.scheduled.length, 0);
});

test("two desktop consumers execute a claimed operation once and stopping cancels future polling", async () => {
  const pending = [{ id: "request-1", connection_id: context.connection_id, request: { action: "chooseFolder" } }];
  let actions = 0, polls = 0;
  const consumers = [];
  const clocks = [];
  for (let i = 0; i < 2; i++) {
    const timers = clock(); clocks.push(timers);
    consumers.push(consumerForTest({ ...timers, getContext: () => context, storage: storage(), reload: () => {}, nativeAvailable: () => true,
      native: async () => { actions++; return "/chosen/project"; },
      api: async path => { if (path.includes("consumer=window")) { polls++; return pending.shift() || {}; } return {}; },
    }));
  }
  consumers.forEach(consumer => consumer.start()); await flush();
  assert.equal(actions, 1); assert.equal(polls, 2);
  consumers.forEach(consumer => consumer.stop());
  for (const timers of clocks) { await timers.tick(); assert.equal(timers.scheduled.length, 0); }
  assert.equal(polls, 2);
});

test("a window stopped while waiting for a claim reports non-execution", async () => {
  const timers = clock(); let release, actions = 0, body;
  const consumer = consumerForTest({ ...timers, getContext: () => context, storage: storage(), reload: () => {}, nativeAvailable: () => true,
    native: async () => { actions++; },
    api: async (path, value) => { if (path.includes("consumer=window")) return new Promise(resolve => { release = resolve; }); body = value; return {}; },
  });
  consumer.start(); await flush(); consumer.stop(); release({ id: "request-1", connection_id: context.connection_id, request: { action: "chooseFolder" } }); await flush();
  assert.equal(actions, 0); assert.match(body.error, /未执行/); assert.equal(timers.scheduled.length, 0);
});

test("a native registration ticket never goes to a changed origin or starts an action", async () => {
  const timers = clock(); let requests = 0, actions = 0;
  const consumer = createAgentNativeConsumer({ ...timers, getOrigin: () => "http://localhost:17777", getContext: () => context, storage: storage(), reload: () => {}, nativeAvailable: () => true,
    native: async action => { if (action === "refreshSession") return { url: "http://attacker.invalid/#secret-ticket" }; actions++; },
    api: async () => { requests++; },
  });
  consumer.start(); await flush(); consumer.stop();
  assert.equal(requests, 0); assert.equal(actions, 0);
});

test("expired window proof registers anew and retries only the original completion", async () => {
  const timers = clock(); let registrations = 0, actions = 0, claims = 0, completes = 0;
  const receipts = [];
  const consumer = createAgentNativeConsumer({ ...timers, getOrigin: () => "http://localhost:17777", getContext: () => context, storage: storage(), reload: () => {}, nativeAvailable: () => true,
    native: async action => action === "refreshSession" ? { url: `http://localhost:17777/#ticket-${registrations + 1}` } : (actions++, { ok: true }),
    api: async (path, body, method, headers) => {
      if (path === "/agent/native/window") { registrations++; assert.equal(body.ticket, `ticket-${registrations}`); return { window_proof: `proof-${registrations}` }; }
      assert.equal(headers["X-Cage-Agent-Window"], `proof-${registrations}`);
      if (path.includes("consumer=window")) { claims++; return { id: "original", connection_id: context.connection_id, request: { action: "chooseFolder" } }; }
      completes++; receipts.push(body);
      if (completes === 1) { const error = new Error("expired"); error.status = 403; throw error; }
      return {};
    },
  });
  consumer.start(); await flush();
  assert.equal(actions, 1); assert.equal(completes, 1); assert.equal(registrations, 1);
  await timers.tick(); consumer.stop();
  assert.equal(registrations, 2); assert.equal(claims, 1); assert.equal(actions, 1); assert.equal(completes, 2);
  assert.deepEqual(receipts[0], receipts[1]);
  assert.ok(!JSON.stringify(receipts).includes("proof-"));
  assert.ok(!JSON.stringify(receipts).includes("ticket-"));
});

test("independent desktop consumers retain distinct private window proofs", async () => {
  const proofs = new Set(); const consumers = []; let registrations = 0;
  for (let i = 0; i < 2; i++) {
    const timers = clock();
    const consumer = createAgentNativeConsumer({ ...timers, getOrigin: () => "http://localhost:17777", getContext: () => context, storage: storage(), reload: () => {}, nativeAvailable: () => true,
      native: async () => ({ url: `http://localhost:17777/#window-${i}` }),
      api: async (path, body, method, headers) => {
        if (path === "/agent/native/window") { registrations++; return { window_proof: body.ticket + "-proof" }; }
        proofs.add(headers["X-Cage-Agent-Window"]); return {};
      },
    });
    consumers.push(consumer); consumer.start();
  }
  await flush(); consumers.forEach(consumer => consumer.stop());
  assert.equal(registrations, 2); assert.equal(proofs.size, 2);
});
