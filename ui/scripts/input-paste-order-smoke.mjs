import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";
import { startDisplay } from "./rfb-wire-fixture.mjs";

const display = await startDisplay(process.env.CAGE_UI_URL || "http://127.0.0.1:5198", { sharedClipboard: true });
let context = "a".repeat(32);
const { browser, page, errors } = await createWorkbench({
  mockWebSockets: false, url: display.url,
  nativeRequest: request => {
    if (request.action === "clipboardRevision") return 1;
    if (request.action === "clipboardSyncRead") return { text: "synthetic shared clipboard", revision: 1 };
    return true;
  },
  async beforeNavigate(page, state) {
    state.status.ready = state.status.vm_running = true;
    await page.route("**/api/claude-desktop", route => route.fulfill({ json: { installed: true, running: true, managed: true } }));
    await page.route("**/api/input", route => route.fulfill({ json: { available: true, context, rect: [20, 20, 1, 24] } }));
  },
});
page.setDefaultTimeout(10000);
async function until(predicate) {
  for (let attempt = 0; attempt < 200; attempt++) {
    if (predicate()) return;
    await page.waitForTimeout(20);
  }
  assert(predicate(), "expected RFB input did not arrive");
}
async function delaySync() {
  await page.evaluate(() => {
    window.pendingSync = false;
    window.cageSyncClipboard = () => new Promise(resolve => {
      window.pendingSync = true;
      window.releaseSync = resolve;
    });
  });
}
try {
  await page.getByRole("button", { name: "Claude Desktop", exact: true }).click();
  await page.getByText("正在连接画面", { exact: true }).waitFor({ state: "hidden" });
  await page.waitForFunction(() => window.cageSharedClipboardActive === true);
  await page.locator(".remote-canvas canvas").click({ position: { x: 40, y: 40 } });
  display.stats.keys.length = 0;
  await delaySync();
  await page.keyboard.press("Meta+v");
  await page.waitForFunction(() => window.pendingSync);
  await page.keyboard.press("Enter");
  assert(!display.stats.keys.some(key => key.symbol === 118 || key.symbol === 0xff0d));
  await page.evaluate(() => window.releaseSync());
  await until(() => display.stats.keys.some(key => key.symbol === 0xff0d && !key.down));
  const ordered = display.stats.keys.map(key => [key.symbol, key.down]);
  assert(ordered.findIndex(([symbol, down]) => symbol === 118 && down) < ordered.findIndex(([symbol, down]) => symbol === 0xff0d && down));

  display.stats.keys.length = 0;
  await delaySync();
  await page.keyboard.press("Meta+v");
  await page.waitForFunction(() => window.pendingSync);
  context = "b".repeat(32);
  await page.keyboard.press("Enter");
  await page.evaluate(() => window.releaseSync());
  await page.getByText("粘贴位置已切换，请回到原输入位置重试", { exact: true }).waitFor();
  assert(!display.stats.keys.some(key => key.symbol === 118 || key.symbol === 0xff0d));
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ result: "PASS", checks: ["shared clipboard gate reserves the real RFB queue", "paste precedes Enter", "focus change cancels paste and queued Enter"], wire: ordered }));
} finally { await browser.close(); await display.close(); }
