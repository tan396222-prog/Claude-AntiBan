import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const { browser, page, state, errors } = await createWorkbench({
  beforeNavigate: async (page) => page.addInitScript(() => {
    const timeout = window.setTimeout.bind(window);
    const interval = window.setInterval.bind(window);
    window.setTimeout = (callback, delay, ...args) => timeout(callback, delay === 5000 ? 40 : delay, ...args);
    window.setInterval = (callback, delay, ...args) => interval(callback, delay === 5000 ? 40 : delay, ...args);
  }),
});
let requests = 0, pending = 0, maximum = 0;
await page.route("**/api/claude-desktop", async (route) => {
  requests++;
  pending++;
  maximum = Math.max(maximum, pending);
  await new Promise((resolve) => setTimeout(resolve, 120));
  pending--;
  await route.fulfill({ json: { installed: false, running: false, version: "", failed: false } });
});
const waitFor = async (predicate) => {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  assert.fail("Desktop polling condition did not complete");
};
const visible = (value) => page.evaluate((value) => {
  window.cageWindowVisible = value;
  window.dispatchEvent(new Event("cage-window-visibility"));
}, value);
try {
  state.status.vm_running = state.status.ready = true;
  await page.reload();
  await page.getByRole("button", { name: "Claude Desktop", exact: true }).click();
  await page.getByRole("button", { name: "下载并安装", exact: true }).waitFor();
  await waitFor(() => requests >= 3);
  await page.getByRole("button", { name: "刷新 Claude Desktop 状态", exact: true }).click();
  assert.equal(maximum, 1, "slow Desktop status reads must never overlap");
  await visible(false);
  await visible(true);
  await waitFor(() => pending === 0);
  assert.equal(maximum, 1, "rapid hide and resume must reuse a pending status read");
  await visible(false);
  await waitFor(() => pending === 0);
  const hiddenCount = requests;
  await new Promise((resolve) => setTimeout(resolve, 240));
  assert.equal(requests, hiddenCount, "hidden windows must stop Desktop status polling");
  await visible(true);
  await waitFor(() => requests > hiddenCount);
  await page.getByRole("button", { name: "工作台", exact: true }).click();
  await waitFor(() => pending === 0);
  const closedCount = requests;
  await new Promise((resolve) => setTimeout(resolve, 240));
  assert.equal(requests, closedCount, "leaving Desktop must release its status timer");
  assert.deepEqual(errors, []);
  console.log("Desktop slow requests, hidden window, resume, and navigation passed");
} finally {
  await browser.close();
}
