import assert from "node:assert/strict";
import test from "node:test";
import { LoginBrowser } from "./browser-login.mjs";

test("login redirects and popups stay private, closing one login leaves the other open", async () => {
  const closed = [];
  let contexts = 0;
  const browser = new LoginBrowser(async (method, params) => {
    if (method === "Target.createBrowserContext") return { browserContextId: String(++contexts) };
    if (method === "Target.createTarget") return { targetId: "tab-" + params.browserContextId };
    if (method === "Target.disposeBrowserContext") closed.push(params.browserContextId);
  });
  const page = { url: "https://claude.ai/oauth/authorize?state=secret", title: "secret" };
  assert.equal(browser.tab(page), page);
  for (const id of ["a", "b"]) {
    await browser.open(id.repeat(24), page.url, async tab => {
      assert.equal(browser.tab(tab).url, "https://claude.ai");
    });
  }
  const popup = { ...page, targetId: "popup", browserContextId: "1", url: "http://localhost:9123/callback?code=secret" };
  const privateTab = browser.tab(popup);
  assert.equal(privateTab.title, "账号授权");
  assert.equal(privateTab.url, "http://localhost:9123");
  await browser.finish("a".repeat(24));
  assert.deepEqual(closed, ["1"]);
  assert.equal(browser.tab(popup, privateTab).url, privateTab.url);
  await browser.finish("b".repeat(24));
  assert.deepEqual(closed, ["1", "2"]);
});
