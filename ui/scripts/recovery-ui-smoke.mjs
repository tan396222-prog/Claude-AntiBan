import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = new URL("../../docs/test-reports/2026-11-01-system-remediation/", import.meta.url);
await fs.mkdir(output, { recursive: true });
const calls = [];
let update = { phase: "available", currentVersion: "0.2.0-alpha.1", latestVersion: "0.2.0-alpha.2", message: "发现新版本，可下载更新", retryable: true };
const fixture = await createWorkbench({ nativeRequest: async (body) => {
  calls.push(body);
  if (body.action === "workbenchReady") return true;
  if (body.action !== "appUpdate") return null;
  if (body.operation === "install") update = { ...update, phase: "downloading", receivedBytes: 50, totalBytes: 100, message: "正在下载更新" };
  if (body.operation === "cancel") update = { ...update, phase: "available", message: "更新已暂停，可稍后继续" };
  return update;
} });
const { browser, page, state, errors } = fixture;
const checks = [];
try {
  await page.getByRole("button", { name: "下载更新", exact: true }).waitFor();
  const button = page.getByRole("button", { name: "下载更新", exact: true });
  assert.match(await button.getAttribute("title"), /点击后将自动停止环境、安装并重启/);
  const bounds = await button.boundingBox();
  assert.ok(bounds.x < 248 && bounds.y > 700, "update action is not in the bottom-left sidebar");
  await page.screenshot({ path: fileURLToPath(new URL("update-available.png", output)) });
  await button.click();
  assert.equal(calls.filter((call) => call.operation === "install").length, 1);
  await page.getByRole("progressbar", { name: "更新下载进度" }).waitFor();
  assert.equal(await page.getByRole("progressbar").getAttribute("aria-valuenow"), "50");
  assert.equal(await page.getByRole("dialog", { name: "Cage 更新" }).count(), 0);
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("cage-app-update", { detail: { phase: "failed", latestVersion: "0.2.0-alpha.2", message: "更新源暂不可访问，仓库尚未公开", retryable: true } })));
  await page.getByRole("button", { name: "重试更新", exact: true }).waitFor();
  await page.getByRole("alert").filter({ hasText: "更新源暂不可访问，仓库尚未公开" }).waitFor();
  checks.push("bottom-left update affordance", "single install request without a dialog", "inline ring progress", "private source failure visible");

  const downloads = [{ id: "qa-download", name: "report.txt", state: "failed", error: "目标目录暂时无法写入", retryable: true }];
  let retries = 0, editorRestarts = 0, componentUpdates = 0;
  await page.route("**/api/browser/tabs", (route) => route.fulfill({ json: { tabs: state.tabs, active: state.active, downloads } }));
  await page.route("**/api/browser/download-retry", (route) => {
    assert.deepEqual(route.request().postDataJSON(), { id: "qa-download" });
    retries++;
    Object.assign(downloads[0], { state: "saved", retryable: false, error: "", path: "/Users/you/Cage/Downloads/report.txt" });
    return route.fulfill({ json: { ok: true } });
  });
  await page.route("**/api/browser/editor-restart", (route) => {
    assert.equal(route.request().postDataJSON().workspace, state.settings.workspace);
    editorRestarts++;
    return route.fulfill({ json: { ok: true } });
  });
  await page.route("**/api/environment/versions", (route) => route.fulfill({ json: {
    vm_running: true, controller: { version: "0.2.0-alpha.2" }, components: { status: "update_required", update_required: true },
    tools: { chromium: { current: "156", required: "系统安全更新" }, code_server: { current: "4.100", required: "4.144.0" } }, tools_update_allowed: false,
  } }));
  await page.route("**/api/environment/update", (route) => { componentUpdates++; return route.fulfill({ json: { id: "qa-components" } }); });
  await page.getByRole("button", { name: "启动 Cage 环境", exact: true }).click();
  await page.getByRole("heading", { name: "Cage 环境已就绪", exact: true }).waitFor();
  await page.getByText("“report.txt”未能保存：目标目录暂时无法写入", { exact: true }).waitFor();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await page.getByRole("button", { name: "浏览器下载", exact: true }).click();
  await page.getByRole("button", { name: "重试保存", exact: true }).click();
  await page.getByText("已保存到本机", { exact: true }).waitFor();
  assert.equal(retries, 1);
  await page.screenshot({ path: fileURLToPath(new URL("download-recovery.png", output)) });
  await page.getByRole("dialog", { name: "下载文件" }).getByRole("button", { name: "关闭面板" }).click();
  await page.getByRole("button", { name: "打开 默认工作区 的编辑器", exact: true }).click();
  await page.getByRole("button", { name: "恢复编辑器", exact: true }).click();
  await page.getByRole("dialog", { name: "重启编辑器？" }).getByRole("button", { name: "重启编辑器", exact: true }).click();
  await page.getByText("编辑器已恢复", { exact: true }).waitFor();
  assert.equal(editorRestarts, 1);
  assert.equal(state.status.vm_running, true);
  checks.push("download retry reports durable save", "editor recovery leaves VM running");
  await page.getByRole("button", { name: "设置", exact: false }).first().click();
  await page.getByRole("button", { name: "环境与恢复", exact: true }).click();
  await page.getByRole("region", { name: "环境版本" }).getByText("需要更新", { exact: true }).waitFor();
  assert.equal(await page.getByRole("button", { name: "更新环境工具", exact: true }).isDisabled(), true);
  await page.getByRole("button", { name: "更新环境工具", exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: fileURLToPath(new URL("environment-versions.png", output)) });
  await page.getByRole("button", { name: "更新环境组件", exact: true }).click();
  await page.getByRole("dialog", { name: "更新环境组件？" }).getByRole("button", { name: "更新组件", exact: true }).click();
  await page.waitForTimeout(50);
  assert.equal(componentUpdates, 1);
  assert.ok(calls.some((call) => call.action === "workbenchReady"), "successful initialized workbench did not acknowledge launch");
  assert.deepEqual(errors, []);
  checks.push("environment versions and guarded tool update", "explicit component update", "initialized workbench launch acknowledgement");
} finally { await browser.close(); }

let prematureReady = false;
const broken = await createWorkbench({ startupFailure: true, nativeRequest: async (body) => {
  if (body.action === "workbenchReady") prematureReady = true;
  return { phase: "current", currentVersion: "0.2.0-alpha.2" };
} });
try {
  await broken.page.waitForResponse((response) => response.url().endsWith("/api/status") && response.status() === 503);
  await broken.page.waitForTimeout(100);
  assert.equal(prematureReady, false, "failed API initialization acknowledged update success");
  checks.push("startup failure never acknowledges upgrade");
} finally { await broken.browser.close(); }
await fs.writeFile(new URL("ui-checks.json", output), JSON.stringify({ checks }, null, 2));
console.log(JSON.stringify({ checks }));
