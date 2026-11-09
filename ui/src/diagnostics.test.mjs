import test from "node:test";
import assert from "node:assert/strict";

test("reports retry after local controller failure, bound payloads, and omit arbitrary objects", async (t) => {
  const saved = { fetch: globalThis.fetch, setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout };
  t.after(() => Object.assign(globalThis, saved));
  const attempts = [];
  let retry;
  let online = false;
  globalThis.setTimeout = (fn) => { retry = fn; return { unref() {} }; };
  globalThis.clearTimeout = () => {};
  globalThis.fetch = async (path, options) => {
    assert.equal(path, "/api/diagnostics/events");
    attempts.push(JSON.parse(options.body));
    if (!online) throw new Error("controller offline");
    return { ok: true };
  };
  const { reportDiagnostic } = await import("./diagnostics.mjs?report-test");
  reportDiagnostic("frontend", new Error("failure " + "x".repeat(20_000)));
  await Promise.resolve(); await Promise.resolve();
  assert.ok(retry, "failed reports were lost");
  assert.ok(attempts[0].message.length <= 4096);
  online = true;
  await retry();
  reportDiagnostic("frontend", { password: "never-serialize-objects" });
  await Promise.resolve(); await Promise.resolve();
  assert.equal(attempts.length, 3);
  assert.equal(attempts[0].message, attempts[1].message);
  assert.ok(!attempts[2].message.includes("never-serialize-objects"));
  for (let n = 0; n < 50; n++) reportDiagnostic("frontend", "repeated");
  for (let n = 0; n < 30; n++) await Promise.resolve();
  assert.equal(attempts.length, 21, "client rate limit must bound accepted events to 20 per minute");
});
