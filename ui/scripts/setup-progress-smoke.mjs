import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const { browser, page, state, errors } = await createWorkbench();
try {
  let starts = 0;
  await page.route("**/api/start", (route) => {
    starts++;
    state.status.job = { id: "setup-" + starts, kind: "start", running: true, message: "正在准备基础环境",
      setup_progress: { phase: "image", label: "下载 Debian 13 基础镜像", received: 25 * 1048576, total: 100 * 1048576 } };
    return route.fulfill({ json: state.status.job });
  });
  await page.getByRole("button", { name: "启动 Cage 环境", exact: true }).click();
  const panel = page.getByRole("region", { name: "环境安装进度" });
  const bar = panel.getByRole("progressbar");
  await panel.getByText("25 MiB / 100 MiB").waitFor();
  assert.equal(await bar.getAttribute("aria-valuenow"), "25");
  assert.equal(await panel.locator('[aria-current="step"]').textContent(), "系统镜像");
  await panel.getByRole("button", { name: "操作详情" }).click();
  await page.getByRole("dialog").waitFor();
  await page.keyboard.press("Escape");
  if (process.env.CAGE_SETUP_SCREENSHOT) await page.screenshot({ path: process.env.CAGE_SETUP_SCREENSHOT, animations: "disabled" });
  state.status.job.setup_progress = { phase: "tools", label: "下载 code-server", received: 8 * 1048576 };
  await panel.getByText("已接收 8 MiB").waitFor();
  assert.equal(await bar.getAttribute("aria-valuenow"), null);
  state.status.job.setup_progress = { phase: "services", label: "正在配置浏览器与终端" };
  await panel.getByRole("heading", { name: "正在配置浏览器与终端" }).waitFor();
  assert.equal(await bar.getAttribute("aria-valuenow"), null);
  assert.equal(await panel.locator(".setup-transfer-bytes").count(), 0);
  await panel.getByRole("button", { name: "停止安装" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "取消" }).click();
  assert.equal(state.requests.filter(r => r.path === "/stop").length, 0);
  await page.setViewportSize({ width: 600, height: 600 });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  state.status.job = { ...state.status.job, running: false, error: "下载重试次数已用尽，未完成文件已保存" };
  await panel.getByRole("alert").waitFor();
  assert.equal(await bar.count(), 0);
  await panel.getByText("可手动继续安装，已保存的下载会尝试续传。").waitFor();
  assert.equal(starts, 1);
  await panel.getByRole("button", { name: "继续安装" }).click();
  await panel.getByText("25 MiB / 100 MiB").waitFor();
  assert.equal(starts, 2);
  state.status.job = { ...state.status.job, running: false, error: "下载再次中断，未完成文件已保存" };
  await panel.getByRole("alert").waitFor();
  await panel.getByRole("button", { name: "继续安装" }).click();
  await panel.getByText("25 MiB / 100 MiB").waitFor();
  assert.equal(starts, 3);
  await panel.getByRole("button", { name: "停止安装" }).click();
  const stopped = page.waitForRequest("**/api/stop");
  await page.getByRole("dialog").getByRole("button", { name: "停止安装" }).click();
  await stopped;
  assert.deepEqual(errors, []);
  console.log("PASS: measured bytes and percent, unknown size, phase only, details, compact layout, cancel and retry.");
} finally { await browser.close(); }
