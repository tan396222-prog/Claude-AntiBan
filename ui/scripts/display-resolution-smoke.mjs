import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { chooseInline, createWorkbench } from "./workbench-fixture.mjs";
import { startDisplay } from "./rfb-wire-fixture.mjs";

const display = await startDisplay(process.env.CAGE_UI_URL || "http://127.0.0.1:5198", { frameInterval: 1000 });
const { browser, page, state, errors } = await createWorkbench({
  mockWebSockets: false, url: display.url, viewport: { width: 1100, height: 800 },
});
page.setDefaultTimeout(10000);
const output = process.env.CAGE_DISPLAY_REPORT || "/tmp/cage-display-resolution";
const requests = [], actions = [];
let failResize = false;
const desktop = { installed: true, running: true, managed: true, version: "fixture" };
await page.route("**/api/claude-desktop**", async (route) => {
  const path = new URL(route.request().url()).pathname;
  if (path.endsWith("/vnc-credentials")) return route.fulfill({ json: { password: "fixture" } });
  if (path.endsWith("/resize")) {
    const size = route.request().postDataJSON();
    requests.push(size);
    if (failResize) return route.fulfill({ status: 502, body: "fixture: guest busy" });
    display.resize(size);
    return route.fulfill({ json: { ok: true } });
  }
  if (route.request().method() === "GET") return route.fulfill({ json: desktop });
  actions.push(path);
  return route.fulfill({ json: { ok: true } });
});
async function matched(zoom = 1) {
  await page.waitForFunction((zoom) => {
    const box = document.querySelector(".claude-desktop-view .remote-canvas"), canvas = box?.querySelector("canvas");
    if (!canvas) return false;
    const bounds = box.getBoundingClientRect(), shown = canvas.getBoundingClientRect();
    return canvas.width === Math.floor(bounds.width * 2 / zoom) &&
      canvas.height === Math.floor(bounds.height * 2 / zoom) &&
      Math.abs(shown.width - bounds.width) < 2 && Math.abs(shown.height - bounds.height) < 2;
  }, zoom, { timeout: 10000 });
}
try {
  state.status.vm_running = state.status.ready = true;
  await page.reload();
  await page.getByRole("button", { name: "Claude Desktop", exact: true }).click();
  await matched();
  await page.setViewportSize({ width: 1000, height: 740 });
  await matched();
  await page.getByRole("button", { name: "收起侧栏", exact: true }).click();
  await matched();
  const connections = display.stats.connections;
  const selector = page.getByLabel("Desktop 显示大小", { exact: true });
  const screen = page.locator(".desktop-screen");
  const toolbar = page.locator(".claude-desktop-toolbar");
  const toolbarBefore = await toolbar.boundingBox();
  const before = await screen.boundingBox();
  await selector.click();
  await fs.mkdir(output, { recursive: true });
  await page.screenshot({ path: output + "/display-size-open.png" });
  const options = page.getByRole("listbox");
  const bounds = await options.boundingBox();
  const trigger = await selector.boundingBox();
  assert.equal(bounds.x, trigger.x);
  assert.ok(bounds.y >= trigger.y + trigger.height && bounds.y + bounds.height > before.y);
  assert.deepEqual(await toolbar.boundingBox(), toolbarBefore);
  assert.deepEqual(await screen.boundingBox(), before);
  await selector.press("Escape");
  await options.waitFor({ state: "hidden" });
  assert.deepEqual(await screen.boundingBox(), before);
  await selector.press("Enter");
  await selector.press("ArrowDown");
  await selector.press("ArrowDown");
  await selector.press("Enter");
  await matched(1.25);
  await chooseInline(selector, "0.75");
  await matched(0.75);
  assert.equal(display.stats.connections, connections, "zoom must preserve the active RFB connection");
  failResize = true;
  await chooseInline(selector, "1.1");
  await page.getByRole("alert").filter({ hasText: "画面尺寸未能同步" }).waitFor();
  failResize = false;
  await matched(1.1);
  await page.getByRole("alert").filter({ hasText: "画面尺寸未能同步" }).waitFor({ state: "hidden" });
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  await matched(1.1);
  await page.reload();
  await page.getByRole("button", { name: "Claude Desktop", exact: true }).click();
  await matched(1.1);
  assert.equal(await selector.getAttribute("value"), "1.1", "zoom preference must survive reload");
  await chooseInline(selector, "1");
  await matched();
  await page.setViewportSize({ width: 820, height: 558 });
  await matched();
  assert.deepEqual(actions, [], "display changes must not restart the application");
  assert.deepEqual(errors, []);
  await page.screenshot({ path: `${output}/small-window.png` });
  await fs.writeFile(`${output}/result.json`, JSON.stringify({ requests, rfb: display.stats, actions, errors }, null, 2));
  console.log("PASS: real RFB framebuffer follows viewport, sidebar and zoom; failure recovery, persistence, small window, no application restart");
} catch (error) {
  console.error(JSON.stringify({ requests, rfb: display.stats, errors, body: await page.locator("body").innerText(), canvas: await page.locator(".remote-canvas").evaluateAll((boxes) => boxes.map((box) => {
    const canvas = box.querySelector("canvas");
    return { box: box.getBoundingClientRect().toJSON(), width: canvas?.width, height: canvas?.height, shown: canvas?.getBoundingClientRect().toJSON() };
  })) }));
  throw error;
} finally {
  await browser.close();
  await display.close();
}
