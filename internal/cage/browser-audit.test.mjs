import test from "node:test";
import assert from "node:assert/strict";
import { auditBrowser } from "./browser-audit.mjs";

function browser(failMethod, failStage = "network") {
  const contexts = new Set(), excluded = new Set(), pages = new Map(), calls = [];
  let unsafeDisposal = false, stage = "settings";
  const observation = { timezone: "UTC", language: "ja-JP", platform: "Linux" };
  const cdp = async (method, params, sessionId) => {
    if (method === "Target.createBrowserContext") stage = "network";
    calls.push(method);
    if (method === failMethod && stage === failStage && !params?.expression?.includes("document.readyState"))
      throw new Error("injected failure");
    switch (method) {
      case "Target.createBrowserContext":
        contexts.add("audit-context");
        return { browserContextId: "audit-context" };
      case "Target.createTarget": {
        assert.equal(params.hidden, params.browserContextId ? true : undefined);
        assert.equal(params.background, true);
        assert.ok(excluded.has(params.browserContextId || params.url));
        const targetId = params.browserContextId ? "network-page" : "settings-page";
        pages.set(targetId, params.browserContextId || params.url);
        return { targetId };
      }
      case "Target.attachToTarget":
        return { sessionId: params.targetId + "-session" };
      case "Page.navigate":
        assert.equal(sessionId, "network-page-session", "only isolated pages navigate");
        return {};
      case "Runtime.evaluate":
        if (params.expression.includes("document.readyState")) return { result: { value: true } };
        if (sessionId === "network-page-session") {
          assert.ok(!params.expression.includes("navigator.language"));
          return { result: { value: { network: { status: 200 } } } };
        }
        assert.equal(sessionId, "settings-page-session");
        assert.ok(!params.expression.includes("document.cookie"));
        return { result: { value: observation } };
      case "Target.closeTarget":
        // Context disposal must wait for the page close to complete.
        await new Promise(resolve => setImmediate(resolve));
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
        throw new Error("Unexpected command: " + method);
    }
  };
  return { cdp, excluded, contexts, pages, observation, calls, unsafe: () => unsafeDisposal };
}

test("successful audits close hidden pages before disposing network contexts", async () => {
  for (let i = 0; i < 3; i++) {
    const b = browser();
    assert.deepEqual(await auditBrowser(b.cdp, b.excluded, true, "UTC"), {
      ...b.observation, network: { status: 200 },
    });
    assert.equal(b.unsafe(), false);
    assert.equal(b.pages.size, 0);
    assert.equal(b.contexts.size, 0);
    assert.equal(b.excluded.size, 0);
  }
});

for (const method of ["Target.createTarget", "Target.attachToTarget", "Runtime.evaluate"]) {
  for (const stage of ["settings", "network"]) {
    test(method + " failure in " + stage + " releases its audit resources", async () => {
      const b = browser(method, stage);
      await assert.rejects(auditBrowser(b.cdp, b.excluded, true, "UTC"), /injected failure/);
      assert.equal(b.unsafe(), false);
      assert.equal(b.pages.size, 0);
      assert.equal(b.contexts.size, 0);
      assert.equal(b.excluded.size, 0);
    });
  }
}

test("a failed network page close preserves its context and keeps it excluded", async () => {
  const b = browser("Target.closeTarget");
  await auditBrowser(b.cdp, b.excluded, true, "UTC");
  assert.equal(b.unsafe(), false);
  assert.equal(b.pages.size, 1);
  assert.equal(b.contexts.size, 1);
  assert.equal(b.excluded.size, 1);
});

test("settings-only monitoring reads the normal profile without a network context", async () => {
  const b = browser();
  assert.deepEqual(await auditBrowser(b.cdp, b.excluded, false, "UTC"), b.observation);
  assert.equal(b.calls.includes("Target.createBrowserContext"), false);
  assert.equal(b.calls.includes("Page.navigate"), false);
  assert.equal(b.pages.size, 0);
  assert.equal(b.excluded.size, 0);
});

test("a settings page that cannot close stays excluded", async () => {
  const b = browser("Target.closeTarget", "settings");
  await auditBrowser(b.cdp, b.excluded, false, "UTC");
  assert.equal(b.pages.size, 1);
  assert.ok(b.excluded.has(b.pages.get("settings-page")));
});
