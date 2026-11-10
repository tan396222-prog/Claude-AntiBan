import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const inputs = [], status = { installed: true, running: true, screen: true, input: true, input_protocol: 1, persistent: true };
let image, geometry = "input-geometry-1";
const { page, browser, errors } = await createWorkbench({
  async beforeNavigate(page, state) {
    state.account.guest_system = state.status.guest_system = "macos";
    state.status.vm_running = state.status.ready = true;
    image = await page.evaluate(() => {
      const canvas = document.createElement("canvas");
      canvas.width = 640; canvas.height = 480;
      const context = canvas.getContext("2d");
      context.fillStyle = "#f7f5f1"; context.fillRect(0, 0, 640, 480);
      context.fillStyle = "#303030"; context.font = "22px sans-serif";
      context.fillText("Synthetic Claude input surface", 30, 45);
      return canvas.toDataURL().split(",")[1];
    });
    await page.route("**/api/macos/claude/window", route => route.fulfill({ json: status }));
    await page.routeWebSocket("**/api/macos/claude/connection**", socket => {
      socket.onMessage(data => {
        const { id, request } = JSON.parse(data);
        let result = { ok: true };
        if (request.action === "status") result = status;
        else if (request.action === "frame") result = { image, pid: 23, window: 70, geometry, contentWidth: 640, contentHeight: 480 };
        else {
          inputs.push(request);
          if (request.action === "ime") result = { available: true, context: "a".repeat(32), preedit_supported: false,
            rect: [80, 70, 1, 24], width: 640, height: 480 };
        }
        socket.send(JSON.stringify({ id, result }));
      });
    });
  },
});
page.setDefaultTimeout(10000);
async function until(predicate) {
  for (let attempt = 0; attempt < 200; attempt++) {
    if (predicate()) return;
    await page.waitForTimeout(20);
  }
  assert(predicate(), "expected native window input did not arrive");
}
try {
  await page.getByRole("button", { name: "Claude Desktop", exact: true }).click();
  const surface = page.getByAltText("Claude Desktop 客机窗口");
  await surface.waitFor();
  await page.getByLabel("向 Claude Desktop 输入").waitFor({ state: "attached" });
  const bounds = await surface.boundingBox();
  await page.mouse.move(bounds.x + 80, bounds.y + 100);
  await page.mouse.down();
  await page.mouse.move(bounds.x + 360, bounds.y + 160, { steps: 5 });
  await page.mouse.up();
  await until(() => inputs.some(input => input.action === "pointer" && input.phase === "up"));
  const drag = inputs.filter(input => input.action === "pointer");
  const firstDown = drag.findIndex(input => input.phase === "down");
  assert(firstDown >= 0 && drag.slice(firstDown + 1, -1).some(input => input.phase === "move" && input.buttons === 1));
  assert.equal(drag.at(-1).phase, "up");
  assert(drag.every(input => input.geometry === geometry && input.pid === 23 && input.window === 70));
  await surface.click({ button: "right", position: { x: 150, y: 160 } });
  await surface.dblclick({ position: { x: 200, y: 190 } });
  await until(() => inputs.some(input => input.button === 2 && input.phase === "up") && inputs.some(input => input.clicks === 2));

  await page.keyboard.press("Meta+f");
  assert.equal(await page.evaluate(() => window.cageEditAction("selectAll")), true);
  await page.keyboard.press("F5");
  await until(() => inputs.some(input => input.action === "key" && input.code === "F5" && !input.down));
  assert(inputs.some(input => input.code === "KeyF" && input.command));
  assert(inputs.some(input => input.code === "KeyA" && input.command));
  await until(() => inputs.some(input => input.action === "ime" && input.mode === "status"));
  await page.getByLabel("向 Claude Desktop 输入").evaluate(element => {
    element.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
    element.value = "nihao";
    element.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertCompositionText", isComposing: true, data: "nihao" }));
    element.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true, data: "你好🙂" }));
  });
  await page.keyboard.press("Enter");
  await until(() => inputs.some(input => input.action === "ime" && input.mode === "commit"));
  const composition = inputs.filter(input => input.action === "ime" && input.mode !== "status");
  assert.deepEqual(composition.map(input => input.mode), ["begin", "update", "commit"]);
  assert.equal(composition[2].text, "你好🙂");
  assert(composition.every(input => input.session === composition[0].session && input.geometry === geometry));
  assert.deepEqual(composition.map(input => input.sequence), [0, 1, 2]);
  await until(() => inputs.some(input => input.code === "Enter" && !input.down));
  assert(inputs.indexOf(composition[2]) < inputs.findIndex(input => input.code === "Enter" && input.down));

  const resetCount = inputs.filter(input => input.action === "input-reset").length;
  await page.mouse.down();
  await page.evaluate(() => window.dispatchEvent(new Event("blur")));
  await until(() => inputs.filter(input => input.action === "input-reset").length > resetCount);
  await page.mouse.up();
  const beforeResize = inputs.filter(input => input.action === "input-reset").length;
  geometry = "input-geometry-2";
  await until(() => inputs.filter(input => input.action === "input-reset").length > beforeResize);
  await surface.click({ position: { x: 40, y: 50 } });
  await page.keyboard.press("ArrowLeft");
  await until(() => inputs.some(input => input.code === "ArrowLeft" && !input.down && input.geometry === geometry));
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ result: "PASS", checks: ["drag with held left button", "right click", "double click count", "Command find", "host edit menu select-all", "F5", "IME begin/update/commit before Enter", "blur releases input", "geometry change resets and rebinds input"], requests: inputs.filter(input => input.action !== "frame" && input.mode !== "status") }));
} finally { await browser.close(); }
