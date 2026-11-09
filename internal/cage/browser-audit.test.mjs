import test from "node:test";
import assert from "node:assert/strict";
import { auditBrowser } from "./browser-audit.mjs";

function browser(failMethod) {
  const contexts = new Set();
  const excluded = new Set();
  const pages = new Map();
  let unsafeDisposal = false;
  const observation = { timezone: "UTC", language: "en-US", platform: "Linux" };
  const cdp = async (method, params) => {
    if (method === failMethod) throw new Error("injected failure");
    switch (method) {
      case "Target.createBrowserContext":
        contexts.add("audit-context");
        return { browserContextId: "audit-context" };
      case "Target.createTarget":
        assert.equal(params.hidden, true);
        assert.ok(excluded.has(params.browserContextId));
        pages.set("audit-page", params.browserContextId);
        return { targetId: "audit-page" };
      case "Target.attachToTarget":
        return { sessionId: "audit-session" };
      case "Runtime.evaluate":
        return { result: { value: observation } };
      case "Target.closeTarget":
        // Disposal must await the close, not merely send both commands.
        await new Promise((resolve) => setImmediate(resolve));
        pages.delete(params.targetId);
        return { success: true };
      case "Target.disposeBrowserContext":
        if ([...pages.values()].includes(params.browserContextId)) {
          unsafeDisposal = true;
          throw new Error("live hidden page references this context");
        }
        contexts.delete(params.browserContextId);
        return {};
      default:
        throw new Error(`Unexpected command: ${method}`);
    }
  };
  return {
    cdp,
    excluded,
    contexts,
    pages,
    observation,
    unsafe: () => unsafeDisposal,
  };
}

test("successful audits close their hidden pages before disposing contexts", async () => {
  const b = browser();
  for (let i = 0; i < 3; i++) {
    assert.deepEqual(
      await auditBrowser(b.cdp, b.excluded, false, "UTC"),
      b.observation,
    );
    assert.equal(b.unsafe(), false);
    assert.equal(b.pages.size, 0);
    assert.equal(b.contexts.size, 0);
    assert.equal(b.excluded.size, 0);
  }
});

for (const method of [
  "Target.createTarget",
  "Target.attachToTarget",
  "Runtime.evaluate",
]) {
  test(`${method} failure still releases the audit resources safely`, async () => {
    const b = browser(method);
    await assert.rejects(
      auditBrowser(b.cdp, b.excluded, false, "UTC"),
      /injected failure/,
    );
    assert.equal(b.unsafe(), false);
    assert.equal(b.pages.size, 0);
    assert.equal(b.contexts.size, 0);
    assert.equal(b.excluded.size, 0);
  });
}

test("a failed page close preserves its context and keeps it out of user tabs", async () => {
  const b = browser("Target.closeTarget");
  await auditBrowser(b.cdp, b.excluded, false, "UTC");
  assert.equal(b.unsafe(), false);
  assert.equal(b.pages.size, 1);
  assert.equal(b.contexts.size, 1);
  assert.equal(b.excluded.size, 1);
});
