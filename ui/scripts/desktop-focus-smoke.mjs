// Isolated UI test: notifications use the production SSE consumer, without
// accessing the user's browser, login URL, or running desktop application.
import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const { browser, page, state, errors } = await createWorkbench();
try {
  await page.addInitScript(() => {
    window.EventSource = class extends EventTarget {
      constructor() {
        super();
        window.testBrowserEvents = this;
        queueMicrotask(() => this.onopen?.());
      }
      close() { if (window.testBrowserEvents === this) window.testBrowserEvents = null; }
    };
  });
  await page.route("**/api/claude-desktop", (route) => route.fulfill({ json: {
    installed: true, running: true, failed: false, managed: true, version: "fixture",
  } }));
  state.status.vm_running = true;
  state.status.ready = true;
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await page.waitForFunction(() => Boolean(window.testBrowserEvents?.onmessage));
  const notify = (desktopFocus) => page.evaluate((desktopFocus) => {
    window.testBrowserEvents.onmessage({ data: JSON.stringify({
      tabs: [], active: "", downloads: [], uploads: [], desktopFocus,
    }) });
  }, desktopFocus);
  await notify(true);
  await page.getByRole("region", { name: "Claude Desktop", exact: true }).waitFor();
  await page.getByRole("button", { name: "关闭应用", exact: true }).waitFor();
  assert.equal(state.requests.some((r) => r.path === "/claude-desktop/start"), false);
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await notify(false);
  await page.getByRole("region", { name: "Claude Desktop", exact: true }).waitFor({ state: "hidden" });
  await notify(true);
  await page.getByRole("region", { name: "Claude Desktop", exact: true }).waitFor();
  assert.deepEqual(errors, []);
  console.log("PASS: callback switches to the running desktop; ordinary snapshots do not reopen it; repeated callbacks work.");
} finally {
  await browser.close();
}
