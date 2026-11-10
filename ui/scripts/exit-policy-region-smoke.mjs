import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { chooseInline, createWorkbench } from "./workbench-fixture.mjs";

const output = new URL("../../docs/test-reports/2026-11-07-user-feedback/", import.meta.url);
await fs.mkdir(output, { recursive: true });
const { browser, page, state, errors } = await createWorkbench();
try {
  state.settings.exit_policy = { mode: "dynamic", scope: "city", country_code: "JP", region: "Kanagawa", city: "Yokohama" };
  await page.reload();
  await page.route("**/api/settings/probe", route => route.fulfill({ json: {
    ip: "192.0.2.10", country: "Japan", country_code: "JP", region: "Kanagawa", city: "Kawasaki",
    timezone: "Asia/Tokyo", checked_at: new Date().toISOString(),
  } }));
  const openSettings = async () => {
    await page.getByRole("button", { name: /^设置/ }).click();
    await page.getByRole("button", { name: "网络与代理", exact: true }).click();
  };
  await openSettings();
  await page.getByText("中国大陆出口禁止放行；静态 IP 和不限地区也必须确认出口国家。", { exact: true }).waitFor();
  const picker = page.getByRole("combobox", { name: "动态 IP 的允许范围", exact: true });
  const layoutTop = locator => locator.evaluate(element => {
    let top = element.getBoundingClientRect().top;
    for (let parent = element.parentElement; parent; parent = parent.parentElement) top += parent.scrollTop;
    return top;
  });
  const country = page.getByLabel("允许的国家／地区代码", { exact: true });
  await country.click();
  assert.equal(await page.getByRole("option", { name: "中国", exact: true }).count(), 0, "mainland must not be offered as an allowed exit country");
  await country.press("Escape");
  const before = await layoutTop(country);
  await picker.click();
  const options = page.getByRole("listbox");
  await options.waitFor();
  const expanded = await layoutTop(country);
  const list = await options.boundingBox();
  assert.ok(Math.abs(expanded - before) < 1, "floating options must not move the following fields");
  assert.ok(list.height > 0, "options must remain visible");
  await picker.press("ArrowDown");
  await picker.press("Enter");
  await page.getByLabel("允许的城市", { exact: true }).waitFor({ state: "hidden" });
  assert.equal(await picker.getAttribute("value"), "region");
  assert.equal(await page.getByLabel("所属州／省", { exact: true }).inputValue(), "Kanagawa");
  await page.getByRole("button", { name: "检测当前出口", exact: true }).click();
  await page.getByText("Japan · Kanagawa · Kawasaki", { exact: true }).waitFor();
  await page.getByRole("button", { name: "用检测结果填写", exact: true }).click();
  await page.screenshot({ path: fileURLToPath(new URL("region-settings.png", output)), fullPage: true });
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await page.getByRole("heading", { name: "网络与代理", exact: true }).waitFor({ state: "hidden" });
  assert.equal(state.settings.exit_policy.scope, "region");
  assert.equal(state.settings.exit_policy.region, "Kanagawa");
  assert.equal(state.settings.exit_policy.country_code, "JP");
  assert.equal(state.settings.exit_policy.city, "Yokohama", "relaxing the scope must preserve the previously saved city");
  await page.reload();
  await openSettings();
  assert.equal(await picker.getAttribute("value"), "region");
  assert.equal(await page.getByLabel("所属州／省", { exact: true }).inputValue(), "Kanagawa");
  await chooseInline(picker, "city");
  assert.equal(await page.getByLabel("允许的城市", { exact: true }).inputValue(), "Yokohama");
  await picker.click();
  await picker.press("Escape");
  assert.equal(await picker.getAttribute("aria-expanded"), "false");
  await chooseInline(picker, "any");
  assert.match(await picker.textContent(), /中国大陆除外/);
  await page.getByText(/命中大陆网段、定位为中国大陆、无法确认出口地区/).waitFor();
  assert.deepEqual(errors, []);
  console.log("PASS: inline mouse/keyboard expansion, region binding, city preservation, save and reload");
} finally { await browser.close(); }
