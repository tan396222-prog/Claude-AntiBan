import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const { browser, page, errors } = await createWorkbench();
const windowsRuntime = process.env.CAGE_RESOURCE_RUNTIME === "hyperv";
let allocation = { automatic: true, memory_gib: 12, cpus: 5 };
let rejectSave = false;
const saves = [];
await page.route("**/api/environment/resources", async (route) => {
  if (route.request().method() === "POST") {
    if (rejectSave) return route.fulfill({ status: 400, contentType: "text/plain; charset=utf-8", body: "资源配置保存失败" });
    const body = route.request().postDataJSON();
    saves.push(body);
    if (body.allocation) allocation = body.allocation.automatic ? { automatic: true, memory_gib: 12, cpus: 5 } : body.allocation;
    return route.fulfill({ json: { ok: true } });
  }
  return route.fulfill({ json: { ...allocation, ...(windowsRuntime ? { runtime: "hyperv", min_memory_gib: 4, min_cpus: 2 } : {}), running: false, recommended_memory_bytes: 12 * 2 ** 30, recommended_cpus: 5, max_memory_gib: 12, max_cpus: 5 } });
});
async function open() {
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("navigation", { name: "设置分组" }).getByRole("button", { name: "环境与恢复", exact: true }).click();
  await page.getByLabel("内存上限（GiB）", { exact: true }).waitFor();
}
try {
  await open();
  assert.equal(await page.getByRole("button", { name: "保存资源配置", exact: true }).isDisabled(), true);
  assert.equal(await page.getByRole("button", { name: "恢复默认", exact: true }).isDisabled(), true);
  if (windowsRuntime) {
    assert.equal(await page.getByLabel("内存上限（GiB）", { exact: true }).getAttribute("min"), "4");
    assert.equal(await page.getByLabel("CPU 逻辑核心数", { exact: true }).getAttribute("min"), "2");
    await page.getByLabel("内存上限（GiB）", { exact: true }).fill("3");
    await page.getByRole("button", { name: "保存资源配置", exact: true }).click();
    assert.equal(saves.length, 0);

  }
  await page.getByLabel("内存上限（GiB）", { exact: true }).fill("4");
  await page.getByLabel("CPU 逻辑核心数", { exact: true }).fill("2");
  assert.equal(await page.getByText("没有未保存的修改", { exact: true }).count(), 0);
  await page.getByRole("button", { name: "返回工作台", exact: true }).click();
  const discard = page.getByRole("dialog", { name: "放弃尚未保存的修改？", exact: true });
  await discard.waitFor();
  await discard.getByRole("button", { name: "取消", exact: true }).click();
  assert.equal(await page.getByLabel("内存上限（GiB）", { exact: true }).inputValue(), "4");
  await page.screenshot({ path: "/tmp/cage-resource-settings.png", fullPage: true });
  await page.getByRole("button", { name: "保存资源配置", exact: true }).click();
  await page.getByText("已保存，下次启动环境时生效。", { exact: true }).waitFor();
  assert.deepEqual(allocation, { automatic: false, memory_gib: 4, cpus: 2 });
  await page.reload();
  await open();
  assert.equal(await page.getByLabel("内存上限（GiB）", { exact: true }).inputValue(), "4");
  assert.equal(await page.getByLabel("CPU 逻辑核心数", { exact: true }).inputValue(), "2");
  const count = saves.length;
  await page.getByLabel("内存上限（GiB）", { exact: true }).fill("13");
  await page.getByRole("button", { name: "保存资源配置", exact: true }).click();
  assert.equal(saves.length, count);
  await page.getByLabel("内存上限（GiB）", { exact: true }).fill("6");
  rejectSave = true;
  await page.getByRole("button", { name: "保存资源配置", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "资源配置保存失败" }).waitFor();
  await page.waitForResponse(response => response.url().endsWith("/api/environment/resources") && response.request().method() === "GET" && response.ok());

  assert.equal(await page.getByRole("alert").filter({ hasText: "资源配置保存失败" }).isVisible(), true);
  assert.equal(await page.getByLabel("内存上限（GiB）", { exact: true }).inputValue(), "6");
  rejectSave = false;
  await page.getByRole("button", { name: "恢复默认", exact: true }).click();
  assert.equal(await page.getByLabel("内存上限（GiB）", { exact: true }).inputValue(), "12");
  assert.equal(await page.getByLabel("CPU 逻辑核心数", { exact: true }).inputValue(), "5");
  assert.equal(allocation.automatic, false, "reset stays a draft until saved");
  await page.getByRole("button", { name: "保存资源配置", exact: true }).click();
  await page.getByText("已保存，下次启动环境时生效。", { exact: true }).waitFor();
  assert.equal(allocation.automatic, true);
  await page.reload();
  await open();
  assert.equal(await page.getByRole("button", { name: "恢复默认", exact: true }).isDisabled(), true);
  assert.equal(await page.getByLabel("内存上限（GiB）", { exact: true }).inputValue(), "12");
  assert.equal(await page.getByRole("alert").filter({ hasText: "资源配置保存失败" }).count(), 0);
  assert.deepEqual(errors, []);
  console.log("Resource settings: manual save, reload, bounds, failure retention, defaults, editing, reset and persistence passed.");
} finally { await browser.close(); }
