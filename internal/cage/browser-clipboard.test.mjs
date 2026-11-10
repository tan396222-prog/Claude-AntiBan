import test from "node:test";
import assert from "node:assert/strict";
import { BrowserClipboard } from "./browser-clipboard.mjs";

test("clipboard events require a live default context and private binding token", async () => {
  const copies = [];
  const observer = new BrowserClipboard(
    async () => ({}),
    (value) => copies.push(value),
  );
  await observer.attach("session", "tab");
  const state = observer.sessions.get("session");
  const event = (method, params) =>
    observer.event({ sessionId: "session", method, params });
  const copied = (
    text,
    token = state.token,
    executionContextId = 3,
    context = "context-a",
  ) =>
    event("Runtime.bindingCalled", {
      name: state.name,
      executionContextId,
      payload: JSON.stringify({ token, text, context }),
    });
  copied("before context");
  event("Runtime.executionContextCreated", {
    context: { id: 3, auxData: { isDefault: true, frameId: "frame" } },
  });
  event("Runtime.executionContextCreated", {
    context: { id: 4, auxData: { isDefault: false, frameId: "frame" } },
  });
  copied("forged", "wrong token");
  copied("isolated", state.token, 4);
  copied("x".repeat(65537));
  copied("invalid context", state.token, 3, null);
  copied("invalid context", state.token, 3, "x".repeat(65));
  copied("exact🙂");
  assert.deepEqual(copies, [
    { id: "tab", text: "exact🙂", context: "context-a" },
  ]);
  event("Page.frameNavigated", { frame: { id: "frame" } });
  copied("old document");
  event("Runtime.executionContextCreated", {
    context: { id: 5, auxData: { isDefault: true, frameId: "frame" } },
  });
  copied("new document", state.token, 5);
  observer.detachTab("tab");
  copied("closed tab", state.token, 5);
  assert.deepEqual(
    copies.map((value) => value.text),
    ["exact🙂", "new document"],
  );
});

test("host copy context reaches every existing frame and newly attached child", async () => {
  const calls = [];
  const observer = new BrowserClipboard(
    async (method, params, sessionId) => {
      calls.push({ method, params, sessionId });
      if (method === "Runtime.enable" && sessionId === "child")
        observer.event({
          sessionId,
          method: "Runtime.executionContextCreated",
          params: {
            context: {
              id: 3,
              auxData: { isDefault: true, frameId: "cross-process-frame" },
            },
          },
        });
      return {};
    },
    () => {},
  );
  await observer.attach("root", "tab");
  observer.event({
    sessionId: "root",
    method: "Runtime.executionContextCreated",
    params: {
      context: { id: 1, auxData: { isDefault: true, frameId: "root-frame" } },
    },
  });
  await observer.setContext("tab", "context-a");
  observer.sessions.get("root").contexts.set(2, "same-process-frame");
  await observer.attach("child", "tab");
  assert.ok(
    calls.some(
      (call) =>
        call.sessionId === "child" &&
        call.method === "Runtime.evaluate" &&
        call.params.expression.includes('"context-a"'),
    ),
  );
  const before = calls.length;
  await observer.setContext("tab", "context-b");
  const updates = calls
    .slice(before)
    .filter((call) => call.method === "Runtime.evaluate");
  assert.deepEqual(
    updates.map((call) => [call.sessionId, call.params.contextId]),
    [
      ["root", 1],
      ["root", 2],
      ["child", 3],
    ],
  );
  assert.ok(
    updates.every((call) => call.params.expression.includes('"context-b"')),
  );
  observer.detachTab("tab");
  assert.equal(observer.copyContexts.has("tab"), false);
  assert.equal(observer.sessions.size, 0);
});

test("private clipboard format notifications require an enabled receiver and the binding token", async () => {
  for (const includeChanges of [false, true]) {
    const copies = [];
    const observer = new BrowserClipboard(async () => ({}), value => copies.push(value), { includeChanges });
    await observer.attach("session", "tab");
    const state = observer.sessions.get("session");
    observer.event({ sessionId: "session", method: "Runtime.executionContextCreated", params: { context: { id: 1, auxData: { isDefault: true, frameId: "frame" } } } });
    for (const token of ["wrong", state.token]) observer.event({ sessionId: "session", method: "Runtime.bindingCalled", params: {
      name: state.name, executionContextId: 1, payload: JSON.stringify({ token, context: "gesture", changed: true }),
    } });
    assert.deepEqual(copies, includeChanges ? [{ id: "tab", context: "gesture", changed: true }] : []);
  }
});

test("failed setup removes the session and allows retry", async () => {
  let fail = true;
  const observer = new BrowserClipboard(
    async () => {
      if (fail) throw new Error("CDP unavailable");
      return {};
    },
    () => {},
  );
  await assert.rejects(observer.attach("s", "t"), /CDP unavailable/);
  assert.equal(observer.sessions.size, 0);
  fail = false;
  assert.deepEqual(await observer.attach("s", "t"), { skipped: [] });
});

test("copy context waits for pending frame installation and frames discovered during the wait", async () => {
  const calls = [];
  const gates = new Map();
  for (const id of ["child", "nested"]) {
    let release, entered;
    const pending = new Promise((resolve) => {
      release = resolve;
    });
    const reached = new Promise((resolve) => {
      entered = resolve;
    });
    gates.set(id, { pending, reached, release, entered });
  }
  const observer = new BrowserClipboard(
    async (method, params, sessionId) => {
      calls.push({ method, params, sessionId });
      if (method === "Runtime.enable")
        observer.event({
          sessionId,
          method: "Runtime.executionContextCreated",
          params: {
            context: {
              id: 1,
              auxData: { isDefault: true, frameId: sessionId + "-frame" },
            },
          },
        });
      if (
        method === "Page.addScriptToEvaluateOnNewDocument" &&
        gates.has(sessionId)
      ) {
        gates.get(sessionId).entered();
        await gates.get(sessionId).pending;
      }
      return {};
    },
    () => {},
  );
  await observer.attach("root", "tab");
  const child = observer.attach("child", "tab");
  await gates.get("child").reached;
  let completed = false;
  const update = observer.setContext("tab", "context-a").then(() => {
    completed = true;
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(completed, false, "the handshake must wait for the child hook");
  assert.equal(
    calls.some(
      (call) =>
        call.sessionId === "child" && call.method === "Runtime.evaluate",
    ),
    false,
  );
  const nested = observer.attach("nested", "tab");
  await gates.get("nested").reached;
  gates.get("child").release();
  await child;
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(
    completed,
    false,
    "the handshake must include a newly discovered frame",
  );
  gates.get("nested").release();
  await Promise.all([nested, update]);
  assert.equal(completed, true);
  for (const id of ["child", "nested"])
    assert.ok(
      calls.some(
        (call) =>
          call.sessionId === id &&
          call.method === "Runtime.evaluate" &&
          call.params.expression.includes('"context-a"'),
      ),
    );
});
