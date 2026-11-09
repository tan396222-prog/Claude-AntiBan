import test from "node:test";
import assert from "node:assert/strict";
import { api, native } from "./api.js";
import { clientKeyboard, clientStorageKey, runtimeURL, setClientContext } from "./client-context.mjs";

function environment(t) {
  const saved = { fetch: globalThis.fetch, window: globalThis.window, location: globalThis.location };
  globalThis.location = new URL("http://localhost:17777/");
  globalThis.window = { webkit: { messageHandlers: { cage: { postMessage: async () => true } } } };
  setClientContext({ mode: "remote", connection_id: "first", instance_id: "instance-one" });
  t.after(() => {
    setClientContext({ mode: "local", connection_id: "local" });
    for (const [key, value] of Object.entries(saved)) {
      if (value === undefined) delete globalThis[key]; else globalThis[key] = value;
    }
  });
}

test("an old API result cannot continue an action in a new remote environment", async (t) => {
  environment(t);
  let release;
  globalThis.fetch = (path, options) => {
    assert.equal(path, "/api/input/files/create");
    assert.equal(options.headers["X-Cage-Connection"], "first");
    return new Promise((resolve) => { release = resolve; });
  };
  const pending = api("/input/files/create", { target: "browser" });
  setClientContext({ mode: "remote", connection_id: "second", instance_id: "instance-two" });
  release(new Response(JSON.stringify({ id: "old-upload" })));
  await assert.rejects(pending, (error) => error.code === "connection_changed");
});

test("a native picker finishing after a host switch cannot yield an upload handle", async (t) => {
  environment(t);
  let release;
  window.webkit.messageHandlers.cage.postMessage = (request) => {
    assert.equal(request.connection_id, "first");
    return new Promise((resolve) => { release = resolve; });
  };
  const pending = native("chooseUploadFiles", { multiple: true });
  setClientContext({ mode: "remote", connection_id: "second" });
  release({ fileSelection: "old-file-handle", count: 1 });
  await assert.rejects(pending, (error) => error.code === "connection_changed");
});

test("quitting a remote client never suspends the cloud account", async (t) => {
  environment(t);
  globalThis.fetch = () => { throw new Error("quit must not call a runtime API"); };
  let request;
  window.webkit.messageHandlers.cage.postMessage = async (value) => { request = value; return true; };
  await native("quitNow");
  assert.deepEqual(request, { action: "quitNow", connection_id: "first" });
});

test("an account device operation retains its host generation and rejects a late result", async (t) => {
  environment(t);
  let release;
  window.webkit.messageHandlers.cage.postMessage = (request) => {
    assert.deepEqual(request, { action: "accountDevice", operation: "unlock", kind: "automatic", connection_id: "first" });
    return new Promise((resolve) => { release = resolve; });
  };
  const pending = native("accountDevice", { operation: "unlock", kind: "automatic" });
  setClientContext({ mode: "remote", connection_id: "second" });
  release({ unlocked: true });
  await assert.rejects(pending, (error) => error.code === "connection_changed");
});

test("stream URLs carry the generation while layout follows the persistent instance", (t) => {
  environment(t);
  assert.equal(runtimeURL("/browser/events?after=1"), "/api/browser/events?after=1&connection=first");
  const key = clientStorageKey("cage-tab-layout");
  setClientContext({ mode: "remote", connection_id: "reconnected", instance_id: "instance-one" });
  assert.equal(clientStorageKey("cage-tab-layout"), key);
  setClientContext({ mode: "remote", connection_id: "other", instance_id: "instance-two" });
  assert.notEqual(clientStorageKey("cage-tab-layout"), key);
});

test("remote keyboard defaults follow the client and a saved choice does not alter server settings", (t) => {
  environment(t);
  const previous = globalThis.localStorage;
  const values = new Map();
  globalThis.localStorage = { getItem: (key) => values.get(key) ?? null };
  t.after(() => { if (previous === undefined) delete globalThis.localStorage; else globalThis.localStorage = previous; });
  const mac = { mode: "remote", client_platform: "darwin", instance_id: "linux-server" };
  const windows = { mode: "remote", client_platform: "windows", instance_id: "linux-server" };
  assert.equal(clientKeyboard("native", mac), "mac");
  assert.equal(clientKeyboard("mac", windows), "native");
  values.set(clientStorageKey("cage-keyboard", mac), "native");
  assert.equal(clientKeyboard("mac", mac), "native");
  assert.equal(clientKeyboard("native", { ...mac, instance_id: "other-server" }), "mac");
  assert.equal(clientKeyboard("native", { mode: "local", client_platform: "darwin" }), "native");
});
