import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = new URL("../../docs/test-reports/2026-11-04-desktop-memory/", import.meta.url);
await fs.mkdir(output, { recursive: true });
const { browser, page, state, errors } = await createWorkbench();
let unavailable = true;
const desktop = { installed: true, running: false, failed: true, failure_reason: "oom-kill", version: "4.4212.0" };
await page.route("**/api/claude-desktop**", async route => {
  if (route.request().method() === "POST") {
    return route.fulfill({ status: 502, body: "测试：打开操作失败，请重试" });
  }
  if (unavailable) return route.fulfill({ status: 502, body: "无法读取 Claude Desktop 状态，请重试" });
  return route.fulfill({ json: desktop });
});
try {
  state.status.vm_running = true;
  state.status.ready = true;
  await page.reload();
  await page.getByRole("button", { name: "Claude Desktop", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "无法读取 Claude Desktop 状态" }).waitFor();
  unavailable = false;
  await page.getByRole("button", { name: "刷新 Claude Desktop 状态", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "环境内存不足" }).waitFor();
  assert.equal(await page.getByText("无法读取 Claude Desktop 状态，请重试", { exact: true }).count(), 0);
  await page.screenshot({ path: new URL("oom-message.png", output).pathname });
  desktop.failed = false;
  desktop.failure_reason = "";
  await page.getByRole("button", { name: "刷新 Claude Desktop 状态", exact: true }).click();
  await page.getByRole("alert").waitFor({ state: "hidden" });
  await page.getByRole("button", { name: "打开 Claude Desktop", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "打开操作失败" }).waitFor();
  await page.getByRole("button", { name: "刷新 Claude Desktop 状态", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "打开操作失败" }).waitFor();
  assert.deepEqual(errors, []);
  const checks = ["status recovery clears stale read error", "OOM reason displayed", "healthy status clears OOM message", "status polling preserves failed user operation"];
  await fs.writeFile(new URL("ui-checks.json", output), JSON.stringify({ complete: true, checks }, null, 2));
  console.log(JSON.stringify({ complete: true, checks }));
} finally { await browser.close(); }
