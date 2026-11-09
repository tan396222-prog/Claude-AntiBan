// Exercise the shipped noVNC client through its real RFB handshake and raw frames.
// The fixture owns all requests and never reaches a user's VM.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const { startDisplay } = await import("./rfb-wire-fixture.mjs");
const display = await startDisplay(process.env.CAGE_UI_URL || "http://127.0.0.1:5199");
const fixture = await createWorkbench({ mockWebSockets: false, url: display.url });
const { page, state, browser, errors } = fixture;
const report = display.stats;
report.checks = [];
const browserErrors = [];
page.on("console", (message) => {
  if (message.type() === "error") browserErrors.push(message.text());
});
try {
  await page.getByRole("button", { name: "启动 Cage 环境", exact: true }).click();
  await page.getByRole("heading", { name: "Cage 环境已就绪", exact: true }).waitFor();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await page.waitForFunction(() => Boolean(document.querySelector(".remote-canvas canvas")));
  await page.getByText("正在连接画面", { exact: true }).waitFor({ state: "hidden" });
  assert.ok(report.connections > 0);
  const deadline = Date.now() + 3000;
  while (report.frames < 5 && Date.now() < deadline)
    await new Promise((resolve) => setTimeout(resolve, 20));
  assert.ok(report.frames >= 5, "display did not deliver its first frames");
  const originalTabs = structuredClone(state.tabs);
  const before = report.frames;
  await page.waitForFunction(() => document.querySelector(".remote-canvas canvas")?.width > 0);
  await page.getByRole("button", { name: "工作台", exact: true }).click();
  // Bounded observation proves frames stop, rather than only inspecting CSS.
  await new Promise((resolve) => setTimeout(resolve, 500));
  const hiddenStart = report.frames;
  await new Promise((resolve) => setTimeout(resolve, 1000));
  report.hiddenFrames = report.frames - hiddenStart;
  report.visibleFrames = before;
  if (!process.env.CAGE_PERF_BASELINE) {
    assert.equal(report.hiddenFrames, 0, "hidden workbench continued receiving frames");
    assert.ok(report.closed > 0, "display connection was not closed");
  }
  assert.deepEqual(state.tabs, originalTabs, "hiding the display changed guest tabs");
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await page.getByText("正在连接画面", { exact: true }).waitFor({ state: "hidden" });
  await page.waitForFunction(() => document.querySelector(".remote-canvas canvas")?.width > 0);
  const resumed = report.frames;
  await new Promise((resolve) => setTimeout(resolve, 250));
  assert.ok(report.frames > resumed, "display frames did not resume");
  if (!process.env.CAGE_PERF_BASELINE) {
    await page.evaluate(() => {
      window.cageWindowVisible = false;
      window.dispatchEvent(new Event("cage-window-visibility"));
    });
    await new Promise((resolve) => setTimeout(resolve, 300));
    const paused = report.frames;
    await new Promise((resolve) => setTimeout(resolve, 300));
    assert.equal(report.frames, paused, "occluded native window continued receiving frames");
    await page.evaluate(() => {
      window.cageWindowVisible = true;
      window.dispatchEvent(new Event("cage-window-visibility"));
    });
    await page.getByText("正在连接画面", { exact: true }).waitFor({ state: "hidden" });
    await new Promise((resolve) => setTimeout(resolve, 250));
    assert.ok(report.frames > paused, "native window visibility did not resume frames");
  }
  assert.deepEqual(errors, []);
  report.checks.push("real RFB frames", "tabs preserved", "frames resumed");
  if (!process.env.CAGE_PERF_BASELINE) report.checks.push("hidden frames stopped", "native occlusion paused");
  const soakSeconds = Number(process.env.CAGE_PERF_SOAK_SECONDS || 0);
  if (soakSeconds > 0) {
    const session = await page.context().newCDPSession(page);
    await session.send("Performance.enable");
    report.soak = [];
    const started = Date.now();
    while ((Date.now() - started) / 1000 < soakSeconds) {
      const beforeResume = report.frames;
      await page.evaluate(() => { window.cageWindowVisible = true; window.dispatchEvent(new Event("cage-window-visibility")); });
      await page.getByText("正在连接画面", { exact: true }).waitFor({ state: "hidden" });
      await new Promise((resolve) => setTimeout(resolve, 1000));
      assert.ok(report.frames > beforeResume, "soak: visible display did not resume frames");
      await page.evaluate(() => { window.cageWindowVisible = false; window.dispatchEvent(new Event("cage-window-visibility")); });
      await new Promise((resolve) => setTimeout(resolve, 500));
      const hidden = report.frames;
      await new Promise((resolve) => setTimeout(resolve, 8500));
      assert.equal(report.frames, hidden, "soak: frames continued in hidden window");
      const { metrics } = await session.send("Performance.getMetrics");
      report.soak.push({ seconds: (Date.now() - started) / 1000, ...Object.fromEntries(metrics.filter((x) => ["JSHeapUsedSize", "JSHeapTotalSize", "TaskDuration", "Documents", "Nodes"].includes(x.name)).map((x) => [x.name, x.value])) });
      if (process.env.CAGE_PERF_REPORT) await fs.writeFile(process.env.CAGE_PERF_REPORT, JSON.stringify(report, null, 2));
    }
    report.checks.push(`${soakSeconds}s repeated display reconnect soak`);
    await session.detach();
  }
  const output = process.env.CAGE_PERF_REPORT;
  if (output) await fs.writeFile(output, JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
} catch (error) {
  console.error(JSON.stringify({ report, errors, browserErrors, visible: await page.locator("body").innerText() }));
  throw error;
} finally {
  await browser.close();
  await display.close();
}
