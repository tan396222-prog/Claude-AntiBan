import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const epoch = new Date("2026-11-07T14:00:00Z");
for (const platform of ["darwin", "windows", "linux"]) {
  const calls = [];
  let update = platform === "linux" ? { phase: "manual", message: "使用系统包更新" } : {
    phase: "failed", message: "更新源请求受限，请稍后重试", currentVersion: "0.1.0", latestVersion: "0.2.0",
    retryable: true, retryAt: new Date(+epoch + 60*60*1000).toISOString(),
  };
  const fixture = await createWorkbench({
    clientContext: { mode: "local", connection_id: "local", client_platform: platform, server_platform: platform, connection: { state: "disconnected" } },
    beforeNavigate: page => page.clock.install({ time: epoch }),
    nativeRequest: async body => {
      if (body.action !== "appUpdate") return null;
      calls.push(body.operation);
      if (body.operation === "check") update = { phase: "current", currentVersion: "0.1.0", retryAt: "" };
      return update;
    },
  });
  const { page, browser, errors } = fixture;
  try {
    await page.waitForFunction(() => document.querySelector(".app-update-button") || !document.querySelector(".loading-screen"));
    if (platform === "linux") {
      await page.clock.fastForward(7*60*60*1000);
      assert.deepEqual(calls, ["status"], "Linux must keep system package updates");
    } else {
      await page.getByRole("button", { name: "等待重试更新", exact: true }).waitFor();
      assert.ok(await page.getByRole("button", { name: "等待重试更新", exact: true }).isDisabled());
      assert.deepEqual(calls, ["status"], "startup must respect the native retry deadline");
      await page.clock.fastForward(6*60*1000);
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
      assert.deepEqual(calls, ["status"], "the five-minute timer and focus must not bypass cooldown");
      await page.clock.fastForward(53*60*1000);
      assert.deepEqual(calls, ["status"], "must not retry before the server deadline");
      await page.clock.fastForward(2*60*1000);
      await page.waitForFunction(() => !document.querySelector(".app-update-button"));
      assert.deepEqual(calls, ["status", "check"], "retry once when the server deadline passes");
    }
    assert.deepEqual(errors, []);
    console.log(`PASS: ${platform} update cooldown`);
  } finally { await browser.close(); }
}
