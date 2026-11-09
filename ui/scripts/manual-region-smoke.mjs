import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { chooseInline, createWorkbench } from "./workbench-fixture.mjs";

const output = new URL("../../docs/test-reports/2026-11-01-manual-region/", import.meta.url);
await fs.mkdir(output, { recursive: true });
const { browser, page, state, errors } = await createWorkbench();
try {
  state.settings.auto_language = true;
  await page.reload();
  await page.route("**/api/settings/probe", (route) => route.fulfill({
    status: 400,
    json: { error: "已测得出口 IP，但位置查询均失败；未使用直连回退" },
  }));
  const policy = structuredClone(state.settings.exit_policy);
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("button", { name: "时区与语言", exact: true }).click();
  await page.getByRole("button", { name: "现在检测", exact: true }).first().click();
  await page.getByRole("alert").filter({ hasText: "位置查询均失败" }).waitFor();
  await page.getByRole("button", { name: "手动设置时区和语言", exact: true }).click();
  assert.equal(await page.getByRole("switch").nth(0).isChecked(), false);
  assert.equal(await page.getByRole("switch").nth(1).isChecked(), false);
  await page.getByLabel("系统时区", { exact: true }).fill("Asia/Tokyo");
  await chooseInline(page.getByRole("combobox", { name: "系统语言", exact: true }), "ja_JP.UTF-8");
  await chooseInline(page.getByRole("combobox", { name: "网页首选语言", exact: true }), "ja-JP");
  await page.getByText("国家、城市限制仍须检测出口所在地。", { exact: false }).waitFor();
  await page.screenshot({ path: fileURLToPath(new URL("manual-settings.png", output)) });
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await page.getByRole("heading", { name: "时区与语言", exact: true }).waitFor({ state: "hidden" });
  assert.equal(state.settings.auto_timezone, false);
  assert.equal(state.settings.auto_language, false);
  assert.equal(state.settings.timezone, "Asia/Tokyo");
  assert.equal(state.settings.locale, "ja_JP.UTF-8");
  assert.equal(state.settings.browser_language, "ja-JP");
  assert.deepEqual(state.settings.exit_policy, policy);
  await page.reload();
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("button", { name: "时区与语言", exact: true }).click();
  assert.equal(await page.getByLabel("系统时区", { exact: true }).inputValue(), "Asia/Tokyo");
  assert.equal(await page.getByRole("combobox", { name: "系统语言", exact: true }).getAttribute("value"), "ja_JP.UTF-8");
  assert.equal(await page.getByRole("combobox", { name: "网页首选语言", exact: true }).getAttribute("value"), "ja-JP");
  assert.equal(await page.getByRole("switch").nth(0).isChecked(), false);
  assert.equal(await page.getByRole("switch").nth(1).isChecked(), false);
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ passed: true, checks: ["failed lookup recovery", "manual fields editable", "manual values saved and reloaded", "exit policy preserved"] }));
} finally {
  await browser.close();
}
