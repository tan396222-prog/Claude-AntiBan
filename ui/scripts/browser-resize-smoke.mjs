import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";
import { startDisplay } from "./rfb-wire-fixture.mjs";

const display = await startDisplay(process.env.CAGE_UI_URL || "http://127.0.0.1:5198", { frameInterval: 200 });
const { browser, page, state, errors } = await createWorkbench({
  url: display.url, mockWebSockets: false, viewport: { width: 1000, height: 740 },
});
page.setDefaultTimeout(10000);
await page.route("**/api/browser/resize", route => {
  return route.fulfill({ json: { ok: true } });
});
try {
  state.status.ready = state.status.vm_running = true;
  state.tabs = [{ id: "page", kind: "web", title: "Resize", url: "https://example.test" }];
  state.active = "page";
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  const canvas = page.locator(".browser-lane .remote-canvas canvas");
  const pixel = () => canvas.evaluate(c => [...c.getContext("2d").getImageData(c.width - 1, c.height - 1, 1, 1).data]);
  await page.waitForFunction(() => {
    const c = document.querySelector(".browser-lane canvas");
    return c?.width && c.getContext("2d").getImageData(0, 0, 1, 1).data[0] > 0;
  });
  display.pauseFrames(true);
  const oldPixel = await pixel();
  const connections = display.stats.connections;
  const oldBox = await canvas.boundingBox();
  const resized = page.waitForRequest(req => req.url().endsWith("/api/browser/resize") && req.postDataJSON().width > oldBox.width * 2);
  await page.setViewportSize({ width: 1240, height: 760 });
  const box = await page.locator(".browser-lane .remote-canvas").boundingBox();
  const shown = await canvas.boundingBox();
  assert.ok(Math.abs(shown.width - box.width) < 1 && Math.abs(shown.height - box.height) < 1,
    "the previous frame must fill the browser pane before the guest responds");
  const x = Math.floor(box.width * .75), y = Math.floor(box.height * .25);
  await canvas.click({ position: { x, y } });
  assert.ok(Math.abs(display.stats.pointer.x - x / box.width * 640) <= 1);
  assert.ok(Math.abs(display.stats.pointer.y - y / box.height * 480) <= 1);
  const desired = (await resized).postDataJSON();
  assert.equal(desired.width, Math.floor(box.width * 2));
  display.resize(desired); // Send dimensions now, withhold all pixel updates.
  await page.waitForFunction(w => document.querySelector(".browser-lane canvas")?.width === w, desired.width);
  assert.deepEqual(await pixel(), oldPixel, "enlarging must preserve the visible corner until new pixels arrive");
  display.pauseFrames(false);
  await page.waitForFunction(value => {
    const c = document.querySelector(".browser-lane canvas");
    const p = c.getContext("2d").getImageData(c.width - 1, c.height - 1, 1, 1).data;
    return p[3] === 255 && p[0] !== value;
  }, oldPixel[0]);
  assert.equal(display.stats.connections, connections);
  assert.deepEqual(errors, []);
  console.log("PASS: browser resize fills the pane, preserves the last frame and pointer mapping, then repaints over the same RFB connection");
} catch (error) {
  console.error({ errors, stats: display.stats, body: await page.locator("body").innerText() });
  throw error;
} finally {
  await browser.close();
  await display.close();
}
