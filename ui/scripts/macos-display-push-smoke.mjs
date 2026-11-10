import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const actions = [], subscriptions = [], acknowledgements = [];
let activeSocket, image, closed = 0;
const status = { installed: true, running: true, screen: true, input: true, persistent: true, push_frames: true };
function frame(sequence, revision, geometry = "geometry-1") {
  const header = Buffer.from(JSON.stringify({
    imageLength: image.length, mime: "image/png", sequence, revision, geometry,
    window: 70, pid: 23, contentWidth: 400, contentHeight: 300, pixelWidth: 800, pixelHeight: 600,
  }));
  const size = Buffer.alloc(4);
  size.writeUInt32BE(header.length);
  activeSocket.send(Buffer.concat([size, header, image]));
}
const { page, browser, errors } = await createWorkbench({
  async beforeNavigate(page, state) {
    state.account.guest_system = "macos";
    state.status.vm_running = state.status.ready = true;
    image = Buffer.from(await page.evaluate(() => {
      const canvas = document.createElement("canvas");
      canvas.width = 800; canvas.height = 600;
      const context = canvas.getContext("2d");
      context.fillStyle = "#f7f5f1"; context.fillRect(0, 0, 800, 600);
      return canvas.toDataURL().split(",")[1];
    }), "base64");
    await page.addInitScript(() => {
      const create = URL.createObjectURL.bind(URL), revoke = URL.revokeObjectURL.bind(URL);
      window.frameURLs = new Set();
      URL.createObjectURL = blob => { const url = create(blob); window.frameURLs.add(url); return url; };
      URL.revokeObjectURL = url => { window.frameURLs.delete(url); revoke(url); };
    });
    await page.route("**/api/macos/claude/window", route => {
      assert.equal(route.request().postDataJSON().action, "status");
      return route.fulfill({ json: status });
    });
    await page.routeWebSocket("**/api/macos/claude/connection**", socket => {
      socket.onClose(() => { closed++; });
      socket.onMessage(data => {
        const { id, request } = JSON.parse(data);
        actions.push(request.action);
        assert.notEqual(request.action, "frame", "capture waits must use the separate frame channel");
        socket.send(JSON.stringify({ id, result: status }));
      });
    });
    await page.routeWebSocket("**/api/macos/claude/frames**", socket => {
      activeSocket = socket;
      socket.onClose(() => { closed++; });
      socket.onMessage(data => {
        const request = JSON.parse(data);
        if (request.ack) { acknowledgements.push(request.ack); return; }
        subscriptions.push(request);
        if (request.revision === 1) frame(`initial-${subscriptions.length}`, request.revision);
      });
    });
  },
});
async function until(condition) {
  const deadline = Date.now() + 5000;
  while (!condition()) {
    assert(Date.now() < deadline, "timed out waiting for a frame channel event");
    await new Promise(resolve => setTimeout(resolve, 20));
  }
}
try {
  await page.getByRole("button", { name: "Claude Desktop", exact: true }).click();
  await page.waitForFunction(() => document.querySelector(".mac-claude-frame img")?.naturalWidth === 800);
  await until(() => acknowledgements.length === 1);
  assert((await page.locator(".mac-claude-frame img").getAttribute("src")).startsWith("blob:"));
  await page.locator(".mac-claude-frame img").click();
  await page.locator(".mac-claude-typing").press("ArrowRight");
  await until(() => actions.includes("pointer") && actions.includes("key"));
  await new Promise(resolve => setTimeout(resolve, 350));
  assert.equal(subscriptions.length, 1, "static windows must retain one subscription");
  assert.equal(acknowledgements.length, 1, "static windows must not request repeated images");
  frame("changed", 1);
  await until(() => acknowledgements.includes("changed"));
  assert.equal(await page.evaluate(() => window.frameURLs.size), 1, "replaced frames must release their blob URLs");
  await page.setViewportSize({ width: 1380, height: 900 });
  await until(() => subscriptions.at(-1).revision > 1);
  const revision = subscriptions.at(-1).revision;
  frame("stale", revision - 1, "stale-geometry");
  frame("resized", revision, "geometry-2");
  await until(() => acknowledgements.includes("resized"));
  assert(!acknowledgements.includes("stale"), "a stale frame must not replace the resized view");
  await page.evaluate(() => { window.cageWindowVisible = false; window.dispatchEvent(new Event("cage-window-visibility")); });
  await until(() => closed === 2);
  assert.equal(await page.evaluate(() => window.frameURLs.size), 0);
  const hiddenActions = actions.length, hiddenSubscriptions = subscriptions.length;
  await new Promise(resolve => setTimeout(resolve, 300));
  assert.equal(actions.length, hiddenActions);
  assert.equal(subscriptions.length, hiddenSubscriptions);
  await page.evaluate(() => { window.cageWindowVisible = true; window.dispatchEvent(new Event("cage-window-visibility")); });
  await until(() => subscriptions.length > hiddenSubscriptions);
  await page.waitForFunction(() => document.querySelector(".mac-claude-frame img")?.naturalWidth === 800);
  assert.deepEqual(errors, []);
  console.log("Binary push, decode acknowledgements, independent input, resize, blob cleanup, hide and resume passed.");
} finally { await browser.close(); }
