// One-click timezone and language smoke test against the isolated fixture.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = new URL("../../docs/test-reports/region-quick/", import.meta.url);
await fs.mkdir(output, { recursive: true });
const presets = [
  { id: "us-west", label: "美国西部（洛杉矶）", label_en: "US West (Los Angeles)", country_code: "US", timezone: "America/Los_Angeles", locale: "en_US.UTF-8", browser_language: "en-US" },
  { id: "jp", label: "日本（东京）", label_en: "Japan (Tokyo)", country_code: "JP", timezone: "Asia/Tokyo", locale: "ja_JP.UTF-8", browser_language: "ja-JP" },
];
const { browser, page, state, errors } = await createWorkbench();
const applied = [];
try {
  state.status.location = { ip: "192.0.2.10", country: "United States", country_code: "US", city: "Los Angeles", timezone: "America/Los_Angeles", checked_at: new Date().toISOString() };
  await page.route("**/api/region/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/presets"))
      return route.fulfill({ json: { presets, recommended: "us-west", current: "", settings: { ...state.settings }, location: state.status.location } });
    const body = route.request().postDataJSON();
    applied.push(body);
    const preset = presets.find((item) => item.id === body.preset);
    Object.assign(state.settings, { timezone: preset.timezone, locale: preset.locale, browser_language: preset.browser_language, auto_timezone: false, auto_language: false });
    state.status.job = { id: "region-job", kind: "settings", running: false, message: "已完成" };
    return route.fulfill({ status: 202, json: state.status.job });
  });
  await page.reload();
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("button", { name: "时区与语言", exact: true }).click();
  await page.getByRole("heading", { name: /一键修改时区和语言/ }).waitFor();
  await page.getByText("America/Los_Angeles · en_US.UTF-8 · 网页 en-US", { exact: false }).waitFor();
  await page.screenshot({ path: fileURLToPath(new URL("region-quick.png", output)) });
  await page.getByRole("button", { name: /一键应用/ }).click();
  const confirm = page.getByRole("dialog").getByRole("button", { name: "应用并重启" });
  if (await confirm.isVisible().catch(() => false)) await confirm.click();
  await assert.doesNotReject(async () => {
    for (let i = 0; i < 50 && applied.length === 0; i++) await page.waitForTimeout(100);
  });
  assert.deepEqual(applied, [{ preset: "us-west" }]);
  assert.equal(state.settings.timezone, "America/Los_Angeles");
  assert.deepEqual(errors, []);
  console.log("region quick apply smoke passed");
} finally {
  await browser.close();
}
