import test from "node:test";
import assert from "node:assert/strict";
import { normalizeURL } from "./api.js";
test("address input retains loopback inside guest and rejects active schemes", () => {
  assert.equal(normalizeURL("localhost:3000"), "http://localhost:3000/");
  assert.equal(
    normalizeURL("127.0.0.1:18881/test"),
    "http://127.0.0.1:18881/test",
  );
  assert.equal(normalizeURL("example.com"), "https://example.com/");
  assert.equal(
    normalizeURL("hello cage"),
    "https://www.google.com/search?q=hello%20cage",
  );
  assert.throws(() => normalizeURL("javascript:alert(1)"));
  assert.throws(() => normalizeURL("file:///etc/passwd"));
});

import { api } from "./api.js";
function nativeRuntime(t, fetcher, bridge) {
  const saved = {
    fetch: globalThis.fetch,
    window: globalThis.window,
    location: globalThis.location,
  };
  globalThis.fetch = fetcher;
  globalThis.window = {
    webkit: { messageHandlers: { cage: { postMessage: bridge } } },
  };
  globalThis.location = new URL("http://localhost:17777/");
  t.after(() => {
    for (const [key, value] of Object.entries(saved)) {
      if (value === undefined) delete globalThis[key];
      else globalThis[key] = value;
    }
  });
}
const json = (data, status = 200) =>
  new Response(JSON.stringify(data), { status });
test("expired concurrent requests renew once and accept the rejected stop exactly once", async (t) => {
  let authorized = false,
    renewals = 0,
    stops = 0,
    exchanges = 0,
    release;
  const entry = new Promise((resolve) => {
    release = resolve;
  });
  nativeRuntime(
    t,
    async (path, options) => {
      if (path === "/api/session") {
        assert.equal(JSON.parse(options.body).token, "one-use-test-ticket");
        authorized = true;
        exchanges++;
        return json({});
      }
      if (!authorized) return new Response("expired", { status: 401 });
      if (path === "/api/stop") {
        stops++;
        return json({ id: "requested-stop" });
      }
      return json({ vm_running: true });
    },
    async ({ action }) => {
      assert.equal(action, "refreshSession");
      renewals++;
      return entry;
    },
  );
  const requests = Promise.all([api("/stop", {}), api("/status")]);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(renewals, 1);
  release({ url: "http://localhost:17777/#one-use-test-ticket" });
  const [stop] = await requests;
  assert.equal(stop.id, "requested-stop");
  assert.equal(stops, 1);
  assert.equal(exchanges, 1);
});
test("non-authentication failures never replay a mutation", async (t) => {
  let calls = 0;
  nativeRuntime(
    t,
    async () => {
      calls++;
      return new Response("operation failed", { status: 500 });
    },
    () => {
      throw Error("unexpected renewal");
    },
  );
  await assert.rejects(api("/stop", {}), { status: 500 });
  assert.equal(calls, 1);
});
test("a rejected renewed session has a bounded retry", async (t) => {
  let attempts = 0,
    renewals = 0;
  nativeRuntime(
    t,
    async (path) => {
      if (path === "/api/session") return json({});
      attempts++;
      return new Response("expired", { status: 401 });
    },
    async () => {
      renewals++;
      return { url: "http://localhost:17777/#test-ticket" };
    },
  );
  await assert.rejects(api("/stop", {}), { status: 401 });
  assert.equal(attempts, 2);
  assert.equal(renewals, 1);
});
test("a new control address reconnects without sending a ticket to the old origin", async (t) => {
  const actions = [];
  nativeRuntime(
    t,
    async (path) => {
      assert.equal(path, "/api/stop");
      return new Response("expired", { status: 401 });
    },
    async ({ action }) => {
      actions.push(action);
      return { url: "http://localhost:17778/#test-ticket" };
    },
  );
  await assert.rejects(api("/stop", {}), /地址已更新/);
  assert.deepEqual(actions, ["refreshSession", "reconnect"]);
});
