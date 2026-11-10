import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";
import { startDisplay } from "./rfb-wire-fixture.mjs";

const display = await startDisplay(process.env.CAGE_UI_URL || "http://127.0.0.1:5198", { sharedClipboard: true });
const reads = [], prepared = [], cancelled = [], delivered = [];
const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==";
const { browser, page, state, errors } = await createWorkbench({
  url: display.url, mockWebSockets: false,
  nativeRequest: request => {
    if (request.action === "clipboardRevision") return 1;
    if (request.action === "clipboardSyncRead") return { text: "synthetic system clipboard", revision: 1 };
    if (request.action === "clipboardReadContent") return new Promise(resolve => reads.push(resolve));
    return true;
  },
  async beforeNavigate(page, state) {
    state.status.ready = state.status.vm_running = true;
    state.status.guest_system = state.account.guest_system = "macos";
    state.tabs = [
      { id: "input-one", kind: "web", title: "输入测试一", url: "https://one.example.test" },
      { id: "input-two", kind: "web", title: "输入测试二", url: "https://two.example.test" },
    ];
    state.active = "input-one";
    await page.route("**/api/browser/tabs", route => route.fulfill({ json: {
      tabs: state.tabs, active: state.active, capabilities: { nativeClipboard: true, systemClipboard: false },
    } }));
    await page.route("**/api/input", route => route.fulfill({ json: { available: true, context: "a".repeat(32), rect: [20, 20, 1, 24] } }));
    await page.route("**/api/input/paste/start", route => {
      const body = route.request().postDataJSON();
      const context = String(prepared.length + 1).padStart(32, "0");
      prepared.push({ ...body, context });
      return route.fulfill({ json: { available: true, context } });
    });
    await page.route("**/api/input/paste/cancel", route => {
      cancelled.push(route.request().postDataJSON());
      return route.fulfill({ json: { ok: true } });
    });
    for (const kind of ["text", "image"]) await page.route(`**/api/input/${kind}`, route => {
      delivered.push({ kind, body: route.request().postDataJSON() });
      return route.fulfill({ json: { ok: true } });
    });
  },
});
page.setDefaultTimeout(10000);
async function until(predicate) {
  for (let attempt = 0; attempt < 200; attempt++) {
    if (predicate()) return;
    await page.waitForTimeout(20);
  }
  assert(predicate(), "expected private clipboard operation did not arrive");
}
try {
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await page.getByText("正在连接画面", { exact: true }).waitFor({ state: "hidden" });
  await page.waitForFunction(() => window.cageSharedClipboardActive === true);
  const canvas = page.locator(".browser-lane .remote-canvas canvas");
  await canvas.click({ position: { x: 40, y: 40 } });
  display.stats.keys.length = 0;
  await page.keyboard.press("Meta+f");
  await until(() => display.stats.keys.some(key => key.symbol === 102 && !key.down));
  assert(display.stats.keys.some(key => key.symbol === 0xffeb && key.down));
  assert(!display.stats.keys.some(key => key.symbol === 0xffe3));
  await page.keyboard.press("Control+a");
  await until(() => display.stats.keys.some(key => key.symbol === 0xffe3 && !key.down));
  await page.evaluate(() => {
    window.pasteIntent = [];
    const fetch = window.fetch.bind(window);
    window.fetch = (...args) => {
      if (String(args[0]).endsWith("/input/paste/start")) window.pasteIntent.push("prepare");
      return fetch(...args);
    };
    const bridge = window.webkit.messageHandlers.cage, post = bridge.postMessage.bind(bridge);
    bridge.postMessage = body => {
      if (body.action === "clipboardReadContent") window.pasteIntent.push("read");
      return post(body);
    };
    const create = URL.createObjectURL.bind(URL);
    URL.createObjectURL = value => { window.pasteIntent.push("decode"); return create(value); };
  });
  display.stats.keys.length = 0;
  await page.keyboard.press("Meta+v");
  await until(() => reads.length === 1 && prepared.length === 1);
  assert.deepEqual(await page.evaluate(() => window.pasteIntent), ["prepare", "read"]);
  await page.keyboard.press("Enter");
  assert(!display.stats.keys.some(key => key.symbol === 0xff0d));
  const longText = "共享架构🙂\n".repeat(2000);
  reads[0]({ text: longText });
  await until(() => delivered.length === 1 && display.stats.keys.some(key => key.symbol === 0xff0d && !key.down));
  assert.equal(delivered[0].kind, "text");
  assert.equal(delivered[0].body.text, longText);
  assert.equal(delivered[0].body.id, "input-one");
  assert.equal(delivered[0].body.context, prepared[0].context);
  assert(!display.stats.keys.some(key => key.symbol === 118), "private clipboard text must not depend on a guest OS paste chord");

  await page.keyboard.press("Meta+v");
  await until(() => reads.length === 2);
  await page.getByRole("tab", { name: "输入测试二", exact: true }).click();
  await until(() => state.active === "input-two");
  reads[1]({ png });
  await until(() => cancelled.some(item => item.context === prepared[1].context));
  assert.equal(delivered.length, 1, "an old tab must reject the delayed PNG");

  await canvas.click({ position: { x: 50, y: 50 } });
  assert.deepEqual(await page.evaluate(() => [window.cageEditAction("paste"), window.cageEditAction("paste")]), [true, true]);
  await until(() => reads.length === 4 && prepared.length === 4);
  reads[3]({ text: "second queued paste" });
  await page.waitForTimeout(80);
  assert.equal(delivered.length, 1, "the second clipboard read must wait for the first paste");
  reads[2]({ png });
  await until(() => delivered.length === 3);
  assert.deepEqual(delivered.slice(1).map(item => item.kind), ["image", "text"]);
  assert(delivered.slice(1).every(item => item.body.id === "input-two"));

  await page.evaluate(() => { window.pasteIntent.length = 0; });
  await page.getByLabel("Cage 网页输入").evaluate((element, encoded) => {
    const data = new DataTransfer();
    data.items.add(new File([Uint8Array.from(atob(encoded), value => value.charCodeAt(0))], "screenshot.png", { type: "image/png" }));
    element.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: data }));
  }, png);
  await until(() => delivered.length === 4);
  assert.deepEqual(await page.evaluate(() => window.pasteIntent), ["prepare", "decode"]);
  assert.equal(delivered[3].kind, "image");
  await until(() => cancelled.length === prepared.length);
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ result: "PASS", browser: browser.version(), textBytes: Buffer.byteLength(longText), checks: [
    "Mac browser Command and physical Control keep separate RFB modifiers",
    "private clipboard stays reachable while OS sharing is active", "preparation precedes clipboard read and bitmap decode",
    "long Unicode text uses bound text transfer before Enter", "tab change rejects a pending PNG", "successive pastes stay ordered", "unused and completed preparations are cancelled",
  ], deliveries: delivered.map(item => ({ kind: item.kind, id: item.body.id, context: item.body.context })) }));
} finally { await browser.close(); await display.close(); }
