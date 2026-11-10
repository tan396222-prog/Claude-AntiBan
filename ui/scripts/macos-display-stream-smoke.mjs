import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

let requests = 0, images = 0, closed = 0;
const actions = [];
const status = { installed: true, running: true, screen: true, input: true, persistent: true };
const { page, browser, errors } = await createWorkbench({
  async beforeNavigate(page, state) {
    state.account.guest_system = "macos";
    state.status.vm_running = state.status.ready = true;
    const image = await page.evaluate(() => {
      const canvas = document.createElement("canvas");
      canvas.width = 800; canvas.height = 600;
      const context = canvas.getContext("2d");
      context.fillStyle = "#f7f5f1"; context.fillRect(0, 0, 800, 600);
      return canvas.toDataURL().split(",")[1];
    });
    await page.route("**/api/macos/claude/window", route => {
      assert.equal(route.request().postDataJSON().action, "status");
      return route.fulfill({ json: status });
    });
    await page.routeWebSocket("**/api/macos/claude/connection**", socket => {
      socket.onClose(() => { closed++; });
      socket.onMessage(data => {
        requests++;
        const { id, request } = JSON.parse(data);
        actions.push(request.action);
        let result = status;
        if (request.action === "frame") {
          if (request.after === "frame-1") result = { unchanged: true, sequence: "frame-1" };
          else {
            images++;
            result = { window: 70, pid: 23, image, sequence: "frame-1", geometry: "geometry-1", contentWidth: 400, contentHeight: 300 };
          }
        }
        socket.send(JSON.stringify({ id, result }));
      });
    });
  },
});
try {
  await page.getByRole("button", { name: "Claude Desktop", exact: true }).click();
  await page.locator(".mac-claude-frame img").waitFor();
  await page.waitForFunction(() => document.querySelector(".mac-claude-frame img")?.naturalWidth === 800);
  await page.locator(".mac-claude-frame img").click();
  await page.locator(".mac-claude-typing").press("ArrowRight");
  assert(actions.includes("click") && actions.includes("key"));
  await new Promise(resolve => setTimeout(resolve, 300));
  assert.equal(images, 1, "static frames must keep their existing decoded image");
  await page.evaluate(() => { window.cageWindowVisible = false; window.dispatchEvent(new Event("cage-window-visibility")); });
  await new Promise(resolve => setTimeout(resolve, 150));
  const hiddenRequests = requests;
  await new Promise(resolve => setTimeout(resolve, 350));
  assert.equal(requests, hiddenRequests, "hidden windows must stop requesting frames");
  assert.equal(closed, 1);
  await page.evaluate(() => { window.cageWindowVisible = true; window.dispatchEvent(new Event("cage-window-visibility")); });
  await page.waitForFunction(() => document.querySelector(".mac-claude-frame img")?.naturalWidth === 800);
  await new Promise(resolve => setTimeout(resolve, 150));
  assert.equal(images, 2, "restoring a window must request a fresh frame");
  assert.deepEqual(errors, []);
  console.log("Persistent frame/input channel, unchanged-frame reuse, hide and restore passed.");
} finally { await browser.close(); }
