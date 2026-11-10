import assert from "node:assert/strict";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = process.env.CAGE_RESOURCE_QA_OUTPUT || await mkdtemp(join(tmpdir(), "cage-macos-resource-ui-"));
await mkdir(output, { recursive: true });
const requests = [];
let failSave = false, reads = 0;
const resource = { runtime: "vz-macos", memory_policy: "fixed", automatic_reclaim: false, running: true,
  automatic: false, memory_gib: 4, cpus: 2, min_memory_gib: 4, min_cpus: 2, max_memory_gib: 12, max_cpus: 5,
  minimum_requirements: { source: "existing-configuration" }, recommended_memory_bytes: 12 * 2 ** 30,
  recommended_cpus: 5, configured_memory_bytes: 4 * 2 ** 30, configured_cpus: 2, restart_required: false,
  observation: { cpus: 2, cpu_percent: null, guest: { total_bytes: 4 * 2 ** 30, available_bytes: 2 * 2 ** 30 } } };
const fixture = await createWorkbench({ beforeNavigate: async (page, state) => {
  state.account.guest_system = state.status.guest_system = "macos";
  state.status.vm_running = state.status.ready = true;
  await page.route("**/api/environment/resources", route => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON(); requests.push(body);
      if (failSave) return route.fulfill({ status: 400, contentType: "text/plain; charset=utf-8", body: "资源配置保存失败，请检查磁盘权限。" });
      if (body.allocation) Object.assign(resource, body.allocation, { restart_required: true });
      return route.fulfill({ json: { ok: true } });
    }
    reads++;
    resource.observation.observed_at = new Date().toISOString();
    return route.fulfill({ json: resource });
  });
} });
const { page, browser, errors } = fixture;
try {
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("navigation", { name: "设置分组" }).getByRole("button", { name: "环境与恢复", exact: true }).click();
  const panel = page.getByRole("region", { name: "计算资源", exact: true });
  await panel.getByText(/macOS 客机使用固定的启动内存容量/).waitFor();
  const cpu = panel.locator(".setting-summary-row").filter({ hasText: "CPU 使用率" }).locator("strong");
  assert.equal(await cpu.innerText(), "等待采样");
  assert.equal(await panel.getByLabel("内存上限（GiB）", { exact: true }).getAttribute("min"), "4");
  assert.equal(await panel.getByLabel("CPU 逻辑核心数", { exact: true }).getAttribute("min"), "2");
  await panel.getByText(/当前最低值沿用已有启动配置/).waitFor();
  await panel.getByLabel("内存上限（GiB）", { exact: true }).fill("3");
  await panel.getByRole("button", { name: "保存资源配置", exact: true }).click();
  assert.equal(requests.length, 0);
  await panel.getByLabel("内存上限（GiB）", { exact: true }).fill("6");
  await panel.getByLabel("CPU 逻辑核心数", { exact: true }).fill("3");
  await panel.getByRole("button", { name: "保存资源配置", exact: true }).click();
  await panel.getByText("已保存，重启环境后生效。", { exact: true }).waitFor();
  assert.deepEqual(requests[0].allocation, { automatic: false, memory_gib: 6, cpus: 3 });
  failSave = true;
  await panel.getByLabel("内存上限（GiB）", { exact: true }).fill("8");
  await panel.getByRole("button", { name: "保存资源配置", exact: true }).click();
  const error = panel.getByRole("alert").filter({ hasText: "资源配置保存失败" });
  await error.waitFor();
  const before = reads;
  resource.observation.cpu_percent = 50;
  await cpu.getByText("50% / 2 个核心", { exact: true }).waitFor({ timeout: 6000 });
  assert.ok(reads > before);
  assert.ok(await error.isVisible());
  failSave = false;
  await panel.getByRole("button", { name: "重启并应用已保存配置", exact: true }).click();
  await page.getByRole("dialog", { name: "重启并应用已保存的资源配置？", exact: true }).getByRole("button", { name: "重启并应用", exact: true }).click();
  await error.waitFor({ state: "hidden" });
  assert.deepEqual(requests.at(-1), {});
  await panel.getByRole("heading", { name: "计算资源", exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(output, "macos-resources.png"), animations: "disabled" });
  await panel.getByText("资源统计说明", { exact: true }).click();
  await panel.getByText(/内存估算使用客机物理容量/).scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(output, "macos-resource-observation.png"), animations: "disabled" });
  assert.deepEqual(errors, []);
  const result = { passed: true, browser: browser.browserType().name(), requests, reads,
    checks: ["Mac fixed-capacity policy and minimum source visible", "first CPU sample unknown", "minimum enforced before save", "saved allocation waits for restart", "polling preserves save failure", "restart uses existing confirmation and shared endpoint"] };
  await writeFile(join(output, "result.json"), JSON.stringify(result, null, 2));
  console.log(JSON.stringify(result));
} finally { await browser.close(); }
